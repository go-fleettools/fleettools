// Counts open pull requests across every org the account owns. Batches orgs
// into one query each so the search API sees ~20 calls, not 320: search is
// rate-limited far more tightly than the REST API, and a per-org sweep spends
// eleven minutes to learn one number.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/fleet"
)

type item struct {
	Title  string `json:"title"`
	URL    string `json:"html_url"`
	Draft  bool   `json:"draft"`
	Number int    `json:"number"`
}

func main() { os.Exit(run(os.Stdout, os.Stderr, os.Args[1:])) }

// betweenBatches is the pause the search API wants between queries. A test
// drives many batches and must not wait for any of them.
var betweenBatches = 2500 * time.Millisecond

// run is the whole program, so a test can drive it and read what it says.
// Non-zero when the sweep could not search the whole fleet: a count missing
// whole organisations is not a count of the fleet.
func run(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("prscan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	all := fs.Bool("all", false, "list every repository, not the busiest 25")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	orgs, err := fleet.Orgs()
	if err != nil {
		fmt.Fprintln(stderr, "orgs:", err)
		return 1
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
	fmt.Fprintf(stdout, "orgs: %d  batches: %d\n", len(orgs), len(batches))

	total := 0
	byRepo := map[string]int{}
	var failed []string
	for i, b := range batches {
		q := "is:open is:pr"
		for _, o := range b {
			q += " org:" + o
		}
		raw, err := fleet.GH("api", "-X", "GET", "search/issues", "-f", "q="+q, "-f", "per_page=100", "--paginate")
		if err != nil {
			// Also kept for the summary. A batch that failed covers ~15
			// organisations, and its absence lowers the total silently: on
			// stderr alone it is one line a caller reading the count with
			// `| tail` never sees.
			failed = append(failed, fmt.Sprintf("batch %d (%s): %v", i, strings.Join(b, ", "), err))
			fmt.Fprintf(stderr, "batch %d: %v\n", i, err)
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
		time.Sleep(betweenBatches)
	}

	fmt.Fprintf(stdout, "open PRs: %d across %d repos\n", total, len(byRepo))
	if len(failed) > 0 {
		// A count that is missing whole organisations is not a count of the
		// fleet, and saying so beside the number is the only place a reader
		// will meet it.
		fmt.Fprintf(stdout, "\nINCOMPLETE: %d of %d batches could not be searched, so the count above\n"+
			"is a floor and not a total.\n", len(failed), len(batches))
		for i, f := range failed {
			if i == 3 {
				fmt.Fprintf(stdout, "  ... and %d more\n", len(failed)-3)
				break
			}
			fmt.Fprintf(stdout, "  %s\n", f)
		}
	}
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
	// The cut-off is a display choice, and it has been mistaken for the
	// census more than once: a reader who sums the lines gets a number
	// smaller than the total printed two lines above, and the repositories
	// that fall off the end are invisible rather than absent. -all prints
	// them, so a sweep can be driven from this output instead of guessed at.
	for i, e := range list {
		if !*all && i >= 25 {
			fmt.Fprintf(stdout, "  ... and %d more repos (-all to list them)\n", len(list)-25)
			break
		}
		fmt.Fprintf(stdout, "  %-46s %d\n", e.repo, e.n)
	}

	if len(failed) > 0 {
		return 1
	}
	return 0
}
