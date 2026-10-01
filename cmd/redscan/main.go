// Reports every workflow whose most recent run on its repository's default
// branch failed. One REST call per organisation's repo listing, one per
// repository for all its completed runs on that branch.
//
// It reads every workflow, not the repository's newest run, because the newest
// run belongs to whichever workflow happened to fire last and says nothing
// about the others. go-tex/go-tex.github.io had `playground` failing and
// `pages` succeeding afterwards, and the sweep read the repository as green
// while a workflow on its default branch had been red for hours.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-fleettools/fleettools/internal/fleet"
)

type repo struct {
	FullName string `json:"full_name"`
	Default  string `json:"default_branch"`
	Archived bool   `json:"archived"`
	Fork     bool   `json:"fork"`
}

type red struct{ repo, wf, when string }

func main() {
	fleet.WarnIfStale(os.Stderr)
	os.Exit(run(os.Stdout, os.Stderr))
}

// run is the whole program, so that a test can drive it and read what it says.
// It returns the exit status: non-zero when the sweep could not read the whole
// fleet, which is not the same as finding nothing wrong with it.
func run(stdout, stderr io.Writer) int {
	out, err := fleet.GH("api", "user/orgs", "--paginate", "--jq", ".[].login")
	if err != nil {
		fmt.Fprintln(stderr, "orgs:", err)
		return 1
	}
	var orgs []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			orgs = append(orgs, l)
		}
	}
	fmt.Fprintf(stdout, "orgs: %d\n", len(orgs))

	var mu sync.Mutex
	var repos []repo
	var unreadOrgs []string
	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	for _, o := range orgs {
		wg.Add(1)
		go func(o string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			b, err := fleet.GH("api", "orgs/"+o+"/repos?per_page=100", "--paginate")
			if err != nil {
				// An organisation whose repositories could not be listed
				// contributes none, and the total below would read as its
				// whole population. Same failure as the one over the runs,
				// one level up.
				mu.Lock()
				unreadOrgs = append(unreadOrgs, o+": "+firstLine(err.Error()))
				mu.Unlock()
				return
			}
			dec := json.NewDecoder(strings.NewReader(string(b)))
			for {
				var page []repo
				if dec.Decode(&page) != nil {
					break
				}
				mu.Lock()
				for _, r := range page {
					if !r.Archived && !r.Fork {
						repos = append(repos, r)
					}
				}
				mu.Unlock()
			}
		}(o)
	}
	wg.Wait()
	fmt.Fprintf(stdout, "repos: %d\n", len(repos))

	var reds []red
	var unread, gone, stale []string
	retried := 0
	checked, rechecked := 0, 0
	for _, r := range repos {
		wg.Add(1)
		go func(r repo) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			runs, allRuns, err := latestRuns(r)
			if err != nil {
				// ⛔ AN UNREAD REPOSITORY IS AN UNASKED QUESTION, and this
				// sweep was leaving 18 of 1983 of them per pass. Every one
				// carried a transport error -- `dial tcp 140.82.112.6:443` --
				// not an HTTP status, and every one of the five sampled by
				// hand answered immediately on a second try:
				//
				//	go-browserhttp/gitcorsproxy  success
				//	go-docutils/docutils         success
				//	go-gnulinux/fido             success
				//	go-hocon/docs                success
				//	go-aiquota/plugin-chatgpt    no completed run at all
				//
				// One retry, only for the handful that failed, so the cost is
				// 18 calls against 1983. A second failure is reported as
				// before: the point is to shrink the blind spot, not to hide
				// it, and the INCOMPLETE banner still names whatever is left.
				time.Sleep(retryPause)
				mu.Lock()
				retried++
				mu.Unlock()
				runs, allRuns, err = latestRuns(r)
			}
			if err != nil {
				mu.Lock()
				unread = append(unread, r.FullName+": "+firstLine(err.Error()))
				mu.Unlock()
				return
			}
			var bad []wfRun
			for _, v := range runs {
				if !isRed(v.C) {
					continue
				}
				// Ask a second time before believing it.
				//
				// This endpoint has twice handed back a MONTHS-OLD run as the
				// newest: openweft/terraform-provider-weft reported failing on
				// 2026-05-30 and openweft/weft-app-gtk on 2026-06-20, while the
				// same query -- rerun 45 times, 40 of them at this sweep's own
				// concurrency -- answered "success" from August every time. The
				// record it returned was internally consistent, the workflow
				// name matching that old run rather than the current one, so it
				// was one stale page and not two crossed fields.
				//
				// A red is rare (2 to 7 of 1936) and it sends somebody looking,
				// so one extra call is nothing against a three-month phantom
				// outage. Take whichever answer names the LATER run: RFC 3339 in
				// Z sorts lexically, and a second reading that is older than the
				// first is the stale one.
				if again, againAll, err := latestRuns(r); err == nil {
					for _, w := range again {
						if w.W == v.W && w.D > v.D {
							mu.Lock()
							rechecked++
							mu.Unlock()
							v = w
						}
					}
					allRuns = againAll
				}
				if isRed(v.C) {
					bad = append(bad, v)
				}
			}
			// ⛔ A RED FROM A WORKFLOW THE REPOSITORY NO LONGER HAS CAN NEVER
			// GO GREEN. GitHub keeps the last run of a deleted or superseded
			// workflow for ever, and this reported it as the current state:
			// go-fsctl/go-fsctl.github.io was named red on "pages build and
			// deployment" from 2026-06-22 while its own Hugo workflow had been
			// deploying successfully since — the site answers 200 and its last
			// four runs are green. go-ruby-hanami was the same shape. Two of
			// thirteen, and the reader who checks two and finds both wrong
			// stops checking the other eleven.
			//
			// The extra call is made ONLY for a repository that already has a
			// red, which is 13 of 1983 — the sweep's budget is unchanged.
			// ⛔ ACTIVE IS NOT THE SAME AS STILL RUNNING, and the guard below
			// was written for these very two repositories without catching
			// them: GitHub reports its implicit `pages-build-deployment` as
			// `active` for ever, so a repository that moved to its own deploy
			// workflow keeps a red from the one it abandoned.
			//
			// The property that separates them is not deletion, it is being
			// SKIPPED. A workflow that gates pushes runs on every push, so the
			// number of later runs on the branch is zero. These two sat at 11
			// and 6, months behind green deploys from the workflow that
			// replaced them.
			//
			// Set aside, not dropped: a rarely-scheduled workflow in a busy
			// repository could land here wrongly, and the reader must be able
			// to see that it did.
			if len(bad) > 0 && len(allRuns) > 0 {
				var kept []wfRun
				for _, v := range bad {
					if n := laterRuns(allRuns, v); n >= supersededAfter {
						mu.Lock()
						stale = append(stale, fmt.Sprintf("%s: %s (last ran %s, %d runs on %s since)",
							r.FullName, v.N, v.D[:10], n, r.Default))
						mu.Unlock()
						continue
					}
					kept = append(kept, v)
				}
				bad = kept
			}
			if len(bad) > 0 {
				if live, err := activeWorkflows(r); err == nil {
					var kept []wfRun
					for _, v := range bad {
						if live[v.W] {
							kept = append(kept, v)
						} else {
							mu.Lock()
							// Said out loud, not dropped: a workflow that
							// vanished is worth knowing about, it is just not
							// a branch anybody can turn green.
							gone = append(gone, r.FullName+": "+v.N)
							mu.Unlock()
						}
					}
					bad = kept
				}
			}
			mu.Lock()
			checked++
			for _, v := range bad {
				reds = append(reds, red{r.FullName, v.N, v.D})
			}
			mu.Unlock()
		}(r)
	}
	wg.Wait()

	sort.Slice(reds, func(i, j int) bool { return reds[i].repo < reds[j].repo })
	fmt.Fprintf(stdout, "checked: %d of %d   RED default branches: %d\n", checked, len(repos), len(reds))
	if len(gone) > 0 {
		sort.Strings(gone)
		fmt.Fprintf(stdout, "  (%d red run(s) belong to a workflow the repository no longer has, so nothing can turn them green:)\n", len(gone))
		for _, g := range gone {
			fmt.Fprintf(stdout, "      %s\n", g)
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		fmt.Fprintf(stdout, "  (%d red run(s) belong to a workflow the branch has moved past — skipped by every push since, so it gates nothing:)\n", len(stale))
		for _, g := range stale {
			fmt.Fprintf(stdout, "      %s\n", g)
		}
	}
	if retried > 0 {
		fmt.Fprintf(stdout, "  (%d repository read(s) failed and were asked again; %d still could not be read)\n",
			retried, len(unread))
	}
	if rechecked > 0 {
		// Said out loud rather than swallowed: a sweep that silently repaired
		// its own readings would hide how often the endpoint does this.
		fmt.Fprintf(stdout, "  (%d red verdict(s) withdrawn on a second reading — a stale page)\n", rechecked)
	}
	for _, r := range reds {
		w := r.when
		if len(w) > 16 {
			w = w[:16]
		}
		fmt.Fprintf(stdout, "  %-48s %-26s %s\n", r.repo, r.wf, w)
	}

	// A pass that could not read is not a pass that found nothing.
	//
	// This printed "checked: 284   RED default branches: 0" over a fleet of
	// 1933, and it was read as "the fleet is green" -- by the session that
	// wrote the tool. The 1649 repositories it could not reach had failed on
	// the primary rate limit, silently, because the error was dropped and only
	// the successes were counted. Re-run with a full budget the same afternoon:
	// 1934 of 1934, and TWO red branches that the reassuring zero had hidden.
	if len(unreadOrgs) == 0 && len(unread) == 0 {
		return 0
	}
	fmt.Fprintln(stdout, "\nINCOMPLETE: this pass says nothing about what follows.")
	report(stdout, "organisations whose repositories could not be listed", unreadOrgs, len(orgs))
	report(stdout, "repositories whose latest run could not be read", unread, len(repos))
	return 1
}

// A wfRun is one completed workflow run, reduced to what a sweep needs.
type wfRun struct {
	W int    // the workflow it belongs to
	C string // conclusion
	N string // the workflow's name
	D string // created_at, RFC 3339 in Z, which sorts lexically
}

// latestRuns asks for a repository's recent completed runs on its default
// branch and keeps the newest of EACH WORKFLOW.
//
// It used to ask for one run and take it, and that hid a failing workflow
// behind a newer passing one. go-tex/go-tex.github.io has two: `playground`
// failed at 15:22 on 2026-09-09 and `pages` succeeded at 15:31, so the sweep
// read the repository as green while a workflow on its default branch was red
// and had been for hours.
//
// It is still ONE call per repository -- a page of a hundred instead of one --
// so the sweep costs no more requests than it did. A workflow that has not run
// within the branch's last hundred runs does not appear, which is a dormant
// workflow rather than a hidden failure.
func latestRuns(r repo) ([]wfRun, []wfRun, error) {
	b, err := fleet.GH("api",
		"repos/"+r.FullName+"/actions/runs?branch="+r.Default+"&status=completed&per_page=100",
		"--jq", "[.workflow_runs[] | {w: (.workflow_id // 0), c: (.conclusion // \"\"), "+
			"n: (.name // \"\"), d: (.created_at // \"\")}]")
	if err != nil {
		return nil, nil, err
	}
	var all []wfRun
	if err := json.Unmarshal(b, &all); err != nil {
		return nil, nil, err
	}
	return newestPerWorkflow(all), all, nil
}

// supersededAfter is how many later runs on the same branch make a workflow's
// last verdict stale.
//
// ⛔ MEASURED, not chosen. A workflow that gates pushes runs on every push, so
// the count of runs newer than its own last one is ZERO. The two repositories
// that made this necessary sat at 11 and 6:
//
//	go-fsctl/go-fsctl.github.io   red 2026-06-22, 11 later runs
//	go-ruby-hanami/….github.io    red 2026-07-06,  6 later runs
//
// Both are GitHub's implicit `pages-build-deployment`, abandoned when the
// repository adopted its own deploy workflow — whose runs are green and as
// recent as yesterday. Five is below the smaller witness and far above zero.
// retryPause is how long to wait before asking a second time. Short: the
// failures are a connection that did not open, not a server saying slow down.
const retryPause = 2 * time.Second

const supersededAfter = 5

// laterRuns counts the runs on the branch that finished after v did. It reads
// the SAME response latestRuns already fetched, so knowing this costs nothing.
func laterRuns(all []wfRun, v wfRun) int {
	n := 0
	for _, o := range all {
		if o.D > v.D {
			n++
		}
	}
	return n
}

// newestPerWorkflow keeps one run per workflow: the one naming the later date.
// The endpoint hands them back newest first, and this does not rely on that.
func newestPerWorkflow(all []wfRun) []wfRun {
	best := map[int]wfRun{}
	for _, v := range all {
		if b, seen := best[v.W]; !seen || v.D > b.D {
			best[v.W] = v
		}
	}
	out := make([]wfRun, 0, len(best))
	for _, v := range best {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].N < out[j].N })
	return out
}

// isRed reports whether a run's conclusion is one a person should look at.
func isRed(conclusion string) bool {
	return conclusion == "failure" || conclusion == "timed_out"
}

// report names what could not be read, with a sample of why. A count alone
// invites the reader to assume a cause; the reasons are what tell a rate limit
// from a permission.
func report(w io.Writer, what string, items []string, of int) {
	if len(items) == 0 {
		return
	}
	sort.Strings(items)
	fmt.Fprintf(w, "  %d of %d %s:\n", len(items), of, what)
	for i, it := range items {
		if i == 5 {
			fmt.Fprintf(w, "    ... and %d more\n", len(items)-5)
			break
		}
		fmt.Fprintf(w, "    %s\n", it)
	}
}

// firstLine keeps an error readable in a list of them: gh prints a paragraph
// about rate limits, and the first line is the part that differs.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 90 {
		s = s[:90] + "..."
	}
	return strings.TrimSpace(s)
}

// activeWorkflows is the set of workflow ids the repository still has and has
// not disabled. GitHub reports a removed one as state "deleted" and keeps
// serving its runs, which is how a branch stays red with nothing to fix.
func activeWorkflows(r repo) (map[int]bool, error) {
	out, err := fleet.GH("api", "repos/"+r.FullName+"/actions/workflows?per_page=100",
		"--jq", "[.workflows[] | {i: .id, s: .state}]")
	if err != nil {
		return nil, err
	}
	var rows []struct {
		I int    `json:"i"`
		S string `json:"s"`
	}
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, err
	}
	live := map[int]bool{}
	for _, w := range rows {
		if w.S == "active" {
			live[w.I] = true
		}
	}
	return live, nil
}
