package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// failAt is a temp file that does each operation FOR REAL and then reports the
// error the test asked for at one named step.
//
// ⛔ THE REAL OPERATION FIRST, ALWAYS. A fake whose Close() returns an error
// without closing the descriptor underneath it cost a day on windows-latest
// once: Windows refuses to delete an OPEN file, so the cleanup that this test
// exists to check failed for a reason the fake invented, green on macOS and
// Linux. A fake that reports a failure must keep the real semantics of the call
// it is standing in for.
type failAt struct {
	*os.File
	step string
	err  error
}

func (f failAt) Write(p []byte) (int, error) {
	n, err := f.File.Write(p)
	if f.step == "write" {
		return n, f.err
	}
	return n, err
}

func (f failAt) Chmod(m os.FileMode) error {
	err := f.File.Chmod(m)
	if f.step == "chmod" {
		return f.err
	}
	return err
}

func (f failAt) Close() error {
	err := f.File.Close()
	if f.step == "close" {
		return f.err
	}
	return err
}

// page writes one file with known content and returns its path and content.
func onePage(t *testing.T) (string, string) {
	t.Helper()
	const before = "[Repository →](https://github.com/go-widgets/window)\nLatest tag: **v0.89.0** (`x`, 2026-11-01).\n"
	p := filepath.Join(t.TempDir(), "window.md")
	if err := os.WriteFile(p, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	return p, before
}

// leftovers names every temp file the implementation did not clean up.
func leftovers(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var left []string
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".tagcells-") {
			left = append(left, e.Name())
		}
	}
	return left
}

// ⛔ WHAT MUST BE TRUE OF EVERY FAILURE: the page is UNCHANGED, the error is
// returned, and nothing is left behind. A half-written page is worse than a
// stale one -- the stale page is still a correct document about an older tag.
func TestEveryStepOfAnAtomicWriteFailsWithoutTouchingThePage(t *testing.T) {
	boom := errors.New("boom")
	for _, step := range []string{"write", "chmod", "close"} {
		p, before := onePage(t)
		old := newTempFile
		newTempFile = func(dir, pattern string) (tempFile, error) {
			f, err := os.CreateTemp(dir, pattern)
			if err != nil {
				return nil, err
			}
			return failAt{File: f, step: step, err: boom}, nil
		}
		err := writeAtomic(p, []byte("rewritten\n"))
		newTempFile = old

		if !errors.Is(err, boom) {
			t.Fatalf("%s: err %v, want boom", step, err)
		}
		if got, _ := os.ReadFile(p); string(got) != before {
			t.Fatalf("%s: the page was changed:\n%s", step, got)
		}
		if left := leftovers(t, filepath.Dir(p)); len(left) != 0 {
			t.Fatalf("%s: left behind %v", step, left)
		}
	}
}

func TestAnAtomicWriteThatCannotCreateItsTempFileChangesNothing(t *testing.T) {
	p, before := onePage(t)
	boom := errors.New("no temp file")
	old := newTempFile
	newTempFile = func(string, string) (tempFile, error) { return nil, boom }
	err := writeAtomic(p, []byte("rewritten\n"))
	newTempFile = old

	if !errors.Is(err, boom) {
		t.Fatalf("err %v, want %v", err, boom)
	}
	if got, _ := os.ReadFile(p); string(got) != before {
		t.Fatalf("the page was changed:\n%s", got)
	}
}

// The rename is the step that cannot be injected, because it is the step that
// makes the write visible: a directory in the target's place fails it on every
// platform, and the temp file must still be gone.
func TestAnAtomicWriteThatCannotRenameLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "window.md")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "keep"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(target, []byte("rewritten\n")); err == nil {
		t.Fatal("renaming a file over a non-empty directory must fail")
	}
	if left := leftovers(t, dir); len(left) != 0 {
		t.Fatalf("left behind %v", left)
	}
}

func TestAMissingPageIsNotCreated(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "nope.md")
	if err := writeAtomic(gone, []byte("x")); err == nil {
		t.Fatal("writeAtomic must refuse a path it cannot stat")
	}
	if _, err := os.Stat(gone); !os.IsNotExist(err) {
		t.Fatalf("it created the file: %v", err)
	}
}

// ⛔ AND THE RUN MUST REFUSE, not report success over a page it could not
// write: --write turns the report into an action, and an action that failed
// while the summary still says "rewritten" is the worst of the three outcomes.
func TestAFailedWriteIsExitTwoAndNamesTheError(t *testing.T) {
	p, before := onePage(t)
	boom := errors.New("disk is a lie")
	old := newTempFile
	newTempFile = func(string, string) (tempFile, error) { return nil, boom }
	defer func() { newTempFile = old }()

	var out, errb bytes.Buffer
	rc := run(options{org: "go-widgets", write: true, files: []string{p}}, &out, &errb,
		func(string, string) (string, error) { return "v0.90.0", nil })
	if rc != 2 {
		t.Fatalf("rc %d, want 2\n%s", rc, out.String())
	}
	if !strings.Contains(errb.String(), "disk is a lie") {
		t.Fatalf("the error must be named; errb %q", errb.String())
	}
	if got, _ := os.ReadFile(p); string(got) != before {
		t.Fatalf("the page was changed:\n%s", got)
	}
}
