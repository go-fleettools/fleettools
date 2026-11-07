// Command priorart answers, before the work starts, "has this already been
// done?" — and refuses when the answer is yes.
//
// ⛔⛔ WHY A GATE AND NOT A NOTE. On 2026-11-07 one session redid three separate
// pieces of work on the same repository in a single day:
//
//   - a day spent re-deriving an issue that had been REWRITTEN after
//     bibliography and already held the same diagnosis, a four-size
//     measurement, the corpus number, and an experiment the rediscovery never
//     ran;
//   - two pushed branches, both titled "(NOT MERGED — see #352)", touching the
//     same four files as the new work;
//   - an issue filed as new that duplicated one which had measured the same
//     population by a better method.
//
// "Search the tracker first" was already written down, in a memory, as a rule.
// It stopped none of the three, because of the shape of the moment: the thought
// arrives as "let me fix this", never as "let me check whether this is fixed".
// A rule that has to be remembered at exactly that instant is not a control. So
// the check sits in FRONT of the work and exits non-zero.
//
// It reads the two surfaces every repository has:
//
//   - BRANCHES, by name AND by head-commit subject. A branch is often named for
//     its mechanism and its commit for the symptom: `unrounded-space` answers
//     to "unround", `space-refresh` only to "space". A pushed branch with no
//     pull request is invisible work — it appears on no issue list.
//   - ISSUES AND PULL REQUESTS, open and CLOSED. A closed entry that says "not
//     merged, and here is why" is the most valuable prior art there is, and a
//     search restricted to open items is exactly the one that misses it.
//
// ⛔ A surface it could not read holds the gate SHUT, with status 2: an answer
// not obtained is not a negative answer. The opposite is a gate that opens
// because the network blinked — which opens precisely when nobody is checking.
package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

// gh is the GitHub call (fleet.GH: shared retry policy and call budget),
// replaced in tests.
var gh = fleet.GH

// repos lists an organisation's live repositories, replaced in tests.
var repos = fleet.Repos

const longHelp = `Refuses work on a subject a repository has already covered.

It reads two surfaces and exits 1 if either carries anything:

  BRANCHES          by name AND by head-commit subject — a branch is often
                    named for its mechanism and its commit for the symptom.
                    A pushed branch with no pull request is invisible work.
  ISSUES AND PRs    open and CLOSED. A closed "not merged, and here is why"
                    is the most valuable prior art there is.

  priorart -repo go-tex/engine "running head"
  priorart -org go-tex "running head"        # tracker across the whole org

⛔ GIVE THE DISTINCTIVE NAME, NOT A SENTENCE. Measured on go-tex/engine:
"@themark" returns ONE entry — the issue that holds the work — while
"@themark mark pair read from the class" returns 538 entries and 13 branches,
because "read", "class", "mark" and "pair" match everything that repository
does. The terms are printed before the findings so a useless one is visible
first.

Two calls per repository: one GraphQL (branches, separate budget) and one
search (tracker). With -org the tracker is ONE search call for the whole
organisation; its branches are read only with -branches, one call per
repository, and until then the output says they were not read.

⛔ A surface it could not read holds the gate SHUT (status 2). An answer not
obtained is not a negative answer.

Status: 0 nothing found, 1 prior art found, 2 a surface could not be read.`

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// RootCmd builds the command; tests drive it through cli.Drive.
func RootCmd() *cobra.Command {
	var (
		repoFlags []string
		org       string
		branches  bool
		report    bool
		limit     int
	)
	cmd := cli.New("priorart <subject>",
		"Refuse work on a subject a repository has already covered", longHelp)
	cmd.Args = cobra.ExactArgs(1)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if cli.HelpRequested(cmd) {
			return nil
		}
		return run(cmd.OutOrStdout(), args[0], repoFlags, org, branches, report, limit)
	}
	f := cmd.Flags()
	f.StringArrayVar(&repoFlags, "repo", nil, "repository to read, owner/name (repeatable)")
	f.StringVar(&org, "org", "", "read the tracker of a whole organisation in one call")
	f.BoolVar(&branches, "branches", false, "with -org, also read every repository's branches (one call each)")
	f.BoolVar(&report, "report", false, "print the findings and exit 0 even so")
	// IntVarP, as `brief` does: a one-letter name has to be its own shorthand,
	// or pflag reads `-n` as a shorthand it does not know and refuses the line.
	f.IntVarP(&limit, "n", "n", 10, "show at most this many tracker entries per search (0: all)")
	return cmd
}

func run(w io.Writer, subject string, repoFlags []string, org string, withBranches, report bool, limit int) error {
	terms := Terms(subject)
	if len(terms) == 0 {
		return cli.Unanswerable("subject %q holds no word to search for", subject)
	}
	fmt.Fprintf(w, "subject %q → terms %s\n", subject, strings.Join(terms, ", "))

	rs, err := resolve(repoFlags, org, withBranches)
	if err != nil {
		return cli.Wrap(err)
	}

	found, unread := 0, 0

	// The tracker: one search call per -repo, or one for the whole -org.
	var scopes []string
	for _, r := range rs.tracker {
		scopes = append(scopes, "repo:"+r)
	}
	if org != "" {
		scopes = append(scopes, "org:"+org)
	}
	for _, scope := range scopes {
		items, total, err := Tracker(scope, terms)
		switch {
		case err != nil:
			unread++
			fmt.Fprintf(w, "\n⛔ %s tracker NOT READ, so it has disproved nothing: %v\n", scope, err)
		case len(items) > 0:
			found++
			fmt.Fprintf(w, "\n⛔ %d tracker entr(ies) under %s cover this subject, in the search's own order:\n",
				total, scope)
			shown := items
			if limit > 0 && len(shown) > limit {
				shown = shown[:limit]
			}
			for _, it := range shown {
				fmt.Fprintf(w, "    %-5s %-22s #%-5d [%-6s] %-9s %s\n",
					it.Kind(), it.Repo, it.Number, it.State, "("+it.Hit+")", it.Title)
			}
			// ⛔ The cut is STATED, with the total the search itself reported: a
			// list silently truncated at -n is a measurement whose population is
			// invisible, and the entry that holds the answer may be the one cut.
			if rest := total - len(shown); rest > 0 {
				fmt.Fprintf(w, "    … and %d more (shown: %d of %d; raise with -n, or narrow the subject)\n",
					rest, len(shown), total)
			}
			fmt.Fprintln(w, "   ⚠ A CLOSED entry is often the most valuable: \"not merged, and here is why\".")
		default:
			fmt.Fprintf(w, "   %s tracker: clean (open and closed)\n", scope)
		}
	}

	for _, repo := range rs.branches {
		brs, err := Branches(repo, terms)
		switch {
		case err != nil:
			unread++
			fmt.Fprintf(w, "\n⛔ %s branches NOT READ, so they have disproved nothing: %v\n", repo, err)
		case len(brs) > 0:
			found++
			fmt.Fprintf(w, "\n⛔ %d branch(es) of %s cover this subject:\n", len(brs), repo)
			for _, b := range brs {
				fmt.Fprintf(w, "    %-34s (%s) %s\n", b.Name, b.Hit, b.Subject)
			}
			fmt.Fprintln(w, "   ⚠ A pushed branch with no pull request is INVISIBLE work: read its commit.")
		default:
			fmt.Fprintf(w, "   %s branches: clean (name and head-commit subject)\n", repo)
		}
	}
	for _, r := range rs.unreadBranches {
		fmt.Fprintf(w, "   %s branches: NOT read — pass -branches, or name it with -repo\n", r)
	}

	fmt.Fprintln(w)
	switch {
	case report:
		fmt.Fprintf(w, "-report: %d surface(s) with prior art, %d unread. Exiting 0 as asked.\n", found, unread)
		return nil
	case found > 0:
		return cli.Found("%d surface(s) carry prior art for %q — READ IT before starting", found, subject)
	case unread > 0:
		return cli.Unanswerable("%d surface(s) could not be read; that is not an absence of prior art", unread)
	default:
		n := len(rs.tracker) + len(rs.branches)
		if org != "" {
			n++
		}
		fmt.Fprintf(w, "no prior art for %q on %d surface(s) read\n", subject, n)
		if len(rs.unreadBranches) > 0 {
			fmt.Fprintf(w, "⚠ the branches of %d repositor(ies) were not read; this clean result does not cover them\n",
				len(rs.unreadBranches))
		}
		return nil
	}
}

// targets is which surface is read for which repository.
//
// ⛔ unreadBranches is NOT an empty list that the summary can ignore: a
// repository whose branches were skipped for cost is reported as skipped, so a
// status of 0 can never be read as "and the branches were clean too".
type targets struct {
	tracker        []string
	branches       []string
	unreadBranches []string
}

func resolve(repoFlags []string, org string, withBranches bool) (targets, error) {
	var t targets
	for _, r := range repoFlags {
		if strings.Count(r, "/") != 1 || strings.HasPrefix(r, "/") || strings.HasSuffix(r, "/") {
			return t, cli.Unanswerable("repository %q: want owner/name", r)
		}
		t.tracker = append(t.tracker, r)
		t.branches = append(t.branches, r)
	}
	if org != "" {
		// The tracker is covered org-wide by the single `org:` search below, so
		// the per-repository listing is needed only for the branch surface.
		if withBranches {
			rs, err := repos(org)
			if err != nil {
				return t, fmt.Errorf("organisation %s: %w", org, err)
			}
			for _, r := range rs {
				t.branches = append(t.branches, r.FullName)
			}
		} else {
			rs, err := repos(org)
			if err != nil {
				// Not fatal: the org-wide tracker search still runs, and the
				// branches are reported unread either way.
				t.unreadBranches = append(t.unreadBranches, org+"/* (listing failed: "+err.Error()+")")
			} else {
				for _, r := range rs {
					t.unreadBranches = append(t.unreadBranches, r.FullName)
				}
			}
		}
	}
	if len(t.tracker) == 0 && len(t.branches) == 0 && org == "" {
		return t, cli.Unanswerable("no repository: pass -repo owner/name or -org name")
	}
	sort.Strings(t.branches)
	sort.Strings(t.unreadBranches)
	return t, nil
}
