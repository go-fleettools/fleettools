package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The report line says why a go.mod is moved or held, and what is left alone.
func TestAliasReportNamesTheGoModDecision(t *testing.T) {
	w := newWorld(t)
	w.repo("o/move", "          go-version: stable\n", "module x\n\ngo 1.26.4\n")
	w.repo("o/held", "          go-version: stable\n          go-version: '1.26.4'\n", "module x\n\ngo 1.26.4\n")
	code, out, _ := w.run("-version", "1.27.3", "-list", w.list("o/move", "o/held"))
	if code != 0 || !strings.Contains(out, "ALIAS   o/move: 1 in ci.yml ; go.mod 1.26.4 -> 1.27.3") ||
		!strings.Contains(out, "go.mod HELD at 1.26.4 by the 1.26.4 pin ; leaving 1.26.4 alone") {
		t.Fatalf("code=%d\n%s", code, out)
	}
}

// Resuming a campaign merges pull request after pull request with no CI wait
// between them; the merge is a write and is paced with --pause. A pull request
// this run just opened was already paced by the open.
func TestMergesOfResumedPullRequestsArePaced(t *testing.T) {
	w := newWorld(t)
	w.repo("o/old", pinned272, "")
	w.repo("o/new", pinned272, "")
	st := filepath.Join(t.TempDir(), "s.json")
	seed := `{"from":"1.27.2","version":"1.27.3","repos":{"o/old":{"status":"opened","pr":"https://github.com/o/old/pull/4"}}}`
	if err := os.WriteFile(st, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ := w.run("-from", "1.27.2", "-version", "1.27.3", "-list", w.list("o/old", "o/new"), "-apply", "-follow", "-pause", "7s", "-state", st)
	if code != 0 {
		t.Fatalf("code=%d\n%s", code, out)
	}
	var sevens int
	for _, d := range w.sleeps {
		if d.Seconds() == 7 {
			sevens++
		}
	}
	// o/old: one pause after its merge; o/new: one after its open, none after its merge.
	if sevens != 2 {
		t.Fatalf("pauses of 7s = %d (sleeps %v); want 2", sevens, w.sleeps)
	}
}

// A go-version-file naming a path that is not in this repository (the workflow
// checks another repository out there) is nothing to raise, not an error row;
// go-fleettools/fleettools reads `scanner/go.mod` from a scanner it clones.
func TestAVersionFileOutsideThisRepositoryIsNotAnError(t *testing.T) {
	w := newWorld(t)
	w.repo("o/a", "          go-version-file: scanner/go.mod\n          go-version-file: go.mod\n", "module x\n\ngo 1.27.2\n")
	code, out, _ := w.run("-from", "1.27.2", "-version", "1.27.3", "-gomod", "-list", w.list("o/a"))
	if code != 0 || strings.Contains(out, "ERROR") || !strings.Contains(out, "REPIN   o/a: go-version-file go.mod ") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	// ... but a refusal that is not a 404 still is one
	base := ghRaw
	ghRaw = func(args ...string) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), "contents/scanner/go.mod") {
			return nil, errors.New("HTTP 502")
		}
		return base(args...)
	}
	code, out, _ = w.run("-from", "1.27.2", "-version", "1.27.3", "-gomod", "-list", w.list("o/a"))
	if code != 1 || !strings.Contains(out, "ERROR   o/a: HTTP 502") {
		t.Fatalf("code=%d\n%s", code, out)
	}
}

func TestStateWriteFailuresAreSurfaced(t *testing.T) {
	dir := t.TempDir()
	// the rename target is a directory
	s := &state{path: filepath.Join(dir, "target"), sf: stateFile{Repos: map[string]entry{}}}
	if err := os.Mkdir(s.path, 0o755); err != nil {
		t.Fatal(err)
	}
	if s.set("o/a", entry{Status: stMerged}) == nil {
		t.Fatal("rename onto a directory succeeded")
	}
	// the temporary file cannot be created
	s2 := &state{path: filepath.Join(dir, "s.json"), sf: stateFile{Repos: map[string]entry{}}}
	if err := os.Mkdir(s2.path+".tmp", 0o755); err != nil {
		t.Fatal(err)
	}
	if s2.set("o/a", entry{}) == nil {
		t.Fatal("writing over a directory succeeded")
	}
}

// openPR's failure modes: each is an error naming its step, never a half-made
// pull request.
func TestOpenPRStepsFailLoudly(t *testing.T) {
	ci := "          go-version: stable\n"
	cases := []struct {
		name string
		f    finding
		prep func(w *world)
		want string
	}{
		{"missing alias file", finding{Files: []string{"gone.yml"}, Aliases: 1}, nil, "no such file"},
		{"missing move file", finding{MoveFiles: []string{"gone.yml"}, Moves: 1}, nil, "no such file"},
		{"missing go.mod", finding{GoModFiles: []string{"nope/go.mod"}}, nil, "no such file"},
		{"branch", finding{Files: []string{"ci.yml"}, Aliases: 1}, func(w *world) {
			prev := git
			git = func(d string, a ...string) (string, error) {
				if a[0] == "checkout" {
					return "no", errors.New("exit 1")
				}
				return prev(d, a...)
			}
		}, "branch:"},
		{"add", finding{GoModFiles: []string{"go.mod"}}, func(w *world) {
			w.files["o/r"]["go.mod"] = "module x\n\ngo 1.27.2\n"
			prev := git
			git = func(d string, a ...string) (string, error) {
				if a[0] == "add" {
					return "no", errors.New("exit 1")
				}
				return prev(d, a...)
			}
		}, "add:"},
		{"push", finding{Files: []string{"ci.yml"}, Aliases: 1}, func(w *world) {
			push = func(string, string) (string, error) { return "denied", errors.New("exit 1") }
		}, "gitpush:"},
		{"body", finding{Files: []string{"ci.yml"}, Aliases: 1}, func(w *world) {
			prev := osCreateTempFile
			osCreateTempFile = func(string) (string, error) { return "", errors.New("disk full") }
			t.Cleanup(func() { osCreateTempFile = prev })
		}, "disk full"},
		{"tempdir", finding{Files: []string{"ci.yml"}, Aliases: 1}, func(w *world) {
			prev := osMkdirTemp
			osMkdirTemp = func(string, string) (string, error) { return "", errors.New("no space") }
			t.Cleanup(func() { osMkdirTemp = prev })
		}, "no space"},
		{"unchanged go.mod directive", finding{GoModFiles: []string{"go.mod"}}, func(w *world) {
			w.files["o/r"]["go.mod"] = "module x\n" // no directive at all: nothing to write
		}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pb, pm := osCreateTempFile, osMkdirTemp
			defer func() { osCreateTempFile, osMkdirTemp = pb, pm }()
			w := newWorld(t)
			w.files["o/r"] = map[string]string{".github/workflows/ci.yml": ci}
			if tc.prep != nil {
				tc.prep(w)
			}
			pr, err := openPR("o/r", tc.f, "1.27.3", "1.27.2")
			if tc.want == "" {
				if err != nil || pr != "" {
					t.Fatalf("pr=%q err=%v; want nothing to do", pr, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v; want %q", err, tc.want)
			}
		})
	}
}

func TestOpenPRCannotWriteAReadOnlyCheckout(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes through 0444")
	}
	w := newWorld(t)
	w.files["o/r"] = map[string]string{".github/workflows/ci.yml": "          go-version: stable\n          go-version: '1.27.2'\n", "go.mod": "module x\n\ngo 1.26.0\n"}
	prev := git
	git = func(d string, a ...string) (string, error) {
		out, err := prev(d, a...)
		if a[0] == "clone" {
			_ = filepath.Walk(a[len(a)-1], func(p string, i os.FileInfo, err error) error {
				if err == nil && !i.IsDir() {
					_ = os.Chmod(p, 0o444)
				}
				return nil
			})
		}
		return out, err
	}
	for _, f := range []finding{
		{Files: []string{"ci.yml"}, Aliases: 1},
		{MoveFiles: []string{"ci.yml"}, Moves: 1},
		{GoModFiles: []string{"go.mod"}},
		{Files: []string{"x"}, Aliases: 1, GoMod: "1.26.0"},
	} {
		if f.Files != nil && f.Files[0] == "x" {
			f.Files = nil // only the go.mod raise remains
			w.files["o/r"]["go.mod"] = "module x\n\ngo 1.26.0\n"
		}
		if f.GoModFiles != nil {
			w.files["o/r"]["go.mod"] = "module x\n\ngo 1.27.2\n"
		}
		if _, err := openPR("o/r", f, "1.27.3", "1.27.2"); err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Errorf("%+v: err = %v; want permission denied", f, err)
		}
	}
}
