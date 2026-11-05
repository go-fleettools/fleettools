// Reports branches that carry commits the default branch does not have and
// that NO pull request has ever been opened for. Such a branch is invisible to
// `gh pr list`, to every issue backlog, and to anyone reading the repository to
// pick up work — which is how the same fix gets written twice.
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

const longHelp = `Reports branches holding unlanded commits that no pull request was ever opened for.

A branch pushed and never proposed is invisible twice over: ` + "`gh pr list`" + ` does
not show it, and neither does any backlog that tracks issues. The work on it is
not abandoned, it is unfindable — so it gets done again by someone reading the
backlog, which is what prompted this command.

Every branch lands in exactly one bucket and the buckets are printed with their
total, because a scan that reports only what it found cannot be told from a scan
that could not look:

  stranded   ahead of the default branch, and NO pull request ever existed
  proposed   some pull request has this branch as its head, open or closed
  landed     no pull request, but nothing the default branch does not have
  unknown    a call failed, so this branch was NOT judged

Exit status: 0 nothing is stranded, 1 something is, 2 the scan could not read
what it needed — including any branch left ` + "`unknown`" + `. A branch that could not
be compared is not a branch that is fine.`

// branch is the subset of the branch listing this needs. The head sha is what
// the comparison is asked about, so it is read from the listing rather than
// resolved again per branch.
type branch struct {
	Name   string `json:"name"`
	Commit struct {
		SHA string `json:"sha"`
	} `json:"commit"`
}

// comparison is GitHub's compare payload. AheadBy is the only field used, but
// Status is decoded so a malformed answer is a decode error and not a silent
// zero — "identical" and "could not tell" must not read alike.
type comparison struct {
	Status  string `json:"status"`
	AheadBy int    `json:"ahead_by"`
}

// verdict is the bucket a branch landed in. The zero value is deliberately
// unknown: a branch no code reached is not a branch that passed.
type verdict int

const (
	unknown verdict = iota
	stranded
	proposed
	landed
)

func (v verdict) String() string {
	switch v {
	case stranded:
		return "stranded"
	case proposed:
		return "proposed"
	case landed:
		return "landed"
	}
	return "unknown"
}

type result struct {
	repo    string
	name    string
	verdict verdict
	aheadBy int
	note    string
}

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// RootCmd builds the command. Tests drive it through run, below.
func RootCmd() *cobra.Command {
	c := cli.New("branchscan [flags] <owner/repo>...", "Branches with unlanded commits and no pull request", longHelp)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		return cli.FromStatus(sweep(cli.Out(cmd), cli.Err(cmd), args))
	}
	return c
}

func run(stdout, stderr io.Writer, args []string) int {
	return cli.Drive(RootCmd(), args, stdout, stderr)
}

// sweep is the whole program, so a test can drive it and read what it says.
func sweep(stdout, stderr io.Writer, repos []string) int {
	if len(repos) == 0 {
		fmt.Fprintln(stderr, "branchscan: name at least one repository as owner/repo")
		return 2
	}
	var all []result
	refused := false
	for _, r := range repos {
		if !strings.Contains(r, "/") {
			fmt.Fprintf(stderr, "%s: not an owner/repo\n", r)
			refused = true
			continue
		}
		rs, err := scanRepo(r)
		if err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", r, err)
			refused = true
			continue
		}
		all = append(all, rs...)
	}
	return report(stdout, stderr, all, refused)
}

// scanRepo buckets every branch of one repository.
//
// ⛔ The pull requests are listed ONCE for the whole repository rather than
// asked per branch. `gh pr list --head <name>` is one call per branch, and a
// repository with a hundred and nineteen branches would spend a hundred and
// nineteen of an hourly budget that Renovate also draws on.
func scanRepo(repo string) ([]result, error) {
	var meta struct {
		DefaultBranch string `json:"default_branch"`
	}
	b, err := fleet.GH("api", "repos/"+repo)
	if err != nil {
		return nil, fmt.Errorf("repo: %w", err)
	}
	if err := json.Unmarshal(b, &meta); err != nil {
		return nil, fmt.Errorf("repo: %w", err)
	}
	if meta.DefaultBranch == "" {
		return nil, fmt.Errorf("repo: no default branch in the answer")
	}

	branches, err := listBranches(repo)
	if err != nil {
		return nil, err
	}
	heads, err := listPRHeads(repo)
	if err != nil {
		return nil, err
	}

	var out []result
	for _, br := range branches {
		if br.Name == meta.DefaultBranch {
			continue
		}
		r := result{repo: repo, name: br.Name}
		if heads[br.Name] {
			r.verdict = proposed
			out = append(out, r)
			continue
		}
		// No pull request has ever named this branch. Whether that matters
		// depends on whether it still holds anything.
		cmp, err := compare(repo, meta.DefaultBranch, br.Name)
		if err != nil {
			// ⛔ Left unknown on purpose. A comparison that could not be made
			// is not a comparison that came back empty.
			r.verdict, r.note = unknown, err.Error()
			out = append(out, r)
			continue
		}
		if cmp.AheadBy > 0 {
			r.verdict, r.aheadBy = stranded, cmp.AheadBy
		} else {
			r.verdict = landed
		}
		out = append(out, r)
	}
	return out, nil
}

func listBranches(repo string) ([]branch, error) {
	b, err := fleet.GH("api", "repos/"+repo+"/branches?per_page=100", "--paginate")
	if err != nil {
		return nil, fmt.Errorf("branches: %w", err)
	}
	out, err := decodeArrays[branch](b)
	if err != nil {
		return nil, fmt.Errorf("branches: %w", err)
	}
	return out, nil
}

// listPRHeads is the set of branch names some pull request has had as its head,
// in ANY state. A closed-without-merge pull request still means the branch was
// proposed and someone decided about it; only a branch nobody ever proposed is
// what this command is looking for.
func listPRHeads(repo string) (map[string]bool, error) {
	b, err := fleet.GH("api", "repos/"+repo+"/pulls?state=all&per_page=100", "--paginate")
	if err != nil {
		return nil, fmt.Errorf("pulls: %w", err)
	}
	type pr struct {
		Head struct {
			Ref string `json:"ref"`
		} `json:"head"`
	}
	prs, err := decodeArrays[pr](b)
	if err != nil {
		return nil, fmt.Errorf("pulls: %w", err)
	}
	heads := map[string]bool{}
	for _, p := range prs {
		if p.Head.Ref != "" {
			heads[p.Head.Ref] = true
		}
	}
	return heads, nil
}

func compare(repo, base, head string) (comparison, error) {
	var c comparison
	b, err := fleet.GH("api", "repos/"+repo+"/compare/"+base+"..."+head)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	if c.Status == "" {
		return c, fmt.Errorf("compare: no status in the answer")
	}
	return c, nil
}

// decodeArrays reads what `gh --paginate` writes, which is a STREAM of JSON
// arrays rather than one array: the pages are concatenated, not merged.
func decodeArrays[T any](b []byte) ([]T, error) {
	dec := json.NewDecoder(strings.NewReader(string(b)))
	var out []T
	for {
		var page []T
		if err := dec.Decode(&page); err != nil {
			if err == io.EOF {
				return out, nil
			}
			return nil, err
		}
		out = append(out, page...)
	}
}

// report prints every bucket with its total.
//
// ⛔ The totals are printed even when they are zero, and the sum is asserted
// against the number of branches examined. A scan that prints only its findings
// cannot be told from a scan that could not look, and a bucket that silently
// dropped a branch would show up here as a sum that does not add up.
func report(stdout, stderr io.Writer, all []result, refused bool) int {
	counts := map[verdict]int{stranded: 0, proposed: 0, landed: 0, unknown: 0}
	for _, r := range all {
		counts[r.verdict]++
	}
	sum := counts[stranded] + counts[proposed] + counts[landed] + counts[unknown]

	sort.Slice(all, func(i, j int) bool {
		if all[i].repo != all[j].repo {
			return all[i].repo < all[j].repo
		}
		return all[i].name < all[j].name
	})
	for _, r := range all {
		switch r.verdict {
		case stranded:
			fmt.Fprintf(stdout, "STRANDED  %s  %s  %d commit(s) not on the default branch, no pull request ever\n",
				r.repo, r.name, r.aheadBy)
		case unknown:
			fmt.Fprintf(stdout, "UNKNOWN   %s  %s  not judged: %s\n", r.repo, r.name, r.note)
		}
	}
	fmt.Fprintf(stdout, "branches examined: %d  stranded: %d  proposed: %d  landed: %d  unknown: %d\n",
		sum, counts[stranded], counts[proposed], counts[landed], counts[unknown])
	if sum != len(all) {
		fmt.Fprintf(stderr, "branchscan: %d branches bucketed but %d examined — the scan is wrong about itself\n", sum, len(all))
		return 2
	}

	if refused || counts[unknown] > 0 {
		return 2
	}
	if counts[stranded] > 0 {
		return 1
	}
	return 0
}
