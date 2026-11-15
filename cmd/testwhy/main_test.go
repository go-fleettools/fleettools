package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// drive runs the command and returns its status and combined output.
func drive(t *testing.T, argv ...string) (int, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run(argv, &out, &errOut)
	return code, out.String() + errOut.String()
}

// write puts a log in a temp file and returns its path.
func write(t *testing.T, body string) string {
	t.Helper()
	p := t.TempDir() + "/log.txt"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// realShape is the log that provoked this command: every failure is the same
// known breakage, and three of the --- FAIL lines are a PARENT and its subtests.
const realShape = `=== RUN   TestCollabLocalTabSequentialConvergence
--- FAIL: TestCollabLocalTabSequentialConvergence (0.46s)
    --- FAIL: TestCollabLocalTabSequentialConvergence/gap-300ms (0.02s)
        x_test.go:88: driver output:
            dyld[1]: Library not loaded: libsimdjson.34.dylib
    --- FAIL: TestCollabLocalTabSequentialConvergence/gap-900ms (0.02s)
        x_test.go:88: driver output:
            dyld[2]: Library not loaded: libsimdjson.34.dylib
=== RUN   TestDeadKeyBrowserWiring
--- FAIL: TestDeadKeyBrowserWiring (0.41s)
    y_test.go:79: browser driver output:
        dyld[3]: Library not loaded: libsimdjson.34.dylib
FAIL	example/pkg	15.945s
`

// ⛔ THE CASE THE FIRST VERSION GOT WRONG. The parent's own --- FAIL line has an
// empty body, so nothing matches against it directly; it is attributed only
// because every one of its subtests is. Reported as unexplained, it would send a
// reader hunting a regression that does not exist.
func TestAParentIsAttributedWhenEverySubtestIs(t *testing.T) {
	code, out := drive(t, "-log", write(t, realShape), "-cause", "node=libsimdjson")
	if code != 0 {
		t.Errorf("status = %d, want 0; output:\n%s", code, out)
	}
	if !strings.Contains(out, "failures=4 attributed=4 unexplained=0") {
		t.Errorf("want 4 failures all attributed; got:\n%s", out)
	}
	if !strings.Contains(out, "node: 4") {
		t.Errorf("the cause was not counted:\n%s", out)
	}
	if strings.Contains(out, "unexplained:") {
		t.Errorf("something was reported unexplained:\n%s", out)
	}
}

// …and the other half of that rule: ONE subtest with a different reason keeps
// the parent unattributed, because that subtest is the real finding.
func TestOneRealSubtestKeepsItsParentUnattributed(t *testing.T) {
	const log = `=== RUN   TestOneKidReal
--- FAIL: TestOneKidReal (0.46s)
    --- FAIL: TestOneKidReal/a (0.02s)
        x_test.go:1: dyld[3]: Library not loaded: libsimdjson.34.dylib
    --- FAIL: TestOneKidReal/b (0.02s)
        x_test.go:9: mark for "b.tex" = 1, want 2
FAIL	example/pkg	0.5s
`
	code, out := drive(t, "-log", write(t, log), "-cause", "node=libsimdjson")
	if code != 1 {
		t.Errorf("status = %d, want 1; output:\n%s", code, out)
	}
	for _, want := range []string{
		"unexplained: TestOneKidReal",
		"unexplained: TestOneKidReal/b",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "unexplained: TestOneKidReal/a") {
		t.Errorf("the attributed subtest was reported unexplained:\n%s", out)
	}
}

// ⛔ A PACKAGE THAT DID NOT BUILD PRINTS NO --- FAIL. Counting them reports
// zero, which is the shape of a scan that could not read reporting a clean
// sweep. No set of causes may explain it away.
func TestABuildFailureIsNeverExplainedAway(t *testing.T) {
	const log = "FAIL\texample/broken [build failed]\n"
	code, out := drive(t, "-log", write(t, log), "-cause", "node=libsimdjson")
	if code != 1 {
		t.Errorf("status = %d, want 1; output:\n%s", code, out)
	}
	if !strings.Contains(out, "failures=0") {
		t.Errorf("want the honest zero failure count beside it:\n%s", out)
	}
	if !strings.Contains(out, "did not BUILD") {
		t.Errorf("the build failure was not reported:\n%s", out)
	}
}

// ⛔ A CAUSE MATCHING THE EMPTY STRING WOULD ATTRIBUTE EVERYTHING, turning this
// into a command that always exits 0 — the exact failure it exists to prevent.
func TestAnEmptyCauseIsRefusedRatherThanAccepted(t *testing.T) {
	for _, bad := range []string{"node=", "=libsimdjson", "node"} {
		code, out := drive(t, "-log", write(t, realShape), "-cause", bad)
		if code != 2 {
			t.Errorf("-cause %q: status = %d, want 2; output:\n%s", bad, code, out)
		}
	}
}

// ⛔ THE TIME IS A STANDING WITNESS: a suite that should take seconds and
// returns in 0.0s ran nothing, and every test in it "passed".
func TestASuiteThatReturnedTooFastIsAFinding(t *testing.T) {
	const log = "ok  \texample/pkg\t0.012s\n"
	code, out := drive(t, "-log", write(t, log), "-min-seconds", "1")
	if code != 1 {
		t.Errorf("status = %d, want 1; output:\n%s", code, out)
	}
	if !strings.Contains(out, "ran nothing") {
		t.Errorf("the fast package was not reported:\n%s", out)
	}
	// Without the flag the same log is clean: the check is opt-in, because only
	// the caller knows how long its suite should take.
	if code, out := drive(t, "-log", write(t, log)); code != 0 {
		t.Errorf("without -min-seconds: status = %d, want 0; output:\n%s", code, out)
	}
}

// A package with no test files is not a suite that ran nothing: it is a package
// with nothing to run, and -min-seconds must not call it a finding.
func TestAPackageWithNoTestFilesIsNotAFastSuite(t *testing.T) {
	const log = "?   \texample/pkg\t[no test files]\n"
	code, out := drive(t, "-log", write(t, log), "-min-seconds", "1")
	if code != 0 {
		t.Errorf("status = %d, want 0; output:\n%s", code, out)
	}
}

// A failure no cause explains is the finding, and it is named.
func TestAnUnexplainedFailureIsNamedAndIsTheFinding(t *testing.T) {
	const log = `=== RUN   TestPlainRegression
--- FAIL: TestPlainRegression (0.01s)
    y_test.go:3: Marks=map[] want map[refs.bib:2]
FAIL	example/pkg	0.5s
`
	code, out := drive(t, "-log", write(t, log), "-cause", "node=libsimdjson")
	if code != 1 {
		t.Errorf("status = %d, want 1; output:\n%s", code, out)
	}
	if !strings.Contains(out, "unexplained: TestPlainRegression") {
		t.Errorf("the failure was not named:\n%s", out)
	}
}

// The package line and its TIME are always printed, because a reader deciding
// "same as before" needs them BESIDE the failure count, not instead of it.
func TestThePackageLineAndItsTimeAreAlwaysPrinted(t *testing.T) {
	_, out := drive(t, "-log", write(t, realShape), "-cause", "node=libsimdjson")
	if !strings.Contains(out, "pkg FAIL example/pkg 15.945s") {
		t.Errorf("the package line is missing or unparsed:\n%s", out)
	}
}

// A missing -log cannot be answered, which is a 2 and not a 0.
func TestAMissingLogIsUnanswerableAndNotAPass(t *testing.T) {
	code, out := drive(t)
	if code != 2 {
		t.Errorf("status = %d, want 2; output:\n%s", code, out)
	}
}

// An unreadable log is likewise unanswerable: "no failures found" in a file
// that could not be opened is the oldest wrong answer there is.
func TestAnUnreadableLogIsUnanswerableAndNotAPass(t *testing.T) {
	code, out := drive(t, "-log", t.TempDir()+"/does-not-exist.txt")
	if code != 2 {
		t.Errorf("status = %d, want 2; output:\n%s", code, out)
	}
}

// -all names each attributed failure too, for a reader who wants the whole map
// rather than only what is wrong.
func TestAllNamesTheAttributedFailuresToo(t *testing.T) {
	_, out := drive(t, "-log", write(t, realShape), "-cause", "node=libsimdjson", "-all")
	if !strings.Contains(out, "node: TestDeadKeyBrowserWiring") {
		t.Errorf("-all did not name an attributed failure:\n%s", out)
	}
}

// A log read from standard input, since that is how it arrives from a pipe.
func TestTheLogMayComeFromStandardInput(t *testing.T) {
	c := RootCmd()
	var out bytes.Buffer
	// Two dashes here on purpose: this drives the cobra command directly, past
	// the single-dash compatibility shim that cli.Drive applies.
	c.SetArgs([]string{"--log", "-", "--cause", "node=libsimdjson"})
	c.SetIn(strings.NewReader(realShape))
	c.SetOut(&out)
	c.SetErr(&out)
	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v; output:\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "attributed=4") {
		t.Errorf("standard input was not read:\n%s", out.String())
	}
}

// ⛔ The log `gh run view --log-failed` prints prefixes every line with
// `<job>\t<step>\t<timestamp> `. Read raw, neither `--- FAIL` nor `FAIL\t<pkg>`
// starts a line and the scan reported zero for a red run (#280). The fixture is
// an excerpt of the real log of go-fleettools/fleettools#278.
func TestActionsPrefixedLogIsRead(t *testing.T) {
	code, out := drive(t, "-log", "testdata/actions-race.log")
	if code != 1 {
		t.Errorf("status = %d, want 1; output:\n%s", code, out)
	}
	if !strings.Contains(out, "unexplained: TestPretoolAllowsWhenTheTranscriptVanishesDuringTheWait") {
		t.Errorf("the failing test was not read through the prefix:\n%s", out)
	}
	if !strings.Contains(out, "pkg FAIL github.com/go-fleettools/fleettools/cmd/replystamp 84.803s") {
		t.Errorf("the failing package was not read through the prefix:\n%s", out)
	}
}

// A package FAIL with no `--- FAIL` test (race-only) is an unexplained failure.
func TestPackageFailWithoutATestIsUnexplained(t *testing.T) {
	code, out := drive(t, "-log", "testdata/race-only.log")
	if code != 1 {
		t.Errorf("status = %d, want 1; output:\n%s", code, out)
	}
	if !strings.Contains(out, "failures=1 attributed=0 unexplained=1") ||
		!strings.Contains(out, "package example/b") {
		t.Errorf("a race-only package failure was not counted:\n%s", out)
	}
}

// ⛔ A scan that could not read reported zero: refuse, exit 2.
func TestUnrecognisableLogIsRefused(t *testing.T) {
	code, out := drive(t, "-log", "testdata/unreadable.log")
	if code != 2 {
		t.Errorf("status = %d, want 2; output:\n%s", code, out)
	}
	if strings.Contains(out, "failures=0") {
		t.Errorf("reported a count for an unread log:\n%s", out)
	}
}

// Two packages failing the SAME test name, both attributed: the second is not a
// package FAIL without a test (a matrix log does exactly this).
func TestSameTestNameFailingInTwoPackagesIsNotUnexplained(t *testing.T) {
	log := "--- FAIL: TestRun (0.1s)\n    x_test.go:1: net down\nFAIL\nFAIL\texample/a\t1.0s\n" +
		"--- FAIL: TestRun (0.1s)\n    x_test.go:1: net down\nFAIL\nFAIL\texample/b\t1.0s\n"
	code, out := drive(t, "--log", write(t, log), "--cause", "net=net down")
	if code != 0 || strings.Contains(out, "no --- FAIL test") ||
		!strings.Contains(out, "failures=1 attributed=1 unexplained=0") {
		t.Errorf("status %d; a repeated test name read as a bare package FAIL:\n%s", code, out)
	}
}

// ⛔ A t.Logf line that LOOKS prefixed, in a plain log, must not be stripped
// into a failure that never happened (the #276 class).
func TestLookalikePrefixInAPlainLogInventsNoFailure(t *testing.T) {
	code, out := drive(t, "--log", "testdata/logf-lookalike.log")
	if code != 0 || !strings.Contains(out, "failures=0") || strings.Contains(out, "TestGhost") {
		t.Errorf("status %d; a failure was invented:\n%s", code, out)
	}
}

// skipLogReal is a `go test -v` log in the shape the real one has: the skip
// message sits under its `--- SKIP` line, indented. TestWasm is a guard behind an
// unset variable (the rbgo#682 case).
const skipLogReal = `=== RUN   TestWasm
--- SKIP: TestWasm (0.00s)
    wasm_test.go:7: RBGO_BUILD_IT not set
=== RUN   TestOK
--- PASS: TestOK (0.00s)
PASS
ok  	example/pkg	1.012s
`

// ⛔ A SKIP NO ALLOWANCE ACCEPTS IS THE FINDING: exit 1, and it is named.
func TestAnUndeclaredSkipIsNamedAndExitsOne(t *testing.T) {
	code, out := drive(t, "-log", write(t, skipLogReal), "-skipped")
	if code != 1 {
		t.Errorf("status = %d, want 1; output:\n%s", code, out)
	}
	if !strings.Contains(out, "undeclared skip: TestWasm") ||
		!strings.Contains(out, "skips=1 allowed=0 undeclared=1") {
		t.Errorf("the skip was not named:\n%s", out)
	}
	// An allowance that matches ANOTHER message does not accept it.
	code, out = drive(t, "-log", write(t, skipLogReal), "-skipped", "-allow-skip", "font=no system fonts")
	if code != 1 || !strings.Contains(out, "undeclared skip: TestWasm") {
		t.Errorf("a non-matching allowance accepted the skip: status %d\n%s", code, out)
	}
}

// A declared skip is accounted for: exit 0 and counted under its name.
func TestADeclaredSkipIsAccountedFor(t *testing.T) {
	code, out := drive(t, "-log", write(t, skipLogReal), "-skipped", "-allow-skip", "wasm=RBGO_BUILD_IT")
	if code != 0 {
		t.Errorf("status = %d, want 0; output:\n%s", code, out)
	}
	if !strings.Contains(out, "skips=1 allowed=1 undeclared=0") || !strings.Contains(out, "allowed wasm: 1") ||
		strings.Contains(out, "undeclared skip") {
		t.Errorf("the allowed skip was not counted:\n%s", out)
	}
	_, out = drive(t, "-log", write(t, skipLogReal), "-skipped", "-allow-skip", "wasm=RBGO_BUILD_IT", "-all")
	if !strings.Contains(out, "skip wasm: TestWasm") {
		t.Errorf("-all did not name the allowed skip:\n%s", out)
	}
}

// Without -skipped the skip is not judged: the check is opt-in.
func TestSkipsAreIgnoredWithoutTheFlag(t *testing.T) {
	code, out := drive(t, "-log", write(t, skipLogReal))
	if code != 0 || strings.Contains(out, "skips=") {
		t.Errorf("status %d; skips judged without -skipped:\n%s", code, out)
	}
}

// ⛔ AN ALLOWANCE MATCHING THE EMPTY STRING ACCEPTS EVERY SKIP: refused, as -cause is.
func TestAnEmptyAllowSkipIsRefusedRatherThanAccepted(t *testing.T) {
	for _, bad := range []string{"wasm=", "=RBGO_BUILD_IT", "wasm"} {
		code, out := drive(t, "-log", write(t, skipLogReal), "-skipped", "-allow-skip", bad)
		if code != 2 {
			t.Errorf("-allow-skip %q: status = %d, want 2; output:\n%s", bad, code, out)
		}
		if !strings.Contains(out, "-allow-skip") {
			t.Errorf("-allow-skip %q: the refusal does not name the flag:\n%s", bad, out)
		}
	}
}

// An allowance with the check off is a declaration nothing reads: refused.
func TestAllowSkipWithoutSkippedIsRefused(t *testing.T) {
	code, out := drive(t, "-log", write(t, skipLogReal), "-allow-skip", "wasm=RBGO_BUILD_IT")
	if code != 2 {
		t.Errorf("status = %d, want 2; output:\n%s", code, out)
	}
}

// ⛔ WITHOUT -v A SKIP IS NOT PRINTED. A log that cannot show one must not be
// answered "no undeclared skips".
func TestSkippedOnALogWithoutVerboseOutputIsRefused(t *testing.T) {
	code, out := drive(t, "-log", write(t, "ok  \texample/pkg\t0.012s\n"), "-skipped")
	if code != 2 {
		t.Errorf("status = %d, want 2; output:\n%s", code, out)
	}
	if strings.Contains(out, "skips=0") {
		t.Errorf("reported a count for a log that cannot show skips:\n%s", out)
	}
}

// ⛔ A PARENT CARRIES NO MESSAGE OF ITS OWN, for a skip as for a failure: it is
// attributed when it HAS subtests and EVERY one of them is.
const subtestSkips = `=== RUN   TestParent
--- SKIP: TestParent (0.00s)
    --- SKIP: TestParent/a (0.00s)
        p_test.go:3: no GPU
    --- SKIP: TestParent/b (0.00s)
        p_test.go:3: no GPU
ok  	example/pkg	0.5s
`

func TestAParentSkipIsAttributedWhenEverySubtestIs(t *testing.T) {
	code, out := drive(t, "-log", write(t, subtestSkips), "-skipped", "-allow-skip", "gpu=no GPU")
	if code != 0 {
		t.Errorf("status = %d, want 0; output:\n%s", code, out)
	}
	if !strings.Contains(out, "skips=3 allowed=3 undeclared=0") || strings.Contains(out, "undeclared skip") {
		t.Errorf("the parent was not attributed from its subtests:\n%s", out)
	}
}

// A log cut before its package line (a partial `gh run view --log-failed`) still
// attributes the parent: the last package's skips are not left unjudged.
func TestAParentSkipIsAttributedInALogCutBeforeItsPackageLine(t *testing.T) {
	log := strings.Replace(subtestSkips, "ok  \texample/pkg\t0.5s\n", "", 1)
	code, out := drive(t, "-log", write(t, log), "-skipped", "-allow-skip", "gpu=no GPU")
	if code != 0 || !strings.Contains(out, "skips=3 allowed=3 undeclared=0") {
		t.Errorf("status %d; the parent was left unattributed:\n%s", code, out)
	}
}

// …and ONE subtest skipped for another reason keeps the parent undeclared,
// because that subtest is the real finding.
func TestOneUndeclaredSubtestSkipKeepsItsParentUndeclared(t *testing.T) {
	log := strings.Replace(subtestSkips, "p_test.go:3: no GPU\nok", "p_test.go:9: RBGO_BUILD_IT unset\nok", 1)
	code, out := drive(t, "-log", write(t, log), "-skipped", "-allow-skip", "gpu=no GPU")
	if code != 1 {
		t.Errorf("status = %d, want 1; output:\n%s", code, out)
	}
	for _, want := range []string{"undeclared skip: TestParent\n", "undeclared skip: TestParent/b\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "undeclared skip: TestParent/a") {
		t.Errorf("the allowed subtest was reported:\n%s", out)
	}
}

// A skip body ends at the next test boundary: the NEXT test's output must not be
// read as this skip's reason, or a declared substring in it would accept it.
func TestASkipBodyEndsAtTheNextBoundary(t *testing.T) {
	const log = `=== RUN   TestA
--- SKIP: TestA (0.00s)
    a_test.go:1: some reason
=== RUN   TestB
    b_test.go:1: RBGO_BUILD_IT mentioned by another test
--- PASS: TestB (0.00s)
ok  	example/pkg	0.5s
`
	code, out := drive(t, "-log", write(t, log), "-skipped", "-allow-skip", "wasm=RBGO_BUILD_IT")
	if code != 1 || !strings.Contains(out, "undeclared skip: TestA") {
		t.Errorf("status %d; another test's output accepted the skip:\n%s", code, out)
	}
	// Parallel tests are announced with `=== NAME`, which also ends the body.
	named := strings.Replace(log, "=== RUN   TestB", "=== NAME  TestB", 1)
	code, out = drive(t, "-log", write(t, named), "-skipped", "-allow-skip", "wasm=RBGO_BUILD_IT")
	if code != 1 || !strings.Contains(out, "undeclared skip: TestA") {
		t.Errorf("=== NAME: status %d; another test's output accepted the skip:\n%s", code, out)
	}
}

// The same test name skipped for two reasons in two packages: the accepted one
// must not hide the other (skips are not deduplicated by name).
func TestTheSameSkippedNameInTwoPackagesIsJudgedTwice(t *testing.T) {
	const log = `--- SKIP: TestRun (0.00s)
    a_test.go:1: no GPU
ok  	example/a	0.5s
--- SKIP: TestRun (0.00s)
    b_test.go:1: RBGO_BUILD_IT unset
ok  	example/b	0.5s
`
	code, out := drive(t, "-log", write(t, log), "-skipped", "-allow-skip", "gpu=no GPU")
	if code != 1 || !strings.Contains(out, "skips=2 allowed=1 undeclared=1") {
		t.Errorf("status %d; the second package's skip was hidden:\n%s", code, out)
	}
}

// ⛔ THE TWO CHECKS SHARE ONE EXIT STATUS, so a single return could let the
// satisfied one speak for the broken one. Both directions.
const failAndSkip = `=== RUN   TestBrowser
--- FAIL: TestBrowser (0.01s)
    y_test.go:3: dyld: Library not loaded: libsimdjson.34.dylib
=== RUN   TestWasm
--- SKIP: TestWasm (0.00s)
    wasm_test.go:7: RBGO_BUILD_IT not set
FAIL	example/pkg	0.5s
`

func TestAnExplainedFailureDoesNotExcuseAnUndeclaredSkip(t *testing.T) {
	code, out := drive(t, "-log", write(t, failAndSkip), "-cause", "node=libsimdjson", "-skipped")
	if code != 1 || !strings.Contains(out, "undeclared skip: TestWasm") ||
		!strings.Contains(out, "failures=1 attributed=1 unexplained=0") {
		t.Errorf("status %d; the explained failure spoke for the skip:\n%s", code, out)
	}
}

func TestADeclaredSkipDoesNotExcuseAnUnexplainedFailure(t *testing.T) {
	code, out := drive(t, "-log", write(t, failAndSkip), "-skipped", "-allow-skip", "wasm=RBGO_BUILD_IT")
	if code != 1 || !strings.Contains(out, "unexplained: TestBrowser") ||
		!strings.Contains(out, "skips=1 allowed=1 undeclared=0") {
		t.Errorf("status %d; the declared skip spoke for the failure:\n%s", code, out)
	}
	// and with both satisfied it is clean
	code, out = drive(t, "-log", write(t, failAndSkip), "-cause", "node=libsimdjson", "-skipped", "-allow-skip", "wasm=RBGO_BUILD_IT")
	if code != 0 {
		t.Errorf("both satisfied: status %d, want 0:\n%s", code, out)
	}
}
