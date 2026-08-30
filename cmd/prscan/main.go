// Counts open pull requests across every org the account owns. Batches orgs
// into one query each so the search API sees ~20 calls, not 320: search is
// rate-limited far more tightly than the REST API, and a per-org sweep spends
// eleven minutes to learn one number.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
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
		if attempt < 5 && (strings.Contains(msg, "rate") || strings.Contains(msg, "abuse") || strings.Contains(msg, "too quickly")) {
			time.Sleep(time.Duration(20*(attempt+1)) * time.Second)
			continue
		}
		return nil, fmt.Errorf("%s", strings.TrimSpace(msg))
	}
}

type item struct {
	Title  string `json:"title"`
	URL    string `json:"html_url"`
	Draft  bool   `json:"draft"`
	Number int    `json:"number"`
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

	// Build batches under the search query length ceiling.
	var batches [][]string
	cur := []string{}
	n := 0
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
	fmt.Printf("orgs: %d  batches: %d\n", len(orgs), len(batches))

	total := 0
	byRepo := map[string]int{}
	for i, b := range batches {
		q := "is:open is:pr"
		for _, o := range b {
			q += " org:" + o
		}
		raw, err := gh("api", "-X", "GET", "search/issues", "-f", "q="+q, "-f", "per_page=100", "--paginate")
		if err != nil {
			fmt.Fprintf(os.Stderr, "batch %d: %v\n", i, err)
			continue
		}
		// --paginate concatenates JSON objects; count items across them.
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		for {
			var page struct {
				Items []item `json:"items"`
			}
			if err := dec.Decode(&page); err != nil {
				break
			}
			for _, it := range page.Items {
				total++
				parts := strings.Split(it.URL, "/")
				if len(parts) > 5 {
					byRepo[parts[3]+"/"+parts[4]]++
				}
			}
		}
		time.Sleep(2500 * time.Millisecond)
	}

	fmt.Printf("open PRs: %d across %d repos\n", total, len(byRepo))
	type rc struct {
		repo string
		n    int
	}
	var list []rc
	for r, c := range byRepo {
		list = append(list, rc{r, c})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].n != list[j].n {
			return list[i].n > list[j].n
		}
		return list[i].repo < list[j].repo
	})
	for i, e := range list {
		if i >= 25 {
			fmt.Printf("  ... and %d more repos\n", len(list)-25)
			break
		}
		fmt.Printf("  %-46s %d\n", e.repo, e.n)
	}
}
