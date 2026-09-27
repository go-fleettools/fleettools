package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// withFakeGH puts a stub `gh` first on PATH. The script decides its answer from
// the arguments, the way the real one decides from the URL.
func withFakeGH(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	body := "#!/bin/sh\n" + script + "\n"
	if err := os.WriteFile(dir+"/gh", []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
}

// oneOrgTwoRepos answers the org list and the repo list, and leaves the runs
// endpoint to the caller's own case.
const oneOrgTwoRepos = `
case "$*" in
  *user/orgs*) echo "acme" ;;
  *orgs/acme/repos*)
    echo '[{"full_name":"acme/green","default_branch":"main","archived":false,"fork":false},
           {"full_name":"acme/red","default_branch":"main","archived":false,"fork":false},
           {"full_name":"acme/old","default_branch":"main","archived":true,"fork":false},
           {"full_name":"acme/copy","default_branch":"main","archived":false,"fork":true}]' ;;
`

func TestAFailedRunIsReportedAndTheRestAreCounted(t *testing.T) {
	withFakeGH(t, oneOrgTwoRepos+`
  *acme/green/actions/runs*) echo '[{"w":1,"c":"success","n":"CI","d":"2026-09-05T10:00:00Z"}]' ;;
  *acme/red/actions/runs*)   echo '[{"w":1,"c":"failure","n":"CI","d":"2026-09-05T20:40:00Z"}]' ;;
esac`)
	var out, errOut strings.Builder
	if code := run(&out, &errOut); code != 0 {
		t.Errorf("exit = %d, want 0: %s", code, errOut.String())
	}
	got := out.String()
	// Archived and forked repositories are not the fleet's own work.
	if !strings.Contains(got, "repos: 2") {
		t.Errorf("archived and forked repositories were counted:\n%s", got)
	}
	if !strings.Contains(got, "checked: 2 of 2   RED default branches: 1") {
		t.Errorf("the summary reads:\n%s", got)
	}
	if !strings.Contains(got, "acme/red") || strings.Contains(got, "acme/green ") {
		t.Errorf("the wrong repository was named:\n%s", got)
	}
	if strings.Contains(got, "INCOMPLETE") {
		t.Errorf("a complete pass claimed to be incomplete:\n%s", got)
	}
}

func TestARunThatCouldNotBeReadIsNotABranchThatIsGreen(t *testing.T) {
	// The defect this is here for: the tool printed "checked: 284   RED
	// default branches: 0" over 1933 repositories and the count of successes
	// read as the population. Two red branches were behind it.
	withFakeGH(t, oneOrgTwoRepos+`
  *acme/green/actions/runs*) echo '[{"w":1,"c":"success","n":"CI","d":"2026-09-05T10:00:00Z"}]' ;;
  *actions/runs*) echo "API rate limit exceeded for user ID 1." >&2; exit 1 ;;
esac`)
	var out, errOut strings.Builder
	code := run(&out, &errOut)
	got := out.String()
	if code == 0 {
		t.Errorf("an incomplete pass exited 0:\n%s", got)
	}
	if !strings.Contains(got, "checked: 1 of 2") {
		t.Errorf("the summary hides what was not read:\n%s", got)
	}
	if !strings.Contains(got, "INCOMPLETE") {
		t.Errorf("the pass did not say it was incomplete:\n%s", got)
	}
	// The reason is what tells a rate limit from a permission.
	if !strings.Contains(got, "acme/red") || !strings.Contains(got, "rate limit") {
		t.Errorf("the report does not name what failed, or why:\n%s", got)
	}
}

func TestAnOrganisationThatCouldNotBeListedIsNotAnEmptyOne(t *testing.T) {
	// The same failure one level up: an organisation whose repositories cannot
	// be listed contributes none, and "repos: N" then reads as the whole
	// population.
	withFakeGH(t, `
case "$*" in
  *user/orgs*) printf 'acme\nbeta\n' ;;
  *orgs/acme/repos*)
    echo '[{"full_name":"acme/green","default_branch":"main","archived":false,"fork":false}]' ;;
  *orgs/beta/repos*) echo "API rate limit exceeded for user ID 1." >&2; exit 1 ;;
  *actions/runs*) echo '[{"w":1,"c":"success","n":"CI","d":"2026-09-05T10:00:00Z"}]' ;;
esac`)
	var out, errOut strings.Builder
	code := run(&out, &errOut)
	got := out.String()
	if code == 0 {
		t.Errorf("a pass that could not list an organisation exited 0:\n%s", got)
	}
	if !strings.Contains(got, "1 of 2 organisations") || !strings.Contains(got, "beta") {
		t.Errorf("the unlisted organisation is not named:\n%s", got)
	}
	// And what it DID read is still reported, rather than the whole pass being
	// thrown away.
	if !strings.Contains(got, "checked: 1 of 1") {
		t.Errorf("what was read was not reported:\n%s", got)
	}
}

func TestWithoutTheOrganisationsThereIsNothingToSweep(t *testing.T) {
	withFakeGH(t, `echo "gh: not logged in" >&2; exit 1`)
	var out, errOut strings.Builder
	if code := run(&out, &errOut); code == 0 {
		t.Error("a pass that could not list the organisations exited 0")
	}
	if !strings.Contains(errOut.String(), "orgs:") {
		t.Errorf("stderr = %q", errOut.String())
	}
}

func TestKeepingAnErrorReadableInAList(t *testing.T) {
	long := "gh: API rate limit exceeded for user ID 11405852. If you reach out to GitHub Support for help, please include the request ID"
	got := firstLine(long + "\nsecond line")
	if strings.Contains(got, "second line") {
		t.Errorf("the second line survived: %q", got)
	}
	if len(got) > 95 {
		t.Errorf("a %d-character reason would push the others off the screen", len(got))
	}
	if got := firstLine("  short  "); got != "short" {
		t.Errorf("firstLine(short) = %q", got)
	}
}

func TestALongListOfFailuresIsTruncatedRatherThanPrintedWhole(t *testing.T) {
	// 1649 repositories failed in the pass that prompted all this. Printing
	// them all would bury the summary that says how many there were.
	var repos []string
	for i := 0; i < 8; i++ {
		repos = append(repos, `{"full_name":"acme/r`+string(rune('0'+i))+
			`","default_branch":"main","archived":false,"fork":false}`)
	}
	withFakeGH(t, `
case "$*" in
  *user/orgs*) echo "acme" ;;
  *orgs/acme/repos*) echo '[`+strings.Join(repos, ",")+`]' ;;
  *actions/runs*) echo "API rate limit exceeded for user ID 1." >&2; exit 1 ;;
esac`)
	var out, errOut strings.Builder
	if code := run(&out, &errOut); code == 0 {
		t.Error("eight unreadable repositories exited 0")
	}
	got := out.String()
	if !strings.Contains(got, "8 of 8 repositories") {
		t.Errorf("the count is wrong:\n%s", got)
	}
	if !strings.Contains(got, "... and 3 more") {
		t.Errorf("the list was not truncated after five:\n%s", got)
	}
	if strings.Count(got, "rate limit") != 5 {
		t.Errorf("%d reasons printed, want 5:\n%s", strings.Count(got, "rate limit"), got)
	}
}

// TestAStaleRedIsWithdrawnOnTheSecondReading is the failure this guards.
//
// The runs endpoint has twice handed back a MONTHS-OLD run as the newest:
// openweft/terraform-provider-weft reported failing on 2026-05-30 and
// openweft/weft-app-gtk on 2026-06-20, while the same query -- rerun 45 times,
// 40 of them at this sweep's own concurrency -- answered "success" from August
// every time. Each phantom sent somebody looking for a three-month outage that
// was not there.
func TestAStaleRedIsWithdrawnOnTheSecondReading(t *testing.T) {
	count := t.TempDir() + "/n"
	t.Setenv("REDSCAN_TEST_COUNT", count)
	withFakeGH(t, oneOrgTwoRepos+`
  *acme/green/actions/runs*) echo '[{"w":1,"c":"success","n":"CI","d":"2026-09-05T10:00:00Z"}]' ;;
  *acme/red/actions/runs*)
    n=$(cat "$REDSCAN_TEST_COUNT" 2>/dev/null || echo 0); n=$((n+1)); echo $n > "$REDSCAN_TEST_COUNT"
    if [ "$n" = 1 ]; then echo '[{"w":1,"c":"failure","n":"CI","d":"2026-06-20T06:33:00Z"}]'
    else echo '[{"w":1,"c":"success","n":"CI","d":"2026-08-18T10:02:00Z"}]'; fi ;;
esac`)
	var out, errOut strings.Builder
	if code := run(&out, &errOut); code != 0 {
		t.Errorf("exit = %d, want 0: %s", code, errOut.String())
	}
	got := out.String()
	if !strings.Contains(got, "RED default branches: 0") {
		t.Errorf("a stale reading was reported as a red branch:\n%s", got)
	}
	// Said out loud: a sweep that quietly repaired itself would hide how often
	// the endpoint does this.
	if !strings.Contains(got, "1 red verdict(s) withdrawn") {
		t.Errorf("the withdrawal was not reported:\n%s", got)
	}
}

// TestASecondReadingThatIsOlderDoesNotOverruleTheFirst: the rule is "take the
// LATER run", not "take the second answer". A stale page on the retry must not
// erase a red that is real.
func TestASecondReadingThatIsOlderDoesNotOverruleTheFirst(t *testing.T) {
	count := t.TempDir() + "/n"
	t.Setenv("REDSCAN_TEST_COUNT", count)
	withFakeGH(t, oneOrgTwoRepos+`
  *acme/green/actions/runs*) echo '[{"w":1,"c":"success","n":"CI","d":"2026-09-05T10:00:00Z"}]' ;;
  *acme/red/actions/runs*)
    n=$(cat "$REDSCAN_TEST_COUNT" 2>/dev/null || echo 0); n=$((n+1)); echo $n > "$REDSCAN_TEST_COUNT"
    if [ "$n" = 1 ]; then echo '[{"w":1,"c":"failure","n":"CI","d":"2026-09-08T06:33:00Z"}]'
    else echo '[{"w":1,"c":"success","n":"CI","d":"2026-01-01T10:02:00Z"}]'; fi ;;
esac`)
	var out, errOut strings.Builder
	run(&out, &errOut)
	got := out.String()
	if !strings.Contains(got, "RED default branches: 1") || !strings.Contains(got, "acme/red") {
		t.Errorf("a real red was erased by an older second reading:\n%s", got)
	}
	if strings.Contains(got, "withdrawn") {
		t.Errorf("nothing was withdrawn, yet it said so:\n%s", got)
	}
}

// TestANewerWorkflowDoesNotHideAnOlderFailingOne is the failure this replaces.
//
// The sweep asked for ONE run and took it, so a repository with several
// workflows was read through whichever had run most recently.
// go-tex/go-tex.github.io has two: `playground` failed at 15:22 on 2026-09-09
// and `pages` succeeded at 15:31, and the repository was reported green while a
// workflow on its default branch had been red for hours.
func TestANewerWorkflowDoesNotHideAnOlderFailingOne(t *testing.T) {
	withFakeGH(t, oneOrgTwoRepos+`
  *acme/green/actions/runs*) echo '[{"w":1,"c":"success","n":"CI","d":"2026-09-05T10:00:00Z"}]' ;;
  *acme/red/actions/runs*)
    echo '[{"w":2,"c":"success","n":"pages","d":"2026-09-09T15:31:00Z"},
           {"w":1,"c":"failure","n":"playground","d":"2026-09-09T15:22:00Z"},
           {"w":1,"c":"success","n":"playground","d":"2026-09-09T13:36:00Z"}]' ;;
esac`)
	var out, errOut strings.Builder
	if code := run(&out, &errOut); code != 0 {
		t.Errorf("exit = %d, want 0: %s", code, errOut.String())
	}
	got := out.String()
	if !strings.Contains(got, "RED default branches: 1") {
		t.Errorf("the failing workflow was hidden behind the newer one:\n%s", got)
	}
	// It is named by the workflow that failed, not by the one that passed, or
	// nobody knows where to look.
	if !strings.Contains(got, "playground") || strings.Contains(got, "pages") {
		t.Errorf("the wrong workflow was named:\n%s", got)
	}
	// And the older passing run of the SAME workflow does not overrule the
	// newer failing one.
	if !strings.Contains(got, "2026-09-09T15:22") {
		t.Errorf("the run named is not the newest of its workflow:\n%s", got)
	}
}

// TestEveryFailingWorkflowIsNamed: a repository with two red workflows is two
// things to look at, not one.
func TestEveryFailingWorkflowIsNamed(t *testing.T) {
	withFakeGH(t, oneOrgTwoRepos+`
  *acme/green/actions/runs*) echo '[{"w":1,"c":"success","n":"CI","d":"2026-09-05T10:00:00Z"}]' ;;
  *acme/red/actions/runs*)
    echo '[{"w":1,"c":"failure","n":"alpha","d":"2026-09-09T15:22:00Z"},
           {"w":2,"c":"timed_out","n":"beta","d":"2026-09-09T15:20:00Z"}]' ;;
esac`)
	var out, errOut strings.Builder
	run(&out, &errOut)
	got := out.String()
	if !strings.Contains(got, "RED default branches: 2") {
		t.Errorf("two failing workflows were counted as one:\n%s", got)
	}
	if !strings.Contains(got, "alpha") || !strings.Contains(got, "beta") {
		t.Errorf("both should be named:\n%s", got)
	}
}

// TestARedFromAWorkflowTheRepositoryNoLongerHasIsNotReported. GitHub keeps the
// last run of a deleted or superseded workflow for ever, so it stays red with
// nothing anybody can do. go-fsctl/go-fsctl.github.io was named on "pages build
// and deployment" from 2026-06-22 while its own Hugo workflow had been
// deploying since — the site answers 200. Two of thirteen reds were this shape,
// and a reader who checks two and finds both wrong stops checking the rest.
func TestARedFromAWorkflowTheRepositoryNoLongerHasIsNotReported(t *testing.T) {
	withFakeGH(t, oneOrgTwoRepos+`
  *acme/green/actions/workflows*) echo '[{"i":1,"s":"active"}]' ;;
  *acme/red/actions/workflows*)   echo '[{"i":9,"s":"active"}]' ;;
  *acme/green/actions/runs*) echo '[{"w":1,"c":"success","n":"CI","d":"2026-09-05T10:00:00Z"}]' ;;
  *acme/red/actions/runs*)   echo '[{"w":1,"c":"failure","n":"pages build and deployment","d":"2026-06-22T11:04:00Z"}]' ;;
esac`)
	var out, errOut strings.Builder
	if code := run(&out, &errOut); code != 0 {
		t.Errorf("exit = %d: %s", code, errOut.String())
	}
	got := out.String()
	if !strings.Contains(got, "RED default branches: 0") {
		t.Errorf("a run from a workflow that is gone was counted as red:\n%s", got)
	}
	if !strings.Contains(got, "no longer has") || !strings.Contains(got, "pages build and deployment") {
		t.Errorf("it was dropped silently instead of being named:\n%s", got)
	}
}

// TestARedFromAWorkflowThatStillExistsIsStillReported — the other direction,
// which a filter written from one example gets wrong.
func TestARedFromAWorkflowThatStillExistsIsStillReported(t *testing.T) {
	withFakeGH(t, oneOrgTwoRepos+`
  *acme/green/actions/workflows*) echo '[{"i":1,"s":"active"}]' ;;
  *acme/red/actions/workflows*)   echo '[{"i":1,"s":"active"}]' ;;
  *acme/green/actions/runs*) echo '[{"w":1,"c":"success","n":"CI","d":"2026-09-05T10:00:00Z"}]' ;;
  *acme/red/actions/runs*)   echo '[{"w":1,"c":"failure","n":"CI","d":"2026-09-05T20:40:00Z"}]' ;;
esac`)
	var out, errOut strings.Builder
	run(&out, &errOut)
	got := out.String()
	if !strings.Contains(got, "RED default branches: 1") || !strings.Contains(got, "acme/red") {
		t.Errorf("a live failing workflow stopped being reported:\n%s", got)
	}
	if strings.Contains(got, "no longer has") {
		t.Errorf("a live workflow was called gone:\n%s", got)
	}
}

// TestADisabledWorkflowCountsAsGone: GitHub reports one turned off by hand or
// by inactivity with a state that is not "active", and its last run is as
// unfixable as a deleted one's.
func TestADisabledWorkflowCountsAsGone(t *testing.T) {
	withFakeGH(t, oneOrgTwoRepos+`
  *acme/green/actions/workflows*) echo '[{"i":1,"s":"active"}]' ;;
  *acme/red/actions/workflows*)   echo '[{"i":1,"s":"disabled_inactivity"}]' ;;
  *acme/green/actions/runs*) echo '[{"w":1,"c":"success","n":"CI","d":"2026-09-05T10:00:00Z"}]' ;;
  *acme/red/actions/runs*)   echo '[{"w":1,"c":"failure","n":"CI","d":"2026-09-05T20:40:00Z"}]' ;;
esac`)
	var out, errOut strings.Builder
	run(&out, &errOut)
	if got := out.String(); !strings.Contains(got, "RED default branches: 0") {
		t.Errorf("a disabled workflow's last failure was still reported as red:\n%s", got)
	}
}

// TestAWorkflowListThatCannotBeReadKEEPSTheRed. Failing open is the whole
// point: an unread list must not silence a real failure, which is the direction
// that costs something.
func TestAWorkflowListThatCannotBeReadKeepsTheRed(t *testing.T) {
	withFakeGH(t, oneOrgTwoRepos+`
  *actions/workflows*) echo "gone fishing" >&2; exit 1 ;;
  *acme/green/actions/runs*) echo '[{"w":1,"c":"success","n":"CI","d":"2026-09-05T10:00:00Z"}]' ;;
  *acme/red/actions/runs*)   echo '[{"w":1,"c":"failure","n":"CI","d":"2026-09-05T20:40:00Z"}]' ;;
esac`)
	var out, errOut strings.Builder
	run(&out, &errOut)
	if got := out.String(); !strings.Contains(got, "RED default branches: 1") {
		t.Errorf("an unreadable workflow list silenced a real red:\n%s", got)
	}
}

// ⛔ laterRuns decides whether a red is DROPPED from the headline, so a wrong
// "superseded" hides a branch somebody should fix. The cases that must stay
// come first.
func TestAGatingWorkflowIsNeverSuperseded(t *testing.T) {
	// A workflow that runs on every push: its own last run IS the newest, so
	// nothing is later than it.
	ci := wfRun{W: 1, C: "failure", N: "ci", D: "2026-09-27T10:00:00Z"}
	all := []wfRun{
		ci,
		{W: 2, C: "success", N: "docs", D: "2026-09-27T09:00:00Z"},
		{W: 1, C: "success", N: "ci", D: "2026-09-26T10:00:00Z"},
	}
	if n := laterRuns(all, ci); n != 0 {
		t.Errorf("laterRuns = %d, want 0 for the newest run on the branch", n)
	}
	if n := laterRuns(all, ci); n >= supersededAfter {
		t.Error("a workflow that runs on every push was called superseded")
	}

	// One later run is not enough either: a docs workflow finishing after a
	// failing test run must not retire the test run's verdict.
	red := wfRun{W: 1, C: "failure", N: "ci", D: "2026-09-27T10:00:00Z"}
	withDocs := append(all, wfRun{W: 2, C: "success", N: "docs", D: "2026-09-27T10:05:00Z"})
	if n := laterRuns(withDocs, red); n >= supersededAfter {
		t.Errorf("laterRuns = %d retired a red that one later run followed", n)
	}
}

// TestTheAbandonedPagesWorkflowIsSuperseded, with the real numbers. The two
// repositories this was written for sat at 11 and 6 later runs.
func TestTheAbandonedPagesWorkflowIsSuperseded(t *testing.T) {
	pages := wfRun{W: 9, C: "failure", N: "pages build and deployment", D: "2026-06-22T11:04:00Z"}
	all := []wfRun{pages}
	for i := 0; i < 11; i++ {
		all = append(all, wfRun{W: 1, C: "success", N: "Deploy Hugo site to Pages",
			D: fmt.Sprintf("2026-09-%02dT09:00:00Z", 10+i)})
	}
	n := laterRuns(all, pages)
	if n != 11 {
		t.Fatalf("laterRuns = %d, want 11", n)
	}
	if n < supersededAfter {
		t.Errorf("11 later runs did not reach the threshold %d", supersededAfter)
	}
	// And the smaller witness, go-ruby-hanami at 6.
	hanami := wfRun{W: 9, C: "failure", N: "pages build and deployment", D: "2026-07-06T12:59:00Z"}
	six := []wfRun{hanami}
	for i := 0; i < 6; i++ {
		six = append(six, wfRun{W: 1, C: "success", N: "deploy-pages",
			D: fmt.Sprintf("2026-08-%02dT09:00:00Z", 10+i)})
	}
	if laterRuns(six, hanami) < supersededAfter {
		t.Errorf("the 6-run witness did not reach the threshold %d", supersededAfter)
	}
}
