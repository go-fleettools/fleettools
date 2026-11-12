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
