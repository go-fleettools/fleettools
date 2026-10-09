package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ⛔ A FLEET PINNED TO AN EXACT VERSION CANNOT MOVE. gopin replaced the
// `stable` alias with a version, which was the right job — and then nothing
// could move that version again. On 2026-10-09 govulncheck reported three
// standard-library vulnerabilities in go1.27.1, fixed in go1.27.2, reachable
// from all five go-pkgx repositories, and the tool that pinned them reported
// "5 already explicit" and stopped.
func TestInspectFindsTheExactVersionBeingMovedFrom(t *testing.T) {
	defer stubGH(t, map[string]string{
		".github/workflows/ci.yml":      "          go-version: '1.27.1'\n        with: { go-version: 1.27.1 }\n",
		".github/workflows/release.yml": "          go-version: \"1.27.1\"\n",
	})()

	f := inspect("o/r", "1.27.2", "1.27.1")
	if f.Err != "" {
		t.Fatalf("inspect: %s", f.Err)
	}
	if f.Aliases != 0 {
		t.Errorf("there is no alias here, but Aliases = %d", f.Aliases)
	}
	// Every quoting the fleet actually uses: bare, single and double.
	if f.Moves != 3 {
		t.Errorf("found %d occurrences, want 3: %+v", f.Moves, f)
	}
	if len(f.MoveFiles) != 2 {
		t.Errorf("found them in %v, want both workflows", f.MoveFiles)
	}
}

// ⛔⛔ THE EXACTNESS IS THE SAFETY. "Move anything older" cannot tell a stale
// pin from a deliberate one, and this fleet has removed a deliberate loong64
// pin as an outlier before. A repository held at 1.22 on purpose must not be
// a candidate for a 1.27.1 → 1.27.2 sweep.
func TestADeliberatelyHeldPinIsNotACandidate(t *testing.T) {
	defer stubGH(t, map[string]string{
		".github/workflows/ci.yml": "          go-version: '1.22.0'\n",
	})()

	f := inspect("o/r", "1.27.2", "1.27.1")
	if f.Moves != 0 || len(f.MoveFiles) != 0 {
		t.Errorf("a pin at 1.22.0 was treated as a 1.27.1 to move: %+v", f)
	}
	// It is still REPORTED, as it always was: invisible is not the same as
	// left alone.
	if len(f.Literals) != 1 || f.Literals[0] != "1.22.0" {
		t.Errorf("the held pin is not reported at all: %+v", f)
	}
}

// AND A PREFIX IS NOT A MATCH. `1.27.1` must not match `1.27.10`, which is a
// real version number one patch release away from existing.
func TestAVersionIsNotMatchedByItsPrefix(t *testing.T) {
	defer stubGH(t, map[string]string{
		".github/workflows/ci.yml": "          go-version: '1.27.10'\n",
	})()

	f := inspect("o/r", "1.27.2", "1.27.1")
	// ⛔ THE POSITIVE CONTROL FIRST. Written without it, this test passed
	// because the fixture was wrong and inspect never read a file at all —
	// Moves was 0 for the one reason that proves nothing.
	if f.Err != "" {
		t.Fatalf("inspect read nothing, so a zero here means nothing: %s", f.Err)
	}
	if len(f.Literals) != 1 || f.Literals[0] != "1.27.10" {
		t.Fatalf("the fixture was not read: %+v", f)
	}
	if f.Moves != 0 {
		t.Errorf("1.27.1 matched inside 1.27.10: %+v", f)
	}
}

func TestOpenMovesTheExactVersionAndLeavesOthers(t *testing.T) {
	r := &fakeRepo{files: map[string]string{
		".github/workflows/ci.yml": "          go-version: '1.27.1'\n" +
			"        with: { go-version: 1.27.1 }\n" +
			"          go-version: '1.22.0'\n",
	}}
	defer r.install(t)()

	f := finding{Repo: "o/r", MoveFiles: []string{"ci.yml"}, Moves: 2}
	if err := open("o/r", f, "1.27.2", "1.27.1"); err != nil {
		t.Fatalf("open: %v", err)
	}
	wf := r.read(t, ".github/workflows/ci.yml")
	if strings.Count(wf, "'1.27.2'") != 2 {
		t.Errorf("both occurrences should have moved:\n%s", wf)
	}
	if strings.Contains(wf, "1.27.1") {
		t.Errorf("an old pin survived:\n%s", wf)
	}
	// ⛔ THE HELD PIN IS UNTOUCHED. Without this the test would pass on a
	// replacement that rewrote every version in the file.
	if !strings.Contains(wf, "'1.22.0'") {
		t.Errorf("the deliberately held pin was rewritten:\n%s", wf)
	}
}

// A FILE CAN HOLD BOTH: one job saying `stable` beside another naming the old
// version is exactly the mixture that made `stable` worth replacing. The
// second pass must read what the first wrote, not the original bytes.
func TestAFileHoldingBothAnAliasAndAnOldPinGetsBoth(t *testing.T) {
	r := &fakeRepo{files: map[string]string{
		".github/workflows/ci.yml": "          go-version: stable\n          go-version: '1.27.1'\n",
	}}
	defer r.install(t)()

	f := finding{Repo: "o/r", Files: []string{"ci.yml"}, Aliases: 1, MoveFiles: []string{"ci.yml"}, Moves: 1}
	if err := open("o/r", f, "1.27.2", "1.27.1"); err != nil {
		t.Fatalf("open: %v", err)
	}
	wf := r.read(t, ".github/workflows/ci.yml")
	if strings.Contains(wf, "stable") {
		t.Errorf("the alias survived:\n%s", wf)
	}
	if strings.Contains(wf, "1.27.1") {
		t.Errorf("the old pin survived:\n%s", wf)
	}
	if strings.Count(wf, "'1.27.2'") != 2 {
		t.Errorf("want both lines at 1.27.2:\n%s", wf)
	}
}

// ⛔ THE TITLE MUST NOT CLAIM THE WRONG JOB. A pull request titled "instead of
// `stable`" against a repository that never said `stable` makes a claim its
// reviewer can check and find false — and then nothing else in the body is
// worth their time either.
func TestTheTitleNamesTheJobItActuallyDoes(t *testing.T) {
	move := prTitle(finding{Moves: 2}, "1.27.2", "1.27.1")
	if strings.Contains(move, "stable") {
		t.Errorf("a repin was titled as an alias replacement: %q", move)
	}
	if !strings.Contains(move, "1.27.1") || !strings.Contains(move, "1.27.2") {
		t.Errorf("the title names neither end of the move: %q", move)
	}
	alias := prTitle(finding{Aliases: 1}, "1.27.2", "")
	if !strings.Contains(alias, "stable") {
		t.Errorf("an alias replacement lost its subject: %q", alias)
	}
}

// -from REFUSES WHAT IT CANNOT MEAN: the same version on both ends, a
// malformed one, and a move BACKWARDS — which is a different act, and not one
// to perform across a fleet because a digit was mistyped.
func TestFromRefusesWhatItCannotMean(t *testing.T) {
	for name, args := range map[string][]string{
		"same version": {"-version", "1.27.1", "-from", "1.27.1"},
		"malformed":    {"-version", "1.27.2", "-from", "not-a-version"},
		"backwards":    {"-version", "1.27.1", "-from", "1.27.2"},
	} {
		var out, errb strings.Builder
		if code := run(args, &out, &errb); code != 2 {
			t.Errorf("%s: code=%d, want 2; stderr=%q", name, code, errb.String())
		}
		if errb.Len() == 0 {
			t.Errorf("%s: refused in silence", name)
		}
	}
}

// ⛔ A SUMMARY MUST NOT CONTRADICT ITS OWN DETAIL. The totals were printed
// off one shared counter, so the first -from run reported
//
//	REPIN   go-pkgx/bottle: 8 × 1.27.1 -> 1.27.2 in ci.yml
//	…
//	5 read · 5 hold the alias · 0 already explicit · 0 unreadable
//
// about five repositories that held no alias at all. The summary is the line
// that gets quoted, so a false one travels further than the detail above it.
func TestTheSummaryDoesNotCallARepinAnAlias(t *testing.T) {
	defer stubGH(t, map[string]string{
		".github/workflows/ci.yml": "          go-version: '1.27.1'\n",
	})()
	t.Setenv("GOPIN_LIST_UNUSED", "")

	var out, errb strings.Builder
	code := run([]string{"-version", "1.27.2", "-from", "1.27.1", "-list", writeList(t, "o/r")}, &out, &errb)
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errb.String())
	}
	got := out.String()
	if !strings.Contains(got, "REPIN") {
		t.Fatalf("the repin was not detected at all, so the summary proves nothing:\n%s", got)
	}
	if strings.Contains(got, "1 hold the alias") {
		t.Errorf("a repin was counted as holding the alias:\n%s", got)
	}
	if !strings.Contains(got, "0 hold the alias") {
		t.Errorf("the alias count is not zero where there is no alias:\n%s", got)
	}
	if !strings.Contains(got, "1 to move from 1.27.1") {
		t.Errorf("the summary does not count the move:\n%s", got)
	}
}

// writeList puts repository names in a file, because -list reads one and
// stdin is not available to a test running beside others.
func writeList(t *testing.T, repos ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "list.txt")
	if err := os.WriteFile(p, []byte(strings.Join(repos, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}
