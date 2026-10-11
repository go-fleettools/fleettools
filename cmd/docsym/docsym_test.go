package main

import (
	"bytes"
	"errors"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func names(refs []Ref) []string {
	var out []string
	for _, r := range refs {
		s := r.Name
		if r.Pkg != "" {
			s = r.Pkg + "." + s
		}
		if r.Member != "" {
			s += "." + r.Member
		}
		out = append(out, s)
	}
	return out
}

func TestExtractReadsSpansAndGoFencesOnly(t *testing.T) {
	readme := strings.Join([]string{
		"Call `toolkit.NewButton` or `LoadGTKTheme(css)`; set `Entry.Text`.", // 1
		"See https://pkg.go.dev/github.com/go-widgets/toolkit and `go test ./...`.",
		"```go",
		"w, err := window.Open(window.Config{Title: \"x\"}) // window.Ignored in a comment",
		"box.Append(toolkit.NewLabel(\"hi\"))",
		"```",
		"```sh",
		"GOOS=js go build ./cmd/gallery.Wasm",
		"```",
		"Words in spans that are not names: `CGO_ENABLED=0`, `RGBA_8888`, `GOOS`, `go vet`.",
		"`painter.Painter.FillRect` and `*Window`.",
	}, "\n")
	got := strings.Join(names(Extract(readme)), " ")
	// box.Append is extracted: whether `box` is a package is decided at
	// resolution (TestCheck: a qualifier that is no loaded package is NotOurs).
	want := "toolkit.NewButton LoadGTKTheme Entry.Text window.Open window.Config box.Append toolkit.NewLabel painter.Painter.FillRect Window"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestExtractKeepsLineNumbers(t *testing.T) {
	refs := Extract("one\n\n`toolkit.NewButton`\n")
	if len(refs) != 1 || refs[0].Line != 3 {
		t.Fatalf("%+v", refs)
	}
}

// pkg type-checks one small package from source, so the resolution tests use
// real go/types objects without the network or go/packages.
func pkg(t *testing.T, path, src string) *types.Package {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	p, err := (&types.Config{Importer: importer.Default()}).Check(path, fset, []*ast.File{f}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

const trayV14 = `package tray
type Tray struct{ Tooltip string }
func New() *Tray { return nil }
func (t *Tray) Attach() error { return nil }
type Menu struct{}
`

const ownSrc = `package webcanvas
type App interface { Draw(buf []byte); KeyDown(k string) bool }
func Run(id string, a App) {}
type Base struct{ Bounds int }
type Button struct{ Base; Label string }
func (b *Button) SetBounds(x int) {}
`

func index(t *testing.T) *Index {
	tray := pkg(t, "github.com/go-widgets/tray", trayV14)
	own := pkg(t, "github.com/go-widgets/webcanvas", ownSrc)
	ix := &Index{
		byName:  map[string][]*types.Package{"tray": {tray}, "webcanvas": {own}},
		own:     []*types.Package{own},
		all:     []*types.Package{own, tray},
		broken:  map[string]string{},
		version: map[string]string{"github.com/go-widgets/tray": "v0.14.0"},
		members: map[string]bool{},
	}
	addMembers(ix.members, own)
	addMembers(ix.members, tray)
	return ix
}

func TestCheck(t *testing.T) {
	ix := index(t)
	for _, c := range []struct {
		ref   Ref
		want  Verdict
		where string
	}{
		{Ref{Pkg: "tray", Name: "New"}, Found, ""},
		{Ref{Pkg: "tray", Name: "Tray", Member: "Attach"}, Found, ""},
		{Ref{Pkg: "tray", Name: "Tray", Member: "Tooltip"}, Found, ""},
		{Ref{Pkg: "tray", Name: "Gone"}, Missing, "github.com/go-widgets/tray@v0.14.0"},
		{Ref{Pkg: "tray", Name: "Attach"}, Missing, "Attach is a method of Tray, so `Tray.Attach`"},
		{Ref{Pkg: "tray", Name: "Tray", Member: "Quit"}, Missing, "tray@v0.14.0"},
		{Ref{Pkg: "w", Name: "Run"}, NotOurs, ""}, // a variable, not a package
		{Ref{Name: "Run"}, Found, ""},
		{Ref{Name: "Draw"}, Found, ""},      // an interface's method: a *T method set misses it
		{Ref{Name: "KeyDown"}, Found, ""},   // likewise
		{Ref{Name: "SetBounds"}, Found, ""}, // a method, bare
		{Ref{Name: "Bounds"}, Found, ""},    // a field promoted from an embedded struct
		{Ref{Name: "Button", Member: "Bounds"}, Found, ""},
		{Ref{Name: "Menu"}, Found, ""}, // a dependency's type, bare
		{Ref{Name: "NSEvent"}, Unconfirmed, ""},
	} {
		v, where := ix.Check(c.ref)
		if v != c.want || !strings.Contains(where, c.where) {
			t.Errorf("%+v: got %v %q, want %v containing %q", c.ref, v, where, c.want, c.where)
		}
	}
}

func TestABrokenPackageIsNeverJudgedMissing(t *testing.T) {
	ix := index(t)
	ix.broken["github.com/go-widgets/tray"] = "linux: undefined: x"
	if v, _ := ix.Check(Ref{Pkg: "tray", Name: "Gone"}); v != Unreadable {
		t.Fatalf("a miss in a package that did not type-check is a guess: got %v", v)
	}
}

func module(t *testing.T, readme string) string {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(readme), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRun(t *testing.T) {
	load := func(t *testing.T) func(string, []string) (*Index, error) {
		return func(string, []string) (*Index, error) { return index(t), nil }
	}
	dir := module(t, "`tray.New` `tray.Gone` `NSEvent`\n")
	var out, errb bytes.Buffer
	if rc := run(options{goos: []string{"linux"}, dirs: []string{dir}}, &out, &errb, load(t)); rc != 0 {
		t.Fatalf("rc %d: %s", rc, errb.String())
	}
	if !strings.Contains(out.String(), "1 references found, 1 MISSING, 0 allowed by a docsym:allow with its reason, 1 bare names unconfirmed") ||
		strings.Contains(out.String(), "NSEvent") {
		t.Fatal(out.String())
	}
	out.Reset()
	if rc := run(options{goos: []string{"linux"}, fail: true, bare: true, dirs: []string{dir}}, &out, &errb, load(t)); rc != 1 {
		t.Fatalf("-fail with a MISSING: rc %d", rc)
	}
	if !strings.Contains(out.String(), "`NSEvent`: unconfirmed") {
		t.Fatal("-bare lists the unconfirmed names:", out.String())
	}
}

func TestAnAllowNeedsAReason(t *testing.T) {
	got := Allowed(strings.Join([]string{
		"<!-- docsym:allow mvvm.Queue mvvm.NewQueue -- application code: needs mvvm v0.14.0 -->",
		"<!-- docsym:allow tray.Gone -->", // no reason: ignored
		"<!-- docsym:allow tray.Other --  -->",
	}, "\n"))
	if len(got) != 2 || got["mvvm.Queue"] == "" || got["mvvm.NewQueue"] == "" {
		t.Fatalf("%v", got)
	}
	if _, ok := got["tray.Gone"]; ok {
		t.Fatal("an allow without a reason must be ignored")
	}
}

func TestAnAllowedMissIsCountedNotReported(t *testing.T) {
	load := func(string, []string) (*Index, error) { return index(t), nil }
	dir := module(t, "`tray.Gone` `tray.Lost`\n<!-- docsym:allow tray.Gone -- documented on purpose -->\n")
	var out, errb bytes.Buffer
	if rc := run(options{goos: []string{"linux"}, fail: true, dirs: []string{dir}}, &out, &errb, load); rc != 1 {
		t.Fatalf("tray.Lost is still missing: rc %d", rc)
	}
	if strings.Contains(out.String(), "tray.Gone") || !strings.Contains(out.String(), "1 MISSING, 1 allowed") {
		t.Fatal(out.String())
	}
}

func TestAModuleThatCannotBeReadFailsTheRun(t *testing.T) {
	var out, errb bytes.Buffer
	noReadme := t.TempDir()
	if rc := run(options{goos: []string{"linux"}, dirs: []string{noReadme}}, &out, &errb, nil); rc != 2 {
		t.Fatalf("no README: rc %d", rc)
	}
	dir := module(t, "`tray.New`\n")
	broken := func(string, []string) (*Index, error) { return nil, errors.New("go.mod: no module") }
	if rc := run(options{goos: []string{"linux"}, dirs: []string{dir}}, &out, &errb, broken); rc != 2 {
		t.Fatalf("unloadable: rc %d", rc)
	}
}

func TestParseArgs(t *testing.T) {
	for _, c := range []struct {
		args    []string
		code    int
		proceed bool
	}{
		{[]string{"-h"}, 0, false},
		{nil, 2, false},
		{[]string{"-nope", "x"}, 2, false},
		{[]string{"-goos", " , ", "x"}, 2, false},
		{[]string{"x"}, 0, true},
		{[]string{"-goos", "linux", "-fail", "-bare", "x", "y"}, 0, true},
	} {
		var errb bytes.Buffer
		o, code, proceed := parseArgs(c.args, &errb)
		if code != c.code || proceed != c.proceed {
			t.Errorf("%q: got (%d, %v)", c.args, code, proceed)
		}
		if proceed && len(o.dirs) == 0 {
			t.Errorf("%q: no dirs", c.args)
		}
	}
}

// TestLoadTypeChecksARealModule runs the go command on a local module with no
// dependencies -- no network, and none of this repository's own tools.
func TestLoadTypeChecksARealModule(t *testing.T) {
	dir := t.TempDir()
	write := func(name, src string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/m\n\ngo 1.22\n")
	write("m.go", "package m\n\ntype Window struct{ Title string }\n\nfunc (w *Window) Run() error { return nil }\n")
	write("linux.go", "//go:build linux\n\npackage m\n\nfunc OpenWayland() {}\n")
	write("broken/b.go", "package broken\n\nfunc F() { undefinedThing() }\n")

	ix, err := Load(dir, []string{"darwin", "linux"})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		ref  Ref
		want Verdict
	}{
		{Ref{Pkg: "m", Name: "Window", Member: "Run"}, Found},
		{Ref{Pkg: "m", Name: "OpenWayland"}, Found}, // linux only: found because linux was asked
		{Ref{Pkg: "m", Name: "OpenCocoa"}, Missing},
		{Ref{Name: "Title"}, Found},
		{Ref{Pkg: "broken", Name: "G"}, Unreadable},
	} {
		if v, where := ix.Check(c.ref); v != c.want {
			t.Errorf("%+v: got %v (%s), want %v", c.ref, v, where, c.want)
		}
	}
	if _, err := Load(t.TempDir(), []string{"linux"}); err == nil {
		t.Fatal("a directory with no go.mod must be an error, not an empty index")
	}
}

// The audited checkout is read, never written: its go.mod and go.sum are the
// same bytes after a load, and the go command is pointed at a copy.
func TestLoadNeverWritesTheModule(t *testing.T) {
	dir := t.TempDir()
	mod := "module example.com/m\n\ngo 1.22\n"
	sum := "example.com/never v0.0.0 h1:AAAA=\n"
	for name, src := range map[string]string{"go.mod": mod, "go.sum": sum, "m.go": "package m\n\nfunc F() {}\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Load(dir, []string{"linux", "js"}); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"go.mod": mod, "go.sum": sum} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(got) != want {
			t.Fatalf("%s was written: %q (%v)", name, got, err)
		}
	}
	var flags string
	for _, kv := range loadEnv("linux", "/elsewhere/go.mod") {
		if strings.HasPrefix(kv, "GOFLAGS=") {
			flags = kv
		}
	}
	if flags != "GOFLAGS=-mod=mod -modfile=/elsewhere/go.mod" {
		t.Fatalf("the go command must be pointed at the copy: %q", flags)
	}
}

func TestOrgPrefix(t *testing.T) {
	for in, want := range map[string]string{
		"github.com/go-widgets/window": "github.com/go-widgets/",
		"example.com/x":                "",
	} {
		if got := orgPrefix(in); got != want {
			t.Errorf("%s: %q", in, got)
		}
	}
}
