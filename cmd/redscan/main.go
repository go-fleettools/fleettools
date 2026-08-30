// Reports every repository whose default branch's most recent workflow run
// failed. One REST call per repo listing, one per repo's latest run.
package main

import (
	"encoding/json"
	"fmt"
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
	fmt.Printf("orgs: %d\n", len(orgs))

	var mu sync.Mutex
	var repos []repo
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
	fmt.Printf("repos: %d\n", len(repos))

	var reds []red
	checked := 0
	for _, r := range repos {
		wg.Add(1)
		go func(r repo) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			b, err := gh("api",
				"repos/"+r.FullName+"/actions/runs?branch="+r.Default+"&status=completed&per_page=1",
				"--jq", "(.workflow_runs[0] // {}) | {c: (.conclusion // \"\"), n: (.name // \"\"), d: (.created_at // \"\")}")
			if err != nil {
				return
			}
			var v struct{ C, N, D string }
			if json.Unmarshal(b, &v) != nil {
				return
			}
			mu.Lock()
			checked++
			if v.C == "failure" || v.C == "timed_out" {
				reds = append(reds, red{r.FullName, v.N, v.D})
			}
			mu.Unlock()
		}(r)
	}
	wg.Wait()

	sort.Slice(reds, func(i, j int) bool { return reds[i].repo < reds[j].repo })
	fmt.Printf("checked: %d   RED default branches: %d\n", checked, len(reds))
	for _, r := range reds {
		w := r.when
		if len(w) > 16 {
			w = w[:16]
		}
		fmt.Printf("  %-48s %-26s %s\n", r.repo, r.wf, w)
	}
}
