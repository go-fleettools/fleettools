package main

import (
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
  *acme/green/actions/runs*) echo '{"c":"success","n":"CI","d":"2026-09-05T10:00:00Z"}' ;;
  *acme/red/actions/runs*)   echo '{"c":"failure","n":"CI","d":"2026-09-05T20:40:00Z"}' ;;
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
  *acme/green/actions/runs*) echo '{"c":"success","n":"CI","d":"2026-09-05T10:00:00Z"}' ;;
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
  *actions/runs*) echo '{"c":"success","n":"CI","d":"2026-09-05T10:00:00Z"}' ;;
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
