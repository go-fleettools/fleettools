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

// ⛔⛔ AN UNEXPORTED NAME A README QUOTES IS A REFERENCE. It used to match
// nothing: bareRE began `[A-Z]`, so `rowGeometry` was not missing, not bare and
// not counted, and docsym answered `0 MISSING` over a README naming a symbol
// that had been deleted that morning. #354.
//
// ⭐ AND THE SECOND HALF IS WHAT KEEPS THE REPORT READABLE: a lower-case name
// counts only if it is camelCase. Without that, every `err` and `data` in every
// README arrives as a reference nothing can confirm.
func TestALowerCaseCamelNameIsAReferenceAndAPlainWordIsNot(t *testing.T) {
	readme := strings.Join([]string{
		"It computed `rowGeometry` and now calls `rowBytes`, bounded by `maxDecodedSize`.",
		"Ordinary words in spans: `err`, `data`, `nil`, `go`, `main`, `gofmt`, `stdin`.",
		"Still exported, still found: `LoadGTKTheme` and `toolkit.NewButton`.",
	}, "\n")
	got := strings.Join(names(Extract(readme)), " ")
	want := "rowGeometry rowBytes maxDecodedSize LoadGTKTheme toolkit.NewButton"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestPlausibleName(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
		why  string
	}{
		{"rowGeometry", true, "the name #354 was filed for"},
		{"maxCCITTColumns", true, "capitals inside, lower-case first"},
		{"Exported", true, "unchanged behaviour"},
		{"err", false, "a word, not an identifier somebody wrote"},
		{"data", false, "the same"},
		{"gofmt", false, "a command in a span"},
		{"n", false, "one letter has no interior at all"},
		{"GOOS", false, "all capitals, as before"},
		{"RGBA_8888", false, "underscore, as before"},
	} {
		if got := plausibleName(tc.name); got != tc.want {
			t.Errorf("plausibleName(%q) = %v, want %v — %s", tc.name, got, tc.want, tc.why)
		}
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
func (t *Tray) relayout() {}
type Menu struct{}
`

const ownSrc = `package webcanvas
type App interface { Draw(buf []byte); KeyDown(k string) bool }
func Run(id string, a App) {}
type Base struct{ Bounds int }
type Button struct{ Base; Label string; hoverState int }
func (b *Button) SetBounds(x int) {}
func (b *Button) seekGlyph() {}
`

func index(t *testing.T) *Index {
	tray := pkg(t, "github.com/go-widgets/tray", trayV14)
	own := pkg(t, "github.com/go-widgets/webcanvas", ownSrc)
	ix := &Index{
		byName:     map[string][]*types.Package{"tray": {tray}, "webcanvas": {own}},
		own:        []*types.Package{own},
		all:        []*types.Package{own, tray},
		broken:     map[string]string{},
		version:    map[string]string{"github.com/go-widgets/tray": "v0.14.0"},
		members:    map[string]bool{},
		ownMembers: map[string]bool{},
	}
	addMembers(ix.members, own, false)
	addMembers(ix.members, tray, false)
	addMembers(ix.ownMembers, own, true) // the module's own types only, as Load does
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
		// ⛔ A DEPENDENCY's exported method, bare. Once ownMembers also held
		// the module's own exported methods, the `members` lookup kept only
		// this case — and no test named it, so a mutation sweep deleted that
		// lookup with the suite green. Window's README writing `Attach` for
		// tray's method is the ordinary case it serves.
		{Ref{Name: "Attach"}, Found, ""},
		{Ref{Name: "NSEvent"}, Unconfirmed, ""},

		// ⛔ The module's OWN unexported method and field resolve: a README
		// describing its internals names something real. On go-pdfkit/reader
		// seven of twelve lower-case unconfirmed names were exactly this.
		{Ref{Name: "seekGlyph"}, Found, ""},
		{Ref{Name: "hoverState"}, Found, ""},

		// ⛔ But a DEPENDENCY's unexported method does not: it cannot be
		// called from here, so the set it would come from is never built for
		// a dependency. This is the line that fails if ownMembers is ever
		// filled from ix.all instead of ix.own.
		{Ref{Name: "relayout"}, Unconfirmed, ""},
	} {
		v, where := ix.Check(c.ref)
		if v != c.want || !strings.Contains(where, c.where) {
			t.Errorf("%+v: got %v %q, want %v containing %q", c.ref, v, where, c.want, c.where)
		}
	}
}

// ⛔⛔ A DEPENDENCY'S UNEXPORTED NAMES DO NOT BECOME OURS BY EMBEDDING. The
// `relayout` case in TestCheck proved ownMembers is not filled from ix.all — and
// could not catch this, because no own type in that fixture embeds anything.
// Review built the one that does: `type W struct{ bytes.Buffer }`. A method set
// includes PROMOTED methods and the field walk recurses into embedded structs,
// so bytes.Buffer's unexported grow and buf resolved as Found — names no reader
// of this module can use. The rule is now about the object, not the set: an
// unexported name counts only when it is declared in the package scanned.
func TestAnEmbeddedDependencyDoesNotLendItsUnexportedNames(t *testing.T) {
	// Three ways to embed, each a separate path through addMembers/addFields,
	// and all three verified refused by review before being pinned here: a
	// value embedding (W), a POINTER embedding (P — addFields' pointer branch),
	// and an embedded dependency INTERFACE (I — the interface-method loop,
	// where testing.TB carries an unexported `private`).
	own := pkg(t, "github.com/go-widgets/webcanvas", `package webcanvas
import ("bytes"; "testing")
type W struct{ bytes.Buffer; mine int }
type P struct{ *bytes.Buffer }
type I interface{ testing.TB }
func (w *W) ownHelper() {}
`)
	ix := &Index{
		byName:     map[string][]*types.Package{"webcanvas": {own}},
		own:        []*types.Package{own},
		all:        []*types.Package{own},
		broken:     map[string]string{},
		version:    map[string]string{},
		members:    map[string]bool{},
		ownMembers: map[string]bool{},
	}
	addMembers(ix.members, own, false)
	addMembers(ix.ownMembers, own, true)
	for _, c := range []struct {
		name string
		want Verdict
		why  string
	}{
		{"ownHelper", Found, "the control: this module's own unexported method"},
		{"mine", Found, "the control: this module's own unexported field"},
		{"grow", Unconfirmed, "bytes.Buffer's unexported method, promoted through the embedding"},
		{"buf", Unconfirmed, "bytes.Buffer's unexported field, reached by the embedded-field walk"},
		{"lastRead", Unconfirmed, "another of bytes.Buffer's unexported fields"},
		{"readSlice", Unconfirmed, "promoted through the POINTER embedding in P as well as W"},
		{"private", Unconfirmed, "testing.TB's unexported method, through the embedded interface I"},
		{"Len", Found, "exported and promoted: callable on W, so it is a real name"},
	} {
		if v, where := ix.Check(Ref{Name: c.name}); v != c.want {
			t.Errorf("%s: got %v (%s), want %v — %s", c.name, v, where, c.want, c.why)
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
	write("m.go", "package m\n\ntype Window struct{ Title string }\n\nfunc (w *Window) Run() error { return nil }\n\nfunc rowBytes() int { return 0 }\n")
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

		// ⛔⛔ #354, and it took THREE changes to make this line pass: the
		// span had to be extracted (bareRE began [A-Z]), the package had to
		// be type-checked from SOURCE (NeedTypes alone gives export data,
		// which holds no unexported object at all — 22 names in scope against
		// 234), and `resolves` had to stop rejecting every unexported object
		// outright. Each one alone leaves this Unconfirmed.
		{Ref{Name: "rowBytes"}, Found},

		// ⛔ But only for the module's OWN packages. Qualified, it must still
		// not resolve: `m.rowBytes` cannot be written from outside m.
		{Ref{Pkg: "m", Name: "rowBytes"}, Missing},

		// The control: an unexported name nothing has must NOT come back
		// Found. ⚠ It is Unreadable rather than Unconfirmed here, and that is
		// this fixture's own doing, not the change's: it carries a deliberately
		// broken package, and a bare name that resolves nowhere while some
		// package failed to type-check is not judged — a miss there would be a
		// guess. On a module that compiles, this is Unconfirmed, which is what
		// go-pdfkit/reader's README reports for `rowGeometry`.
		{Ref{Name: "rowGeometry"}, Unreadable},
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
