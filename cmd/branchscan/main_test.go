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

// repoWith builds a fake gh for one repository. branches is the JSON array of
// branch objects, pulls the JSON array of pull requests, and compares a shell
// case body keyed on the head name.
func repoWith(branches, pulls, compares string) string {
	return `
case "$*" in
  *compare*) ` + compares + ` ;;
  *branches*) echo '` + branches + `' ;;
  *pulls*) echo '` + pulls + `' ;;
  *repos/acme/one) echo '{"default_branch":"main"}' ;;
  *) echo "unexpected: $*" >&2; exit 1 ;;
esac`
}

func brs(names ...string) string {
	var b strings.Builder
	b.WriteString("[")
	for i, n := range names {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"name":"` + n + `","commit":{"sha":"deadbeef"}}`)
	}
	b.WriteString("]")
	return b.String()
}

// ⛔ The case this command exists for: a branch pushed, never proposed, still
// holding commits. go-embedded-ruby/ruby had one for three days carrying three
// fixes that were then written a second time by someone reading the backlog.
func TestABranchAheadWithNoPullRequestIsStranded(t *testing.T) {
	withFakeGH(t, repoWith(brs("main", "forgotten"), `[]`,
		`echo '{"status":"ahead","ahead_by":4}'`))
	var out, errOut strings.Builder
	code := run(&out, &errOut, []string{"acme/one"})
	if code != 1 {
		t.Errorf("exit = %d, want 1 (something is stranded): %s", code, errOut.String())
	}
	got := out.String()
	if !strings.Contains(got, "STRANDED  acme/one  forgotten  4 commit(s)") {
		t.Errorf("the branch is not named:\n%s", got)
	}
	if !strings.Contains(got, "branches examined: 1  stranded: 1  proposed: 0  landed: 0  unknown: 0") {
		t.Errorf("the totals are wrong:\n%s", got)
	}
}

// ⛔ A pull request that was CLOSED without merging still means the branch was
// proposed and someone decided about it. Listing only open ones would call
// every rejected branch stranded and bury the real finding in noise — which is
// why the command asks for state=all.
func TestAClosedPullRequestStillCountsAsProposed(t *testing.T) {
	withFakeGH(t, repoWith(brs("main", "rejected"),
		`[{"head":{"ref":"rejected"}}]`,
		`echo '{"status":"ahead","ahead_by":9}'`))
	var out, errOut strings.Builder
	code := run(&out, &errOut, []string{"acme/one"})
	if code != 0 {
		t.Errorf("exit = %d, want 0: %s", code, errOut.String())
	}
	got := out.String()
	if strings.Contains(got, "STRANDED") {
		t.Errorf("a proposed branch was reported as stranded:\n%s", got)
	}
	if !strings.Contains(got, "stranded: 0  proposed: 1") {
		t.Errorf("the totals are wrong:\n%s", got)
	}
}

// A branch nobody proposed that holds nothing new is merged or stale, not lost
// work. Gating on it would make the command cry wolf on every repository.
func TestABranchWithNothingNewIsNotStranded(t *testing.T) {
	withFakeGH(t, repoWith(brs("main", "old"), `[]`,
		`echo '{"status":"identical","ahead_by":0}'`))
	var out, errOut strings.Builder
	code := run(&out, &errOut, []string{"acme/one"})
	if code != 0 {
		t.Errorf("exit = %d, want 0: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "stranded: 0  proposed: 0  landed: 1") {
		t.Errorf("the totals are wrong:\n%s", out.String())
	}
}

// ⛔⛔ A comparison that could not be MADE is not a comparison that came back
// empty. Treating the failure as "nothing to report" is how a scan that could
// not read reports zero and is believed.
func TestAComparisonThatFailedIsUnknownAndRefuses(t *testing.T) {
	withFakeGH(t, repoWith(brs("main", "unreadable"), `[]`,
		`echo "HTTP 500" >&2; exit 1`))
	var out, errOut strings.Builder
	code := run(&out, &errOut, []string{"acme/one"})
	if code != 2 {
		t.Errorf("exit = %d, want 2 (the scan could not judge it): %s", code, errOut.String())
	}
	got := out.String()
	if !strings.Contains(got, "UNKNOWN   acme/one  unreadable") {
		t.Errorf("the unjudged branch is not named:\n%s", got)
	}
	if !strings.Contains(got, "unknown: 1") {
		t.Errorf("the totals are wrong:\n%s", got)
	}
	if strings.Contains(got, "stranded: 1") {
		t.Errorf("an unjudged branch was counted as a finding:\n%s", got)
	}
}

// The default branch is not a branch anyone proposes, and counting it would put
// a permanent 1 in whichever bucket it landed in.
func TestTheDefaultBranchIsNotExamined(t *testing.T) {
	withFakeGH(t, repoWith(brs("main"), `[]`, `echo "no branch to compare" >&2; exit 1`))
	var out, errOut strings.Builder
	code := run(&out, &errOut, []string{"acme/one"})
	if code != 0 {
		t.Errorf("exit = %d, want 0: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "branches examined: 0") {
		t.Errorf("the default branch was examined:\n%s", out.String())
	}
}

// ⛔ The totals print even when every bucket is empty. A scan that prints only
// its findings cannot be told from a scan that could not look.
func TestTheTotalsArePrintedWithNothingToReport(t *testing.T) {
	withFakeGH(t, repoWith(brs("main"), `[]`, `exit 1`))
	var out, errOut strings.Builder
	run(&out, &errOut, []string{"acme/one"})
	if !strings.Contains(out.String(), "branches examined: 0  stranded: 0  proposed: 0  landed: 0  unknown: 0") {
		t.Errorf("a quiet pass said nothing about its denominator:\n%s", out.String())
	}
}

// gh --paginate concatenates pages as separate JSON arrays rather than merging
// them. Decoding only the first would silently halve the denominator.
func TestEveryPageOfBranchesIsRead(t *testing.T) {
	withFakeGH(t, `
case "$*" in
  *compare*) echo '{"status":"ahead","ahead_by":1}' ;;
  *branches*) printf '%s\n%s\n' '`+brs("main", "a")+`' '`+brs("b")+`' ;;
  *pulls*) echo '[]' ;;
  *repos/acme/one) echo '{"default_branch":"main"}' ;;
esac`)
	var out, errOut strings.Builder
	run(&out, &errOut, []string{"acme/one"})
	if !strings.Contains(out.String(), "branches examined: 2") {
		t.Errorf("the second page was dropped:\n%s", out.String())
	}
}

func TestNoRepositoryIsRefusedRatherThanReportedClean(t *testing.T) {
	var out, errOut strings.Builder
	if code := run(&out, &errOut, nil); code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if strings.Contains(out.String(), "stranded: 0") {
		t.Errorf("a refusal was dressed up as a clean scan:\n%s", out.String())
	}
}

func TestSomethingThatIsNotOwnerRepoIsRefused(t *testing.T) {
	var out, errOut strings.Builder
	if code := run(&out, &errOut, []string{"justaname"}); code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "not an owner/repo") {
		t.Errorf("stderr reads:\n%s", errOut.String())
	}
}

func TestARepositoryThatCouldNotBeReadRefuses(t *testing.T) {
	withFakeGH(t, `echo "HTTP 404" >&2; exit 1`)
	var out, errOut strings.Builder
	if code := run(&out, &errOut, []string{"acme/one"}); code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
}
