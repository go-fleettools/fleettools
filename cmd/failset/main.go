// Command failset compares two `go test -v` logs by the SET of tests that
// failed, and refuses to confirm a claim about a mutation that the names do
// not support.
//
// ⛔⛔ A COUNT CANNOT TELL A GATE THAT WORKS FROM A GATE THAT WORKS FOR ANOTHER
// REASON. On 2026-11-12, in go-fleettools/fleettools#252, a condition was
// removed from `cmd/aptneed`, the suite went red, and the commit message, the
// README and a report to the user all said that this proved a particular test
// discriminating. An independent reviewer re-ran the same mutation: that test
// PASSED, and two others failed. Red, as claimed — on the opposite tests. The
// gate held; the published reason for it was inverted, and it had been
// published three times before anyone re-ran it.
//
// That was the third occurrence of one lesson in eleven days. The first two
// produced notes saying "compare the labels, not the cardinals". A note is
// applied when one remembers to apply it, and at the moment of a mutation one
// is thinking about the mutation. So this is a program.
//
//	failset --before before.log --after after.log --expect TestThatShouldGoRed
//
// Exit status: 0 every --expect went newly red, 1 one did not and here is what
// did, 2 a log could not be read as a test run at all.
//
// ⛔⛔ AND A BUILD FAILURE IS NOT ZERO FAILURES. The same day, the first run of
// that mutation did not compile: a variable went unused, and the log held no
// `--- FAIL` line at all, only `[build failed]`. A judge counting FAIL lines
// would have answered "nothing went red, the mutation survived" about code
// that does not exist. An unreadable log exits 2 here and never 0 — the same
// rule prwait follows about a run with no jobs, and the same mistake as
// reading an unanswered question as a negative answer.
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

const longHelp = `Compares two 'go test -v' logs by the SET of failing test names.

A mutation is run to prove that some test defends some behaviour. The evidence
for that claim is not "the suite went red" — it is "THIS test went red, and it
was green before". Those differ whenever a mutation breaks something other than
what was aimed at, which is most of the time and is invisible in a pass/fail
count.

  go test -v ./pkg > before.log        # unmutated
  ...apply the mutation...
  go test -v ./pkg > after.log
  failset --before before.log --after after.log --expect TestTheThingIClaim

It prints the tests that went green->red, the ones that went red->green, and
refuses the claim when an --expect is not among the first group.

A subtest counts for its parent: --expect TestA is satisfied by TestA/case
going red, because that is how Go reports it.

⛔ A log with no test verdicts in it at all -- a build failure, a vet failure,
an empty file, a -run filter that matched nothing -- is NOT zero failures. It
is no answer, and it exits 2 rather than report a mutation survived.

Exit status: 0 every --expect went newly red, 1 one did not, 2 a log could not
be read as a test run.`

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// RootCmd builds the command.
func RootCmd() *cobra.Command {
	var (
		before, after string
		expect        []string
	)
	c := cli.New("failset --before BEFORE --after AFTER [--expect TEST]...",
		"compare two go test -v logs by which tests failed, not how many", longHelp)
	c.Flags().StringVar(&before, "before", "", "the log of the unmutated run (required)")
	c.Flags().StringVar(&after, "after", "", "the log of the mutated run (required)")
	c.Flags().StringArrayVar(&expect, "expect", nil,
		"a test that must have gone green->red (repeatable)")
	c.Args = cobra.NoArgs
	c.RunE = func(cmd *cobra.Command, args []string) error {
		return failset(cmd, before, after, expect)
	}
	return c
}

// run keeps the signature this command's tests drive it through.
func run(argv []string, stdout, stderr io.Writer) int {
	return cli.Drive(RootCmd(), argv, stdout, stderr)
}

// readFile is the seam, so the tests do not need a temporary directory to say
// what this does with a log it cannot read.
var readFile = os.ReadFile

// outcome is what a log says happened to one test.
type outcome int

const (
	passed outcome = iota
	failed
	skipped
)

// result is one log, read.
//
// ⛔ verdicts is the count of `--- PASS/FAIL/SKIP` lines seen, kept separately
// from the maps because a log in which NOTHING ran is the case this command
// exists to refuse, and an empty failing set looks identical to a clean run.
type result struct {
	tests    map[string]outcome
	verdicts int
	broken   string // the line saying the package did not build or run at all
}

// these are the ways `go test` reports that a package produced no verdicts.
// Each leaves a log with no `--- FAIL` line in it, which a counter reads as
// success.
var brokenMarkers = []string{
	"[build failed]",
	"[setup failed]",
	"build constraints exclude all Go files",
	"no Go files",
}

// readLog parses a `go test -v` log.
func readLog(path string) (*result, error) {
	b, err := readFile(path)
	if err != nil {
		return nil, err
	}
	r := &result{tests: map[string]outcome{}}
	s := bufio.NewScanner(strings.NewReader(string(b)))
	// A single test's output can be long; the default 64 KiB token is not
	// enough for a log that holds a failing test's whole diff.
	s.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for s.Scan() {
		line := s.Text()
		for _, m := range brokenMarkers {
			if strings.Contains(line, m) {
				r.broken = strings.TrimSpace(line)
			}
		}
		name, out, ok := verdict(line)
		if !ok {
			continue
		}
		r.verdicts++
		// ⛔ LAST WINS. A test that is retried, or a package run more than
		// once in the same log, reports more than one verdict for a name; the
		// final one is the one that stood.
		r.tests[name] = out
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	return r, nil
}

// verdict reads one `--- PASS: Name (0.00s)` line, at any indentation, since
// Go indents a subtest's verdict under its parent's.
func verdict(line string) (string, outcome, bool) {
	t := strings.TrimLeft(line, " \t")
	var out outcome
	switch {
	case strings.HasPrefix(t, "--- FAIL: "):
		out, t = failed, t[len("--- FAIL: "):]
	case strings.HasPrefix(t, "--- PASS: "):
		out, t = passed, t[len("--- PASS: "):]
	case strings.HasPrefix(t, "--- SKIP: "):
		out, t = skipped, t[len("--- SKIP: "):]
	default:
		return "", 0, false
	}
	// The name runs to the elapsed time Go appends: `Name (0.00s)`.
	if i := strings.LastIndex(t, " ("); i >= 0 {
		t = t[:i]
	}
	t = strings.TrimSpace(t)
	if t == "" {
		return "", 0, false
	}
	return t, out, true
}

// failing is the names a result reports as failed, sorted.
func failing(r *result) []string {
	var out []string
	for name, o := range r.tests {
		if o == failed {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// covers says whether a failing name answers for an expected one. A subtest
// counts for its parent, because `--expect TestA` is a claim about TestA and
// Go reports TestA/case going red as both.
func covers(got, want string) bool {
	return got == want || strings.HasPrefix(got, want+"/")
}

// newly is the names failing in b that were not failing in a.
func newly(a, b *result) []string {
	var out []string
	for _, name := range failing(b) {
		if a.tests[name] != failed {
			out = append(out, name)
		}
	}
	return out
}

func failset(cmd *cobra.Command, before, after string, expect []string) error {
	stdout, stderr := cli.Out(cmd), cli.Err(cmd)
	if before == "" || after == "" {
		return cli.Unanswerable("--before and --after each name a `go test -v` log")
	}

	logs := map[string]*result{}
	for _, f := range []struct{ what, path string }{{"--before", before}, {"--after", after}} {
		r, err := readLog(f.path)
		if err != nil {
			return cli.Unanswerable("%s: %v", f.what, err)
		}
		// ⛔⛔ NO VERDICTS IS NO ANSWER. This is the whole reason the command
		// exists in this shape: a log that did not build holds no `--- FAIL`
		// line, so a counter calls it a clean run and reports that the
		// mutation survived.
		if r.verdicts == 0 {
			if r.broken != "" {
				return cli.Unanswerable("%s (%s) ran no tests: %s", f.what, f.path, r.broken)
			}
			return cli.Unanswerable("%s (%s) holds no test verdicts at all — "+
				"that is no answer, not zero failures", f.what, f.path)
		}
		logs[f.what] = r
	}
	a, b := logs["--before"], logs["--after"]

	red := newly(a, b)
	green := newly(b, a)
	report(stdout, red, green)

	// ⛔ Reported as a FINDING, and printed here, because cli.Status does not
	// print the message of a code-1 error: returning the sentence alone sends
	// it nowhere.
	var unmet []string
	for _, want := range expect {
		met := false
		for _, got := range red {
			if covers(got, want) {
				met = true
				break
			}
		}
		if !met {
			unmet = append(unmet, want)
		}
	}
	if len(unmet) == 0 {
		return nil
	}
	for _, want := range unmet {
		fmt.Fprintf(stderr, "failset: %s did not go green->red\n", want)
		if was, ok := b.tests[want]; ok && was == failed {
			fmt.Fprintf(stderr, "  it failed in BOTH runs, so this mutation says nothing about it\n")
		} else if _, ok := b.tests[want]; !ok {
			fmt.Fprintf(stderr, "  no verdict for it in the --after log at all: check the name\n")
		} else {
			fmt.Fprintf(stderr, "  it PASSED under the mutation\n")
		}
	}
	if len(red) > 0 {
		fmt.Fprintf(stderr, "  what went newly red instead: %s\n", strings.Join(red, ", "))
	}
	return cli.Found("%s did not go newly red", strings.Join(unmet, ", "))
}

// report prints both directions. The green->red list is the evidence; the
// red->green one is printed because a mutation that FIXES a test is a fact
// about the suite worth seeing, and is invisible in any count.
func report(w io.Writer, red, green []string) {
	if len(red) == 0 {
		fmt.Fprintf(w, "failset: nothing went green->red\n")
	} else {
		fmt.Fprintf(w, "failset: %d went green->red: %s\n", len(red), strings.Join(red, ", "))
	}
	if len(green) > 0 {
		fmt.Fprintf(w, "failset: %d went red->green: %s\n", len(green), strings.Join(green, ", "))
	}
}
