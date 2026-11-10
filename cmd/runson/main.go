// runson reports which runner images the fleet's workflows ask for.
//
//	runson                      # the distribution, over every checkout
//	runson --value ubuntu-latest  # WHICH repositories ask for that value
//	runson --fail-on ubuntu-latest  # exit 1 if anything still asks for it
//
// ⛔ WHY IT READS CHECKOUTS AND NOT GITHUB. Asking GitHub would be about
// twenty requests per repository across some 1400 of them, several times over
// the hourly budget the fleet's Renovate runners draw on -- a budget this
// machine's sweeps have exhausted five times. The checkouts are already here
// and reading them costs nothing.
//
// ⛔ AND THE PRICE OF READING A DISK IS STALENESS, WHICH IS MEASURED HERE
// RATHER THAN DISCLAIMED. The first version of this tool printed "a clone can
// be months stale, so this bounds the shape of the exposure, not its size" and
// left it there. That sentence is true and useless: it tells a reader to
// distrust the number without telling them how much. internal/checkout already
// measures the distance -- testscan once reported 10 repositories with no CI
// and 2 after its clones were pulled, judgescan 8 and then 4 -- so the warning
// under this table counts the checkouts that are actually behind, and names
// how far.
//
// The failure mode that matters is the quiet one: a checkout predating a
// newly added workflow makes runson UNDERSTATE exposure, and an absence read
// from a stale disk looks exactly like an absence that is real.
//
// Written because actions/runner-images#14748 migrates ubuntu-latest (x64) to
// Ubuntu 26.04 between 2026-10-19 and 2026-11-19: inside that window a red CI
// can be the image moving while the repository did not, and the first question
// is how much of the fleet is standing on it.
package main

import (
	"fmt"
	"io"
	"sort"
	"text/tabwriter"

	"os"

	"github.com/go-fleettools/fleettools/internal/checkout"
	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(rootCmd())
}

func rootCmd() *cobra.Command {
	c := cli.New("runson [flags]",
		"which runner images the fleet's workflows ask for",
		`runson reads the checkouts under the clone root and counts the runs-on
values their workflows ask for, one count per REPOSITORY rather than per file.

It makes no API call. The staleness of the checkouts it read is measured and
printed under the table, because a value absent from a stale disk is
indistinguishable from a value that is absent.`)

	var (
		root    string
		only    string
		value   string
		failOn  string
		noStale bool
	)
	c.Flags().StringVar(&root, "root", checkout.DefaultRoot(), "the clone root to read")
	c.Flags().StringVar(&only, "repo", "", "read only this org/repo")
	c.Flags().StringVar(&value, "value", "", "list the repositories whose workflows ask for this exact value")
	c.Flags().StringVar(&failOn, "fail-on", "", "exit 1 if any repository asks for this exact value")
	c.Flags().BoolVar(&noStale, "no-staleness", false, "skip the staleness measurement (faster, and the table then says nothing about the evidence)")

	c.RunE = func(cmd *cobra.Command, _ []string) error {
		return run(cmd, root, only, value, failOn, noStale)
	}
	return c
}

func run(cmd *cobra.Command, root, only, value, failOn string, noStale bool) error {
	repos, err := checkout.Repos(root, only)
	if err != nil {
		return cli.Wrap(err)
	}
	// ⛔ AN EMPTY POPULATION IS NOT A CLEAN RESULT. Pointed at a root with the
	// wrong layout -- the flat <org>-<repo> working farm rather than
	// <org>/<repo> -- Repos finds nothing, and printing an empty table with a
	// status of 0 would report "no repository asks for ubuntu-latest" when the
	// truth is that nothing was read.
	if len(repos) == 0 {
		return cli.Unanswerable("no checkout under %s: nothing was read, which is not the same as nothing found", root)
	}

	var findings []Finding
	var ages []checkout.Age
	withWorkflows, withoutWorkflows := 0, 0
	for _, repo := range repos {
		dir := root + "/" + repo
		found, had := scanRepo(dir, repo)
		if had {
			withWorkflows++
		} else {
			withoutWorkflows++
		}
		findings = append(findings, found...)
		if !noStale && had {
			behind, age, ok := checkout.Staleness(dir)
			ages = append(ages, checkout.Age{Behind: behind, FetchAge: age, FetchKnown: ok})
		}
	}

	counts := tally(findings)
	out := cli.Out(cmd)

	if value != "" {
		return listOne(out, counts, value)
	}

	fmt.Fprintf(out, "%d checkouts under %s, %d with workflows, %d without\n",
		len(repos), root, withWorkflows, withoutWorkflows)
	fmt.Fprintf(out, "%d (repository, workflow, runs-on) findings\n\n", len(findings))

	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "runs-on\trepos")
	for _, ct := range counts {
		fmt.Fprintf(tw, "%s\t%d\n", ct.Value, len(ct.Repos))
	}
	tw.Flush()

	if w := stalenessNote(ages); w != "" {
		fmt.Fprint(out, w)
	} else if noStale {
		fmt.Fprint(out, "\n⚠ staleness NOT measured (--no-staleness): a value missing from an old\n"+
			"  checkout reads the same as a value that is genuinely gone.\n")
	}

	if failOn != "" {
		for _, ct := range counts {
			if ct.Value == failOn {
				return cli.Found("%d repositories still ask for %q", len(ct.Repos), failOn)
			}
		}
	}
	return nil
}

// listOne prints the repositories behind one value.
//
// ⭐ THE COUNT IS NOT THE ANSWER, THE LIST IS. A distribution nobody can
// follow back to a repository is a number to quote, not evidence to act on --
// and reading the list is what exposed this tool's own worst defect, when an
// earlier version counted `git@github.com:o/r` and `https://github.com/o/r` as
// two repositories and reported 620 where there were 504. The inflated count
// looked entirely reasonable; the list had the same name twice.
func listOne(out io.Writer, counts []Count, value string) error {
	for _, ct := range counts {
		if ct.Value != value {
			continue
		}
		names := append([]string(nil), ct.Repos...)
		sort.Strings(names)
		fmt.Fprintf(out, "%d repositories ask for %q:\n", len(names), value)
		for _, n := range names {
			fmt.Fprintln(out, "  "+n)
		}
		return nil
	}
	// Not a finding and not an error: the question was put and answered.
	fmt.Fprintf(out, "no checkout asks for %q\n", value)
	return nil
}
