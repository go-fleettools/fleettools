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
// ⛔⛔ AND A THIRD SURFACE, because two were not enough. On 2026-11-08 the gate
// ran as written on go-tex/go-tex.github.io, reported clean, and was right to:
// go-widgets/toolkit — a module that repository already imports, at the very
// version it pinned — had carried `FileTree` since toolkit#498, a complete
// Explorer with create, rename, delete, move, import, inline name validation
// and a context menu. A "New file" button was written anyway and closed
// unmerged once a human said *there must already be one*. Nothing about a
// consumer's own tracker or branches can contain a widget that lives upstream:
// "has this been done?" has two halves, and only one is about this repository.
//
// It reads the surfaces a repository has:
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

It reads three surfaces and exits 1 if any carries anything:

  BRANCHES          by name AND by head-commit subject — a branch is often
                    named for its mechanism and its commit for the symptom.
                    A pushed branch with no pull request is invisible work.
  ISSUES AND PRs    open and CLOSED. A closed "not merged, and here is why"
                    is the most valuable prior art there is.
  DEPENDENCIES      the EXPORTED SURFACE of the fleet modules this module
                    already requires — a declared type, function or method
                    whose NAME carries a term, in the version MVS selected.

  priorart -repo go-tex/engine "running head"
  priorart -org go-tex "running head"        # tracker across the whole org
  priorart -deps "file manager"              # the modules we already import
  priorart -deps=../playground "file tree"   # another module's build list

⛔ BEFORE WRITING A COMPONENT, ASK -deps. Nothing in a consumer's own tracker
or branches can hold a widget that lives upstream, and on 2026-11-08 that gap
cost a re-implementation: go-widgets/toolkit had carried FileTree — create,
rename, delete, move, import, inline validation, a context menu — since
toolkit#498, in the very version go-tex's playground pinned. The two surfaces
above reported clean and were right to. Run against that repository, -deps
names FileTree, BeginRename, AskDelete and FileRename.

-deps needs no network for the surface itself (it parses the module cache) but
one call to list the organisations this account belongs to, which is what tells
our modules from a stranger's named go-something. -deps-all skips that and
reads every module in the build list.

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
		deps      string
		depsAll   bool
	)
	cmd := cli.New("priorart <subject>",
		"Refuse work on a subject a repository has already covered", longHelp)
	cmd.Args = cobra.ExactArgs(1)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if cli.HelpRequested(cmd) {
			return nil
		}
		return run(cmd.OutOrStdout(), args[0], repoFlags, org, branches, report, limit, deps, depsAll)
	}
	f := cmd.Flags()
	f.StringArrayVar(&repoFlags, "repo", nil, "repository to read, owner/name (repeatable)")
	f.StringVar(&org, "org", "", "read the tracker of a whole organisation in one call")
	f.BoolVar(&branches, "branches", false, "with -org, also read every repository's branches (one call each)")
	f.BoolVar(&report, "report", false, "print the findings and exit 0 even so")
	// ⛔ A string with NoOptDefVal, not a bool: `-deps` alone means the module
	// here, and `-deps ../playground` names another one. A bool plus a second
	// flag for the directory would let the two disagree.
	f.StringVar(&deps, "deps", "", "also read the EXPORTED SURFACE of the fleet modules this module requires (default: .)")
	f.Lookup("deps").NoOptDefVal = "."
	f.BoolVar(&depsAll, "deps-all", false, "with -deps, read every module in the build list, not only the fleet's")
	// IntVarP, as `brief` does: a one-letter name has to be its own shorthand,
	// or pflag reads `-n` as a shorthand it does not know and refuses the line.
	f.IntVarP(&limit, "n", "n", 10, "show at most this many tracker entries per search (0: all)")
	return cmd
}

func run(w io.Writer, subject string, repoFlags []string, org string, withBranches, report bool, limit int, deps string, depsAll bool) error {
	terms := Terms(subject)
	if len(terms) == 0 {
		return cli.Unanswerable("subject %q holds no word to search for", subject)
	}
	fmt.Fprintf(w, "subject %q → terms %s\n", subject, strings.Join(terms, ", "))

	rs, err := resolve(repoFlags, org, withBranches, deps)
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
			side, merged := false, false
			for _, b := range brs {
				// ⛔ THE DEFAULT BRANCH IS THE STRONGEST PRIOR ART AND THE WEAKEST
				// WARNING, so it is labelled rather than lumped in. The first real
				// run of the released binary matched `main`, whose head commit was
				// the subject itself, and printed "a pushed branch with no pull
				// request is INVISIBLE work" under it — which is exactly backwards:
				// the work is merged and visible. A gate whose loudest line is
				// wrong about the first hit it shows is a gate the reader learns to
				// skim, and then it stops being a control at all.
				mark := "    "
				if b.Default {
					mark, merged = "  ✓ ", true
				} else {
					side = true
				}
				fmt.Fprintf(w, "%s%-32s (%s) %s\n", mark, b.Name, b.Hit, b.Subject)
			}
			if merged {
				fmt.Fprintln(w, "   ✓ is the DEFAULT branch: that subject is already MERGED, not merely attempted.")
			}
			if side {
				fmt.Fprintln(w, "   ⚠ A pushed branch with no pull request is INVISIBLE work: read its commit.")
			}
		default:
			fmt.Fprintf(w, "   %s branches: clean (name and head-commit subject)\n", repo)
		}
	}
	for _, r := range rs.unreadBranches {
		fmt.Fprintf(w, "   %s branches: NOT read — pass -branches, or name it with -repo\n", r)
	}

	if rs.deps != "" {
		r, err := depSurface(rs.deps, terms, depsAll)
		if err != nil {
			unread++
			fmt.Fprintf(w, "\n⛔ the build list of %s was NOT READ, so it has disproved nothing: %v\n", rs.deps, err)
		} else {
			if len(r.Hits) > 0 {
				found++
				fmt.Fprintf(w, "\n⛔ %d exported declaration(s) in %d module(s) you ALREADY REQUIRE cover this subject:\n",
					len(r.Hits), r.Read)
				shown := r.Hits
				if limit > 0 && len(shown) > limit {
					shown = shown[:limit]
				}
				for _, h := range shown {
					fmt.Fprintf(w, "    %-34s %-9s %-10s %s.%s\n",
						h.Module+"@"+h.Version, "("+h.Term+")", h.Kind, h.Pkg, h.Name)
				}
				if rest := len(r.Hits) - len(shown); rest > 0 {
					fmt.Fprintf(w, "    … and %d more (shown: %d of %d; raise with -n)\n", rest, len(shown), len(r.Hits))
				}
				// ⛔ The version is printed because "it exists upstream" and "it
				// exists in the version you depend on" are different answers,
				// and only the second one can be called today.
				fmt.Fprintln(w, "   ⚠ These are the versions THIS module selected: you can call them now.")
			} else {
				fmt.Fprintf(w, "   %d module(s) you require: no exported declaration carries this subject\n", r.Read)
			}
			// ⛔ Named one by one, and counted as unread even when there are
			// hits: a module whose surface was not read has disproved nothing
			// about itself, so "found something AND did not finish looking" is
			// the honest state rather than a result.
			for _, u := range r.Unread {
				unread++
				fmt.Fprintf(w, "   ⛔ NOT READ: %s\n", u)
			}
			if len(r.Unread) > 0 {
				fmt.Fprintf(w, "   ⚠ %d of %d selected module(s) were not read; `go mod download` then run again\n",
					len(r.Unread), r.Read+len(r.Unread))
			}
		}
	}

	fmt.Fprintln(w)
	switch {
	case report:
		fmt.Fprintf(w, "-report: %d surface(s) with prior art, %d unread. Exiting 0 as asked.\n", found, unread)
		return nil
	// ⛔ AN UNREAD SURFACE OUTRANKS A HIT, and it did not use to.
	//
	// `found > 0` was tested first, so a branch match returned "already covered"
	// — exit 1 — on a run whose tracker had answered HTTP 422 and been read by
	// nobody. The warning was printed above a verdict that said the opposite,
	// and a caller keying on the EXIT STATUS, which is what a gate in front of
	// work does, read "found, stop" from a run whose most valuable surface was
	// never queried. Measured twice on 2026-11-09, two repositories, both with
	// the warning printed and both exiting 1; on one of them the branches did
	// hold the answer, so the masking cost nothing. That is luck, not design.
	//
	// Both facts are now in the message, because the status is one number and
	// the reader needs the other half.
	case unread > 0 && found > 0:
		return cli.Unanswerable(
			"%d surface(s) carry prior art for %q AND %d could not be read — READ what was found, and note the gap",
			found, subject, unread)
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
	// deps is the module directory whose build list to read, or "" for not
	// asked. It is a surface like the other two: unreadable holds the gate shut.
	deps string
}

func resolve(repoFlags []string, org string, withBranches bool, deps string) (targets, error) {
	var t targets
	t.deps = deps
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
	if len(t.tracker) == 0 && len(t.branches) == 0 && org == "" && deps == "" {
		return t, cli.Unanswerable("no surface: pass -repo owner/name, -org name or -deps")
	}
	sort.Strings(t.branches)
	sort.Strings(t.unreadBranches)
	return t, nil
}
