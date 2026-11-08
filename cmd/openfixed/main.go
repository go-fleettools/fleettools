// Command openfixed lists OPEN issues that a MERGED pull request has already
// addressed, across owners, so they can be closed.
//
// Sources opened before writing it:
//   - GitHub search syntax: `is:issue`, `is:open`, `org:`/`user:` and
//     `linked:pr` ("issues linked to a pull request by a closing reference").
//   - REST issue timeline: GET /repos/{o}/{r}/issues/{n}/timeline, per_page max
//     100; `cross-referenced` events carry source.issue.pull_request.merged_at
//     (non-null = merged), `connected` and reopened events carry timestamps.
//   - Linking a pull request to an issue: the keywords close(s|d), fix(es|ed),
//     resolve(s|d); a bare #N means the PR's own repository, another one needs
//     owner/name#N; the PR must target the default branch for the link to exist.
//
// Classes: CLOSABLE (merged PR with a closing keyword, or a "connected" event)
// and REVIEW (merged PR mentioned it without one, or the issue was reopened
// after the merge). Issues whose referencing PRs are all unmerged are not
// listed. Dry run by default; --close closes only CLOSABLE ones.
//
// ⛔ A scan that cannot read must not report zero: every issue found by search
// is accounted for as read, or skipped with its reason, per owner.
package main

import (
	"encoding/json"
	"errors"
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

// Replaced in tests.
var (
	gh          = fleet.GH
	startBudget = fleet.StartBudget
	pause       = time.Sleep
)

// searchGap keeps the search API (30 requests a minute) well inside its limit.
const searchGap = 2500 * time.Millisecond

// searchCap is the most results one search query returns, ever.
const searchCap = 1000

const longHelp = `Lists open issues that a merged pull request already addressed.

  openfixed --org go-gfx --org go-quake1
  openfixed --owner some-user --close

For each owner it searches open issues (the ones with a closing reference,
` + "`linked:pr`" + `, are read first) and reads each issue's timeline once.

  CLOSABLE  a merged PR referenced it with a closing keyword (or the timeline
            says "connected"), yet it is open. Also needs the PR to have
            targeted the default branch, which the timeline does not show.
  REVIEW    a merged PR mentioned it without a closing keyword, or the issue
            was reopened after the merge: a person decides.

Issues whose referencing PRs are all unmerged are not listed.

The report states its denominator per owner: issues the search reported, listed,
timelines read, skipped and why. A scan that could not read is never zero:
status 2 if anything was skipped.

--close closes only CLOSABLE issues, with a comment naming the PR. Dry run is
the default. It never closes REVIEW.

Budget: stops before the REST budget falls under --margin (default 1000) so the
fleet's Renovate runners are not starved. The search API is paced (30/min).

Status: 0 every issue read, 1 a --close failed, 2 something was skipped.`

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// RootCmd builds the command; tests drive it through cli.Drive.
func RootCmd() *cobra.Command {
	var (
		orgs, owners []string
		closeIt      bool
		margin       int
		maxReads     int
	)
	cmd := cli.New("openfixed", "List open issues a merged pull request already addressed", longHelp)
	cmd.Args = cobra.NoArgs
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if cli.HelpRequested(cmd) {
			return nil
		}
		var scopes []scope
		seen := map[string]bool{}
		for _, o := range orgs {
			if !seen[strings.ToLower(o)] {
				seen[strings.ToLower(o)] = true
				scopes = append(scopes, scope{o, "org:" + o})
			}
		}
		for _, o := range owners {
			if !seen[strings.ToLower(o)] {
				seen[strings.ToLower(o)] = true
				scopes = append(scopes, scope{o, "user:" + o})
			}
		}
		if len(scopes) == 0 {
			return cli.Unanswerable("no owner: pass --org NAME or --owner NAME (repeatable)")
		}
		return run(cmd.OutOrStdout(), scopes, closeIt, margin, maxReads)
	}
	f := cmd.Flags()
	f.StringArrayVar(&orgs, "org", nil, "organisation to scan (repeatable)")
	f.StringArrayVar(&owners, "owner", nil, "user or organisation to scan (repeatable)")
	f.BoolVar(&closeIt, "close", false, "close the CLOSABLE issues (default: dry run)")
	f.IntVar(&margin, "margin", fleet.DefaultReserve, "stop before the REST budget drops under this many calls")
	f.IntVar(&maxReads, "max-timelines", 400, "read at most this many timelines in all (0: no limit)")
	return cmd
}

type scope struct{ name, query string }

type issue struct {
	Repo   string
	Number int
	Title  string
}

func (i issue) ref() string { return fmt.Sprintf("%s#%d", i.Repo, i.Number) }

type finding struct {
	issue
	Class Class
	PRs   []PR
}

type tally struct {
	reported  int // total_count the search reported (all open issues)
	listed    int // issues obtained from the search
	read      int
	skipped   map[string]int // reason → count
	closable  int
	review    int
	closed    int
	closeFail int
}

func (t *tally) skip(reason string, n int) {
	if n > 0 {
		t.skipped[reason] += n
	}
}

func (t *tally) skippedTotal() int {
	n := 0
	for _, c := range t.skipped {
		n += c
	}
	return n
}

func run(w io.Writer, scopes []scope, doClose bool, margin, maxReads int) error {
	if err := startBudget(margin, margin); err != nil {
		return cli.Unanswerable("%v", err)
	}
	reads := 0
	stopped := false
	tallies := map[string]*tally{}
	var all []finding
	mode := "dry run"
	if doClose {
		mode = "--close"
	}
	fmt.Fprintf(w, "openfixed (%s), budget margin %d\n", mode, margin)

	for _, sc := range scopes {
		t := &tally{skipped: map[string]int{}}
		tallies[sc.name] = t
		issues, reported, err := collect(sc)
		t.reported, t.listed = reported, len(issues)
		if err != nil {
			t.skip("search failed: "+err.Error(), max(reported, 1))
			fmt.Fprintf(w, "\nNOT READ: %s search: %v\n", sc.name, err)
			if errors.Is(err, fleet.ErrBudgetReserve) {
				stopped = true
			}
		}
		if err == nil {
			t.skip(fmt.Sprintf("beyond the search's %d-result cap", searchCap), reported-len(issues))
		}
		for i, is := range issues {
			if stopped {
				t.skip("budget reserve reached", len(issues)-i)
				break
			}
			if maxReads > 0 && reads >= maxReads {
				t.skip("--max-timelines reached", len(issues)-i)
				break
			}
			raw, err := gh("api", "--paginate", "--slurp",
				fmt.Sprintf("repos/%s/issues/%d/timeline?per_page=100", is.Repo, is.Number))
			reads++
			var evs []event
			if err == nil {
				evs, err = parseTimeline(raw)
			}
			if err != nil {
				if errors.Is(err, fleet.ErrBudgetReserve) {
					stopped = true
					t.skip("budget reserve reached", len(issues)-i)
					break
				}
				t.skip("timeline unreadable", 1)
				fmt.Fprintf(w, "NOT READ: %s: %v\n", is.ref(), err)
				continue
			}
			t.read++
			c, prs := Classify(is.Repo, is.Number, evs)
			if c == None {
				continue
			}
			all = append(all, finding{is, c, prs})
			if c == Closable {
				t.closable++
			} else {
				t.review++
			}
		}
	}

	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Class != all[j].Class {
			return all[i].Class == Closable
		}
		return all[i].ref() < all[j].ref()
	})
	fmt.Fprintln(w)
	for _, f := range all {
		var parts []string
		for _, p := range f.PRs {
			parts = append(parts, fmt.Sprintf("%s (merged %s)", p.Ref(), p.Date()))
		}
		fmt.Fprintf(w, "%-8s %s  %s  <- %s\n", f.Class, f.ref(), f.Title, strings.Join(parts, ", "))
		if f.Class != Closable {
			continue
		}
		if !doClose {
			continue
		}
		t := tallies[ownerOf(f.Repo, scopes)]
		if err := closeIssue(f); err != nil {
			t.closeFail++
			fmt.Fprintf(w, "         close FAILED: %v\n", err)
		} else {
			t.closed++
			fmt.Fprintf(w, "         closed\n")
		}
	}

	failed, skipped := 0, 0
	fmt.Fprintln(w, "\nsummary per owner:")
	var cl, rv int
	for _, sc := range scopes {
		t := tallies[sc.name]
		fmt.Fprintf(w, "  %-22s searched %d, listed %d, timelines read %d, skipped %d | CLOSABLE %d, REVIEW %d",
			sc.name, t.reported, t.listed, t.read, t.skippedTotal(), t.closable, t.review)
		if doClose {
			fmt.Fprintf(w, " | closed %d, failed %d", t.closed, t.closeFail)
		}
		fmt.Fprintln(w)
		var reasons []string
		for r := range t.skipped {
			reasons = append(reasons, r)
		}
		sort.Strings(reasons)
		for _, r := range reasons {
			fmt.Fprintf(w, "      skipped %d: %s\n", t.skipped[r], r)
		}
		cl, rv = cl+t.closable, rv+t.review
		failed, skipped = failed+t.closeFail, skipped+t.skippedTotal()
	}
	fmt.Fprintf(w, "total: CLOSABLE %d, REVIEW %d\n", cl, rv)
	if left, ok := fleet.BudgetLeft(); ok {
		fmt.Fprintf(w, "budget: about %d REST calls left (margin %d)\n", left, margin)
	}
	if !doClose && cl > 0 {
		fmt.Fprintln(w, "dry run: nothing was closed; rerun with --close to close the CLOSABLE ones")
	}
	if stopped {
		fmt.Fprintf(w, "STOPPED at the budget margin of %d: the scan is INCOMPLETE; wait for the budget to refill\n", margin)
	}
	switch {
	case skipped > 0:
		return cli.Unanswerable("%d issue(s) were not read; this is not a clean count", skipped)
	case failed > 0:
		return cli.Found("%d close(s) failed", failed)
	}
	return nil
}

func ownerOf(repo string, scopes []scope) string {
	o, _, _ := strings.Cut(repo, "/")
	for _, s := range scopes {
		if strings.EqualFold(s.name, o) {
			return s.name
		}
	}
	return scopes[0].name
}

// collect lists an owner's open issues, those linked by a closing reference
// first. It returns the total the search reported for open issues.
func collect(sc scope) ([]issue, int, error) {
	var out []issue
	seen := map[string]bool{}
	var firstErr error
	reported := 0
	for pass, q := range []string{
		"is:issue is:open linked:pr " + sc.query,
		"is:issue is:open " + sc.query,
	} {
		got, total, err := search(q)
		if pass == 1 {
			reported = total
		}
		for _, is := range got {
			if !seen[is.ref()] {
				seen[is.ref()] = true
				out = append(out, is)
			}
		}
		// The second query is the superset: a failure of the first (the
		// linked-first ordering) loses only the order, so only the second counts.
		if err != nil && pass == 1 {
			firstErr = err
		}
	}
	return out, reported, firstErr
}

func search(q string) ([]issue, int, error) {
	var out []issue
	total := 0
	for page := 1; ; page++ {
		pause(searchGap)
		raw, err := gh("api", "-X", "GET", "search/issues",
			"-f", "q="+q, "-f", "per_page=100", "-f", fmt.Sprintf("page=%d", page))
		if err != nil {
			return out, total, err
		}
		var p struct {
			Total int `json:"total_count"`
			Items []struct {
				Number int    `json:"number"`
				Title  string `json:"title"`
				URL    string `json:"repository_url"`
			} `json:"items"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return out, total, fmt.Errorf("search %q: %w", q, err)
		}
		total = p.Total
		for _, it := range p.Items {
			out = append(out, issue{repoOf(it.URL), it.Number, it.Title})
		}
		if len(p.Items) == 0 || len(out) >= p.Total || len(out) >= searchCap {
			return out, total, nil
		}
	}
}

// repoOf turns https://api.github.com/repos/o/r into o/r.
func repoOf(u string) string {
	_, after, ok := strings.Cut(u, "/repos/")
	if !ok {
		return u
	}
	return after
}

func closeIssue(f finding) error {
	use := f.PRs
	var kw []PR
	for _, p := range f.PRs {
		if p.Keyword {
			kw = append(kw, p)
		}
	}
	if len(kw) > 0 {
		use = kw
	}
	var refs []string
	for _, p := range use {
		refs = append(refs, fmt.Sprintf("%s (merged %s)", p.Ref(), p.Date()))
	}
	body := "Closed: fixed by " + strings.Join(refs, " and ") + ", found by fleettools openfixed"
	if _, err := gh("api", "-X", "POST", fmt.Sprintf("repos/%s/issues/%d/comments", f.Repo, f.Number),
		"-f", "body="+body); err != nil {
		return err
	}
	_, err := gh("api", "-X", "PATCH", fmt.Sprintf("repos/%s/issues/%d", f.Repo, f.Number),
		"-f", "state=closed", "-f", "state_reason=completed")
	return err
}
