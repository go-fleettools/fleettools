package main

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const playground = "github.com/go-notebook/playground"

// landing materialises the real landing's go.mod files (testdata) as a clone.
func landing(t *testing.T, dest string) {
	t.Helper()
	src := filepath.Join("testdata", "go-numerics.github.io")
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, "go.mod.fixture") {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		out := filepath.Join(dest, strings.TrimSuffix(rel, ".fixture"))
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(out, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// The subject is a real landing: go-numerics.github.io pins the playground in
// notebooks/go.mod. Its ROOT go.mod does not require it, which is why a
// root-only read called the landing clean while it served a v0.7.1 build.
func TestScanCloneSeesTheNestedModuleOfARealLanding(t *testing.T) {
	dir := t.TempDir()
	landing(t, dir)

	root, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if _, has, err := Requires(root, playground); err != nil || has {
		t.Fatalf("the blind spot this test stands on is gone: root requires it (%v, %v)", has, err)
	}

	hits, read, unread, err := scanClone(dir, playground)
	if err != nil || len(unread) != 0 {
		t.Fatalf("%v %v", err, unread)
	}
	if read != 5 {
		t.Errorf("read %d go.mod, the landing has 5 (root, figure, notebooks, playground, playground/rubycheck)", read)
	}
	if len(hits) != 1 || hits[0].Path != "notebooks" || hits[0].Version != "v0.16.0" || hits[0].Indirect {
		t.Fatalf("hits %+v", hits)
	}
	if !strings.HasSuffix(hits[0].Module, "/notebooks") {
		t.Errorf("module %q is not the nested one", hits[0].Module)
	}
}

// Alongside the real landing, not in its place: the rules of the walk.
func TestScanCloneWalkRules(t *testing.T) {
	dir := t.TempDir()
	w := func(rel, body string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	req := func(m string) string { return "module m\nrequire x.test/y " + m + "\n" }
	w("go.mod", req("v1.0.0"))
	w("a/b/go.mod", req("v1.1.0"))
	w("vendor/dep/go.mod", req("v9.9.9"))
	w("testdata/go.mod", req("v9.9.9"))
	w(".git/go.mod", req("v9.9.9"))
	w("_old/go.mod", req("v9.9.9"))
	w("broken/go.mod", "module m\nrequire (\n")

	hits, read, unread, err := scanClone(dir, "x.test/y")
	if err != nil {
		t.Fatal(err)
	}
	if read != 2 || len(hits) != 2 || hits[0].Path != "." || hits[1].Path != "a/b" {
		t.Fatalf("read %d hits %+v", read, hits)
	}
	if len(unread) != 1 || !strings.HasPrefix(unread[0], "broken: ") {
		t.Fatalf("an unparsable go.mod must be reported, not read as 'no': %v", unread)
	}
}

func TestRunClones(t *testing.T) {
	o := options{
		module:    playground,
		inventory: inv2(t),
		cloneDir:  t.TempDir(),
		name:      "*.github.io",
		jobs:      2,
	}
	cloned := map[string]bool{}
	var mu sync.Mutex
	clone := func(org, repo, dest string) error {
		mu.Lock()
		cloned[org+"/"+repo] = true
		mu.Unlock()
		switch org + "/" + repo {
		case "go-numerics/go-numerics.github.io":
			landing(t, dest)
			return nil
		case "go-statistics/go-statistics.github.io":
			return os.MkdirAll(dest, 0o755)
		}
		return errors.New("clone refused")
	}
	var out, errb bytes.Buffer
	if rc := run2(o, &out, &errb, clone); rc != 0 {
		t.Fatalf("rc %d\n%s\n%s", rc, out.String(), errb.String())
	}
	s := out.String()
	if !strings.Contains(s, "go-numerics/go-numerics.github.io/notebooks") || !strings.Contains(s, "v0.16.0") ||
		!strings.Contains(s, "2 repositories cloned") || !strings.Contains(s, "5 go.mod read in 1 of them") ||
		!strings.Contains(s, "1 go.mod require "+playground) {
		t.Fatal(s)
	}
	if cloned["go-numerics/not-a-landing"] {
		t.Error("--name did not filter")
	}

	// A repository that cannot be cloned makes the count a floor: exit 2.
	o.name = ""
	out.Reset()
	if rc := run2(o, &out, &errb, clone); rc != 2 || !strings.Contains(errb.String(), "unread: go-numerics/not-a-landing: clone refused") {
		t.Fatalf("rc %d\n%s", rc, errb.String())
	}
}

func TestNameNeedsCloneDir(t *testing.T) {
	var errb bytes.Buffer
	if _, code, _ := parseArgs([]string{"--module", "m", "--name", "x"}, &errb); code != 2 {
		t.Errorf("--name without --clone-dir: code %d", code)
	}
	if _, code, proceed := parseArgs([]string{"--module", "m", "--clone-dir", "d", "--name", "x"}, &errb); code != 0 || !proceed {
		t.Errorf("code %d proceed %v", code, proceed)
	}
}

func run2(o options, out, errb *bytes.Buffer, c cloneFunc) int { return runClones(o, out, errb, c) }

func inv2(t *testing.T) string {
	p := filepath.Join(t.TempDir(), "inventory.json")
	src := `{"taken":"2026-11-15T15:00:00+01:00","repos":[
	 {"org":"go-numerics","name":"go-numerics.github.io"},
	 {"org":"go-statistics","name":"go-statistics.github.io"},
	 {"org":"go-numerics","name":"not-a-landing"}]}`
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}
