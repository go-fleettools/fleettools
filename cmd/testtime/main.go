// testtime says which tests account for a Go package's wall clock.
//
//	go test -json ./... > run.json && testtime run.json
//	go test -json ./... | testtime
//
// ⚠ THE GAP IS PER-TEST, NOT TIMING ALTOGETHER, and the first version of this
// comment got that wrong. cmd/testwhy already parses the elapsed time of every
// PACKAGE and makes it load-bearing: "the time is a witness ... -min-seconds
// refuses a package that finished faster than it plausibly could"
// (cmd/testwhy/main.go:36-37, :430). What nobody had is the breakdown WITHIN a
// package — which tests account for its clock.
//
// ⛔ And the check that produced the wrong sentence deserves naming: `fleetverbs
// --match timing|elapsed|duration` returned 0 verbs over 112 packages, which
// reads like absence and is not — --match searches NAMES and DOCS, so a tool
// whose help says "the time is a witness" without the word "elapsed" in its verb
// line is invisible to it. A scan that could not see reported zero.
//
// Three sessions wanted the per-test breakdown on 2026-11-17 alone, each working
// around its absence: go-tex/engine's ubuntu lane went from 1229 s to past its
// 1800 s bound and the only way anyone could attribute it was to bisect whole
// suite runs on a machine two orders of magnitude away from the one that grew.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

const longHelp = `Reports which tests account for a Go package's wall clock.

Input is ` + "`go test -json`" + ` output, from a FILE or from standard input.

    go test -json ./... > run.json && testtime run.json
    go test -json ./... | testtime

⛔ A PARENT'S ELAPSED ALREADY CONTAINS ITS SUBTESTS. go test reports an Elapsed
for TestX and another for TestX/case, and the first includes the second, so
adding every Test event up double-counts every table-driven test in the package.
Only top-level tests — those whose name has no "/" — are totalled here. Pass
--subtests to list the subtests of the slowest ones instead, which is a
DIFFERENT question and never mixed into the same column.

⛔ AND THE TESTS MAY HAVE RUN IN PARALLEL, in which case their elapsed times
overlap and their sum exceeds the package's own. The report then prints both
numbers and says "parallel" rather than a share: a percentage of a wall clock
that several tests occupied at once is not a share of anything. A serial run
(-p 1 -parallel 1) is what makes the shares meaningful, and the header says
which kind it read.

⚠ IT REPORTS, IT DOES NOT JUDGE. There is no threshold and no exit status for
"too slow": what is slow depends on the suite, and a gate that guessed would be
wrong in both directions. Exit 2 is reserved for input that could not be read or
parsed, because a report over nothing must not look like a clean one.

Exit status: 0 a report was produced, 2 the input could not be read or held no
test events.`

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// RootCmd builds the command. Tests drive it through run, below.
func RootCmd() *cobra.Command {
	var o opts
	c := cli.New("testtime [FILE]",
		"Which tests account for a Go package's wall clock", longHelp)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) > 1 {
			return cli.Unanswerable("usage: testtime [FILE]  (one file, or standard input)")
		}
		in := cmd.InOrStdin()
		if len(args) == 1 && args[0] != "-" {
			f, err := os.Open(args[0])
			if err != nil {
				return cli.Unanswerable("%v", err)
			}
			defer f.Close()
			in = f
		}
		return cli.FromStatus(run(in, cli.Out(cmd), o))
	}
	c.Flags().IntVar(&o.top, "top", 20, "list at most this many tests per package")
	c.Flags().BoolVar(&o.subtests, "subtests", false,
		"list the subtests of the slowest test instead of the top-level tests")
	return c
}

type opts struct {
	top      int
	subtests bool
}

// event is the subset of `go test -json` this reads. Package-level events carry
// no Test; a test's own event carries both.
type event struct {
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Elapsed float64 `json:"Elapsed"`
}

type entry struct {
	name    string
	elapsed time.Duration
}

type pkg struct {
	name     string
	own      time.Duration // the package's own elapsed, as go test reports it
	topLevel []entry
	subs     map[string][]entry // top-level name -> its subtests
	order    int                // first seen, so the report is stable
}

// run reads the JSON stream and writes the report.
func run(in io.Reader, out io.Writer, o opts) int {
	pkgs := map[string]*pkg{}
	var seen int
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] != '{' {
			// `go test -json` interleaves nothing else, but a log pasted into a
			// file often has a shell prompt or a banner at the top. Skipping a
			// non-JSON line is not the same as reading nothing: `seen` below is
			// what decides whether this run had anything to say.
			continue
		}
		var ev event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		if ev.Package == "" {
			continue
		}
		p := pkgs[ev.Package]
		if p == nil {
			p = &pkg{name: ev.Package, subs: map[string][]entry{}, order: len(pkgs)}
			pkgs[ev.Package] = p
		}
		switch {
		case ev.Test == "" && isOutcome(ev.Action):
			p.own = dur(ev.Elapsed)
		case ev.Test != "" && isOutcome(ev.Action):
			seen++
			if top, sub, isSub := strings.Cut(ev.Test, "/"); isSub {
				p.subs[top] = append(p.subs[top], entry{sub, dur(ev.Elapsed)})
			} else {
				p.topLevel = append(p.topLevel, entry{ev.Test, dur(ev.Elapsed)})
			}
		}
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintf(out, "testtime: %v\n", err)
		return 2
	}
	if seen == 0 {
		fmt.Fprintln(out, "testtime: no test events in this input — is it `go test -json` output?")
		return 2
	}
	report(out, pkgs, o)
	return 0
}

func isOutcome(a string) bool { return a == "pass" || a == "fail" || a == "skip" }

// dur converts go test's seconds-as-float to a Duration.
func dur(sec float64) time.Duration { return time.Duration(sec * float64(time.Second)) }

func report(out io.Writer, pkgs map[string]*pkg, o opts) {
	list := make([]*pkg, 0, len(pkgs))
	for _, p := range pkgs {
		list = append(list, p)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].order < list[j].order })

	for _, p := range list {
		var sum time.Duration
		for _, e := range p.topLevel {
			sum += e.elapsed
		}
		sort.Slice(p.topLevel, func(i, j int) bool {
			if p.topLevel[i].elapsed != p.topLevel[j].elapsed {
				return p.topLevel[i].elapsed > p.topLevel[j].elapsed
			}
			return p.topLevel[i].name < p.topLevel[j].name
		})

		// ⛔ THE HEADER SAYS WHICH KIND OF RUN THIS WAS, because the column below
		// means different things in each. When the tests ran in parallel their
		// elapsed times overlap, so the sum exceeds the package's own and a share
		// of it would be a fraction of a clock several tests occupied at once.
		parallel := p.own > 0 && sum > p.own
		fmt.Fprintf(out, "%s  %s", p.name, round(p.own))
		switch {
		case p.own == 0:
			fmt.Fprintf(out, "  (no package elapsed in this input; shares omitted)\n")
		case parallel:
			fmt.Fprintf(out, "  tests total %s — PARALLEL, so shares are omitted\n", round(sum))
		default:
			fmt.Fprintf(out, "  tests total %s\n", round(sum))
		}

		if o.subtests {
			reportSubtests(out, p, o.top)
			continue
		}
		for i, e := range p.topLevel {
			if i >= o.top {
				fmt.Fprintf(out, "  … %d more\n", len(p.topLevel)-o.top)
				break
			}
			if p.own > 0 && !parallel {
				fmt.Fprintf(out, "  %8s  %5.1f%%  %s\n",
					round(e.elapsed), 100*float64(e.elapsed)/float64(p.own), e.name)
				continue
			}
			fmt.Fprintf(out, "  %8s  %s\n", round(e.elapsed), e.name)
		}
	}
}

// reportSubtests lists the subtests of the slowest top-level test, which is a
// different question from "which test is slow" and is never mixed into the same
// column: a parent's elapsed contains these, so they do not add up with it.
func reportSubtests(out io.Writer, p *pkg, top int) {
	if len(p.topLevel) == 0 {
		fmt.Fprintln(out, "  (no top-level tests)")
		return
	}
	parent := p.topLevel[0]
	subs := p.subs[parent.name]
	if len(subs) == 0 {
		fmt.Fprintf(out, "  %s has no subtests\n", parent.name)
		return
	}
	sort.Slice(subs, func(i, j int) bool {
		if subs[i].elapsed != subs[j].elapsed {
			return subs[i].elapsed > subs[j].elapsed
		}
		return subs[i].name < subs[j].name
	})
	fmt.Fprintf(out, "  %s  %s, by subtest:\n", parent.name, round(parent.elapsed))
	for i, s := range subs {
		if i >= top {
			fmt.Fprintf(out, "    … %d more\n", len(subs)-top)
			break
		}
		fmt.Fprintf(out, "    %8s  %s\n", round(s.elapsed), s.name)
	}
}

// round prints a duration at the resolution go test itself reports, so a number
// here can be compared with one from a test log without a conversion.
func round(d time.Duration) string {
	if d >= time.Second {
		return fmt.Sprintf("%.2fs", d.Seconds())
	}
	return fmt.Sprintf("%dms", d.Milliseconds())
}
