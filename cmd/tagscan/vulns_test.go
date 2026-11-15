package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeReport puts a vulnscan --json report on disk and returns its path.
func writeReport(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "vulns.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// aheadAt is the one-organisation fixture every test below shares: acme/moved
// is ahead of v0.2.0, acme/still is level with it.
func aheadAt(t *testing.T) {
	t.Helper()
	withFakeGH(t, oneOrgTwoRepos+`
  *compare*) echo "9" ;;
esac`)
	withRemote(t, map[string]string{
		"https://github.com/acme/moved": "aaa\trefs/heads/main\nbbb\trefs/tags/v0.2.0\n",
		"https://github.com/acme/still": "ccc\trefs/heads/main\nccc\trefs/tags/v0.2.0\n",
	})
}

// TestAnAdvisoryAtThePublishedTagIsSaidBesideTheModule is the whole point of
// the flag: a count of commits cannot tell six commits of prose from eight that
// include a security fix. Measured on 2026-11-15, one organisation printed three
// lines of identical shape whose meanings were a README, a dependency graph, and
// five advisories CALLED by the only version anyone could resolve.
func TestAnAdvisoryAtThePublishedTagIsSaidBesideTheModule(t *testing.T) {
	aheadAt(t)
	rep := writeReport(t, `{"repos":[
	  {"repo":"acme/moved","status":"CALLED","ref":"v0.2.0",
	   "called":[{"id":"GO-2026-6617"},{"id":"GO-2026-6603"}]}
	]}`)
	var out, errOut strings.Builder
	if code := run([]string{"-orgs", "acme", "-vulns", rep}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	got := out.String()
	if !strings.Contains(got, "CALLS 2 advisorie(s)") {
		t.Errorf("the advisory is not stated beside the module:\n%s", got)
	}
	if !strings.Contains(got, "the version consumers resolve is the exposed one") {
		t.Errorf("the finding is not stated in terms of consumers:\n%s", got)
	}
}

// TestAReportTakenOnTheDefaultBranchIsRefusedForTheTag is the gate. vulnscan
// without --at-tag answers about the tree NOBODY IMPORTS; borrowing that answer
// for the tag would reproduce, one tool further along, the exact defect
// --at-tag exists to remove.
func TestAReportTakenOnTheDefaultBranchIsRefusedForTheTag(t *testing.T) {
	aheadAt(t)
	rep := writeReport(t, `{"repos":[{"repo":"acme/moved","status":"CLEAN"}]}`)
	var out, errOut strings.Builder
	if code := run([]string{"-orgs", "acme", "-vulns", rep}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	got := out.String()
	if !strings.Contains(got, "DEFAULT BRANCH") || !strings.Contains(got, "says nothing about v0.2.0") {
		t.Errorf("a report about main was not refused for the tag:\n%s", got)
	}
	if strings.Contains(got, "is clean") {
		t.Errorf("a CLEAN about main was borrowed for the tag:\n%s", got)
	}
}

// TestAReadingAboutAnotherTagIsStaleAndNotTransferred: a release between the two
// passes leaves the report describing a version nobody resolves any more.
// Reporting it as the current state is how a measurement outlives its subject.
func TestAReadingAboutAnotherTagIsStaleAndNotTransferred(t *testing.T) {
	aheadAt(t)
	rep := writeReport(t, `{"repos":[{"repo":"acme/moved","status":"CALLED","ref":"v0.1.0",
	  "called":[{"id":"GO-2026-6617"}]}]}`)
	var out, errOut strings.Builder
	if code := run([]string{"-orgs", "acme", "-vulns", rep}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	got := out.String()
	if !strings.Contains(got, "about v0.1.0, not the v0.2.0 this pass found") {
		t.Errorf("a reading about another tag was not named as stale:\n%s", got)
	}
	if strings.Contains(got, "CALLS 1 advisorie(s)") {
		t.Errorf("a finding about v0.1.0 was attributed to v0.2.0:\n%s", got)
	}
}

// TestAModuleMissingFromTheReportIsNotClean: silence is the dangerous answer.
// A module with no note under it reads as a module with no advisory, so the
// absence has to be printed.
func TestAModuleMissingFromTheReportIsNotClean(t *testing.T) {
	aheadAt(t)
	rep := writeReport(t, `{"repos":[{"repo":"acme/elsewhere","status":"CLEAN","ref":"v1.0.0"}]}`)
	var out, errOut strings.Builder
	if code := run([]string{"-orgs", "acme", "-vulns", rep}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if got := out.String(); !strings.Contains(got, "absent from the vulnscan report") {
		t.Errorf("a module nobody scanned read as clean:\n%s", got)
	}
}

// TestAReportThatCouldNotBeReadSaysSoOnEveryLine: an unreadable report leaves
// every line unannotated, which looks exactly like a fleet with no advisories.
// That is this repository's oldest defect -- a pass that could not read
// reporting zero -- so it is said once, loudly, above the lines it affects.
func TestAReportThatCouldNotBeReadSaysSoOnEveryLine(t *testing.T) {
	aheadAt(t)
	var out, errOut strings.Builder
	if code := run([]string{"-orgs", "acme", "-vulns", filepath.Join(t.TempDir(), "nope.json")}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	got := out.String()
	if !strings.Contains(got, "could not be read") {
		t.Errorf("a missing report was passed over in silence:\n%s", got)
	}
	if !strings.Contains(got, "NOT a statement that nothing is exposed") {
		t.Errorf("the silence is not named as silence:\n%s", got)
	}
}

// TestAFrozenModuleIsAnnotatedToo is the case the first draft of --vulns
// forgot, found by reading my own diff rather than by a test failing. A FROZEN
// module is the WORSE one: an advisory at a tag consumers are merely behind is
// fixed by publishing the next version, while a frozen tag is one nothing will
// move off -- there is no later version on the history they are on. Annotating
// every case except that one would have been the wrong way round.
func TestAFrozenModuleIsAnnotatedToo(t *testing.T) {
	// "No common ancestor" is what git answers for a tag left on a rewritten
	// history, and tagscan sorts those into FROZEN rather than unread.
	withFakeGH(t, oneOrgTwoRepos+`
  *compare*) echo "No common ancestor" >&2; exit 1 ;;
esac`)
	withRemote(t, map[string]string{
		"https://github.com/acme/moved": "aaa\trefs/heads/main\nbbb\trefs/tags/v0.2.0\n",
		"https://github.com/acme/still": "ccc\trefs/heads/main\nccc\trefs/tags/v0.2.0\n",
	})
	rep := writeReport(t, `{"repos":[{"repo":"acme/moved","status":"CALLED","ref":"v0.2.0",
	  "called":[{"id":"GO-2026-6617"}]}]}`)
	var out, errOut strings.Builder
	run([]string{"-orgs", "acme", "-vulns", rep}, &out, &errOut)
	got := out.String()
	if !strings.Contains(got, "FROZEN") {
		t.Fatalf("the fixture did not produce a frozen module:\n%s", got)
	}
	if !strings.Contains(got, "CALLS 1 advisorie(s)") {
		t.Errorf("a frozen module's advisory is not stated:\n%s", got)
	}
}

// TestAReportWithNoRepositoriesIsAMistakeAndNotAnEmptyFleet: any valid JSON
// unmarshals into the report's shape -- `{}`, another tool's output, a
// half-written file -- and would then annotate every line with "absent from the
// report". Each of those lines would be true and all of them together would
// hide the actual mistake, which is that the wrong file was named.
func TestAReportWithNoRepositoriesIsAMistakeAndNotAnEmptyFleet(t *testing.T) {
	aheadAt(t)
	rep := writeReport(t, `{"generated":"2026-11-15T00:00:00Z","repos":[]}`)
	var out, errOut strings.Builder
	if code := run([]string{"-orgs", "acme", "-vulns", rep}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	got := out.String()
	// The message names the file, so the assertion matches the part that does
	// not move: "<path> a vulnscan --json report?".
	if !strings.Contains(got, "no repositories in it") || !strings.Contains(got, "a vulnscan --json report?") {
		t.Errorf("an empty report passed for a clean fleet:\n%s", got)
	}
	if strings.Contains(got, "absent from the vulnscan report") {
		t.Errorf("an empty report annotated the modules one by one:\n%s", got)
	}
}

// TestWithoutTheFlagNothingIsSaidAboutAdvisories is the control. Without it, a
// note printed unconditionally -- or never -- would pass every test above while
// changing what an ordinary pass means.
func TestWithoutTheFlagNothingIsSaidAboutAdvisories(t *testing.T) {
	aheadAt(t)
	var out, errOut strings.Builder
	if code := run([]string{"-orgs", "acme"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	got := out.String()
	if !strings.Contains(got, "cannot reach") {
		t.Fatalf("the ordinary line is gone:\n%s", got)
	}
	for _, unwanted := range []string{"advisorie", "vulnscan report", "DEFAULT BRANCH"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("an ordinary pass now talks about %q:\n%s", unwanted, got)
		}
	}
}
