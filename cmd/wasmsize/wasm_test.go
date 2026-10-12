package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func uleb(v int) []byte {
	var out []byte
	for {
		c := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			out = append(out, c|0x80)
			continue
		}
		return append(out, c)
	}
}

func str(s string) []byte { return append(uleb(len(s)), s...) }

func section(id byte, body []byte) []byte {
	return append(append([]byte{id}, uleb(len(body))...), body...)
}

func cat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

// sample is a module laid out by hand: one imported function, one imported
// memory, three function bodies of 4, 130 and 2 bytes (130 needs a two-byte
// size), names for all four functions, and a data section of 5 bytes.
func sample() []byte {
	imports := cat(uleb(4),
		str("go"), str("debug"), []byte{0}, uleb(0),
		str("env"), str("mem"), []byte{2, 1}, uleb(1), uleb(4),
		str("env"), str("tab"), []byte{1, 0x70, 0}, uleb(0),
		str("env"), str("g"), []byte{3, 0x7f, 0})
	bodies := [][]byte{bytes.Repeat([]byte{1}, 4), bytes.Repeat([]byte{2}, 130), {0, 0x0b}}
	code := uleb(len(bodies))
	for _, b := range bodies {
		code = cat(code, uleb(len(b)), b)
	}
	fnames := cat(uleb(4),
		uleb(0), str("go_.debug"),
		uleb(1), str("github.com_go_ruby_rails_rails._App_.Run"),
		uleb(2), str("github.com_go_ruby_rails_rails_internal.parse.func1"),
		uleb(3), str("fmt.Println"))
	names := cat(str("name"), []byte{0}, str("mod"), []byte{1}, uleb(len(fnames)), fnames)
	return cat([]byte("\x00asm"), []byte{1, 0, 0, 0},
		section(1, []byte{0}),
		section(2, imports),
		section(10, code),
		section(11, []byte{0, 0, 0, 0, 0}),
		section(0, names))
}

func TestParseSample(t *testing.T) {
	b := sample()
	m, err := parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if m.imported != 1 {
		t.Errorf("imported functions = %d, want 1 (the memory import is not one)", m.imported)
	}
	// A body's size includes its own LEB128 size prefix.
	if want := []int{5, 132, 3}; len(m.bodies) != 3 || m.bodies[0] != want[0] || m.bodies[1] != want[1] || m.bodies[2] != want[2] {
		t.Errorf("bodies = %v, want %v", m.bodies, want)
	}
	if m.names[3] != "fmt.Println" || len(m.names) != 4 {
		t.Errorf("names = %v", m.names)
	}
	sum := 8
	for _, n := range m.sections {
		sum += n
	}
	if sum != len(b) {
		t.Errorf("sections add up to %d bytes, the file has %d", sum, len(b))
	}
	if m.sections["data"] != 7 || m.sections["custom:name"] == 0 {
		t.Errorf("sections = %v", m.sections)
	}
}

// A module cut short must be refused, never half-read and never a panic,
// unless the cut falls exactly between sections: that is a valid module with
// fewer sections.
func TestParseRefusesEveryTruncation(t *testing.T) {
	b := sample()
	boundary := map[int]bool{8: true}
	r := &reader{b: b, off: 8}
	for r.off < len(b) {
		r.off++ // id
		size, err := r.uleb()
		if err != nil {
			t.Fatal(err)
		}
		r.off += size
		boundary[r.off] = true
	}
	accepted := 0
	for n := 0; n < len(b); n++ {
		_, err := parse(b[:n])
		if err == nil && !boundary[n] {
			t.Errorf("a module cut mid-section at %d of %d bytes was accepted", n, len(b))
		}
		if err != nil && boundary[n] {
			t.Errorf("a module cut between sections at %d bytes was refused: %v", n, err)
		}
		if err == nil {
			accepted++
		}
	}
	if accepted != 5 { // the header alone, then after each of the first four sections
		t.Errorf("%d truncations accepted, want 5", accepted)
	}
}

// Every byte of the sample overwritten with every value must give a module
// or an error, never a panic: the lengths inside sections are read from the
// file, so each one is a chance to index past the end.
func TestParseNeverPanics(t *testing.T) {
	b := sample()
	for i := range b {
		for _, v := range []byte{0x00, 0x01, 0x7f, 0x80, 0xff} {
			c := bytes.Clone(b)
			c[i] = v
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("byte %d set to %#x: panic %v", i, v, r)
					}
				}()
				_, _ = parse(c)
			}()
		}
	}
}

func TestParseRefusals(t *testing.T) {
	for name, b := range map[string][]byte{
		"not wasm":      []byte("\x7fELF\x02\x01\x01\x00"),
		"version 2":     {0, 'a', 's', 'm', 2, 0, 0, 0},
		"unknown id":    cat([]byte("\x00asm\x01\x00\x00\x00"), section(42, nil)),
		"long LEB128":   cat([]byte("\x00asm\x01\x00\x00\x00"), []byte{10, 0x80, 0x80, 0x80, 0x80, 0x80, 0}),
		"LEB128 > u32":  cat([]byte("\x00asm\x01\x00\x00\x00"), []byte{10, 0xff, 0xff, 0xff, 0xff, 0x7f}),
		"import kind 9": cat([]byte("\x00asm\x01\x00\x00\x00"), section(2, cat(uleb(1), str("a"), str("b"), []byte{9}))),
	} {
		if _, err := parse(b); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestAttribution(t *testing.T) {
	a := newAttributor([]string{
		"fmt", "github.com/go-ruby-rails/rails", "github.com/go-ruby-rails/rails/internal",
		"example.com/a-b", "example.com/a_b", "", "  ",
	})
	for name, want := range map[string]string{
		"fmt.Println": "fmt",
		"github.com_go_ruby_rails_rails._App_.Run":            "github.com/go-ruby-rails/rails",
		"github.com_go_ruby_rails_rails_internal.parse.func1": "github.com/go-ruby-rails/rails/internal", // the longest prefix wins
		"example.com_a_b.F": "<ambiguous example.com_a_b>",
		"type_.eq.fmt.pp":   "<compiler-generated>",
		"go_.buildid":       "<compiler-generated>",
		"wasm_export_run":   "<unattributed>",
		"":                  "<unnamed>",
		"fmtx.F":            "<unattributed>", // a prefix must end at a "."
	} {
		if got := a.pkg(name); got != want {
			t.Errorf("pkg(%q) = %q, want %q", name, got, want)
		}
	}
	if got := a.collisions["example.com_a_b"]; len(got) != 2 {
		t.Errorf("collisions = %v", a.collisions)
	}
}

// With the module column, -by module uses it: protobuf's packages are one
// module, which the three-element guess splits.
func TestModuleColumn(t *testing.T) {
	m := &module{bodies: []int{10, 20, 30}, names: map[int]string{
		0: "google.golang.org_protobuf_internal_impl.F",
		1: "google.golang.org_protobuf_types_known.G",
		2: "fmt.H",
	}}
	lines := []string{
		"google.golang.org/protobuf/internal/impl google.golang.org/protobuf",
		"google.golang.org/protobuf/types/known google.golang.org/protobuf",
		"fmt ",
	}
	rows := tally(m, newAttributor(lines), true)
	if len(rows) != 2 || rows[0].key != "google.golang.org/protobuf" || rows[0].bytes != 30 || rows[1].key != "std" {
		t.Errorf("with modules: %+v", rows)
	}
	rows = tally(m, newAttributor([]string{"google.golang.org/protobuf/internal/impl", "google.golang.org/protobuf/types/known", "fmt"}), true)
	if len(rows) != 3 {
		t.Errorf("without modules the guess splits protobuf in two: %+v", rows)
	}
}

func TestModulePath(t *testing.T) {
	for pkg, want := range map[string]string{
		"fmt":                                 "std",
		"internal/abi":                        "std",
		"github.com/go-ruby-rails/rails/x/y":  "github.com/go-ruby-rails/rails",
		"github.com/nats-io/nats-server/v2/s": "github.com/nats-io/nats-server/v2",
		"github.com/a/b/vendor":               "github.com/a/b",
		"go.yaml.in/yaml/v3":                  "go.yaml.in/yaml/v3",
		"example.com/x":                       "example.com/x",
		"<unattributed>":                      "<unattributed>",
	} {
		if got := modulePath(pkg); got != want {
			t.Errorf("modulePath(%q) = %q, want %q", pkg, got, want)
		}
	}
}

func TestReportOnSample(t *testing.T) {
	dir := t.TempDir()
	wasm := filepath.Join(dir, "s.wasm")
	deps := filepath.Join(dir, "deps.txt")
	if err := os.WriteFile(wasm, sample(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(deps, []byte("fmt\ngithub.com/go-ruby-rails/rails\ngithub.com/go-ruby-rails/rails/internal\nexample.com/a-b\nexample.com/a_b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := run([]string{"-deps", deps, "-by", "module", wasm}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	// Both rails packages fold into the module: 5 + 132 bytes, 2 functions.
	if !strings.Contains(out.String(), "137  ") || !strings.Contains(out.String(), "2 funcs  github.com/go-ruby-rails/rails\n") {
		t.Errorf("module view:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "example.com_a_b: example.com/a-b, example.com/a_b") {
		t.Errorf("the collision is not reported:\n%s", out.String())
	}
	out.Reset()
	if code := run([]string{"-deps", deps, "-match", "internal", "-top", "1", wasm}, &out, &errb); code != 0 {
		t.Fatal(errb.String())
	}
	if strings.Count(out.String(), "funcs") != 1 || !strings.Contains(out.String(), "rails/internal") {
		t.Errorf("-match/-top:\n%s", out.String())
	}
}

func TestRunRefusals(t *testing.T) {
	dir := t.TempDir()
	noNames := filepath.Join(dir, "n.wasm")
	if err := os.WriteFile(noNames, []byte("\x00asm\x01\x00\x00\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	junk := filepath.Join(dir, "j.wasm")
	if err := os.WriteFile(junk, []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	deps := filepath.Join(dir, "deps.txt")
	if err := os.WriteFile(deps, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string][]string{
		"no file":       {"-deps", deps},
		"no -deps":      {noNames},
		"bad -by":       {"-deps", deps, "-by", "file", noNames},
		"bad flag":      {"-nope"},
		"missing wasm":  {"-deps", deps, filepath.Join(dir, "absent.wasm")},
		"not wasm":      {"-deps", deps, junk},
		"stripped":      {"-deps", deps, noNames},
		"missing -deps": {"-deps", filepath.Join(dir, "absent.txt"), noNames},
	} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != 2 {
			t.Errorf("%s: exit %d, want 2", name, code)
		}
		if name == "stripped" && !strings.Contains(errb.String(), "-ldflags=-s") {
			t.Errorf("stripped: the message should say how to fix it: %s", errb.String())
		}
	}
}

// TestRealGoBuild measures a real js/wasm build of a small program: the
// program's own package and fmt must be found by name. A build that fails is
// a failure of this test, not a skip.
func TestRealGoBuild(t *testing.T) {
	dir := t.TempDir()
	src := "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"hello\") }\n"
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/hello-wasm\n\ngo 1.27\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "GOOS=js", "GOARCH=wasm", "CGO_ENABLED=0", "GOFLAGS=")
	build := func(out string, extra ...string) {
		cmd := exec.Command("go", append(append([]string{"build"}, extra...), "-o", out, ".")...)
		cmd.Dir, cmd.Env = dir, env
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go build: %v\n%s", err, b)
		}
	}
	wasm := filepath.Join(dir, "hello.wasm")
	build(wasm)
	list := exec.Command("go", "list", "-deps", ".")
	list.Dir, list.Env = dir, env
	depsOut, err := list.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	deps := filepath.Join(dir, "deps.txt")
	if err := os.WriteFile(deps, depsOut, 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := run([]string{"-deps", deps, "-top", "0", wasm}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	for _, want := range []string{"  fmt\n", "  main\n", "  runtime\n", "custom:name"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}
	stripped := filepath.Join(dir, "hello-s.wasm")
	build(stripped, "-ldflags=-s")
	errb.Reset()
	if code := run([]string{"-deps", deps, stripped}, &out, &errb); code != 2 || !strings.Contains(errb.String(), "without -s") {
		t.Errorf("a -s build: exit %d, %s", code, errb.String())
	}
}
