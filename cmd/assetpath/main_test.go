// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// corpusAt builds a directory of n arXiv-shaped paper sub-directories under
// parent, named dir. The NAME of dir is the caller's choice on purpose: several
// tests below exist to show the name must not matter.
func corpusAt(t *testing.T, parent, dir string, n int) string {
	t.Helper()
	root := filepath.Join(parent, dir)
	for i := 0; i < n; i++ {
		id := filepath.Join(root, fmt.Sprintf("2401.%05d", 10000+i))
		if err := os.MkdirAll(id, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(id, "main.tex"),
			[]byte(`\documentclass{article}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// withRoots points the search at a tree of the test's own and restores it after.
func withRoots(t *testing.T, roots ...string) {
	t.Helper()
	prev := searchRoots
	searchRoots = func() []string { return roots }
	t.Cleanup(func() { searchRoots = prev })
}

// isolate points the registry at a temp home, so a test never reads or writes
// the real one.
func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

// runCmd drives the command the way the fleet's CLIs are driven and returns
// stdout, stderr and the status the process would exit with.
func runCmd(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	c := RootCmd()
	c.SetArgs(args)
	c.SetOut(&out)
	c.SetErr(&errb)
	err := c.Execute()
	code := 0
	if err != nil {
		var ee exitErr
		code = 2
		if errors.As(err, &ee) {
			code = ee.code
		}
		errb.WriteString(err.Error())
	}
	return out.String(), errb.String(), code
}

// ⛔ THE DEFECT THIS TOOL EXISTS FOR, AS A TEST.
//
// On 2026-11-10 a session looked for the arXiv corpus by searching for
// `*corpus*` and `*arxiv*`, found nothing, and reported that the machine did not
// have one. It was there all along — 174 papers in a shared scratch area, under
// a three-letter directory name holding neither word. That search could not
// have succeeded whatever the machine held.
//
// So: a corpus whose directory name matches nothing anyone would guess.
func TestACorpusIsFoundThoughItsNameSaysNothing(t *testing.T) {
	isolate(t)
	tmp := t.TempDir()
	want := corpusAt(t, tmp, "axc", 25)
	withRoots(t, tmp)

	out, errs, code := runCmd(t, "arxiv-corpus")
	if code != 0 {
		t.Fatalf("exit %d, want 0 (found); stderr=%s", code, errs)
	}
	if got := strings.TrimSpace(out); got != want {
		t.Errorf("resolved to %q, want %q", got, want)
	}
}

// ⛔ The control for the test above. With Min at 1 every directory holding a
// single paper would answer — a scratch copy included — and the caller would
// measure against it without ever knowing. Mutate Min to 1 and this is the test
// that goes red.
func TestAHandfulOfPapersIsNotACorpus(t *testing.T) {
	isolate(t)
	tmp := t.TempDir()
	corpusAt(t, tmp, "stray-copy", 3)
	withRoots(t, tmp)

	_, _, code := runCmd(t, "arxiv-corpus")
	if code != 1 {
		t.Errorf("exit %d, want 1: three papers are not a corpus", code)
	}
}

// ⛔⛔ THE WHOLE POINT OF THE EXIT CODES. A root that could not be read must
// never come back as "it is not here". That conflation IS the original defect,
// and a tool repeating it would be worse than no tool.
func TestAnUnreadableRootIsRefusedNotCalledAbsent(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads an unreadable directory, so this cannot be shown here")
	}
	isolate(t)
	tmp := t.TempDir()
	locked := filepath.Join(tmp, "locked")
	if err := os.MkdirAll(filepath.Join(locked, "inside"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	withRoots(t, tmp)

	_, errs, code := runCmd(t, "arxiv-corpus")
	if code == 1 {
		t.Fatalf("an unreadable root was reported as an absence:\n%s", errs)
	}
	if code != 2 {
		t.Fatalf("exit %d, want 2 (refused to decide); stderr=%s", code, errs)
	}
	if !strings.Contains(errs, "UNFOUND, not absent") {
		t.Errorf("the refusal does not say it is not an absence:\n%s", errs)
	}
}

// A name with no known shape is the same class of answer: this does not know
// what it is looking for, which is not the same as having looked.
func TestAnUnknownNameIsNeverAnAbsence(t *testing.T) {
	isolate(t)
	withRoots(t, t.TempDir())

	_, errs, code := runCmd(t, "no-such-asset")
	if code == 1 {
		t.Fatalf("an unknown name was reported as an absence:\n%s", errs)
	}
	if code != 2 {
		t.Fatalf("exit %d, want 2; stderr=%s", code, errs)
	}
	if !strings.Contains(errs, "--register") {
		t.Errorf("the refusal does not say how to fix it:\n%s", errs)
	}
}

// An empty, readable tree IS an absence and must be reported as one — otherwise
// exit 1 never happens and the 1/2 split is decoration.
func TestAnEmptyReadableTreeIsAnAbsence(t *testing.T) {
	isolate(t)
	withRoots(t, t.TempDir())

	_, _, code := runCmd(t, "arxiv-corpus")
	if code != 1 {
		t.Errorf("exit %d, want 1: an empty readable tree is a genuine absence", code)
	}
}

// Registering answers without walking the disk, which is what makes the second
// session cheap. The search roots point at an EMPTY tree, so a resolve that
// still succeeds can only have come from the registry.
func TestARegisteredAssetResolvesWithoutSearching(t *testing.T) {
	isolate(t)
	want := corpusAt(t, t.TempDir(), "somewhere-else", 25)
	withRoots(t, t.TempDir())

	if _, errs, code := runCmd(t, "arxiv-corpus", "--register", want); code != 0 {
		t.Fatalf("register exit %d: %s", code, errs)
	}
	out, errs, code := runCmd(t, "arxiv-corpus")
	if code != 0 {
		t.Fatalf("exit %d, want 0; stderr=%s", code, errs)
	}
	if got := strings.TrimSpace(out); got != want {
		t.Errorf("resolved to %q, want %q", got, want)
	}
}

// A registry entry pointing at something gone is stale, not an answer — and not
// an absence either: the search still runs.
func TestAStaleRegistryEntryFallsBackToTheSearch(t *testing.T) {
	isolate(t)
	tmp := t.TempDir()
	want := corpusAt(t, tmp, "axc", 25)
	gone := filepath.Join(t.TempDir(), "was-here")
	if err := os.MkdirAll(gone, 0o755); err != nil {
		t.Fatal(err)
	}
	withRoots(t, tmp)

	if _, errs, code := runCmd(t, "arxiv-corpus", "--register", gone); code != 0 {
		t.Fatalf("register exit %d: %s", code, errs)
	}
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}

	out, errs, code := runCmd(t, "arxiv-corpus")
	if code != 0 {
		t.Fatalf("exit %d, want 0; stderr=%s", code, errs)
	}
	if got := strings.TrimSpace(out); got != want {
		t.Errorf("resolved to %q, want the searched %q", got, want)
	}
	if !strings.Contains(errs, "which is gone") {
		t.Errorf("the stale entry was not reported:\n%s", errs)
	}
}

// ⛔⛔ AMBIGUITY IS NOT AN ANSWER, and this is the test the machine wrote.
//
// The first real run of this command returned THREE sibling directories of the
// right shape where one was expected. The draft at that moment registered
// matches[0] — first in alphabetical order, and not the right one —
// and would have pointed every later session at a corpus nobody chose, in
// silence. Choosing for the caller is this command's own defect wearing a
// different hat.
func TestSeveralDirectoriesOfTheRightShapeAreNotAnAnswer(t *testing.T) {
	isolate(t)
	tmp := t.TempDir()
	a := corpusAt(t, tmp, "axc-one", 25)
	b := corpusAt(t, tmp, "axc-two", 25)
	withRoots(t, tmp)

	out, errs, code := runCmd(t, "arxiv-corpus")
	if code != 2 {
		t.Fatalf("exit %d, want 2: two candidates are an ambiguity, not a find", code)
	}
	for _, want := range []string{a, b} {
		if !strings.Contains(out, want) {
			t.Errorf("the ambiguity does not list %s:\n%s", want, out)
		}
	}
	if !strings.Contains(errs, "--register") {
		t.Errorf("the refusal does not say how to settle it:\n%s", errs)
	}

	// ⛔ And nothing was recorded: a guess written to the registry would be
	// worse than the ambiguity, because the next session would never see it.
	out2, _, code2 := runCmd(t, "arxiv-corpus")
	if code2 != 2 {
		t.Fatalf("the second run exited %d: a choice was recorded behind the refusal:\n%s", code2, out2)
	}
}

// A find records itself, so the walk happens once per machine and not once per
// session. Asserted by resolving again with the roots emptied.
func TestAFindIsRecordedSoTheNextSessionDoesNotWalk(t *testing.T) {
	isolate(t)
	tmp := t.TempDir()
	want := corpusAt(t, tmp, "axc", 25)
	withRoots(t, tmp)
	if _, errs, code := runCmd(t, "arxiv-corpus"); code != 0 {
		t.Fatalf("first resolve exit %d: %s", code, errs)
	}

	withRoots(t, t.TempDir()) // nothing to find any more
	out, errs, code := runCmd(t, "arxiv-corpus")
	if code != 0 {
		t.Fatalf("second resolve exit %d, want 0; stderr=%s", code, errs)
	}
	if got := strings.TrimSpace(out); got != want {
		t.Errorf("second resolve gave %q, want the recorded %q", got, want)
	}
}
