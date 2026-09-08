// Reports every repository whose default branch's most recent workflow run
// failed. One REST call per repo listing, one per repo's latest run.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

func gh(args ...string) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		cmd := exec.Command("gh", args...)
		var errb strings.Builder
		cmd.Stderr = &errb
		out, err := cmd.Output()
		if err == nil {
			return out, nil
		}
		msg := errb.String()
		if attempt < 5 && (strings.Contains(msg, "secondary rate") || strings.Contains(msg, "abuse") || strings.Contains(msg, "too quickly")) {
			time.Sleep(time.Duration(20*(attempt+1)) * time.Second)
			continue
		}
		return nil, fmt.Errorf("%s", strings.TrimSpace(msg))
	}
}

type repo struct {
	FullName string `json:"full_name"`
	Default  string `json:"default_branch"`
	Archived bool   `json:"archived"`
	Fork     bool   `json:"fork"`
}

type red struct{ repo, wf, when string }

func main() { os.Exit(run(os.Stdout, os.Stderr)) }

// run is the whole program, so that a test can drive it and read what it says.
// It returns the exit status: non-zero when the sweep could not read the whole
// fleet, which is not the same as finding nothing wrong with it.
func run(stdout, stderr io.Writer) int {
	out, err := gh("api", "user/orgs", "--paginate", "--jq", ".[].login")
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
			b, err := gh("api", "orgs/"+o+"/repos?per_page=100", "--paginate")
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
	var unread []string
	checked, rechecked := 0, 0
	for _, r := range repos {
		wg.Add(1)
		go func(r repo) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			v, err := latestRun(r)
			if err != nil {
				mu.Lock()
				unread = append(unread, r.FullName+": "+firstLine(err.Error()))
				mu.Unlock()
				return
			}
			if isRed(v.C) {
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
				if again, err := latestRun(r); err == nil && again.D > v.D {
					mu.Lock()
					rechecked++
					mu.Unlock()
					v = again
				}
			}
			mu.Lock()
			checked++
			if isRed(v.C) {
				reds = append(reds, red{r.FullName, v.N, v.D})
			}
			mu.Unlock()
		}(r)
	}
	wg.Wait()

	sort.Slice(reds, func(i, j int) bool { return reds[i].repo < reds[j].repo })
	fmt.Fprintf(stdout, "checked: %d of %d   RED default branches: %d\n", checked, len(repos), len(reds))
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

// latestRun asks for the newest completed run on a repository's default branch.
func latestRun(r repo) (struct{ C, N, D string }, error) {
	var v struct{ C, N, D string }
	b, err := gh("api",
		"repos/"+r.FullName+"/actions/runs?branch="+r.Default+"&status=completed&per_page=1",
		"--jq", "(.workflow_runs[0] // {}) | {c: (.conclusion // \"\"), n: (.name // \"\"), d: (.created_at // \"\")}")
	if err != nil {
		return v, err
	}
	return v, json.Unmarshal(b, &v)
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
