// Command gotest runs `go test` and refuses to call a selection a pass when
// part of it selected nothing.
//
// ⛔⛔ `ok` IS ABOUT THE TESTS THAT RAN, NOT ABOUT THE ONE YOU ASKED FOR.
// Measured on 2026-11-09, in go-zpkg/zbk, while checking a README gate:
//
//	$ go test -run 'TestREADME|TestUsage|TestMain' ./cmd/zbk
//	ok  	github.com/go-zpkg/zbk/cmd/zbk	0.159s
//
// The gate was `TestTheREADMECountsTheSubcommandsItHas`, and `TestREADME`
// matched NOTHING. Three other alternatives did — the same run with -v shows
// TestMainMultiCallDispatch, TestUsageAndErrors, TestMainSeam and
// TestRunTestUsage — so the selection was not empty, `go test` had nothing to
// warn about, and the `ok` was true. The gate had simply not run, and ran red
// the moment it was named correctly:
//
//	39 subcommands, and numberWords does not spell that
//
// ⛔ GO ONLY WARNS WHEN THE SELECTION IS WHOLLY EMPTY, and even then it exits
// 0. Measured the same day:
//
//	$ go test -run NoSuchTestNameAtAll ./cmd/covblocks ; echo $?
//	ok  	…/cmd/covblocks	0.051s [no tests to run]
//	0
//
// So the information exists in both cases and the VERDICT does not. That is
// what this adds, and it is the only thing it adds: every argument is passed
// through to `go test` unchanged.
//
//	gotest -run 'TestFoo|TestBar' ./pkg      # fails if either matched nothing
//	gotest ./...                             # no -run: a plain pass-through
//
// ⭐ IT CHECKS EACH ALTERNATIVE, not the selection as a whole, because that is
// where the defect lives: a pattern with four alternatives and one typo reports
// the three that worked.
//
// ⛔ AND IT ADDS -v WHEN IT HAS TO, saying so. The names that ran are only
// printed by `go test -v`; without it there is nothing to check against, and
// silently running a different command than the one asked for is worse than the
// extra output.
//
// Exit status: 0 the selection ran and `go test` passed, 1 an alternative
// matched no test or `go test` failed, 2 the arguments could not be read.
package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/go-fleettools/fleettools/internal/loadgate"
	"github.com/spf13/cobra"
)

const longHelp = `Runs ` + "`go test`" + ` and refuses to call a selection a pass when part of it
selected nothing.

` + "`ok`" + ` is about the tests that RAN, not about the one you asked for. A -run
pattern of several alternatives where one matches nothing still prints ok,
because the selection was not empty — so the gate you were checking never ran
and nothing said so.

Every argument is passed through to ` + "`go test`" + ` unchanged. -v is added when -run
is present and -v is not, because the names that ran are only printed by -v;
it says so when it does.

Exit status: 0 the selection ran and go test passed, 1 an alternative matched
no test or go test failed, 2 the arguments could not be read.`

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// RootCmd builds the command.
//
// Flags are NOT declared here, deliberately: every word after `gotest` belongs
// to `go test`, and a flag of our own would be a word `go test` then never
// sees. DisableFlagParsing is what keeps `-run`, `-count` and the rest intact.
func RootCmd() *cobra.Command {
	c := cli.New("gotest [go test arguments...]",
		"go test, refusing a selection where part of it selected nothing", longHelp)
	c.DisableFlagParsing = true
	c.RunE = func(cmd *cobra.Command, args []string) error {
		return gotest(cmd, args)
	}
	return c
}

// run keeps the signature this command's tests drive it through.
func run(argv []string, stdout, stderr io.Writer) int {
	return cli.Drive(RootCmd(), argv, stdout, stderr)
}

// goTest is the seam: it runs `go test` with args, copying its output to out,
// and returns the exit status. Tests replace it with canned output, because
// what this command decides is a function of that text.
var goTest = func(args []string, out io.Writer) (int, error) {
	c := exec.Command("go", append([]string{"test"}, args...)...)
	c.Stdout = out
	c.Stderr = out
	c.Stdin = nil
	if err := c.Run(); err != nil {
		var ee *exec.ExitError
		if ok := asExitError(err, &ee); ok {
			return ee.ExitCode(), nil
		}
		return 0, err
	}
	return 0, nil
}

// loadCheck is the seam for the load gate, for exactly the reason goTest is one:
// what this command DECIDES about a busy machine cannot be tested by waiting for
// the machine to be busy.
//
// ⛔ Its refusal path was a mutation SURVIVOR until this existed, and no test
// could have been written without it: on a loaded machine a test asserting the
// refusal passes, and the same test on an idle CI runner asserts the opposite.
// A guard whose verdict depends on the host is a guard nothing holds.
var loadCheck = loadgate.Check

func asExitError(err error, target **exec.ExitError) bool {
	if ee, ok := err.(*exec.ExitError); ok {
		*target = ee
		return true
	}
	return false
}

func gotest(cmd *cobra.Command, args []string) error {
	stdout, stderr := cli.Out(cmd), cli.Err(cmd)
	// ⛔ ASKING THIS COMMAND WHAT IT DOES IS NOT RUNNING A TEST, and with
	// DisableFlagParsing cobra never sees --help: it arrives as an ordinary
	// word, so cli.HelpRequested cannot answer and the first version ran
	// `go test --help` instead. Its own test caught it.
	//
	// `go test`'s flags are `go help testflag`; --help here is this command's.
	for _, a := range args {
		if a == "--help" || a == "-h" {
			return cmd.Help()
		}
	}

	// ⛔⛔ A BENCHMARK IS A MEASUREMENT, AND THIS FLEET DOES NOT MEASURE ON A BUSY
	// MACHINE. The rule is old; the gate is here because on 2026-11-16 it was
	// broken by the session that had written a gate for it the day before — one
	// that covers two corpus tools and left `go test -bench` guarded by nothing.
	//
	// This is the right place for it because every `-run` already comes through
	// here (guard-bash refuses `go test -run`), and a benchmark run almost always
	// carries `-run '^$'` to keep the tests out of it.
	//
	// ⚠ It fires on -bench ONLY. An ordinary test run is a PASS/FAIL, which load
	// does not change; refusing those would make this wrapper unusable and teach
	// everyone to go around it.
	why, benchesWanted := benchRequested(args)
	if benchesWanted {
		load, err := loadCheck(why)
		if err != nil {
			fmt.Fprintln(stderr, "gotest:", err)
			return cli.Unanswerable("a benchmark taken here measures the machine. " +
				"Wait, or pass --under-load '<why>', which is STAMPED into the output")
		}
		fmt.Fprintln(stdout, loadgate.Banner(load, why))
		args = withoutUnderLoad(args) // `go test` has never heard of it
	}
	fuzzWanted := fuzzRequested(args)

	pattern, hasRun := runPattern(args)
	if !hasRun {
		// Nothing to check: a run with no -run selects everything, and
		// "everything" cannot have an alternative that matched nothing.
		status, err := goTest(args, stdout)
		if err != nil {
			return cli.Unanswerable("go test: %v", err)
		}
		return cli.FromStatus(status)
	}

	effective := args
	if !hasVerbose(args) {
		effective = append([]string{"-v"}, args...)
		fmt.Fprintln(stderr, "gotest: added -v, because only -v prints which tests ran")
	}

	var buf strings.Builder
	status, err := goTest(effective, io.MultiWriter(stdout, &buf))
	if err != nil {
		return cli.Unanswerable("go test: %v", err)
	}
	output := buf.String()

	alts, err := alternatives(pattern)
	if err != nil {
		return cli.Unanswerable("%v", err)
	}
	ran := ranNames(output)

	// ⛔ NOTHING RAN AT ALL is reported before the per-alternative verdict: it
	// is the same defect and a clearer sentence, and `go test` already prints
	// "[no tests to run]" for it while exiting 0.
	//
	// ⛔⛔ UNLESS BENCHMARKS OR FUZZING WERE ASKED FOR, and this exception is not a softening
	// of the rule — it is the rule applied to what was actually selected.
	// `-run '^$' -bench X` and `-run '^$' -fuzz Y` are the DOCUMENTED ways to run
	// benchmarks or fuzz targets without the tests: the empty test selection is the point
	// of the command, not an accident, and `go test` itself prints "[no tests to run]" and exits 0.
	//
	// Found by running this very command through itself: every `gotest -run '^$'
	// -bench …` and `gotest -run '^$' -fuzz …` exited 1 saying the selection was empty.
	// Left alone, routing benchmarks or fuzz runs through this wrapper would make
	// the wrapper hostile to the runs it exists to guard, and everyone would go back around it.
	//
	// ⚠ The check still applies to the benchmark's or fuzz's OWN pattern in spirit, but this
	// command cannot see which benchmarks or fuzz targets ran: `go test` prints their names
	// in a format ranNames does not read. Saying so here rather than pretending
	// the verdict covers them.
	//
	// ⛔ THE WHOLE PATTERN VERDICT IS SKIPPED, not just the "nothing ran" half,
	// and for the same reason: `^$` is an alternative that matches nothing BY
	// DESIGN, so the per-alternative check fires on it too. This command cannot
	// tell a deliberately empty selection from a typo once -bench or -fuzz is present, and
	// the cost of being wrong is asymmetric — a false refusal here drives every
	// benchmark or fuzz run back to bare `go test`, which is ungated. Stated rather than
	// hidden: in a -bench or -fuzz run, gotest checks no pattern.
	if benchesWanted || fuzzWanted {
		return cli.FromStatus(status)
	}
	// ⛔⛔ A RUN THAT NEVER GOT TO SELECTING IS NOT AN EMPTY SELECTION, and the
	// sentence below then names an `ok` that was never printed. Measured
	// 2026-11-22 in this repository:
	//
	//	$ gotest -run TestJSONGolden -update ./cmd/recur
	//	# .
	//	no Go files in …/go-fleettools-fleettools-recur343
	//	FAIL	. [setup failed]
	//	gotest: -run TestJSONGolden selected NO test at all — `ok` above is about nothing
	//
	// `go test` exited 1 before compiling anything: cmd/go wants the package list
	// BEFORE the first test-binary flag (`go help test`), so `./cmd/recur` written
	// after an unknown `-update` is an argument for the binary and the package
	// defaulted to `.`, which holds no Go file. Control, same tree, three forms:
	// `-update ./cmd/recur` and `-update=true ./cmd/recur` both fail that way,
	// `./cmd/recur -update=true` passes — so it is the ORDER, not a swallowed
	// value, and nothing about the pattern.
	//
	// ⛔ This command exists because `ok` is about what RAN. Pointing at an `ok`
	// that is not there is the same defect, committed by the gate: the cause was
	// three lines up, in the output, and the verdict invented another one.
	//
	// TestAFailingRunKeepsItsStatus already held the other half — a failure where
	// a test DID run is not reported as a selection problem — and this half,
	// where none ran, was the hole.
	//
	// ⚠ The verdict is told apart rather than dropped: the status is `go test`'s,
	// and the sentence says the selection was NOT checked rather than that it was
	// empty. Zero tests after a FAILED run says nothing about the pattern.
	if len(ran) == 0 && status != 0 {
		fmt.Fprintf(stderr, "gotest: go test FAILED before any test ran, so -run %s was never checked — the cause is in its output above, not here\n", pattern)
		return cli.FromStatus(status)
	}
	if len(ran) == 0 {
		fmt.Fprintf(stderr, "gotest: -run %s selected NO test at all — `ok` above is about nothing\n", pattern)
		return cli.Found("no test ran")
	}

	missed, err := unmatched(alts, ran)
	if err != nil {
		return cli.Unanswerable("%v", err)
	}
	if len(missed) > 0 {
		fmt.Fprintf(stderr, "gotest: %d of %d alternative(s) in -run %s matched no test that ran:\n",
			len(missed), len(alts), pattern)
		for _, m := range missed {
			fmt.Fprintf(stderr, "  %s\n", m)
		}
		// ⚠ Not "which is why `go test` said nothing": `go test` may well have
		// failed for its own reasons on this very run. What is true either way is
		// WHY it never warned — it warns only when the selection is WHOLLY empty.
		fmt.Fprintf(stderr, "gotest: %d test(s) did run, so the selection was not empty — `go test` warns only when NOTHING matched\n", len(ran))
		return cli.Found("an alternative matched no test")
	}
	return cli.FromStatus(status)
}

// runPattern finds -run's value, in every spelling `go test` accepts: `-run X`,
// `-run=X`, `--run X`, `--run=X`, and the `-test.run` form a compiled test
// binary takes.
//
// The LAST one wins, as the flag package does, because a wrapper that read the
// first would check a pattern the test never used.
func runPattern(args []string) (string, bool) {
	pattern, found := "", false
	for i := 0; i < len(args); i++ {
		a := args[i]
		for _, name := range []string{"-run", "--run", "-test.run", "--test.run"} {
			if a == name {
				if i+1 < len(args) {
					pattern, found = args[i+1], true
					i++
				}
				break
			}
			if strings.HasPrefix(a, name+"=") {
				pattern, found = strings.TrimPrefix(a, name+"="), true
				break
			}
		}
	}
	return pattern, found
}

// benchRequested reports whether these arguments ask for benchmarks, and returns
// the reason given for measuring anyway.
//
// ⚠ `-bench` must be matched as a WHOLE flag. `-benchtime 20x` and `-benchmem`
// both begin with it and neither asks for a benchmark on its own — a prefix match
// would gate a run that only sets how long one would take.
//
// ⛔ And an EMPTY -bench is not a benchmark run: `go test -bench=` selects none,
// exactly as `-run=` does. Gating it would refuse a command that measures nothing.
func benchRequested(args []string) (why string, ok bool) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-bench" || a == "--bench" || a == "-test.bench" || a == "--test.bench":
			if i+1 < len(args) && args[i+1] != "" {
				ok = true
			}
		case strings.HasPrefix(a, "-bench="), strings.HasPrefix(a, "--bench="),
			strings.HasPrefix(a, "-test.bench="), strings.HasPrefix(a, "--test.bench="):
			if _, v, _ := strings.Cut(a, "="); v != "" {
				ok = true
			}
		case a == "--under-load" || a == "-under-load":
			if i+1 < len(args) {
				why = args[i+1]
			}
		case strings.HasPrefix(a, "--under-load="):
			why = strings.TrimPrefix(a, "--under-load=")
		case strings.HasPrefix(a, "-under-load="):
			why = strings.TrimPrefix(a, "-under-load=")
		}
	}
	return why, ok
}

// fuzzRequested reports whether these arguments ask for a fuzz run.
//
// ⚠ `-fuzz` must be matched as a WHOLE flag. `-fuzztime` and `-fuzzminimizetime`
// both begin with it and neither asks for a fuzz target on its own — a prefix match
// would treat a run that only sets how long fuzzing runs as asking for a target.
//
// ⛔ And an EMPTY -fuzz is not a fuzz run: `go test -fuzz=` selects none,
// exactly as `-run=` does.
func fuzzRequested(args []string) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-fuzz" || a == "--fuzz" || a == "-test.fuzz" || a == "--test.fuzz":
			if i+1 < len(args) && args[i+1] != "" {
				return true
			}
		case strings.HasPrefix(a, "-fuzz="), strings.HasPrefix(a, "--fuzz="),
			strings.HasPrefix(a, "-test.fuzz="), strings.HasPrefix(a, "--test.fuzz="):
			if _, v, _ := strings.Cut(a, "="); v != "" {
				return true
			}
		}
	}
	return false
}

// withoutUnderLoad strips this wrapper's own flag before `go test` sees it.
// ⛔ Passing it through would make every gated run fail with "flag provided but
// not defined", which reads as a broken command rather than a working gate.
func withoutUnderLoad(args []string) []string {
	out := args[:0:0]
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--under-load" || a == "-under-load" {
			i++ // its value goes too
			continue
		}
		if strings.HasPrefix(a, "--under-load=") || strings.HasPrefix(a, "-under-load=") {
			continue
		}
		out = append(out, a)
	}
	return out
}

// hasVerbose reports whether the arguments already ask for the names.
// `-v=true` and `-test.v` count; `-vet=off` does not, which is why this is not
// a prefix match.
func hasVerbose(args []string) bool {
	for _, a := range args {
		switch {
		case a == "-v", a == "--v", a == "-test.v":
			return true
		case strings.HasPrefix(a, "-v="), strings.HasPrefix(a, "--v="),
			strings.HasPrefix(a, "-test.v="):
			return true
		}
	}
	return false
}

// alternatives splits the TOP-LEVEL `|` of a -run pattern's first
// slash-segment.
//
// ⛔ THE SLASH IS NOT ALTERNATION. `go test` splits -run on `/` and applies
// each part to one level of the test name, so only the first part selects
// top-level tests — which is what `=== RUN` lines name. A pattern with a slash
// is still checked, on its first part.
//
// Parentheses and brackets are tracked so `Test(A|B)` stays one alternative
// rather than becoming two broken ones, and a backslash escapes the next byte.
func alternatives(pattern string) ([]string, error) {
	if pattern == "" {
		return nil, fmt.Errorf("-run was given an empty pattern")
	}
	head := pattern
	depth, class := 0, false
	for i := 0; i < len(head); i++ {
		switch c := head[i]; {
		case c == '\\':
			i++
		case class:
			if c == ']' {
				class = false
			}
		case c == '[':
			class = true
		case c == '(':
			depth++
		case c == ')':
			depth--
		case c == '/' && depth == 0:
			head = head[:i]
			i = len(head)
		}
	}

	var out []string
	start, depth, class := 0, 0, false
	for i := 0; i < len(head); i++ {
		switch c := head[i]; {
		case c == '\\':
			i++
		case class:
			if c == ']' {
				class = false
			}
		case c == '[':
			class = true
		case c == '(':
			depth++
		case c == ')':
			depth--
		case c == '|' && depth == 0:
			out = append(out, head[start:i])
			start = i + 1
		}
	}
	out = append(out, head[start:])
	for _, a := range out {
		if strings.TrimSpace(a) == "" {
			return nil, fmt.Errorf("-run %s has an empty alternative", pattern)
		}
	}
	return out, nil
}

// ranNames is every TOP-LEVEL test named by a `=== RUN` line. Subtests are
// indented and carry a `/`, and they are not what a -run alternative's first
// segment selects.
func ranNames(output string) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(output, "\n") {
		if !strings.HasPrefix(line, "=== RUN") {
			continue
		}
		name := strings.TrimSpace(strings.TrimPrefix(line, "=== RUN"))
		if name == "" || strings.Contains(name, "/") {
			continue
		}
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// unmatched names the alternatives that no test which ran matches.
//
// Each is compiled as `go test` compiles it: an UNANCHORED Go regexp, so
// `TestREADME` matches `TestREADMECounts` — which is exactly why the one that
// matched nothing on 2026-11-09 was a real absence and not a spelling
// preference.
func unmatched(alts, ran []string) ([]string, error) {
	var out []string
	for _, a := range alts {
		re, err := regexp.Compile(a)
		if err != nil {
			return nil, fmt.Errorf("-run alternative %q is not a regexp: %v", a, err)
		}
		hit := false
		for _, n := range ran {
			if re.MatchString(n) {
				hit = true
				break
			}
		}
		if !hit {
			out = append(out, a)
		}
	}
	sort.Strings(out)
	return out, nil
}
