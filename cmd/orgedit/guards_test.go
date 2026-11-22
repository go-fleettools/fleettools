package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// armFirst makes the fake program fail, with a distinctive word on stderr, on
// any argument line containing pattern. The word is how a test tells THIS
// failure from the different one a swallowed error would produce further on.
func (w *world) armFirst(prog, pattern, word string) {
	w.t.Helper()
	p := filepath.Join(w.bin, prog)
	b, err := os.ReadFile(p)
	if err != nil {
		w.t.Fatal(err)
	}
	arm := "case \"$*\" in\n  *" + pattern + "*) echo " + word + " >&2; exit 1 ;;\n"
	s := strings.Replace(string(b), "case \"$*\" in\n", arm, 1)
	if s == string(b) {
		w.t.Fatalf("no case in the fake %s", prog)
	}
	if err := os.WriteFile(p, []byte(s), 0o755); err != nil {
		w.t.Fatal(err)
	}
}

// Every program the landing runs, failing on its own: the run must stop at
// that program, saying that program's words, and never reach the merge.
func TestEachProgramFailingOnItsOwnStopsTheRunWithItsWords(t *testing.T) {
	type tc struct {
		prog, pattern string
		clone         bool // a clone already exists
	}
	for _, c := range []tc{
		{"git", "clone", false},
		{"git", "fetch", true},
		{"git", "set-head", false},
		{"git", "symbolic-ref", false},
		{"git", "switch", false},
		{"git", " add ", false},
		{"git", "commit", false},
		{"git", "rev-parse", false},
		{"gh", `"pr list"`, false},
		{"gh", `"pr create"`, false},
		{"gh", "statusCheckRollup", false},
	} {
		w := newWorld(t, map[string]answer{"go-a": ok(target1)})
		if c.clone {
			w.clone("go-a", target1)
		}
		word := "boom" + strings.NewReplacer(" ", "", `"`, "").Replace(c.pattern)
		w.armFirst(c.prog, c.pattern, word)
		st, _, errOut := w.drive("--orgs", "go-a")
		// Call returns the program's words as its output even when it fails, so
		// a swallowed error could still echo the word: the failing command line
		// is what only the real error carries.
		if st != 1 || !strings.Contains(errOut, "STOPPED at go-a/.github") || !strings.Contains(errOut, word) ||
			!strings.Contains(errOut, c.prog+" ") || strings.Contains(errOut, "is unreadable") ||
			strings.Contains(w.logged(), "ghmerge") {
			t.Errorf("%s %s: status %d: %s", c.prog, c.pattern, st, errOut)
		}
	}
}

func TestAnAnswerOfPrListThatIsUnreadableOrIncompleteStopsTheRun(t *testing.T) {
	for name, tc := range map[string]struct{ list, want string }{
		"garbage":   {"garbage", "answered something unreadable"},
		"no number": {`[{"number":0,"headRefOid":"abc123"}]`, "no number or head"},
		"no head":   {`[{"number":9,"headRefOid":""}]`, "no number or head"},
	} {
		w := newWorld(t, map[string]answer{"go-a": ok(target1)})
		if err := os.WriteFile(filepath.Join(w.dir, "prlist-go-a_.github"), []byte(tc.list), 0o644); err != nil {
			t.Fatal(err)
		}
		st, _, errOut := w.drive("--orgs", "go-a")
		if st != 1 || !strings.Contains(errOut, tc.want) || strings.Contains(w.logged(), "ghmerge") {
			t.Errorf("%s: status %d: %s", name, st, errOut)
		}
	}
}

func TestAnUnreadableListAfterCreateStopsTheRun(t *testing.T) {
	w := newWorld(t, map[string]answer{"go-a": ok(target1)})
	b, _ := os.ReadFile(filepath.Join(w.bin, "gh"))
	s := strings.Replace(string(b), `echo '[{"number":7,"headRefOid":"abc123"}]' > $f;`, `echo garbage > $f;`, 1)
	if s == string(b) {
		t.Fatal("create arm not found")
	}
	if err := os.WriteFile(filepath.Join(w.bin, "gh"), []byte(s), 0o755); err != nil {
		t.Fatal(err)
	}
	st, _, errOut := w.drive("--orgs", "go-a")
	if st != 1 || !strings.Contains(errOut, "answered something unreadable") || strings.Contains(w.logged(), "ghmerge") {
		t.Fatalf("status %d: %s", st, errOut)
	}
}

func TestACloneFileThatCannotBeReadOrWrittenStopsTheRun(t *testing.T) {
	for name, tc := range map[string]struct {
		mode os.FileMode
		want string
	}{
		"unreadable": {0o000, "permission denied"},
		"read-only":  {0o444, "permission denied"},
	} {
		w := newWorld(t, map[string]answer{"go-a": ok(target1)})
		d := w.clone("go-a", target1)
		if err := os.Chmod(filepath.Join(d, ciFile), tc.mode); err != nil {
			t.Fatal(err)
		}
		st, _, errOut := w.drive("--orgs", "go-a")
		if os.Geteuid() == 0 {
			t.Skip("root reads and writes anything")
		}
		if st != 1 || !strings.Contains(errOut, tc.want) || strings.Contains(w.logged(), "gitpush") {
			t.Errorf("%s: status %d: %s", name, st, errOut)
		}
	}
}

func TestAMissingFileInTheCloneStopsTheRun(t *testing.T) {
	w := newWorld(t, map[string]answer{"go-a": ok(target1)})
	d := w.clone("go-a", target1)
	if err := os.Remove(filepath.Join(d, ciFile)); err != nil {
		t.Fatal(err)
	}
	st, _, errOut := w.drive("--orgs", "go-a")
	if st != 1 || !strings.Contains(errOut, "no such file") {
		t.Fatalf("status %d: %s", st, errOut)
	}
}

func TestAnUnresolvablePathStopsTheRun(t *testing.T) {
	w := newWorld(t, map[string]answer{"go-a": ok(target1)})
	old := absPath
	absPath = func(string) (string, error) { return "", errors.New("abs boom") }
	t.Cleanup(func() { absPath = old })
	st, _, errOut := w.drive("--orgs", "go-a")
	if st != 2 || !strings.Contains(errOut, "abs boom") || w.logged() != "" {
		t.Fatalf("status %d: %s", st, errOut)
	}
}
