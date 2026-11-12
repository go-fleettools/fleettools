package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// logs replaces the file seam with a map of canned logs.
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

// ⛔⛔ THE CASE THIS COMMAND WAS WRITTEN FOR, with the real logs of
// go-fleettools/fleettools#252 on 2026-11-12. The mutation removed a staleness
// condition from cmd/aptneed. The suite went red, and I published that this
// proved TestANameAptCouldNotCheckIsNotCalledATypo discriminating.
//
// It did not. That test PASSED under the mutation; two others failed. Red, as
// claimed, on the opposite tests — and no count could have shown it.
func TestTheClaimThatWasPublishedBackwardsIsRefused(t *testing.T) {
	logs(t, map[string]string{
		"before.log": `=== RUN   TestAPackageAptCannotLocateIsNotRetried
--- PASS: TestAPackageAptCannotLocateIsNotRetried (0.00s)
--- PASS: TestANameAptCouldNotCheckIsNotCalledATypo (0.00s)
--- PASS: TestANameAptCouldCheckIsStillRefusedOutright (0.00s)
ok  	github.com/go-fleettools/fleettools/cmd/aptneed	0.054s
`,
		"after.log": `--- FAIL: TestAPackageAptCannotLocateIsNotRetried (0.00s)
--- PASS: TestANameAptCouldNotCheckIsNotCalledATypo (0.00s)
--- FAIL: TestANameAptCouldCheckIsStillRefusedOutright (0.00s)
FAIL	github.com/go-fleettools/fleettools/cmd/aptneed	0.060s
`,
	})
	code, out, errb := drive(t, "--before", "before.log", "--after", "after.log",
		"--expect", "TestANameAptCouldNotCheckIsNotCalledATypo")
	if code != 1 {
		t.Fatalf("exit %d, want the claim refused\nstdout %q\nstderr %q", code, out, errb)
	}
	if !strings.Contains(errb, "it PASSED under the mutation") {
		t.Errorf("it did not say the expected test passed: %q", errb)
	}
	// ⛔ AND IT NAMES WHAT DID GO RED, because "your claim is wrong" without
	// that sends the reader back to the same count they misread.
	for _, want := range []string{
		"TestAPackageAptCannotLocateIsNotRetried",
		"TestANameAptCouldCheckIsStillRefusedOutright",
	} {
		if !strings.Contains(errb, want) {
			t.Errorf("it does not name %s as newly red: %q", want, errb)
		}
	}
}

// And the same two logs with the claim made correctly are accepted.
func TestTheClaimTheRunActuallySupportsIsAccepted(t *testing.T) {
	logs(t, map[string]string{
		"before.log": "--- PASS: TestA (0.00s)\n--- PASS: TestB (0.00s)\nok\n",
		"after.log":  "--- FAIL: TestA (0.00s)\n--- PASS: TestB (0.00s)\nFAIL\n",
	})
	code, out, errb := drive(t, "--before", "before.log", "--after", "after.log", "--expect", "TestA")
	if code != 0 {
		t.Fatalf("exit %d: %q %q", code, out, errb)
	}
	if !strings.Contains(out, "1 went green->red: TestA") {
		t.Errorf("stdout = %q", out)
	}
}

// ⛔⛔ A BUILD FAILURE IS NO ANSWER, NOT ZERO FAILURES. This is the log the
// first run of that same mutation actually produced: the variable it stopped
// using went unused, so nothing compiled and nothing ran. A judge counting
// `--- FAIL` lines reads it as a clean run and reports the mutation survived.
func TestALogThatDidNotBuildIsRefusedRatherThanCalledClean(t *testing.T) {
	logs(t, map[string]string{
		"before.log": "--- PASS: TestA (0.00s)\nok\n",
		"after.log": `# github.com/go-fleettools/fleettools/cmd/aptneed [build failed]
./main.go:204:13: declared and not used: updateStatus
FAIL	github.com/go-fleettools/fleettools/cmd/aptneed [build failed]
FAIL
`,
	})
	code, _, errb := drive(t, "--before", "before.log", "--after", "after.log", "--expect", "TestA")
	if code != 2 {
		t.Fatalf("exit %d, want 2: a log that did not build is no answer\nstderr %q", code, errb)
	}
	if !strings.Contains(errb, "build failed") {
		t.Errorf("it did not say why there was no answer: %q", errb)
	}
}

// An empty log, or one whose -run filter matched nothing, is the same refusal
// without a marker to quote.
func TestALogWithNoVerdictsAtAllIsRefused(t *testing.T) {
	logs(t, map[string]string{
		"before.log": "--- PASS: TestA (0.00s)\nok\n",
		"after.log":  "testing: warning: no tests to run\nPASS\nok  \tpkg\t0.001s\n",
	})
	code, _, errb := drive(t, "--before", "before.log", "--after", "after.log")
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.Contains(errb, "no test verdicts at all") {
		t.Errorf("stderr = %q", errb)
	}
}

// ⛔ The --before log is checked the same way. A baseline that did not build
// makes every test in the --after log look newly red.
func TestAnUnbuildableBaselineIsRefusedToo(t *testing.T) {
	logs(t, map[string]string{
		"before.log": "FAIL\tpkg [build failed]\n",
		"after.log":  "--- FAIL: TestA (0.00s)\nFAIL\n",
	})
	code, _, errb := drive(t, "--before", "before.log", "--after", "after.log", "--expect", "TestA")
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.Contains(errb, "--before") {
		t.Errorf("it did not say which log: %q", errb)
	}
}

// A subtest answers for its parent, because that is how Go reports one.
func TestASubtestGoingRedSatisfiesAClaimAboutItsParent(t *testing.T) {
	logs(t, map[string]string{
		"before.log": "    --- PASS: TestA/case (0.00s)\n--- PASS: TestA (0.00s)\nok\n",
		"after.log":  "    --- FAIL: TestA/case (0.00s)\n--- FAIL: TestA (0.00s)\nFAIL\n",
	})
	code, _, errb := drive(t, "--before", "before.log", "--after", "after.log", "--expect", "TestA")
	if code != 0 {
		t.Fatalf("exit %d: %q", code, errb)
	}
}

// ⛔ AND A PARENT GOING RED DOES NOT ANSWER FOR AN ARBITRARY SIBLING. covers()
// is deliberately one-directional: TestA failing says nothing about TestAB.
func TestAPrefixThatIsNotASubtestDoesNotCount(t *testing.T) {
	logs(t, map[string]string{
		"before.log": "--- PASS: TestA (0.00s)\n--- PASS: TestAB (0.00s)\nok\n",
		"after.log":  "--- FAIL: TestA (0.00s)\n--- PASS: TestAB (0.00s)\nFAIL\n",
	})
	code, _, errb := drive(t, "--before", "before.log", "--after", "after.log", "--expect", "TestAB")
	if code != 1 {
		t.Fatalf("exit %d, want the claim about TestAB refused: %q", code, errb)
	}
}

// A test that was ALREADY red says nothing about the mutation, and saying so
// is the difference between evidence and coincidence.
func TestATestThatWasAlreadyRedIsNotEvidence(t *testing.T) {
	logs(t, map[string]string{
		"before.log": "--- FAIL: TestA (0.00s)\nFAIL\n",
		"after.log":  "--- FAIL: TestA (0.00s)\nFAIL\n",
	})
	code, _, errb := drive(t, "--before", "before.log", "--after", "after.log", "--expect", "TestA")
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(errb, "failed in BOTH runs") {
		t.Errorf("stderr = %q", errb)
	}
}

// A name with no verdict in the --after log is a typo in the claim, and is
// told apart from a test that ran and passed.
func TestANameThatNeverRanIsCalledOutAsSuch(t *testing.T) {
	logs(t, map[string]string{
		"before.log": "--- PASS: TestA (0.00s)\nok\n",
		"after.log":  "--- FAIL: TestA (0.00s)\nFAIL\n",
	})
	code, _, errb := drive(t, "--before", "before.log", "--after", "after.log", "--expect", "TestTypo")
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(errb, "check the name") {
		t.Errorf("stderr = %q", errb)
	}
}

// ⛔ A mutation that makes a test PASS is a fact about the suite, and is
// invisible in any count. It is reported without being an error.
func TestATestThatWentRedToGreenIsReported(t *testing.T) {
	logs(t, map[string]string{
		"before.log": "--- FAIL: TestA (0.00s)\n--- PASS: TestB (0.00s)\nFAIL\n",
		"after.log":  "--- PASS: TestA (0.00s)\n--- FAIL: TestB (0.00s)\nFAIL\n",
	})
	code, out, _ := drive(t, "--before", "before.log", "--after", "after.log", "--expect", "TestB")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "red->green: TestA") {
		t.Errorf("the mutation fixed a test and nothing said so: %q", out)
	}
}

// With no --expect at all it is a report rather than a gate, and says so by
// succeeding.
func TestWithNothingClaimedItOnlyReports(t *testing.T) {
	logs(t, map[string]string{
		"before.log": "--- PASS: TestA (0.00s)\nok\n",
		"after.log":  "--- FAIL: TestA (0.00s)\nFAIL\n",
	})
	code, out, _ := drive(t, "--before", "before.log", "--after", "after.log")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "green->red: TestA") {
		t.Errorf("stdout = %q", out)
	}
}

// Nothing newly red at all is the sentence a reader needs most: the mutation
// changed nothing, so whatever it was meant to prove, it did not.
func TestAMutationThatChangedNothingSaysSo(t *testing.T) {
	logs(t, map[string]string{
		"before.log": "--- PASS: TestA (0.00s)\nok\n",
		"after.log":  "--- PASS: TestA (0.00s)\nok\n",
	})
	code, out, _ := drive(t, "--before", "before.log", "--after", "after.log")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "nothing went green->red") {
		t.Errorf("stdout = %q", out)
	}
}

// A test reported twice in one log — a retry, or two runs of the package —
// keeps its LAST verdict.
func TestTheLastVerdictForANameIsTheOneThatStood(t *testing.T) {
	logs(t, map[string]string{
		"before.log": "--- PASS: TestA (0.00s)\nok\n",
		"after.log":  "--- FAIL: TestA (0.00s)\n--- PASS: TestA (0.00s)\nok\n",
	})
	code, out, _ := drive(t, "--before", "before.log", "--after", "after.log")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "nothing went green->red") {
		t.Errorf("a retried test that ended green was reported red: %q", out)
	}
}

func TestWhatItRefusesToBeAskedFor(t *testing.T) {
	logs(t, map[string]string{"a.log": "--- PASS: TestA (0.00s)\nok\n"})
	for _, c := range []struct {
		name string
		argv []string
		says string
	}{
		{"no logs", []string{"--expect", "TestA"}, "each name a"},
		{"only one log", []string{"--before", "a.log"}, "each name a"},
		{"a log that is not there", []string{"--before", "a.log", "--after", "gone.log"}, "no such file"},
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

// ⛔ The real seam, exercised once: the tests above replace it, so without this
// nothing would ever read an actual file.
func TestTheRealReaderReadsAFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.log")
	if err := os.WriteFile(p, []byte("--- FAIL: TestA (0.00s)\nFAIL\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := readLog(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.verdicts != 1 || r.tests["TestA"] != failed {
		t.Errorf("read %+v", r)
	}
}

// Help is asked for rather than run, and cli.New routes it to stderr.
func TestHelpIsAskedForRatherThanRun(t *testing.T) {
	code, _, errb := drive(t, "--help")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(errb, "SET of failing test names") {
		t.Errorf("stderr = %q", errb)
	}
}
