// Command covblocks counts the BLOCKS a Go coverage profile never entered, and
// fails when there are any.
//
// It exists because the obvious instrument gives the wrong answer and the
// convenient one is a one-liner nobody tests.
//
// ⛔ `go tool cover -func` ROUNDS TO ONE DECIMAL. So a gate written as
//
//	go tool cover -func=cov.out | tail -1 | grep -q '100.0%'
//
// passes at 99.95%, and that is not a corner case anybody has to wait for.
// Demonstrated 2026-10-11 on a real go-pkgx/bk profile — 2592 blocks, 4221
// statements, nothing uncovered — with exactly ONE one-statement block flipped
// to zero:
//
//	$ go tool cover -func=one-off.out | tail -1
//	total:                  (statements)            100.0%      → exit 0
//	$ covblocks one-off.out
//	…/advisories.go:34.3,35.1 1 stmt(s)
//	2592 block(s), 4221 statement(s); 1 block(s) uncovered, 1 statement(s) uncovered
//	covblocks: 1 uncovered block(s) > 0 allowed                 → exit 1
//
// One statement in 4221 is 0.024%, so `-func` prints a clean hundred. The
// threshold is arithmetic, not luck: any profile with fewer than one uncovered
// statement per two thousand reads as 100.0%.
//
// A note on where this came from, because the remembered version was wrong. A
// note in this fleet said `-func` had printed 100.0% for a profile with THREE
// uncovered blocks. Re-measured, that profile (3949 statements, 4 uncovered)
// prints 99.9% — so `-func` WOULD have caught that one, and the note's number
// did not survive being checked. The hazard is real and the example above is
// constructed to show it rather than recalled.
//
// `-func` has a second limitation found the same day: it resolves source files
// through the module, so reading a profile from outside its checkout fails with
// "no required module provides package …". A block count needs only the
// profile.
//
// ⛔ AND THE ONE-LINER IS NOT THE FORMAT. The idiom it replaces is
//
//	awk 'NR>1 && $NF==0' cov.out
//
// which reads one line at a time. The profile format permits the SAME block to
// appear more than once, and Go's own parser sums the counts —
// golang.org/x/tools/cover/profile.go does `p.Blocks[j-1].Count += b.Count`,
// and errors if two entries for one position disagree about NumStmt. A
// line-by-line check would call a block uncovered on the strength of one entry
// while another entry covered it.
//
// Measured honestly: of three real profiles from this fleet (2592, 1268 and
// 2592 blocks), NONE repeated a block, so that is a difference in SEMANTICS and
// not a bug anybody is hitting today. This tool parses with x/tools/cover so it
// is right by construction rather than by luck — and because a second
// implementation of a format is the thing that drifts.
//
//	covblocks cov.out                 # the uncovered blocks, and the totals
//	covblocks -max 3 cov.out          # tolerate three, fail on a fourth
//	covblocks a.out b.out             # several profiles, one verdict
//
// ⛔ AN EMPTY PROFILE IS A FAILURE. A profile with no blocks reports the same
// "0 uncovered" as one that covered everything, and this fleet has published a
// zero that meant "could not read".
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/go-fleettools/fleettools/internal/fleet"
	"golang.org/x/tools/cover"
)

func main() {
	fleet.WarnIfStale(os.Stderr)
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(argv []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("covblocks", flag.ContinueOnError)
	fs.SetOutput(stderr)
	max := fs.Int("max", 0, "how many uncovered `blocks` to tolerate before failing")
	quiet := fs.Bool("q", false, "print the totals only, not each uncovered block")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: covblocks [-max 0] [-q] profile [profile...]")
		fmt.Fprintln(stderr, "\nCounts the blocks a Go coverage profile never entered, and exits 1 if there")
		fmt.Fprintln(stderr, "are more than -max. `go tool cover -func` rounds to one decimal, so a gate")
		fmt.Fprintln(stderr, "written against it passes at 99.95%.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(argv); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	paths := fs.Args()
	if len(paths) == 0 {
		fs.Usage()
		return 2
	}
	if *max < 0 {
		fmt.Fprintln(stderr, "covblocks: -max cannot be negative")
		return 2
	}

	var (
		blocks, stmts       int
		uncovered, uncStmts int
		lines               []string
		perFile             = map[string]int{}
	)
	for _, path := range paths {
		profiles, err := cover.ParseProfiles(path)
		if err != nil {
			fmt.Fprintf(stderr, "covblocks: %s: %v\n", path, err)
			return 2
		}
		for _, p := range profiles {
			for _, b := range p.Blocks {
				blocks++
				stmts += b.NumStmt
				if b.Count != 0 {
					continue
				}
				uncovered++
				uncStmts += b.NumStmt
				perFile[p.FileName]++
				lines = append(lines, fmt.Sprintf("%s:%d.%d,%d.%d %d stmt(s)",
					p.FileName, b.StartLine, b.StartCol, b.EndLine, b.EndCol, b.NumStmt))
			}
		}
	}

	// ⛔ A PROFILE WITH NO BLOCKS IS NOT A PASS. It reports the same zero as a
	// profile that covered everything.
	if blocks == 0 {
		fmt.Fprintf(stderr, "covblocks: %d profile(s) hold no blocks at all — nothing was measured\n", len(paths))
		return 2
	}

	if !*quiet {
		for _, l := range lines {
			fmt.Fprintln(stdout, l)
		}
	}
	// The totals ALWAYS, named, so a run that read nothing cannot be mistaken
	// for one that found nothing.
	fmt.Fprintf(stdout, "%d block(s), %d statement(s); %d block(s) uncovered, %d statement(s) uncovered\n",
		blocks, stmts, uncovered, uncStmts)
	for _, f := range sortedByCount(perFile) {
		fmt.Fprintf(stdout, "  %-60s %d\n", f, perFile[f])
	}

	if uncovered > *max {
		fmt.Fprintf(stderr, "covblocks: %d uncovered block(s) > %d allowed\n", uncovered, *max)
		return 1
	}
	return 0
}

// sortedByCount orders files by how many uncovered blocks they hold, most
// first, then by name — so two runs over one profile print the same thing and
// the worst file is the first line a reader sees.
func sortedByCount(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if m[out[i]] != m[out[j]] {
			return m[out[i]] > m[out[j]]
		}
		return out[i] < out[j]
	})
	return out
}
