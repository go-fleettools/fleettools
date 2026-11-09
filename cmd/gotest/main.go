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
		fmt.Fprintf(stderr, "gotest: %d test(s) did run, which is why `go test` said nothing\n", len(ran))
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
