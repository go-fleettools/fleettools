// Merges dependency pull requests that are both mergeable and fully green.
//
// Only PRs whose head branch starts with "renovate/" are touched: a human PR
// is never merged by this. A PR with no checks at all is left alone too --
// green means checks ran and passed, not that none exist.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
		// A transient network fault is worth retrying too, not just a rate
		// limit. One pass lost 186 of 460 candidates to "no route to host"
		// while the machine's link flapped -- they were not bad pull requests,
		// and giving up on the first dial error turned a blip into a silent
		// hole in the sweep.
		transient := strings.Contains(msg, "no route to host") ||
			strings.Contains(msg, "operation timed out") ||
			strings.Contains(msg, "connection reset") ||
			strings.Contains(msg, "i/o timeout") ||
			strings.Contains(msg, "TLS handshake timeout") ||
			strings.Contains(msg, "EOF") ||
			// gh's own wording, which the Go-level strings above do not cover.
			// A DNS outage mid-sweep produced 109 of these and the retry never
			// fired, because I matched the errors I had SEEN rather than the
			// ones the tool actually emits.
			strings.Contains(msg, "error connecting to") ||
			strings.Contains(msg, "no such host") ||
			strings.Contains(msg, "check your internet connection")
		if attempt < 5 && (transient || strings.Contains(msg, "secondary rate") || strings.Contains(msg, "abuse") || strings.Contains(msg, "too quickly") || strings.Contains(msg, "rate limit")) {
			time.Sleep(time.Duration(20*(attempt+1)) * time.Second)
			continue
		}
		return nil, fmt.Errorf("%s", strings.TrimSpace(msg))
	}
}

type prRef struct {
	repo string
	num  int
}

// ghPR reads a pull request over REST and reshapes it into the fields the
// GraphQL view handed back, so its caller is unchanged.
//
// `gh pr view` is GraphQL, and that budget is SEPARATE from the REST one and
// far smaller in practice: one call per candidate over a few passes of ~300
// exhausted it while REST still read 5000/5000, and 23 pull requests came back
// as "cannot read PR" -- a rate limit wearing the mask of a failure.
//
// REST reports mergeable as null while GitHub is still computing it, so an
// unknown state is reported as UNKNOWN rather than guessed either way.
// refusalKind names why a merge was refused, because the causes need
// different answers: a scope refusal waits on `gh auth refresh -s workflow`,
// a conflict waits on Renovate rebasing, and a moved base only wants the sweep
// run again.
// gh2 runs a command other than gh, with the same backoff.
func gh2(name string, args ...string) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		cmd := exec.Command(name, args...)
		var errb strings.Builder
		cmd.Stderr = &errb
		out, err := cmd.Output()
		if err == nil {
			return out, nil
		}
		msg := errb.String() + string(out)
		transient := strings.Contains(msg, "no route to host") ||
			strings.Contains(msg, "operation timed out") ||
			strings.Contains(msg, "connection reset") ||
			strings.Contains(msg, "i/o timeout") ||
			strings.Contains(msg, "error connecting to") ||
			strings.Contains(msg, "secondary rate") ||
			strings.Contains(msg, "rate limit")
		if attempt < 5 && transient {
			time.Sleep(time.Duration(20*(attempt+1)) * time.Second)
			continue
		}
		return nil, fmt.Errorf("%s", strings.TrimSpace(msg))
	}
}

func refusalKind(err error) string {
	m := strings.ToLower(err.Error())
	switch {
	case strings.Contains(m, "workflow` scope"), strings.Contains(m, "workflow scope"):
		return "workflow-scope"
	case strings.Contains(m, "merge conflicts"), strings.Contains(m, "not mergeable"):
		return "conflict"
	case strings.Contains(m, "base branch was modified"):
		return "base-moved"
	case strings.Contains(m, "--auto"), strings.Contains(m, "requirements have been met"):
		return "requirements-unmet"
	case strings.Contains(m, "review"):
		return "review-required"
	default:
		return "other"
	}
}

func ghPR(repo string, num int) ([]byte, error) {
	raw, err := gh("api", fmt.Sprintf("repos/%s/pulls/%d", repo, num))
	if err != nil {
		return nil, err
	}
	var pr struct {
		Title     string `json:"title"`
		Draft     bool   `json:"draft"`
		Mergeable *bool  `json:"mergeable"`
		Head      struct {
			Ref string `json:"ref"`
			SHA string `json:"sha"`
		} `json:"head"`
	}
	if err := json.Unmarshal(raw, &pr); err != nil {
		return nil, err
	}
	mergeable := "UNKNOWN"
	if pr.Mergeable != nil {
		if *pr.Mergeable {
			mergeable = "MERGEABLE"
		} else {
			mergeable = "CONFLICTING"
		}
	}

	type roll struct {
		Conclusion string `json:"conclusion"`
		Status     string `json:"status"`
		State      string `json:"state"`
	}
	rolls := []roll{}
	if cr, err := gh("api", fmt.Sprintf("repos/%s/commits/%s/check-runs?per_page=100", repo, pr.Head.SHA)); err == nil {
		var v struct {
			CheckRuns []struct {
				Conclusion string `json:"conclusion"`
				Status     string `json:"status"`
			} `json:"check_runs"`
		}
		if json.Unmarshal(cr, &v) == nil {
			for _, c := range v.CheckRuns {
				rolls = append(rolls, roll{Conclusion: strings.ToUpper(c.Conclusion), Status: strings.ToUpper(c.Status)})
			}
		}
	}

	return json.Marshal(map[string]any{
		"headRefName": pr.Head.Ref, "mergeable": mergeable, "mergeStateStatus": "",
		"isDraft": pr.Draft, "title": pr.Title, "statusCheckRollup": rolls,
	})
}

// heldByOthers reads agentsync's leases. A fleet-wide sweep cannot claim a
// hundred repositories -- that would block every other session -- so it does the
// opposite: it looks at what someone else has claimed and stays out. This is the
// discipline that would have kept me out of go-pkgx/packages while a build
// campaign was running there.
func heldByOthers() map[string]string {
	out := map[string]string{}
	dir := os.Getenv("AGENTSYNC_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return out
		}
		dir = filepath.Join(home, ".claude", "agentsync")
	}
	me := os.Getenv("CLAUDE_CODE_SESSION_ID")
	entries, err := os.ReadDir(filepath.Join(dir, "locks"))
	if err != nil {
		return out // no leases at all is the common case, not an error
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, "locks", e.Name()))
		if err != nil {
			continue
		}
		var l struct {
			Resource string    `json:"resource"`
			Owner    string    `json:"owner"`
			Note     string    `json:"note"`
			Expires  time.Time `json:"expires"`
		}
		if json.Unmarshal(b, &l) != nil || l.Owner == me || time.Now().After(l.Expires) {
			continue
		}
		out[l.Resource] = l.Owner
	}
	return out
}

func main() {
	out, err := gh("api", "user/orgs", "--paginate", "--jq", ".[].login")
	if err != nil {
		fmt.Fprintln(os.Stderr, "orgs:", err)
		os.Exit(1)
	}
	var orgs []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			orgs = append(orgs, l)
		}
	}
	sort.Strings(orgs)

	var batches [][]string
	cur, n := []string{}, 0
	for _, o := range orgs {
		if n+len(o)+5 > 220 && len(cur) > 0 {
			batches = append(batches, cur)
			cur, n = []string{}, 0
		}
		cur = append(cur, o)
		n += len(o) + 5
	}
	if len(cur) > 0 {
		batches = append(batches, cur)
	}

	var refs []prRef
	for _, b := range batches {
		q := "is:open is:pr"
		for _, o := range b {
			q += " org:" + o
		}
		raw, err := gh("api", "-X", "GET", "search/issues", "-f", "q="+q, "-f", "per_page=100", "--paginate")
		if err != nil {
			fmt.Fprintln(os.Stderr, "search:", err)
			continue
		}
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		for {
			var page struct {
				Items []struct {
					Number int    `json:"number"`
					URL    string `json:"html_url"`
				} `json:"items"`
			}
			if dec.Decode(&page) != nil {
				break
			}
			for _, it := range page.Items {
				p := strings.Split(it.URL, "/")
				if len(p) > 5 {
					refs = append(refs, prRef{p[3] + "/" + p[4], it.Number})
				}
			}
		}
		time.Sleep(2 * time.Second)
	}
	fmt.Printf("candidates: %d\n", len(refs))
	held := heldByOthers()
	if len(held) > 0 {
		fmt.Printf("claimed by other sessions, skipping: %d repositories\n", len(held))
	}

	sem := make(chan struct{}, 3)
	var wg sync.WaitGroup
	var mu sync.Mutex
	merged, skipped := 0, map[string]int{}
	reported := 0
	detail := map[string][]string{}

	for _, r := range refs {
		wg.Add(1)
		go func(r prRef) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			// REST, not `gh pr view`. That command is GraphQL, whose budget is
			// SEPARATE from the REST one and far smaller in practice: one call
			// per candidate across a few passes of ~300 candidates exhausted it
			// while REST still showed 5000/5000, and 23 pull requests came back
			// as "cannot read PR" -- a rate limit wearing the mask of a failure.
			b, err := ghPR(r.repo, r.num)
			if err != nil {
				mu.Lock()
				skipped["view-failed"]++
				// Report WHY, for the first few. A counter that says 186 failed
				// without saying why is the same defect as a summary that counts
				// lines: it looks like a measurement and is not one.
				if reported < 5 {
					reported++
					fmt.Printf("  view-failed %s#%d: %v\n", r.repo, r.num, err)
				}
				mu.Unlock()
				return
			}
			var pr struct {
				HeadRefName       string `json:"headRefName"`
				Mergeable         string `json:"mergeable"`
				MergeStateStatus  string `json:"mergeStateStatus"`
				IsDraft           bool   `json:"isDraft"`
				Title             string `json:"title"`
				StatusCheckRollup []struct {
					Conclusion string `json:"conclusion"`
					Status     string `json:"status"`
					State      string `json:"state"`
				} `json:"statusCheckRollup"`
			}
			if json.Unmarshal(b, &pr) != nil {
				mu.Lock()
				skipped["parse"]++
				mu.Unlock()
				return
			}

			// The Go toolchain is never merged by this. The org presets set
			// automerge:false on it and give it a branch of its own, because
			// go1.27.0 miscompiles on loong64 (golang/go#81000) and the choice
			// of when to take a toolchain belongs to a person, not to a sweep
			// that merges whatever is green.
			title := strings.ToLower(pr.Title)
			goBump := strings.Contains(pr.HeadRefName, "renovate/go-1") ||
				((strings.Contains(title, "dependency go to") || strings.Contains(title, "module go to")) &&
					!strings.Contains(title, "go to v0"))

			reason := ""
			switch {
			case held[r.repo] != "":
				reason = "claimed-by-another-session"
			case !strings.HasPrefix(pr.HeadRefName, "renovate/"):
				reason = "not-a-renovate-branch"
			case goBump:
				reason = "go-toolchain-left-to-a-person"
			case pr.IsDraft:
				reason = "draft"
			case pr.Mergeable != "MERGEABLE":
				reason = "conflicting"
			case len(pr.StatusCheckRollup) == 0:
				reason = "no-checks"
			}
			if reason == "" {
				for _, c := range pr.StatusCheckRollup {
					v := c.Conclusion
					if v == "" {
						v = c.State
					}
					if v == "" {
						v = c.Status
					}
					if v != "SUCCESS" && v != "NEUTRAL" && v != "SKIPPED" {
						reason = "not-green"
						break
					}
				}
			}
			if reason != "" {
				mu.Lock()
				skipped[reason]++
				if reason == "not-green" || reason == "no-checks" {
					detail[reason] = append(detail[reason], fmt.Sprintf("%s#%d", r.repo, r.num))
				}
				mu.Unlock()
				return
			}

			// ghmerge, not `gh pr merge`. Two reasons, both learned the hard
			// way: it serves ~/.github-token, which carries the `workflow`
			// scope that gh's keyring token lacks -- that alone unblocks the
			// largest class in the fleet, 78 green pull requests in one pass --
			// and it refuses unless a check actually RAN and passed, so "nothing
			// is failing" cannot pass for "everything passed".
			if _, err := gh2("ghmerge", r.repo, fmt.Sprint(r.num)); err != nil {
				mu.Lock()
				// Break the refusals down. "merge-refused: 83" hid three
				// unrelated causes behind one number, and twice today I read a
				// conflict as a scope refusal because they print the same way
				// in a summary.
				skipped["merge-refused ("+refusalKind(err)+")"]++
				mu.Unlock()
				fmt.Printf("  REFUSED %s#%d: %v\n", r.repo, r.num, err)
				return
			}
			mu.Lock()
			merged++
			mu.Unlock()
			fmt.Printf("  merged %s#%d  %s\n", r.repo, r.num, pr.Title)
		}(r)
	}
	wg.Wait()

	fmt.Printf("\nmerged: %d\n", merged)
	keys := make([]string, 0, len(skipped))
	for k := range skipped {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("  skipped %-22s %d\n", k, skipped[k])
	}
	for _, k := range []string{"not-green", "no-checks"} {
		if len(detail[k]) == 0 {
			continue
		}
		sort.Strings(detail[k])
		fmt.Printf("\n%s:\n", k)
		for _, d := range detail[k] {
			fmt.Printf("  %s\n", d)
		}
	}
}
