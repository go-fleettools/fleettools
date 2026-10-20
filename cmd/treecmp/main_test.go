package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runCmd(t *testing.T, args ...string) (string, int) {
	t.Helper()
	var out bytes.Buffer
	c := RootCmd()
	c.SetOut(&out)
	c.SetErr(&out)
	c.SetArgs(args)
	// Run first: `return out.String(), execute(c)` evaluates left to right
	// and returns the output from BEFORE the command ran.
	code := execute(c)
	return out.String(), code
}

func TestIdenticalTreesAgree(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	for _, d := range []string{a, b} {
		write(t, d, "x/one.txt", "1")
		write(t, d, "two.bin", strings.Repeat("z", 200000)) // more than one chunk
	}
	if _, code := runCmd(t, a, b); code != 0 {
		t.Fatalf("exit %d for identical trees", code)
	}
}

// Positive witnesses: each kind of difference must be seen.
func TestEveryKindOfDifferenceIsSeen(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	write(t, a, "same", "s")
	write(t, b, "same", "s")
	write(t, a, "late", strings.Repeat("a", 100000)+"X") // differs after the first chunk
	write(t, b, "late", strings.Repeat("a", 100000)+"Y")
	write(t, a, "gone", "g")
	write(t, b, "extra", "e")

	r, err := Compare(a, b, true)
	if err != nil {
		t.Fatal(err)
	}
	if r.Same != 1 || r.Differ != 1 || r.Missing != 1 || r.Extra != 1 {
		t.Errorf("got %+v", r)
	}
	if _, code := runCmd(t, a, b); code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	// Without --both an extra file in the reference is not a difference.
	r, _ = Compare(a, b, false)
	if r.Extra != 0 {
		t.Errorf("extra counted without --both: %+v", r)
	}
}

func TestAnEmptyTreeIsNotAMatch(t *testing.T) {
	if _, code := runCmd(t, t.TempDir(), t.TempDir()); code != 2 {
		t.Errorf("exit %d for an empty generated tree, want 2", code)
	}
}

func TestUsageErrorsExitTwo(t *testing.T) {
	if _, code := runCmd(t, "only-one"); code != 2 {
		t.Errorf("one argument: exit %d, want 2", code)
	}
	if _, code := runCmd(t, "-h"); code != 0 {
		t.Errorf("-h: exit %d, want 0", code)
	}
}
