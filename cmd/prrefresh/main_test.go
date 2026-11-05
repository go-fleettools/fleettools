package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-fleettools/fleettools/internal/fleet"
)

func withFakeGH(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/gh", []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	old := fleet.Backoff
	fleet.Backoff = func(int) time.Duration { return 0 }
	t.Cleanup(func() { fleet.Backoff = old })
}

// pr builds the JSON of one pull request. mergeable is spliced in raw so a test
// can pass the literal null GitHub sends while it is still computing.
func pr(number int, state, mergeable, headSHA string) string {
	return `{"number":` + itoa(number) + `,"state":"` + state + `","mergeable":` + mergeable +
		`,"base":{"ref":"main","sha":"base1111"},"head":{"ref":"topic","sha":"` + headSHA + `"}}`
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// behindRepo is a fake gh for one pull request that is behind its base.
func behindRepo(pullJSON, behindBy, updateBody string) string {
	return `
case "$*" in
  *update-branch*) ` + updateBody + ` ;;
  *compare*) echo '{"status":"behind","behind_by":` + behindBy + `}' ;;
  *pulls/7) echo '` + pullJSON + `' ;;
  *) echo "unexpected: $*" >&2; exit 1 ;;
esac`
}

// ⛔ The case the command exists for. A pull request in conflict with its base
// gets NO workflow run — not a failing one, none — so it keeps whatever green
// it had before the conflict and reads as ready.
func TestAConflictingPullRequestIsNamedAndNotUpdated(t *testing.T) {
	withFakeGH(t, behindRepo(pr(7, "open", "false", "head1111"), "3",
		`echo "update-branch must NOT be called for a conflicting PR" >&2; exit 1`))
	var out, errOut strings.Builder
	code := run(&out, &errOut, []string{"acme/one", "7"})
	if code != 1 {
		t.Errorf("exit = %d, want 1 (it needs a person): %s", code, errOut.String())
	}
	got := out.String()
	if !strings.Contains(got, "CONFLICT  #7") {
		t.Errorf("the pull request is not named as conflicting:\n%s", got)
	}
	if !strings.Contains(got, "no workflow run will be produced") {
		t.Errorf("the consequence is not stated:\n%s", got)
	}
}

// ⛔ GitHub answers mergeable:null while a background job computes it, and the
// documented remedy is to ask again. Reading null as false would report a
// conflict for every pull request asked about too soon.
func TestANullMergeableIsAskedAgainAndNotReadAsAConflict(t *testing.T) {
	dir := t.TempDir()
	script := `
case "$*" in
  *update-branch*) echo '{"message":"ok"}' ;;
  *compare*) echo '{"status":"behind","behind_by":2}' ;;
  *pulls/7)
    n=$(cat "` + dir + `/n" 2>/dev/null || echo 0)
    echo $((n+1)) > "` + dir + `/n"
    if [ "$n" -lt 2 ]; then
      echo '` + pr(7, "open", "null", "head1111") + `'
    else
      echo '` + pr(7, "open", "true", "head1111") + `'
    fi ;;
  *) echo "unexpected: $*" >&2; exit 1 ;;
esac`
	withFakeGH(t, script)
	var out, errOut strings.Builder
	code := run(&out, &errOut, []string{"--wait", "30s", "acme/one", "7"})
	if code != 0 {
		t.Errorf("exit = %d, want 0 (it was refreshed once the answer arrived): %s\n%s",
			code, errOut.String(), out.String())
	}
	if got := out.String(); !strings.Contains(got, "REFRESHED #7") {
		t.Errorf("the pull request was not refreshed:\n%s", got)
	}
}

// ⛔ "Could not ask" is not "the answer is no". A mergeability that never
// arrives leaves the pull request unjudged and exits 2, distinct from the
// conflict verdict.
func TestAMergeabilityThatNeverArrivesIsUnjudgedAndNotAConflict(t *testing.T) {
	withFakeGH(t, behindRepo(pr(7, "open", "null", "head1111"), "3",
		`echo "update-branch must NOT be called" >&2; exit 1`))
	var out, errOut strings.Builder
	code := run(&out, &errOut, []string{"--wait", "0s", "acme/one", "7"})
	if code != 2 {
		t.Errorf("exit = %d, want 2 (it could not be judged): %s", code, errOut.String())
	}
	got := out.String()
	if !strings.Contains(got, "UNJUDGED  #7") {
		t.Errorf("the pull request is not named as unjudged:\n%s", got)
	}
	if strings.Contains(got, "CONFLICT") {
		t.Errorf("an unanswered question was reported as a conflict:\n%s", got)
	}
}

// A branch that is not behind its base is left alone, and update-branch is not
// called at all — calling it would write a merge commit for nothing.
func TestABranchThatIsNotBehindIsLeftAlone(t *testing.T) {
	withFakeGH(t, behindRepo(pr(7, "open", "true", "head1111"), "0",
		`echo "update-branch must NOT be called for a current PR" >&2; exit 1`))
	var out, errOut strings.Builder
	code := run(&out, &errOut, []string{"acme/one", "7"})
	if code != 0 {
		t.Errorf("exit = %d, want 0: %s", code, errOut.String())
	}
	if got := out.String(); !strings.Contains(got, "CURRENT   #7") {
		t.Errorf("the pull request is not named as current:\n%s", got)
	}
}

// ⛔ On a machine where other sessions push, a head that moved between the read
// and the update must stop the write rather than merge into a head nobody
// looked at. The verdict is told from the head itself, not from the message.
func TestAHeadThatMovedStopsTheWriteAndIsNamed(t *testing.T) {
	dir := t.TempDir()
	script := `
case "$*" in
  *update-branch*) echo '{"message":"expected_head_sha does not match"}' >&2; exit 1 ;;
  *compare*) echo '{"status":"behind","behind_by":2}' ;;
  *pulls/7)
    n=$(cat "` + dir + `/n" 2>/dev/null || echo 0)
    echo $((n+1)) > "` + dir + `/n"
    if [ "$n" -lt 2 ]; then
      echo '` + pr(7, "open", "true", "head1111") + `'
    else
      echo '` + pr(7, "open", "true", "head2222") + `'
    fi ;;
  *) echo "unexpected: $*" >&2; exit 1 ;;
esac`
	withFakeGH(t, script)
	var out, errOut strings.Builder
	code := run(&out, &errOut, []string{"acme/one", "7"})
	if code != 1 {
		t.Errorf("exit = %d, want 1: %s", code, errOut.String())
	}
	got := out.String()
	if !strings.Contains(got, "MOVED     #7") {
		t.Errorf("the moved head is not named:\n%s", got)
	}
	if !strings.Contains(got, "nothing was written") {
		t.Errorf("the report does not say the write did not happen:\n%s", got)
	}
}

// ⛔ --dry-run must not put a pull request in the bucket that means it was
// written to. The bucket is what a reader scans.
func TestADryRunReportsBehindAndNotRefreshed(t *testing.T) {
	withFakeGH(t, behindRepo(pr(7, "open", "true", "head1111"), "5",
		`echo "update-branch must NOT be called under --dry-run" >&2; exit 1`))
	var out, errOut strings.Builder
	code := run(&out, &errOut, []string{"--dry-run", "acme/one", "7"})
	if code != 1 {
		t.Errorf("exit = %d, want 1 (there is work to do): %s", code, errOut.String())
	}
	got := out.String()
	if !strings.Contains(got, "BEHIND    #7") {
		t.Errorf("the pull request is not named as behind:\n%s", got)
	}
	if strings.Contains(got, "REFRESHED") {
		t.Errorf("a dry run claimed to have refreshed something:\n%s", got)
	}
}

// ⛔ Every total is printed, zero included: a report that lists only what it
// changed cannot be told from one that could not look.
func TestEveryTotalIsPrintedEvenAtZero(t *testing.T) {
	withFakeGH(t, behindRepo(pr(7, "open", "true", "head1111"), "0", `exit 1`))
	var out, errOut strings.Builder
	run(&out, &errOut, []string{"acme/one", "7"})
	want := "pull requests examined: 1  refreshed: 0  current: 1  behind: 0  conflict: 0  moved: 0  notopen: 0  unjudged: 0"
	if got := out.String(); !strings.Contains(got, want) {
		t.Errorf("the totals line is wrong:\nwant %s\ngot  %s", want, got)
	}
}

// ⛔ An empty population is a refusal, not a clean run: otherwise a mistyped
// repository prints the same thing as a quiet one.
func TestNoPullRequestsIsARefusalAndNotACleanRun(t *testing.T) {
	withFakeGH(t, `case "$*" in *pulls*) echo '[]' ;; *) exit 1 ;; esac`)
	var out, errOut strings.Builder
	code := run(&out, &errOut, []string{"--all-open", "acme/one"})
	if code != 2 {
		t.Errorf("exit = %d, want 2: %s", code, out.String())
	}
	if !strings.Contains(errOut.String(), "no pull requests to refresh") {
		t.Errorf("the refusal does not say what was wrong: %s", errOut.String())
	}
}

// Naming numbers and --all-open together says two different things; it is
// refused rather than silently resolved one way.
func TestNumbersAndAllOpenTogetherAreRefused(t *testing.T) {
	withFakeGH(t, `exit 1`)
	var out, errOut strings.Builder
	if code := run(&out, &errOut, []string{"--all-open", "acme/one", "7"}); code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
}

func TestABadRepositoryOrNumberIsRefused(t *testing.T) {
	withFakeGH(t, `exit 1`)
	for _, args := range [][]string{
		{},
		{"notarepo", "7"},
		{"acme/one", "zero"},
		{"acme/one", "-3"},
	} {
		var out, errOut strings.Builder
		if code := run(&out, &errOut, args); code != 2 {
			t.Errorf("run(%q) = %d, want 2", args, code)
		}
	}
}

// A pull request that is not open cannot be refreshed, and saying so is not the
// same as saying nothing needed doing.
func TestAClosedPullRequestIsNamedRatherThanCountedAsCurrent(t *testing.T) {
	withFakeGH(t, behindRepo(pr(7, "closed", "null", "head1111"), "0", `exit 1`))
	var out, errOut strings.Builder
	code := run(&out, &errOut, []string{"acme/one", "7"})
	if code != 1 {
		t.Errorf("exit = %d, want 1: %s", code, errOut.String())
	}
	if got := out.String(); !strings.Contains(got, "NOTOPEN   #7") {
		t.Errorf("the closed pull request is not named:\n%s", got)
	}
}

// A call that fails leaves the pull request unjudged and exits 2 — never 0.
func TestACallThatFailsLeavesThePullRequestUnjudged(t *testing.T) {
	withFakeGH(t, `echo "gh: boom" >&2; exit 1`)
	var out, errOut strings.Builder
	code := run(&out, &errOut, []string{"acme/one", "7"})
	if code != 2 {
		t.Errorf("exit = %d, want 2: %s", code, out.String())
	}
	if got := out.String(); !strings.Contains(got, "UNJUDGED  #7") {
		t.Errorf("the pull request is not named as unjudged:\n%s", got)
	}
}

func TestHelpIsAnsweredWithZero(t *testing.T) {
	var out, errOut strings.Builder
	if code := run(&out, &errOut, []string{"--help"}); code != 0 {
		t.Errorf("--help = %d, want 0", code)
	}
}
