package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ⛔ THE CACHE IS THE REASON THIS COMMAND IS CHEAP ENOUGH TO RUN DAILY, and
// nothing held it. Eleven modules are stated over thirteen pages and a module
// named on two pages must be asked for once: `git ls-remote` is a network round
// trip per module, which is what makes this check runnable with no token at all
// instead of against the REST budget every session and Renovate share. Deleting
// the cache changes no verdict and no exit code -- only the number of times we
// go out, which no other test can see.
func TestAModuleNamedOnTwoPagesIsAskedForOnce(t *testing.T) {
	dir := t.TempDir()
	page := "[Repository →](https://github.com/go-widgets/window)\nLatest tag: **v0.89.0** (`x`, 2026-11-01).\n"
	var files []string
	for _, name := range []string{"a.md", "b.md", "c.md"} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(page), 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, p)
	}
	asked := 0
	counting := func(_, _ string) (Release, error) {
		asked++
		return rel("window", "v0.89.0")["window"], nil
	}
	var out, errb bytes.Buffer
	if rc := run(options{org: "go-widgets", files: files}, &out, &errb, counting); rc != 0 {
		t.Fatalf("rc %d\n%s", rc, out.String())
	}
	if asked != 1 {
		t.Fatalf("asked %d times for one module over three pages, want 1", asked)
	}
	if !strings.Contains(out.String(), "3 file(s), 3 cell(s), 1 module(s)") {
		t.Fatalf("three claims, one module: %q", out.String())
	}
}

// ⛔ A CALL THAT FAILED MUST NOT READ AS "THIS MODULE HAS NO RELEASE". Both
// answers come back as an error from latestTag, and the one that matters is
// told apart by its text: without the guard, a failed `git ls-remote` returns
// empty output, `highest("")` says there is no release tag, and every module
// reads as unreleased the moment the network is down -- which reports every
// page as current, because nothing can contradict it any more.
func TestAFailedLsRemoteIsNotReportedAsNoRelease(t *testing.T) {
	old := lsRemote
	lsRemote = func(string) ([]byte, error) { return nil, os.ErrPermission }
	defer func() { lsRemote = old }()

	_, err := latestTag("go-widgets", "window")
	if err == nil {
		t.Fatal("a failed call must be an error")
	}
	if !strings.Contains(err.Error(), "git ls-remote") {
		t.Fatalf("the error must name the call that failed: %v", err)
	}
	if strings.Contains(err.Error(), "no release") {
		t.Fatalf("a call that failed was reported as an absent release: %v", err)
	}

	// The other side: output that came back IS parsed, so the test above is not
	// passing on a latestTag that always fails.
	lsRemote = func(string) ([]byte, error) {
		return []byte("deadbeef\trefs/tags/v0.9.0\nfeedface\trefs/tags/v0.10.0\n"), nil
	}
	if got, err := latestTag("go-widgets", "window"); err != nil || got.Tag != "v0.10.0" {
		t.Fatalf("got %+v, %v; want v0.10.0 (semver, not text)", got, err)
	}
}

// ⛔ A FILE THAT COULD NOT BE READ EXITS 2 -- AND SO DOES A FILE WITH NO CELL,
// which is why asserting the code alone holds nothing: both answers are 2, so
// the guard can be deleted and the suite stays green while the command stops
// saying WHICH file it could not open. The message is the part that differs.
func TestAnUnreadableFileIsNamedAndNotMistakenForAnEmptyPage(t *testing.T) {
	var out, errb bytes.Buffer
	missing := filepath.Join(t.TempDir(), "gone.md")
	rc := run(options{org: "go-widgets", files: []string{missing}}, &out, &errb,
		func(string, string) (Release, error) { return rel("window", "v1.0.0")["window"], nil })
	if rc != 2 {
		t.Fatalf("rc %d, want 2", rc)
	}
	if !strings.Contains(errb.String(), "gone.md") {
		t.Fatalf("the file that could not be read must be named; errb %q", errb.String())
	}
	if strings.Contains(out.String(), "file(s),") {
		t.Fatalf("a run that could not read its input must not print a summary: %q", out.String())
	}
}

// ⛔ AND THE USAGE ERROR MUST NOT TURN INTO A RUN OVER NO FILES. Both end in a
// non-zero status -- `run` with an empty file list reads no cell and exits 2 --
// so the status says nothing about whether the arguments were validated. What
// differs is that one of them printed a summary about nothing.
func TestMissingArgumentsAreAUsageErrorAndRunNothing(t *testing.T) {
	c := RootCmd()
	var out, errb bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&errb)
	c.SetArgs([]string{"--org", "go-widgets"})
	err := c.Execute()
	if err == nil {
		t.Fatal("no FILE argument must be refused")
	}
	// The message travels in the error, which the caller prints; what matters
	// here is that it is the USAGE error and not a status from a run.
	if !strings.Contains(err.Error(), "usage: tagcells") {
		t.Fatalf("the refusal must say what to type; err %q, out %q, errb %q", err, out.String(), errb.String())
	}
	if strings.Contains(out.String(), "file(s),") {
		t.Fatalf("it reported on zero files instead of refusing: %q", out.String())
	}
}
