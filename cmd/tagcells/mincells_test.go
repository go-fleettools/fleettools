package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// claimless writes one page that holds a claim and one that holds none, and
// returns both paths. Two files, one cell: the shape of every multi-page run.
func claimless(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	withClaim := filepath.Join(dir, "window.md")
	without := filepath.Join(dir, "_index.md")
	page := "[Repository →](https://github.com/go-widgets/window)\n" +
		"Latest tag: **v0.89.0** (`git ls-remote --tags`, 2026-11-01).\n"
	if err := os.WriteFile(withClaim, []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(without, []byte("# The modules\n\nA page that states no version.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return withClaim, without
}

// ⛔ THE RUN THAT READS TEN CLAIMS OUT OF ELEVEN IS THE ONE THIS COMMAND USED
// TO CALL A PASS. Exit 2 asks "did I read NOTHING?", which is the right
// question about one file and nearly no question about thirteen: a sentence
// reworded past the shapes read here leaves the other pages reporting
// cheerfully and the unread one indistinguishable from a page with nothing to
// state. So the page that holds no claim is NAMED, every run, and a caller that
// knows how many there should be can say so.
func TestAPageWithNoClaimIsNamedEvenWhenTheRunSucceeds(t *testing.T) {
	with, without := claimless(t)
	var out, errb bytes.Buffer
	current := func(_, repo string) (Release, error) { return rel("window", "v0.89.0")["window"], nil }
	if rc := run(options{org: "go-widgets", files: []string{with, without}}, &out, &errb, current); rc != 0 {
		t.Fatalf("rc %d, want 0: the one claim there is, is current\n%s", rc, out.String())
	}
	if !strings.Contains(out.String(), "_index.md  no version claim of go-widgets found on this page") {
		t.Fatalf("the claim-less page must be named; out %q", out.String())
	}
	if strings.Contains(out.String(), "window.md  no version claim") {
		t.Fatalf("the page that does hold a claim must not be named; out %q", out.String())
	}
}

func TestMinCellsRefusesARunThatLostAClaim(t *testing.T) {
	with, without := claimless(t)
	current := func(_, repo string) (Release, error) { return rel("window", "v0.89.0")["window"], nil }

	// One cell is what these two pages hold, so --min-cells 1 passes...
	var out, errb bytes.Buffer
	if rc := run(options{org: "go-widgets", minCells: 1, files: []string{with, without}}, &out, &errb, current); rc != 0 {
		t.Fatalf("rc %d, want 0; err %q", rc, errb.String())
	}
	// ...and the same pages, asked for the two they held yesterday, refuse.
	out.Reset()
	errb.Reset()
	if rc := run(options{org: "go-widgets", minCells: 2, files: []string{with, without}}, &out, &errb, current); rc != 2 {
		t.Fatalf("rc %d, want 2: a claim that used to be stated is gone", rc)
	}
	if !strings.Contains(errb.String(), "1 cell(s) read, --min-cells says 2") {
		t.Fatalf("the refusal must give both numbers; errb %q", errb.String())
	}
}

// The default must be exactly today's rule, or every caller in the fleet
// changes meaning the day this lands.
func TestMinCellsDefaultsToTheOldRule(t *testing.T) {
	with, without := claimless(t)
	var out, errb bytes.Buffer
	current := func(_, repo string) (Release, error) { return rel("window", "v0.89.0")["window"], nil }
	if rc := run(options{org: "go-widgets", files: []string{with, without}}, &out, &errb, current); rc != 0 {
		t.Fatalf("rc %d, want 0 with no --min-cells", rc)
	}
	// And a stale claim still outranks it: exit 1 is about the page's contents.
	out.Reset()
	old := func(_, repo string) (Release, error) { return rel("window", "v0.90.0")["window"], nil }
	if rc := run(options{org: "go-widgets", minCells: 1, files: []string{with, without}}, &out, &errb, old); rc != 1 {
		t.Fatalf("rc %d, want 1: --min-cells was satisfied and the claim is stale", rc)
	}
}

// ⛔ AND THE ORDER OF THE TWO REFUSALS IS PART OF THE CONTRACT: a run whose
// tags could not be read must not be reported as a lost claim. "I could not
// ask" and "the page no longer says" are different failures, and the first
// one hides the second -- it reads zero cells for a reason that has nothing to
// do with the pages.
func TestAnUnreadTagOutranksMinCells(t *testing.T) {
	with, without := claimless(t)
	var out, errb bytes.Buffer
	broken := func(_, repo string) (Release, error) { return Release{}, os.ErrPermission }
	if rc := run(options{org: "go-widgets", minCells: 99, files: []string{with, without}}, &out, &errb, broken); rc != 2 {
		t.Fatalf("rc %d, want 2", rc)
	}
	if strings.Contains(errb.String(), "--min-cells") {
		t.Fatalf("an unread tag must not be reported as a lost claim; errb %q", errb.String())
	}
}

func TestMinCellsIsAFlag(t *testing.T) {
	var errb bytes.Buffer
	o, code, proceed := parseArgs([]string{"--org", "go-widgets", "--min-cells", "11", "a.md"}, &errb)
	if code != 0 || !proceed || o.minCells != 11 {
		t.Fatalf("code %d proceed %v minCells %d err %q", code, proceed, o.minCells, errb.String())
	}
}
