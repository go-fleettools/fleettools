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

// jsonl builds a `go test -json` log from compact specs: "PKG ACTION TEST
// [OUTPUT...]", with TEST "-" for a package-level event.
//
// ⛔ The tests build REAL events rather than hand-written JSON, because what
// this command must get right is which EVENT carries a verdict — and a fixture
// that blurred that would be a fixture shaped unlike every real input.
func jsonl(t *testing.T, specs ...string) string {
	t.Helper()
	var b strings.Builder
	for _, s := range specs {
		parts := strings.SplitN(s, " ", 4)
		if len(parts) < 3 {
			t.Fatalf("bad spec %q", s)
		}
		pkg, action, test := parts[0], parts[1], parts[2]
		if test == "-" {
			test = ""
		}
		out := ""
		if len(parts) == 4 {
			out = parts[3]
		}
		fmt.Fprintf(&b, `{"Action":%q,"Package":%q`, action, pkg)
		if test != "" {
			fmt.Fprintf(&b, `,"Test":%q`, test)
		}
		if out != "" {
			fmt.Fprintf(&b, `,"Output":%q`, out+"\n")
		}
		b.WriteString("}\n")
	}
	return b.String()
}

// pkgRun wraps one package's events with the start and result Go emits.
func pkgRun(t *testing.T, pkg string, result string, specs ...string) []string {
	t.Helper()
	out := []string{pkg + " start -"}
	out = append(out, specs...)
	return append(out, pkg+" "+result+" -")
}

func logs(t *testing.T, files map[string]string) {
	t.Helper()
	old := readFile
	t.Cleanup(func() { readFile = old })
	readFile = func(path string) ([]byte, error) {
		b, ok := files[path]
		if !ok {
			return nil, errors.New("no such file")
		}
		return []byte(b), nil
	}
}

func drive(t *testing.T, argv ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(argv, &out, &errb)
	return code, out.String(), errb.String()
}

// ⛔⛔ THE CASE THIS COMMAND WAS WRITTEN FOR, with the real result of
// go-fleettools/fleettools#252 on 2026-11-12. A mutation removed a staleness
// condition from cmd/aptneed. I published that this proved
// TestANameAptCouldNotCheckIsNotCalledATypo discriminating. It did not: that
// test PASSED and two others failed.
func TestTheClaimThatWasPublishedBackwardsIsRefused(t *testing.T) {
	const p = "example.com/aptneed"
	logs(t, map[string]string{
		"before": jsonl(t, pkgRun(t, p, "pass",
			p+" pass TestAPackageAptCannotLocateIsNotRetried",
			p+" pass TestANameAptCouldNotCheckIsNotCalledATypo",
			p+" pass TestANameAptCouldCheckIsStillRefusedOutright")...),
		"after": jsonl(t, pkgRun(t, p, "fail",
			p+" fail TestAPackageAptCannotLocateIsNotRetried",
			p+" pass TestANameAptCouldNotCheckIsNotCalledATypo",
			p+" fail TestANameAptCouldCheckIsStillRefusedOutright")...),
	})
	code, _, errb := drive(t, "--before", "before", "--after", "after",
		"--expect", "TestANameAptCouldNotCheckIsNotCalledATypo")
	if code != 1 {
		t.Fatalf("exit %d, want the claim refused: %q", code, errb)
	}
	if !strings.Contains(errb, "it PASSED under the mutation") {
		t.Errorf("stderr = %q", errb)
	}
	for _, want := range []string{
		"TestAPackageAptCannotLocateIsNotRetried",
		"TestANameAptCouldCheckIsStillRefusedOutright",
	} {
		if !strings.Contains(errb, want) {
			t.Errorf("it does not name %s as newly red: %q", want, errb)
		}
	}
}

// ⛔⛔ FALSE PASS 1, FOUND IN REVIEW, AND THE REASON THIS READS -json AT ALL.
// With `go test -v` text, a PASSING test's own output can forge another
// test's verdict: TestY printing "--- FAIL: TestX (0.00s)" made the old
// version report TestX as newly red and exit 0.
//
// In -json that line is an "output" event and can never be a verdict, so the
// forgery is not merely caught — it is unrepresentable.
func TestATestCannotForgeAnotherTestsVerdict(t *testing.T) {
	const p = "example.com/pkg"
	logs(t, map[string]string{
		"before": jsonl(t, pkgRun(t, p, "pass",
			p+" pass TestX", p+" pass TestY")...),
		"after": jsonl(t, pkgRun(t, p, "pass",
			p+" pass TestX",
			p+" output TestY --- FAIL: TestX (0.00s)",
			p+" pass TestY")...),
	})
	code, out, errb := drive(t, "--before", "before", "--after", "after", "--expect", "TestX")
	if code != 1 {
		t.Fatalf("exit %d, want the forged claim refused\nstdout %q\nstderr %q", code, out, errb)
	}
	if !strings.Contains(out, "nothing went green->red") {
		t.Errorf("a printed line was taken for a verdict: %q", out)
	}
}

// ⛔⛔ FALSE PASS 2: text output carries no package, so two packages with a
// test of the same name collided. Here TestX is red in pkgA in BOTH runs and
// green in pkgB in both; keyed by name alone that reads as green->red.
func TestTheSameNameInTwoPackagesIsTwoTests(t *testing.T) {
	const a, b = "example.com/a", "example.com/b"
	logs(t, map[string]string{
		"before": jsonl(t, append(
			pkgRun(t, a, "fail", a+" fail TestX"),
			pkgRun(t, b, "pass", b+" pass TestX")...)...),
		"after": jsonl(t, append(
			pkgRun(t, b, "pass", b+" pass TestX"),
			pkgRun(t, a, "fail", a+" fail TestX")...)...),
	})
	code, out, _ := drive(t, "--before", "before", "--after", "after", "--expect", "TestX")
	if code != 1 {
		t.Fatalf("exit %d, want refused: TestX was red in a and green in b in BOTH runs\n%s", code, out)
	}
}

// ⛔⛔ FALSE PASS 3: "not failing before" is not "passing before". A test
// ABSENT from the before run was counted as green, so it could then "go
// green->red" although it had never been shown to pass.
func TestATestMissingFromTheBeforeRunNeverWentGreenToRed(t *testing.T) {
	const p = "example.com/pkg"
	logs(t, map[string]string{
		"before": jsonl(t, pkgRun(t, p, "pass", p+" pass TestY")...),
		"after":  jsonl(t, pkgRun(t, p, "fail", p+" fail TestX", p+" pass TestY")...),
	})
	code, _, errb := drive(t, "--before", "before", "--after", "after", "--expect", "TestX")
	if code != 1 {
		t.Fatalf("exit %d, want refused: TestX was never green", code)
	}
	if !strings.Contains(errb, "did not record it PASSING") {
		t.Errorf("stderr = %q", errb)
	}
}

// And a SKIPPED test was not green either.
func TestATestSkippedInTheBeforeRunNeverWentGreenToRed(t *testing.T) {
	const p = "example.com/pkg"
	logs(t, map[string]string{
		"before": jsonl(t, pkgRun(t, p, "pass", p+" skip TestX")...),
		"after":  jsonl(t, pkgRun(t, p, "fail", p+" fail TestX")...),
	})
	code, _, _ := drive(t, "--before", "before", "--after", "after", "--expect", "TestX")
	if code != 1 {
		t.Fatalf("exit %d, want refused: TestX was skipped, not green", code)
	}
}

// ⛔⛔ FAILING IS STICKY WITHIN ONE RUN. `go test -count=2` reports a name
// once per iteration. Review measured a test that FAILED on the first and
// PASSED on the second being recorded as passed — so a genuinely red test
// could be reported as going green->red against the next run, which is a
// false pass built out of a flake.
func TestATestThatFailedOnceInARunIsFailedForThatRun(t *testing.T) {
	const p = "example.com/pkg"
	logs(t, map[string]string{
		"before": jsonl(t, pkgRun(t, p, "fail",
			p+" fail TestFlaky", p+" pass TestFlaky")...),
		"after": jsonl(t, pkgRun(t, p, "fail", p+" fail TestFlaky")...),
	})
	code, out, _ := drive(t, "--before", "before", "--after", "after", "--expect", "TestFlaky")
	if code != 1 {
		t.Fatalf("exit %d, want refused: TestFlaky was red in the before run too\n%s", code, out)
	}
}

// ⛔⛔ A LOG THAT IS PART JSON AND PART TEXT ANSWERS FOR NEITHER. The lines
// that did not parse used to be dropped in silence, so a -json run with a
// plain `panic:` tail appended read as complete — the comment said they were
// counted and the code did not.
func TestALogThatIsPartJsonAndPartTextIsRefused(t *testing.T) {
	const p = "example.com/pkg"
	logs(t, map[string]string{
		"before": jsonl(t, pkgRun(t, p, "pass", p+" pass TestA")...),
		"after": jsonl(t, pkgRun(t, p, "fail", p+" fail TestA")...) +
			"panic: test timed out after 1s\nFAIL\texample.com/pkg\t1.0s\n",
	})
	code, _, errb := drive(t, "--before", "before", "--after", "after", "--expect", "TestA")
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.Contains(errb, "part JSON and part something else") {
		t.Errorf("stderr = %q", errb)
	}
}

// ⛔⛔ A PACKAGE THAT FAILED WITH NO FAILING TEST FAILED OUTSIDE THE TESTS —
// a TestMain calling os.Exit(1), a panic after the last test, a cleanup that
// exploded. Every test that ran passed, so a reading based on verdicts alone
// calls it a clean, complete run. Review measured that.
func TestAPackageThatFailedWithNoFailingTestIsNoAnswer(t *testing.T) {
	const p = "example.com/pkg"
	logs(t, map[string]string{
		"before": jsonl(t, pkgRun(t, p, "pass", p+" pass TestA")...),
		"after":  jsonl(t, pkgRun(t, p, "fail", p+" pass TestA")...),
	})
	code, _, errb := drive(t, "--before", "before", "--after", "after")
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.Contains(errb, "failed outside the tests") {
		t.Errorf("stderr = %q", errb)
	}
}

// ⛔ AND A PACKAGE THAT FAILED BECAUSE A TEST FAILED IS AN ORDINARY ANSWER,
// which is the control: without it the rule above could refuse every red run
// and no test would notice.
func TestAPackageThatFailedBecauseATestFailedIsAnAnswer(t *testing.T) {
	const p = "example.com/pkg"
	logs(t, map[string]string{
		"before": jsonl(t, pkgRun(t, p, "pass", p+" pass TestA")...),
		"after":  jsonl(t, pkgRun(t, p, "fail", p+" fail TestA")...),
	})
	code, out, errb := drive(t, "--before", "before", "--after", "after", "--expect", "TestA")
	if code != 0 {
		t.Fatalf("exit %d: %q %q", code, out, errb)
	}
}

// ⛔ A SUBTEST ANSWERING FOR A PARENT THAT HAS NO VERDICT OF ITS OWN, which
// is the only shape that actually exercises covers()'s subtest clause: review
// measured `got == want` surviving, because the existing case's fixture also
// carries a failing parent and that satisfies the claim by equality.
//
// A log holding only the subtest's verdict is what a filtered or concatenated
// run gives.
func TestASubtestAnswersForAParentWithNoVerdictOfItsOwn(t *testing.T) {
	const p = "example.com/pkg"
	logs(t, map[string]string{
		"before": jsonl(t, pkgRun(t, p, "pass", p+" pass TestA/case")...),
		"after":  jsonl(t, pkgRun(t, p, "fail", p+" fail TestA/case")...),
	})
	code, out, errb := drive(t, "--before", "before", "--after", "after", "--expect", "TestA")
	if code != 0 {
		t.Fatalf("exit %d: the subtest did not answer for its parent\n%q %q", code, out, errb)
	}
}

// ⛔ RED->GREEN MEANS PASSING NOW, not merely "no longer failing". Review
// measured `!= failed` surviving: a test that went red->ABSENT or red->SKIPPED
// would have been reported as fixed by the mutation, which is a claim about
// the suite that nothing supports.
func TestATestThatWentRedToAbsentIsNotReportedAsFixed(t *testing.T) {
	const p = "example.com/pkg"
	logs(t, map[string]string{
		"before": jsonl(t, pkgRun(t, p, "fail", p+" fail TestGone", p+" pass TestB")...),
		"after":  jsonl(t, pkgRun(t, p, "fail", p+" fail TestB")...),
	})
	code, out, _ := drive(t, "--before", "before", "--after", "after", "--expect", "TestB")
	if code != 0 {
		t.Fatalf("exit %d: %q", code, out)
	}
	if strings.Contains(out, "red->green") {
		t.Errorf("a test that simply stopped running was called fixed: %q", out)
	}
}

// And a skipped one is not "fixed" either.
func TestATestThatWentRedToSkippedIsNotReportedAsFixed(t *testing.T) {
	const p = "example.com/pkg"
	logs(t, map[string]string{
		"before": jsonl(t, pkgRun(t, p, "fail", p+" fail TestS", p+" pass TestB")...),
		"after":  jsonl(t, pkgRun(t, p, "fail", p+" skip TestS", p+" fail TestB")...),
	})
	code, out, _ := drive(t, "--before", "before", "--after", "after", "--expect", "TestB")
	if code != 0 {
		t.Fatalf("exit %d: %q", code, out)
	}
	if strings.Contains(out, "red->green") {
		t.Errorf("a skipped test was called fixed: %q", out)
	}
}

// The claim the runs actually support is accepted.
func TestTheClaimTheRunsSupportIsAccepted(t *testing.T) {
	const p = "example.com/pkg"
	logs(t, map[string]string{
		"before": jsonl(t, pkgRun(t, p, "pass", p+" pass TestA", p+" pass TestB")...),
		"after":  jsonl(t, pkgRun(t, p, "fail", p+" fail TestA", p+" pass TestB")...),
	})
	code, out, errb := drive(t, "--before", "before", "--after", "after", "--expect", "TestA")
	if code != 0 {
		t.Fatalf("exit %d: %q %q", code, out, errb)
	}
	if !strings.Contains(out, "1 went green->red: TestA") {
		t.Errorf("stdout = %q", out)
	}
}

// ⛔⛔ AN ABORTED RUN ANSWERS FOR NO TEST, and the signal is structural: a
// panic, a timeout and a kill all leave the RUNNING test with an "run" event
// and no verdict, while every test that finished keeps its own. A count of
// failures reads that as a complete run.
func TestATestThatStartedAndNeverReportedIsNoAnswer(t *testing.T) {
	const p = "example.com/pkg"
	logs(t, map[string]string{
		"before": jsonl(t, pkgRun(t, p, "pass", p+" pass TestA", p+" pass TestB")...),
		"after": jsonl(t, pkgRun(t, p, "fail",
			p+" fail TestA",
			p+" run TestB",
			p+" output TestB panic: test timed out after 30s")...),
	})
	code, _, errb := drive(t, "--before", "before", "--after", "after", "--expect", "TestA")
	if code != 2 {
		t.Fatalf("exit %d, want 2: the run was killed\n%q", code, errb)
	}
	if !strings.Contains(errb, "never reported a result") {
		t.Errorf("stderr = %q", errb)
	}
}

// A package that did not build is the same refusal, and it is reported even
// though other packages produced verdicts.
func TestAPackageThatDidNotBuildIsNoAnswer(t *testing.T) {
	const a, b = "example.com/a", "example.com/b"
	logs(t, map[string]string{
		"before": jsonl(t, append(
			pkgRun(t, a, "pass", a+" pass TestA"),
			pkgRun(t, b, "pass", b+" pass TestB")...)...),
		"after": `{"Action":"build-fail","ImportPath":"example.com/b [test]"}` + "\n" +
			jsonl(t, pkgRun(t, a, "fail", a+" fail TestA")...),
	})
	code, _, errb := drive(t, "--before", "before", "--after", "after", "--expect", "TestA")
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.Contains(errb, "the build failed") {
		t.Errorf("stderr = %q", errb)
	}
}

// A package-level fail carrying FailedBuild says the same thing a different
// way, which is how Go reports it when the build error is attributed.
func TestAPackageResultThatNamesAFailedBuildIsNoAnswer(t *testing.T) {
	const p = "example.com/pkg"
	logs(t, map[string]string{
		"before": jsonl(t, pkgRun(t, p, "pass", p+" pass TestA")...),
		"after": `{"Action":"start","Package":"example.com/pkg"}` + "\n" +
			`{"Action":"fail","Package":"example.com/pkg","FailedBuild":"example.com/pkg [test]"}` + "\n",
	})
	code, _, errb := drive(t, "--before", "before", "--after", "after")
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.Contains(errb, "the build failed") {
		t.Errorf("stderr = %q", errb)
	}
}

// A package that started and never reported is an aborted run too.
func TestAPackageThatNeverReportedIsNoAnswer(t *testing.T) {
	const p = "example.com/pkg"
	logs(t, map[string]string{
		"before": jsonl(t, pkgRun(t, p, "pass", p+" pass TestA")...),
		"after":  jsonl(t, p+" start -", p+" pass TestA"),
	})
	code, _, errb := drive(t, "--before", "before", "--after", "after")
	if code != 2 {
		t.Fatalf("exit %d, want 2: %q", code, errb)
	}
}

// ⛔ The BEFORE run is checked the same way: an aborted baseline makes every
// test in the after run look newly red.
func TestAnAbortedBaselineIsRefusedToo(t *testing.T) {
	const p = "example.com/pkg"
	logs(t, map[string]string{
		"before": jsonl(t, pkgRun(t, p, "fail", p+" run TestA")...),
		"after":  jsonl(t, pkgRun(t, p, "fail", p+" fail TestA")...),
	})
	code, _, errb := drive(t, "--before", "before", "--after", "after", "--expect", "TestA")
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.Contains(errb, "--before") {
		t.Errorf("it did not say which run: %q", errb)
	}
}

// ⛔⛔ A `go test -v` TEXT LOG IS REFUSED, WITH THE REASON. It is the format
// this command used to accept, and the one in which a test can forge another
// test's verdict — so it must be told what to do rather than read as an empty
// run.
func TestAPlainTextLogIsRefusedAndSaysWhy(t *testing.T) {
	logs(t, map[string]string{
		"before": "=== RUN   TestA\n--- PASS: TestA (0.00s)\nok  \tpkg\t0.01s\n",
		"after":  "=== RUN   TestA\n--- FAIL: TestA (0.00s)\nFAIL\n",
	})
	code, _, errb := drive(t, "--before", "before", "--after", "after", "--expect", "TestA")
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	for _, want := range []string{"re-run with -json", "forge"} {
		if !strings.Contains(errb, want) {
			t.Errorf("stderr does not say %q: %q", want, errb)
		}
	}
}

// A subtest answers for its parent.
func TestASubtestGoingRedSatisfiesAClaimAboutItsParent(t *testing.T) {
	const p = "example.com/pkg"
	logs(t, map[string]string{
		"before": jsonl(t, pkgRun(t, p, "pass", p+" pass TestA", p+" pass TestA/case")...),
		"after":  jsonl(t, pkgRun(t, p, "fail", p+" fail TestA", p+" fail TestA/case")...),
	})
	code, _, errb := drive(t, "--before", "before", "--after", "after", "--expect", "TestA")
	if code != 0 {
		t.Fatalf("exit %d: %q", code, errb)
	}
}

// ⛔⛔ AND THE SEPARATOR IS REQUIRED, which is the direction that was NOT
// pinned: a review measured `HasPrefix(got, want)` without the "/" surviving,
// because the only case asked whether TestA answers for TestAB. Here TestAB
// goes red and the claim is about TestA, which it must NOT satisfy.
func TestARedTestWhoseNameMerelyStartsWithTheClaimDoesNotCount(t *testing.T) {
	const p = "example.com/pkg"
	logs(t, map[string]string{
		"before": jsonl(t, pkgRun(t, p, "pass", p+" pass TestA", p+" pass TestAB")...),
		"after":  jsonl(t, pkgRun(t, p, "fail", p+" pass TestA", p+" fail TestAB")...),
	})
	code, _, errb := drive(t, "--before", "before", "--after", "after", "--expect", "TestA")
	if code != 1 {
		t.Fatalf("exit %d, want refused: TestAB is not TestA\n%q", code, errb)
	}
}

// The reverse, which was the only direction pinned before.
func TestAPrefixThatIsNotASubtestDoesNotCount(t *testing.T) {
	const p = "example.com/pkg"
	logs(t, map[string]string{
		"before": jsonl(t, pkgRun(t, p, "pass", p+" pass TestA", p+" pass TestAB")...),
		"after":  jsonl(t, pkgRun(t, p, "fail", p+" fail TestA", p+" pass TestAB")...),
	})
	code, _, _ := drive(t, "--before", "before", "--after", "after", "--expect", "TestAB")
	if code != 1 {
		t.Fatalf("exit %d, want refused", code)
	}
}

// ⛔ A test SKIPPED under the mutation is not evidence either, and the reason
// a reader needs is which of the three things happened — not merely "no".
func TestATestSkippedUnderTheMutationIsNotEvidence(t *testing.T) {
	const p = "example.com/pkg"
	logs(t, map[string]string{
		"before": jsonl(t, pkgRun(t, p, "pass", p+" pass TestA")...),
		"after":  jsonl(t, pkgRun(t, p, "pass", p+" skip TestA")...),
	})
	code, _, errb := drive(t, "--before", "before", "--after", "after", "--expect", "TestA")
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(errb, "SKIPPED under the mutation") {
		t.Errorf("stderr = %q", errb)
	}
}

// ⛔ Two packages whose test of the same name BOTH go red are two findings,
// and are reported in a stable order — the name first, then the package, so
// a reader comparing two runs of this command sees the same list twice.
//
// A blank line is left in one log on purpose: `go test -json` output that has
// been concatenated or edited carries them, and skipping them must not be
// mistaken for skipping an event.
func TestTheSameNameRedInTwoPackagesIsTwoFindings(t *testing.T) {
	const a, b = "example.com/zzz", "example.com/aaa"
	logs(t, map[string]string{
		"before": jsonl(t, append(
			pkgRun(t, a, "pass", a+" pass TestX"),
			pkgRun(t, b, "pass", b+" pass TestX")...)...) + "\n\n",
		"after": jsonl(t, append(
			pkgRun(t, a, "fail", a+" fail TestX"),
			pkgRun(t, b, "fail", b+" fail TestX")...)...),
	})
	code, out, errb := drive(t, "--before", "before", "--after", "after", "--expect", "TestX")
	if code != 0 {
		t.Fatalf("exit %d: %q", code, errb)
	}
	if !strings.Contains(out, "2 went green->red") {
		t.Errorf("both packages' failures were not counted: %q", out)
	}
}

// A test already red in both runs is evidence of nothing, and says so.
func TestATestThatWasAlreadyRedIsNotEvidence(t *testing.T) {
	const p = "example.com/pkg"
	logs(t, map[string]string{
		"before": jsonl(t, pkgRun(t, p, "fail", p+" fail TestA")...),
		"after":  jsonl(t, pkgRun(t, p, "fail", p+" fail TestA")...),
	})
	code, _, errb := drive(t, "--before", "before", "--after", "after", "--expect", "TestA")
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(errb, "failed in BOTH runs") {
		t.Errorf("stderr = %q", errb)
	}
}

// A name with no verdict at all in the after run is a typo in the claim.
func TestANameThatNeverRanIsCalledOutAsSuch(t *testing.T) {
	const p = "example.com/pkg"
	logs(t, map[string]string{
		"before": jsonl(t, pkgRun(t, p, "pass", p+" pass TestA")...),
		"after":  jsonl(t, pkgRun(t, p, "fail", p+" fail TestA")...),
	})
	code, _, errb := drive(t, "--before", "before", "--after", "after", "--expect", "TestTypo")
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(errb, "check the name") {
		t.Errorf("stderr = %q", errb)
	}
}

// ⛔ A mutation that makes a test PASS is a fact about the suite and is
// invisible in every count, so it is reported without being an error.
func TestATestThatWentRedToGreenIsReported(t *testing.T) {
	const p = "example.com/pkg"
	logs(t, map[string]string{
		"before": jsonl(t, pkgRun(t, p, "fail", p+" fail TestA", p+" pass TestB")...),
		"after":  jsonl(t, pkgRun(t, p, "fail", p+" pass TestA", p+" fail TestB")...),
	})
	code, out, _ := drive(t, "--before", "before", "--after", "after", "--expect", "TestB")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "red->green: TestA") {
		t.Errorf("the mutation fixed a test and nothing said so: %q", out)
	}
}

// With nothing claimed it is a report rather than a gate.
func TestWithNothingClaimedItOnlyReports(t *testing.T) {
	const p = "example.com/pkg"
	logs(t, map[string]string{
		"before": jsonl(t, pkgRun(t, p, "pass", p+" pass TestA")...),
		"after":  jsonl(t, pkgRun(t, p, "fail", p+" fail TestA")...),
	})
	code, out, _ := drive(t, "--before", "before", "--after", "after")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "green->red: TestA") {
		t.Errorf("stdout = %q", out)
	}
}

// Nothing newly red at all is the sentence a reader needs most.
func TestAMutationThatChangedNothingSaysSo(t *testing.T) {
	const p = "example.com/pkg"
	logs(t, map[string]string{
		"before": jsonl(t, pkgRun(t, p, "pass", p+" pass TestA")...),
		"after":  jsonl(t, pkgRun(t, p, "pass", p+" pass TestA")...),
	})
	code, out, _ := drive(t, "--before", "before", "--after", "after")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "nothing went green->red") {
		t.Errorf("stdout = %q", out)
	}
}

func TestWhatItRefusesToBeAskedFor(t *testing.T) {
	const p = "example.com/pkg"
	logs(t, map[string]string{
		"a": jsonl(t, pkgRun(t, p, "pass", p+" pass TestA")...),
	})
	for _, c := range []struct {
		name string
		argv []string
		says string
	}{
		{"no logs", []string{"--expect", "TestA"}, "each name a"},
		{"only one log", []string{"--before", "a"}, "each name a"},
		{"a log that is not there", []string{"--before", "a", "--after", "gone"}, "no such file"},
	} {
		t.Run(c.name, func(t *testing.T) {
			code, _, errb := drive(t, c.argv...)
			if code != 2 {
				t.Fatalf("exit %d, want 2", code)
			}
			if !strings.Contains(errb, c.says) {
				t.Errorf("it said %q", errb)
			}
		})
	}
}

// ⛔ The real seam, exercised once: the tests above replace it, so without
// this nothing would ever read an actual file.
func TestTheRealReaderReadsAFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.json")
	body := jsonl(t, pkgRun(t, "example.com/pkg", "fail", "example.com/pkg fail TestA")...)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := readRun(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.tests[testID{"example.com/pkg", "TestA"}] != failed {
		t.Errorf("read %+v", r.tests)
	}
	if r.incomplete != "" {
		t.Errorf("a complete run was called incomplete: %q", r.incomplete)
	}
}

// Help is asked for rather than run, and cli.New routes it to stderr.
func TestHelpIsAskedForRatherThanRun(t *testing.T) {
	code, _, errb := drive(t, "--help")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(errb, "SET of failing tests") {
		t.Errorf("stderr = %q", errb)
	}
}
