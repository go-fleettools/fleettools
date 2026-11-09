package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// gotestRun drives the command with a canned `go test`, and returns the exit
// status, stdout, stderr and the arguments the fake was handed.
func gotestRun(t *testing.T, status int, runErr error, output string, args ...string) (int, string, string, []string) {
	t.Helper()
	old := goTest
	t.Cleanup(func() { goTest = old })
	var saw []string
	goTest = func(a []string, out io.Writer) (int, error) {
		saw = append([]string(nil), a...)
		_, _ = io.WriteString(out, output)
		return status, runErr
	}
	var out, errb bytes.Buffer
	rc := run(args, &out, &errb)
	return rc, out.String(), errb.String(), saw
}

// ⛔⛔ THE CASE THIS COMMAND EXISTS FOR, in the output `go test` actually
// printed on 2026-11-09 in go-zpkg/zbk. Three alternatives, one of them a name
// that matched nothing, four tests running from the other two, and `ok`.
const theRealOutput = `=== RUN   TestMainMultiCallDispatch
--- PASS: TestMainMultiCallDispatch (0.00s)
=== RUN   TestUsageAndErrors
--- PASS: TestUsageAndErrors (0.00s)
=== RUN   TestMainSeam
--- PASS: TestMainSeam (0.00s)
=== RUN   TestRunTestUsage
=== RUN   TestRunTestUsage/no_project
--- PASS: TestRunTestUsage (0.00s)
    --- PASS: TestRunTestUsage/no_project (0.00s)
PASS
ok  	github.com/go-zpkg/zbk/cmd/zbk	0.159s
`

func TestAnAlternativeThatMatchedNothingFailsAlthoughGoTestPassed(t *testing.T) {
	rc, out, errb, _ := gotestRun(t, 0, nil, theRealOutput,
		"-run", "TestREADME|TestUsage|TestMain", "./cmd/zbk")
	if rc != 1 {
		t.Fatalf("exit %d, want 1 — `go test` exited 0 and that is the whole point", rc)
	}
	if !strings.Contains(errb, "TestREADME") {
		t.Errorf("stderr does not name the alternative that matched nothing: %q", errb)
	}
	// The three that did match must NOT be named: a verdict that listed them
	// too would be read as "none of this ran".
	for _, ok := range []string{"TestUsage\n", "TestMain\n"} {
		if strings.Contains(errb, "  "+ok) {
			t.Errorf("stderr names an alternative that DID match: %q", errb)
		}
	}
	if !strings.Contains(errb, "4 test(s) did run") {
		t.Errorf("stderr does not say how many ran, which is why go test said nothing: %q", errb)
	}
	// go test's own output still reaches the caller.
	if !strings.Contains(out, "ok  \tgithub.com/go-zpkg/zbk/cmd/zbk") {
		t.Errorf("go test's output did not pass through: %q", out)
	}
}

// Every alternative matched: the command is a pass-through and says nothing.
func TestEveryAlternativeMatchedIsSilent(t *testing.T) {
	rc, _, errb, _ := gotestRun(t, 0, nil, theRealOutput,
		"-run", "TestUsage|TestMain", "./cmd/zbk")
	if rc != 0 {
		t.Fatalf("exit %d, want 0; stderr: %s", rc, errb)
	}
	if strings.Contains(errb, "matched no test") {
		t.Errorf("stderr complains about a pattern that matched: %q", errb)
	}
}

// ⛔ NOTHING RAN AT ALL gets its own sentence. `go test` prints
// "[no tests to run]" here and exits 0, so without this the run is a pass
// about nothing.
func TestAnEmptySelectionIsNotAPass(t *testing.T) {
	const empty = "testing: warning: no tests to run\nPASS\nok  \texample.com/m\t0.04s [no tests to run]\n"
	rc, _, errb, _ := gotestRun(t, 0, nil, empty, "-run", "NoSuchTest", "./m")
	if rc != 1 {
		t.Fatalf("exit %d, want 1", rc)
	}
	if !strings.Contains(errb, "selected NO test at all") {
		t.Errorf("stderr does not say the selection was empty: %q", errb)
	}
}

// Without -run there is nothing to check, and nothing is added.
func TestWithoutRunItIsAPlainPassThrough(t *testing.T) {
	rc, _, errb, saw := gotestRun(t, 0, nil, "ok  \texample.com/m\t0.1s\n", "./...")
	if rc != 0 {
		t.Fatalf("exit %d, want 0", rc)
	}
	if len(saw) != 1 || saw[0] != "./..." {
		t.Errorf("arguments were changed: %v", saw)
	}
	if errb != "" {
		t.Errorf("stderr should be silent: %q", errb)
	}
}

// A failing `go test` keeps its status, and is not reported as a selection
// problem: the two are different findings and this fleet has confused them.
func TestAFailingRunKeepsItsStatus(t *testing.T) {
	const failed = "=== RUN   TestUsageAndErrors\n--- FAIL: TestUsageAndErrors (0.00s)\nFAIL\n"
	rc, _, errb, _ := gotestRun(t, 1, nil, failed, "-run", "TestUsage", "./m")
	if rc != 1 {
		t.Fatalf("exit %d, want 1", rc)
	}
	if strings.Contains(errb, "matched no test") {
		t.Errorf("a real failure was reported as a selection problem: %q", errb)
	}
}

// -v is added when it must be, and SAID. Silently running a different command
// than the one asked for is worse than the extra output.
func TestVerboseIsAddedAndAnnounced(t *testing.T) {
	_, _, errb, saw := gotestRun(t, 0, nil, theRealOutput, "-run", "TestUsage", "./m")
	if len(saw) == 0 || saw[0] != "-v" {
		t.Fatalf("-v was not added: %v", saw)
	}
	if !strings.Contains(errb, "added -v") {
		t.Errorf("adding -v was not announced: %q", errb)
	}
}

func TestVerboseAlreadyThereIsNotAddedTwice(t *testing.T) {
	_, _, errb, saw := gotestRun(t, 0, nil, theRealOutput, "-v", "-run", "TestUsage", "./m")
	for i, a := range saw {
		if a == "-v" && i != 0 {
			t.Errorf("-v appears twice: %v", saw)
		}
	}
	if strings.Contains(errb, "added -v") {
		t.Errorf("announced adding -v when it was already there: %q", errb)
	}
}

// `go test` failing to START is unanswerable, not a verdict about tests.
func TestAnUnrunnableGoTestIsUnanswerable(t *testing.T) {
	rc, _, errb, _ := gotestRun(t, 0, errors.New("exec: \"go\": not found"), "", "-run", "TestX", "./m")
	if rc != 2 {
		t.Fatalf("exit %d, want 2 (unanswerable)", rc)
	}
	if !strings.Contains(errb, "not found") {
		t.Errorf("stderr does not carry the cause: %q", errb)
	}
}

func TestAnEmptyPatternIsRefused(t *testing.T) {
	rc, _, errb, _ := gotestRun(t, 0, nil, theRealOutput, "-run=", "./m")
	if rc != 2 {
		t.Fatalf("exit %d, want 2", rc)
	}
	if !strings.Contains(errb, "empty pattern") {
		t.Errorf("stderr does not name the problem: %q", errb)
	}
}

func TestAnUncompilablePatternIsUnanswerable(t *testing.T) {
	rc, _, errb, _ := gotestRun(t, 0, nil, theRealOutput, "-run", "Test(", "./m")
	if rc != 2 {
		t.Fatalf("exit %d, want 2", rc)
	}
	if !strings.Contains(errb, "not a regexp") {
		t.Errorf("stderr does not say the pattern is not a regexp: %q", errb)
	}
}

func TestRunPattern(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
		ok   bool
	}{
		{"separate word", []string{"-run", "TestX", "./m"}, "TestX", true},
		{"equals", []string{"-run=TestX"}, "TestX", true},
		{"two dashes", []string{"--run", "TestX"}, "TestX", true},
		{"two dashes equals", []string{"--run=TestX"}, "TestX", true},
		{"the compiled binary's spelling", []string{"-test.run=TestX"}, "TestX", true},
		{"absent", []string{"-count=1", "./..."}, "", false},
		// The flag package lets the last win, and a wrapper that read the first
		// would check a pattern the test never used.
		{"the last one wins", []string{"-run", "TestA", "-run", "TestB"}, "TestB", true},
		// A trailing -run with nothing after it is not a pattern.
		{"dangling", []string{"./m", "-run"}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := runPattern(tc.args)
			if got != tc.want || ok != tc.ok {
				t.Errorf("runPattern(%v) = %q,%v; want %q,%v", tc.args, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestHasVerbose(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{[]string{"-v"}, true},
		{[]string{"-v=true"}, true},
		{[]string{"-test.v"}, true},
		{[]string{"-test.v=true"}, true},
		// ⛔ NOT a prefix match: -vet is a different flag and a wrapper that
		// read it as -v would stop adding -v and then check nothing.
		{[]string{"-vet=off"}, false},
		{[]string{"-count=1", "./..."}, false},
	} {
		if got := hasVerbose(tc.args); got != tc.want {
			t.Errorf("hasVerbose(%v) = %v, want %v", tc.args, got, tc.want)
		}
	}
}

func TestAlternatives(t *testing.T) {
	for _, tc := range []struct {
		name    string
		pattern string
		want    []string
		wantErr bool
	}{
		{"one", "TestX", []string{"TestX"}, false},
		{"three", "TestA|TestB|TestC", []string{"TestA", "TestB", "TestC"}, false},
		// A group is ONE alternative: splitting it would invent two patterns
		// that match nothing and report both.
		{"a group", "Test(A|B)", []string{"Test(A|B)"}, false},
		{"a group beside an alternative", "Test(A|B)|TestC", []string{"Test(A|B)", "TestC"}, false},
		{"a character class", "Test[A|B]", []string{"Test[A|B]"}, false},
		{"an escaped pipe", `Test\|X`, []string{`Test\|X`}, false},
		// ⛔ The slash is go test's LEVEL separator, not alternation: only the
		// first part selects the top-level tests `=== RUN` names.
		{"a subtest path", "TestA|TestB/sub", []string{"TestA", "TestB"}, false},
		{"a slash inside a group is not a separator", "Test(a/b)|TestC", []string{"Test(a/b)", "TestC"}, false},
		{"empty", "", nil, true},
		{"an empty alternative", "TestA||TestB", nil, true},
		{"a trailing pipe", "TestA|", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := alternatives(tc.pattern)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("alternatives(%q) = %v, want an error", tc.pattern, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("alternatives(%q): %v", tc.pattern, err)
			}
			if strings.Join(got, "\x00") != strings.Join(tc.want, "\x00") {
				t.Errorf("alternatives(%q) = %v, want %v", tc.pattern, got, tc.want)
			}
		})
	}
}

func TestRanNames(t *testing.T) {
	const out = `=== RUN   TestA
=== RUN   TestA/sub
=== RUN   TestB
=== RUN   TestA
--- PASS: TestA (0.00s)
=== RUN
`
	got := ranNames(out)
	if strings.Join(got, ",") != "TestA,TestB" {
		t.Errorf("ranNames = %v; want TestA,TestB (subtests excluded, duplicates collapsed, a bare line ignored)", got)
	}
}

func TestUnmatchedIsUnanchoredLikeGoTest(t *testing.T) {
	// `go test -run TestRead` runs TestReadme: the pattern is a search, not an
	// equality. So an alternative with NO match is a real absence.
	missed, err := unmatched([]string{"TestRead", "TestWrite"}, []string{"TestReadmeCounts"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(missed, ",") != "TestWrite" {
		t.Errorf("unmatched = %v, want TestWrite", missed)
	}
}

func TestUnmatchedRefusesAPatternItCannotCompile(t *testing.T) {
	if _, err := unmatched([]string{"Test("}, []string{"TestA"}); err == nil {
		t.Fatal("accepted a pattern that is not a regexp")
	}
}

// ⛔ THE SEAM ITSELF, against the real `go`. Every test above replaces goTest,
// so without this the one function that actually runs anything is the only
// untested part — and an exec wrapper that mis-handles a non-zero status would
// turn every failing suite into "unanswerable".
func TestTheRealGoTestReportsAStatusRatherThanAnError(t *testing.T) {
	// A package pattern that matches nothing: `go test` prints a complaint and
	// exits non-zero, which must arrive as a STATUS and not as an error.
	status, err := goTest([]string{"./no-such-package-anywhere"}, io.Discard)
	if err != nil {
		t.Fatalf("a non-zero `go test` came back as an error: %v", err)
	}
	if status == 0 {
		t.Error("a package pattern matching nothing exited 0")
	}

	// And a run that succeeds comes back as 0. This package's own tests are the
	// nearest thing to hand, selected down to one cheap table.
	status, err = goTest([]string{"-run", "TestHasVerbose", "."}, io.Discard)
	if err != nil {
		t.Fatalf("goTest: %v", err)
	}
	if status != 0 {
		t.Errorf("a passing run reported status %d", status)
	}
}

func TestHelpIsNotARun(t *testing.T) {
	old := goTest
	t.Cleanup(func() { goTest = old })
	called := false
	goTest = func([]string, io.Writer) (int, error) { called = true; return 0, nil }
	var out, errb bytes.Buffer
	if rc := run([]string{"--help"}, &out, &errb); rc != 0 {
		t.Fatalf("exit %d, want 0", rc)
	}
	if called {
		t.Error("--help ran go test")
	}
}
