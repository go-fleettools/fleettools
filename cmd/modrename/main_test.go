package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const from, to = "github.com/ajroetker/go-highway", "github.com/go-simd/highway"

func TestRenameOnlyModulePaths(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{`import "github.com/ajroetker/go-highway/hwy"`, `import "github.com/go-simd/highway/hwy"`},
		{"module github.com/ajroetker/go-highway\n", "module github.com/go-simd/highway\n"},
		{"go get github.com/ajroetker/go-highway@latest", "go get github.com/go-simd/highway@latest"},
		{"(github.com/ajroetker/go-highway)", "(github.com/go-simd/highway)"},
		// A file path records where something was generated: never rewritten.
		{"// /Users/ajroetker/go/src/github.com/ajroetker/go-highway/hwy/x.c", "// /Users/ajroetker/go/src/github.com/ajroetker/go-highway/hwy/x.c"},
		// A longer name is a different module.
		{"github.com/ajroetker/go-highway2/x", "github.com/ajroetker/go-highway2/x"},
		{"github.com/ajroetker/go-highway.v2", "github.com/ajroetker/go-highway.v2"},
		{"xgithub.com/ajroetker/go-highway", "xgithub.com/ajroetker/go-highway"},
		// The URL form IS the module path after the scheme's "//" -- but the
		// character before it is '/', so it is read as a path and left: a
		// link to the upstream repository stays a link to upstream.
		{"https://github.com/ajroetker/go-highway", "https://github.com/ajroetker/go-highway"},
	} {
		got, _ := Rename([]byte(c.in), from, to)
		if string(got) != c.want {
			t.Errorf("%q\n got %q\nwant %q", c.in, got, c.want)
		}
	}
	got, n := Rename([]byte(from+" "+from+"/x /"+from), from, to)
	if n != 2 || string(got) != to+" "+to+"/x /"+from {
		t.Fatalf("%d %q", n, got)
	}
}

func repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":       "module " + from + "\n\ngo 1.22\n",
		"a.go":         "package a\n\nimport _ \"" + from + "/hwy\"\n",
		"README.md":    "go get " + from + "\n",
		"x_arm64.s":    "// /Users/me/src/" + from + "/x.c\n// and a bare " + from + "\n",
		"notes.txt":    from + "\n",
		"hwy/go.mod":   "module " + from + "/hwy\n",
		"unrelated.go": "package a\n",
	}
	for name, src := range files {
		p := filepath.Join(dir, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "x"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	return dir
}

func TestRunReportsThenWrites(t *testing.T) {
	dir := repo(t)
	var out, errb bytes.Buffer
	if rc := run(options{from: from, to: to, dir: dir}, &out, &errb); rc != 1 {
		t.Fatalf("report: rc %d %s", rc, errb.String())
	}
	if !strings.Contains(out.String(), "would rewrite 4 occurrence(s) in 4 file(s); left alone (not go.mod/.go/.md, or file paths only): .s 1, .txt 1") {
		t.Fatal(out.String())
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a.go")); !strings.Contains(string(b), from) {
		t.Fatal("a report must not write")
	}
	out.Reset()
	if rc := run(options{from: from, to: to, dir: dir, write: true}, &out, &errb); rc != 0 {
		t.Fatalf("write: rc %d %s", rc, errb.String())
	}
	for name, want := range map[string]string{
		"go.mod": "module " + to + "\n", "a.go": to + "/hwy", "README.md": "go get " + to, "hwy/go.mod": "module " + to + "/hwy",
		"x_arm64.s": "/Users/me/src/" + from, "notes.txt": from,
	} {
		b, _ := os.ReadFile(filepath.Join(dir, name))
		if !strings.Contains(string(b), want) {
			t.Errorf("%s: %q does not contain %q", name, b, want)
		}
	}
	// Now the tree is dirty: a second run refuses.
	if rc := run(options{from: from, to: to, dir: dir, write: true}, &out, &errb); rc != 2 || !strings.Contains(errb.String(), "uncommitted") {
		t.Fatalf("dirty tree: rc %d %s", rc, errb.String())
	}
}

func TestNothingToRenameFails(t *testing.T) {
	dir := repo(t)
	var out, errb bytes.Buffer
	if rc := run(options{from: "github.com/none/such", to: to, dir: dir}, &out, &errb); rc != 2 {
		t.Fatalf("rc %d", rc)
	}
	if rc := run(options{from: from, to: to, dir: t.TempDir()}, &out, &errb); rc != 2 {
		t.Fatalf("not a checkout: rc %d", rc)
	}
}

func TestParseArgs(t *testing.T) {
	for _, c := range []struct {
		args    []string
		code    int
		proceed bool
	}{
		{[]string{"-h"}, 0, false},
		{[]string{"-from", "a", "-to", "a", "d"}, 2, false},
		{[]string{"-from", "a", "-to", "b"}, 2, false},
		{[]string{"-from", "a", "-to", "b", "d", "e"}, 2, false},
		{[]string{"-from", "a", "-to", "b", "d"}, 0, true},
	} {
		var errb bytes.Buffer
		if _, code, proceed := parseArgs(c.args, &errb); code != c.code || proceed != c.proceed {
			t.Errorf("%q: (%d,%v)", c.args, code, proceed)
		}
	}
}
