// Command failset compares two `go test -json` runs by the SET of tests that
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
//	go test -json ./pkg > before.json     # unmutated
//	...apply the mutation...
//	go test -json ./pkg > after.json
//	failset --before before.json --after after.json --expect TestTheClaimIMake
//
// Exit status: 0 every --expect went newly red, 1 one did not and here is what
// did, 2 a log could not be read as a complete test run.
//
// ⛔⛔ IT READS `-json` AND REFUSES `-v` TEXT, WHICH IS NOT A PREFERENCE. The
// first version parsed `--- PASS:` / `--- FAIL:` lines, and review measured
// what that costs: a test's OWN OUTPUT can forge another test's verdict. A
// passing TestY that prints
//
//	--- FAIL: TestX (0.00s)
//
// made the tool report TestX as newly red and exit 0, supporting a claim
// nothing supported. `go test -v` interleaves test output with the framework's
// own lines at the same indentation and with no delimiter, so NO parser can
// tell them apart — it is the format, not the parsing. In `-json` a test's
// printed text can only ever appear inside an `"Action":"output"` event, while
// a verdict is an `"Action":"pass"|"fail"|"skip"` event. Forgery stops being
// possible rather than becoming unlikely.
//
// ⛔ THE SAME CHANGE FIXES A SECOND FALSE PASS the review found: text output
// carries no package, so two packages with a test of the same name collided
// and `./...` could read a test as green in one and red in the other. Every
// event here names its Package.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

const longHelp = `Compares two 'go test -json' runs by the SET of failing tests.

A mutation is run to prove that some test defends some behaviour. The evidence
for that claim is not "the suite went red" — it is "THIS test went red, and it
was green before". Those differ whenever a mutation breaks something other than
what was aimed at, which is most of the time and is invisible in a count.

  go test -json ./pkg > before.json
  ...apply the mutation...
  go test -json ./pkg > after.json
  failset --before before.json --after after.json --expect TestTheClaimIMake

⛔ It reads -json and REFUSES -v text. A test's own output can forge another
test's verdict line, and 'go test -v' gives a parser no way to tell the two
apart; this was measured, not feared. In -json a verdict is an event and
printed text is a different event.

A subtest answers for its parent: --expect TestA is satisfied by TestA/case
going red, because that is how Go reports it.

⛔ A log that did not RUN TO COMPLETION is not zero failures, it is no answer,
and exits 2: a build failure, a package that never reported a result, or any
test that started and never got a verdict (a panic, a timeout, a killed
process). A test absent from the BEFORE run is not "green" either — it was
never shown to pass, so nothing can have gone green->red.

Exit status: 0 every --expect went newly red, 1 one did not, 2 a log could not
be read as a complete test run.`

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
		"compare two go test -json runs by which tests failed, not how many", longHelp)
	c.Flags().StringVar(&before, "before", "", "the `go test -json` log of the unmutated run (required)")
	c.Flags().StringVar(&after, "after", "", "the `go test -json` log of the mutated run (required)")
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

// event is one line of `go test -json`. Only the fields a verdict needs are
// read; the rest of the schema may grow without this noticing.
type event struct {
	Action      string
	Package     string
	Test        string
	ImportPath  string
	FailedBuild string
}

// outcome is what a run says happened to one test.
type outcome int

// ⛔⛔ `noVerdict` IS FIRST, AND THAT IS THE WHOLE POINT. A map lookup for a
// test the run never mentioned returns the ZERO value, so if `passed` were
// iota 0 every absent test would read as green — which is exactly the false
// pass this enum was reordered to kill, reappearing one level down. The test
// written for that defect caught it here, in the fix for it.
const (
	noVerdict outcome = iota
	passed
	failed
	skipped
)

// testID is the key. ⛔ THE PACKAGE IS PART OF IT: keyed by name alone, two
// packages with a test of the same name merge, and a review measured a false
// pass from exactly that — green in one package and red in another read as
// "went green->red".
type testID struct{ pkg, name string }

// result is one run, read.
type result struct {
	tests      map[testID]outcome
	events     int
	incomplete string
}

// readRun parses a `go test -json` log.
//
// ⛔ A LINE THAT IS NOT JSON IS NOT IGNORED. `go test -v` text is the input
// this command used to accept and must now refuse loudly, so anything that
// does not parse as an event is counted and reported rather than skipped — a
// parser that silently drops what it does not understand reads a `-v` log as
// an empty, successful run.
func readRun(path string) (*result, error) {
	b, err := readFile(path)
	if err != nil {
		return nil, err
	}
	r := &result{tests: map[testID]outcome{}}
	// started holds tests that reported "run" and no verdict yet; whatever is
	// left at the end never finished.
	started := map[testID]bool{}
	// pkgOpen holds packages that started and have not reported a result.
	pkgOpen := map[string]bool{}

	// ⛔ NOT A bufio.Scanner. The whole file is already in memory, so a
	// scanner bought nothing and brought a token-size limit and an error path
	// no input could reach — a branch a coverage gate then asks to be tested,
	// for a failure that cannot happen.
	for _, raw := range strings.Split(string(b), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		var e event
		if err := json.Unmarshal([]byte(line), &e); err != nil || e.Action == "" {
			continue
		}
		r.events++
		switch e.Action {
		case "build-fail":
			if r.incomplete == "" {
				r.incomplete = "the build failed for " + e.ImportPath
			}
		case "start":
			pkgOpen[e.Package] = true
		case "run":
			if e.Test != "" {
				started[testID{e.Package, e.Test}] = true
			}
		case "pass", "fail", "skip":
			if e.Test == "" {
				// A package-level result.
				delete(pkgOpen, e.Package)
				if e.FailedBuild != "" && r.incomplete == "" {
					r.incomplete = "the build failed for " + e.FailedBuild
				}
				continue
			}
			id := testID{e.Package, e.Test}
			delete(started, id)
			switch e.Action {
			case "pass":
				r.tests[id] = passed
			case "fail":
				r.tests[id] = failed
			case "skip":
				r.tests[id] = skipped
			}
		}
	}
	// ⛔⛔ A TEST THAT STARTED AND NEVER REPORTED IS THE SHAPE OF EVERY ABORT.
	// A panic, a timeout and a killed process all leave the running test with
	// no verdict while every test that finished earlier keeps its own, so a
	// count of failures reads the run as complete. This is structural and
	// needs no list of phrases to recognise.
	if r.incomplete == "" {
		for id := range started {
			r.incomplete = fmt.Sprintf("%s in %s started and never reported a result",
				id.name, id.pkg)
			break
		}
	}
	if r.incomplete == "" {
		for pkg := range pkgOpen {
			r.incomplete = pkg + " started and never reported a result"
			break
		}
	}
	return r, nil
}

// failing is the tests a run reports as failed, sorted and printable.
func failing(r *result) []testID {
	var out []testID
	for id, o := range r.tests {
		if o == failed {
			out = append(out, id)
		}
	}
	sortIDs(out)
	return out
}

func sortIDs(ids []testID) {
	sort.Slice(ids, func(i, j int) bool {
		if ids[i].name != ids[j].name {
			return ids[i].name < ids[j].name
		}
		return ids[i].pkg < ids[j].pkg
	})
}

func names(ids []testID) string {
	var out []string
	for _, id := range ids {
		out = append(out, id.name)
	}
	return strings.Join(out, ", ")
}

// covers says whether a failing test answers for an expected name. A subtest
// counts for its parent, because `--expect TestA` is a claim about TestA and
// Go reports TestA/case going red as both.
//
// ⛔ THE SEPARATOR IS REQUIRED. Without it `TestA` would answer for `TestAB`,
// which is a different test — a review measured that mutation surviving,
// because the only case pinning it asked the question the other way round.
func covers(got, want string) bool {
	return got == want || strings.HasPrefix(got, want+"/")
}

// newlyRed is the tests that PASSED in a and FAILED in b.
//
// ⛔⛔ "NOT FAILING BEFORE" IS NOT "PASSING BEFORE". The first version asked
// whether the before run recorded a failure, so a test ABSENT from it — never
// run, newly written, filtered out — counted as green and could then "go
// green->red". A review measured that false pass. Nothing can have gone from
// green to red unless it was shown to be green.
func newlyRed(a, b *result) []testID {
	var out []testID
	for _, id := range failing(b) {
		if a.tests[id] == passed {
			out = append(out, id)
		}
	}
	sortIDs(out)
	return out
}

// newlyGreen is the tests that FAILED in a and PASSED in b.
func newlyGreen(a, b *result) []testID {
	var out []testID
	for _, id := range failing(a) {
		if b.tests[id] == passed {
			out = append(out, id)
		}
	}
	sortIDs(out)
	return out
}

func failset(cmd *cobra.Command, before, after string, expect []string) error {
	stdout, stderr := cli.Out(cmd), cli.Err(cmd)
	if before == "" || after == "" {
		return cli.Unanswerable("--before and --after each name a `go test -json` log")
	}

	runs := map[string]*result{}
	for _, f := range []struct{ what, path string }{{"--before", before}, {"--after", after}} {
		r, err := readRun(f.path)
		if err != nil {
			return cli.Unanswerable("%s: %v", f.what, err)
		}
		// ⛔ A `-v` TEXT LOG LANDS HERE, and must be told what to do rather
		// than reported as an empty run.
		if r.events == 0 {
			return cli.Unanswerable("%s (%s) holds no `go test -json` events — if it is "+
				"`go test -v` text, re-run with -json: a test's own output can forge "+
				"another test's verdict line in that format", f.what, f.path)
		}
		// ⛔⛔ COMPLETENESS BEFORE THE COUNT. A run that aborted, or in which
		// one package of several did not build, HAS verdicts — just not all of
		// them. Asking "were there none?" accepts it; asking "did it finish?"
		// does not.
		if r.incomplete != "" {
			return cli.Unanswerable("%s (%s) did not run to completion, so it answers for "+
				"no test: %s", f.what, f.path, r.incomplete)
		}
		runs[f.what] = r
	}
	a, b := runs["--before"], runs["--after"]

	red := newlyRed(a, b)
	green := newlyGreen(a, b)
	report(stdout, red, green)

	var unmet []string
	for _, want := range expect {
		met := false
		for _, got := range red {
			if covers(got.name, want) {
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
	// ⛔ Printed here, because cli.Status does not print the message of a
	// code-1 error: returning the sentence alone sends it nowhere.
	for _, want := range unmet {
		fmt.Fprintf(stderr, "failset: %s did not go green->red\n", want)
		fmt.Fprintf(stderr, "  %s\n", why(a, b, want))
	}
	if len(red) > 0 {
		fmt.Fprintf(stderr, "  what went newly red instead: %s\n", names(red))
	}
	return cli.Found("%s did not go newly red", strings.Join(unmet, ", "))
}

// why says, for one unmet claim, what the two runs actually recorded. "Your
// claim is wrong" without this sends the reader back to the count they
// misread.
func why(a, b *result, want string) string {
	var saw []string
	for id, o := range b.tests {
		if !covers(id.name, want) {
			continue
		}
		switch {
		case o == failed && a.tests[id] == failed:
			saw = append(saw, "it failed in BOTH runs, so this mutation says nothing about it")
		case o == failed:
			saw = append(saw, "it failed after, but the before run did not record it PASSING "+
				"(absent, skipped or failed), so nothing went green->red")
		case o == passed:
			saw = append(saw, "it PASSED under the mutation")
		case o == skipped:
			saw = append(saw, "it was SKIPPED under the mutation")
		}
	}
	if len(saw) == 0 {
		return "no verdict for it in the --after run at all: check the name"
	}
	sort.Strings(saw)
	return strings.Join(uniq(saw), "; ")
}

func uniq(s []string) []string {
	var out []string
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}

// report prints both directions. The green->red list is the evidence; the
// red->green one is printed because a mutation that FIXES a test is a fact
// about the suite worth seeing, and is invisible in any count.
func report(w io.Writer, red, green []testID) {
	if len(red) == 0 {
		fmt.Fprintf(w, "failset: nothing went green->red\n")
	} else {
		fmt.Fprintf(w, "failset: %d went green->red: %s\n", len(red), names(red))
	}
	if len(green) > 0 {
		fmt.Fprintf(w, "failset: %d went red->green: %s\n", len(green), names(green))
	}
}
