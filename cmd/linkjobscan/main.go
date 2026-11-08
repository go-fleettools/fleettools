// linkjobscan inventories how every repository in the fleet checks its links:
// the hand-written shell loop that linkcheck replaced (SHELL), fleettools'
// linkcheck itself (LINKCHECK, with the pinned version), lychee (LYCHEE), or
// nothing this tool recognises (NONE).
//
//	linkjobscan
//	linkjobscan --owner go-fleettools
//
// Written 2026-11-08 after go-wasmcontainers/docs#62 swapped a curl loop for
// linkcheck and 13 docs repositories were migrated on 2026-11-05, and the
// go-fleettools landing repository was then found still on the loop: landing
// repositories (`<org>.github.io`) had never been swept. GitHub code search does
// not index workflow files, so the only way to know is to read them.
//
// It reads fleetfind's inventory and fetches every repository's
// .github/workflows through GraphQL, batched (about 20 repositories per call):
// one call returns the file names AND their text, so the whole fleet costs
// ~120 calls instead of ~2200 REST calls, and a private repository is read like
// a public one (raw.githubusercontent.com would answer 404 for it).
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

const longHelp = `Classifies how each repository's CI checks links: SHELL (a curl/wget loop over
URLs extracted with grep, the pattern linkcheck replaced), LINKCHECK (fleettools
linkcheck; its pinned version is reported), LYCHEE, or NONE.

⛔ The report states its denominator: repositories listed, read, UNREAD with the
reason. An unread repository is never counted as NONE. Classification is by
text patterns over the workflow files (comments removed): a SHELL needs a
fetcher, a status test, a loop and a grep that extracts URLs, all four.

Precedence in a repository: SHELL, then LINKCHECK, then LYCHEE; the others
present are listed as "also".

Cost: ~repositories/20 GraphQL calls, charged conservatively against the
shared REST projection, which stops the sweep at --reserve so Renovate keeps
its share.

Exit status: 0 nothing on the shell loop, 1 at least one SHELL repository,
2 some repositories could not be read (the counts are then a floor).`

type options struct {
	owners             []string
	inventory          string
	jobs, batch        int
	reserve            int
	archived, showNone bool
}

// Seams a test replaces.
var (
	startBudget = fleet.StartBudget
	fetchBatch  = ghGraphQL
	ghCall      = fleet.GH
)

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// RootCmd builds the command with the real fetch behind it.
func RootCmd() *cobra.Command {
	var o options
	c := cli.New("linkjobscan [flags]", "How does each repository's CI check links?", longHelp)
	f := c.Flags()
	f.StringSliceVar(&o.owners, "owner", nil, "only this organisation (repeatable)")
	f.StringVar(&o.inventory, "inventory", defaultInventory(), "fleetfind's inventory (fleetfind --refresh writes it)")
	f.IntVarP(&o.jobs, "jobs", "j", 3, "GraphQL calls in flight")
	f.IntVar(&o.batch, "batch", 20, "repositories per GraphQL call")
	f.IntVar(&o.reserve, "reserve", fleet.DefaultReserve, "REST budget left untouched for the fleet's Renovate runners")
	f.BoolVar(&o.archived, "archived", false, "include archived repositories")
	f.BoolVar(&o.showNone, "all", false, "also list NONE repositories")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 || o.jobs < 1 || o.batch < 1 {
			return cli.Unanswerable("usage: linkjobscan [--owner ORG] [--inventory FILE] [-j N] [--batch N] [--reserve N] [--archived] [--all]")
		}
		return cli.FromStatus(run(o, cli.Out(cmd), cli.Err(cmd)))
	}
	return c
}

func defaultInventory() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "inventory.json"
	}
	return filepath.Join(dir, "fleetfind", "inventory.json")
}

type inventory struct {
	Taken time.Time `json:"taken"`
	Repos []struct {
		Org      string `json:"org"`
		Name     string `json:"name"`
		Archived bool   `json:"archived"`
	} `json:"repos"`
}

var safeName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// workflows is one repository's .github/workflows: file name -> text. An empty
// map is a repository with no workflows, which is READ, not unread.
type workflows map[string]string

// ghGraphQL fetches the workflow files of a batch of "org/name" repositories in
// one call. Any failure, including one repository answering NOT_FOUND, fails
// the whole batch; the caller bisects.
func ghGraphQL(repos []string) (map[string]workflows, error) {
	var q strings.Builder
	q.WriteString("query{")
	for i, full := range repos {
		org, name, _ := strings.Cut(full, "/")
		if !safeName.MatchString(org) || !safeName.MatchString(name) {
			return nil, fmt.Errorf("refusing to query the odd name %q", full)
		}
		fmt.Fprintf(&q, `r%d:repository(owner:%q,name:%q){object(expression:"HEAD:.github/workflows"){... on Tree{entries{name object{... on Blob{text isTruncated}}}}}}`, i, org, name)
	}
	q.WriteString("}")
	out, err := ghCall("api", "graphql", "-f", "query="+q.String())
	if err != nil {
		return nil, err
	}
	return parseGraphQL(out, repos)
}

type gqlRepo struct {
	Object *struct {
		Entries []struct {
			Name   string `json:"name"`
			Object struct {
				Text        *string `json:"text"`
				IsTruncated bool    `json:"isTruncated"`
			} `json:"object"`
		} `json:"entries"`
	} `json:"object"`
}

func parseGraphQL(body []byte, repos []string) (map[string]workflows, error) {
	var resp struct {
		Data   map[string]*gqlRepo `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("unreadable GraphQL answer: %w", err)
	}
	if len(resp.Errors) > 0 {
		return nil, fmt.Errorf("GraphQL: %s", resp.Errors[0].Message)
	}
	res := map[string]workflows{}
	for i, full := range repos {
		r := resp.Data[fmt.Sprintf("r%d", i)]
		if r == nil {
			return nil, fmt.Errorf("%s: not visible to this account", full)
		}
		w := workflows{}
		if r.Object != nil {
			for _, e := range r.Object.Entries {
				if !strings.HasSuffix(e.Name, ".yml") && !strings.HasSuffix(e.Name, ".yaml") {
					continue
				}
				if e.Object.Text == nil {
					continue // a directory
				}
				if e.Object.IsTruncated {
					return nil, fmt.Errorf("%s: %s is truncated by the API", full, e.Name)
				}
				w[e.Name] = *e.Object.Text
			}
		}
		res[full] = w
	}
	return res, nil
}

// readAll fetches every repository, bisecting a failed batch so that one bad
// repository costs its own reason and not its neighbours' answers. A budget
// stop is not bisected: the rest simply could not be read.
func readAll(repos []string, o options) (map[string]workflows, map[string]string) {
	read := map[string]workflows{}
	unread := map[string]string{}
	var mu sync.Mutex
	var fetch func(batch []string)
	fetch = func(batch []string) {
		got, err := fetchBatch(batch)
		mu.Lock()
		defer mu.Unlock()
		if err == nil {
			for k, v := range got {
				read[k] = v
			}
			return
		}
		if len(batch) == 1 || errors.Is(err, fleet.ErrBudgetReserve) {
			for _, r := range batch {
				unread[r] = err.Error()
			}
			return
		}
		mu.Unlock()
		half := len(batch) / 2
		fetch(batch[:half])
		fetch(batch[half:])
		mu.Lock()
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, o.jobs)
	for i := 0; i < len(repos); i += o.batch {
		end := min(i+o.batch, len(repos))
		wg.Add(1)
		go func(b []string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			fetch(b)
		}(repos[i:end])
	}
	wg.Wait()
	return read, unread
}

func kind(full string) string {
	org, name, _ := strings.Cut(full, "/")
	switch {
	case name == org+".github.io":
		return "landing"
	case name == "docs" || strings.HasSuffix(name, "-docs"):
		return "docs"
	}
	return "other"
}

func run(o options, stdout, stderr io.Writer) int {
	b, err := os.ReadFile(o.inventory)
	if err != nil {
		fmt.Fprintln(stderr, "linkjobscan: no inventory:", err, "-- run `fleetfind -refresh` first")
		return 2
	}
	var inv inventory
	if err := json.Unmarshal(b, &inv); err != nil || len(inv.Repos) == 0 {
		fmt.Fprintln(stderr, "linkjobscan: the inventory is empty or unreadable")
		return 2
	}
	want := map[string]bool{}
	for _, ow := range o.owners {
		want[ow] = true
	}
	var repos []string
	for _, r := range inv.Repos {
		if (r.Archived && !o.archived) || (len(want) > 0 && !want[r.Org]) {
			continue
		}
		repos = append(repos, r.Org+"/"+r.Name)
	}
	if len(repos) == 0 {
		fmt.Fprintln(stderr, "linkjobscan: no repository matches")
		return 2
	}
	calls := len(repos)/o.batch + 2
	if err := startBudget(o.reserve+calls*2, o.reserve); err != nil {
		fmt.Fprintln(stderr, "linkjobscan:", err)
		fmt.Fprintln(stderr, "              not starting: the rest of the budget is the Renovate runners'.")
		return 2
	}

	read, unread := readAll(repos, o)

	type row struct {
		repo string
		v    Verdict
	}
	var rows []row
	counts := map[Class]int{}
	versions := map[string]int{}
	shellBy := map[string][]string{}
	for _, full := range repos {
		w, ok := read[full]
		if !ok {
			continue
		}
		v := Classify(w)
		rows = append(rows, row{full, v})
		counts[v.Class]++
		if v.Class == Linkcheck || containsClass(v.Also, Linkcheck) {
			versions[v.Version]++
		}
		if v.Class == Shell {
			org, _, _ := strings.Cut(full, "/")
			shellBy[org] = append(shellBy[org], full)
		}
	}
	for _, r := range rows {
		if r.v.Class == None && !o.showNone {
			continue
		}
		detail := strings.Join(r.v.Files, ",")
		if r.v.Version != "" {
			detail += " linkcheck@" + r.v.Version
		}
		if r.v.Tool != "" {
			detail += " " + r.v.Tool
		}
		if r.v.Class == Lychee {
			if r.v.Offline {
				detail += " offline"
			} else {
				detail += " online"
			}
		}
		if len(r.v.Also) > 0 {
			var as []string
			for _, a := range r.v.Also {
				as = append(as, string(a))
			}
			detail += " (also " + strings.Join(as, ",") + ")"
		}
		fmt.Fprintf(stdout, "%-9s %-8s %-52s %s\n", r.v.Class, kind(r.repo), r.repo, strings.TrimSpace(detail))
	}

	var ur []string
	for k, why := range unread {
		ur = append(ur, k+": "+why)
	}
	sort.Strings(ur)
	for _, u := range ur {
		fmt.Fprintln(stderr, "linkjobscan: unread:", u)
	}
	fmt.Fprintf(stdout, "\n%d repositories listed (inventory of %s), %d read, %d UNREAD\n",
		len(repos), inv.Taken.Format("2006-01-02 15:04"), len(read), len(unread))
	fmt.Fprintf(stdout, "classes: SHELL %d, LINKCHECK %d, LYCHEE %d, OTHER %d, NONE %d\n",
		counts[Shell], counts[Linkcheck], counts[Lychee], counts[Other], counts[None])
	var vs []string
	for v, n := range versions {
		vs = append(vs, fmt.Sprintf("%s x%d", v, n))
	}
	sort.Strings(vs)
	fmt.Fprintf(stdout, "linkcheck versions: %s\n", strings.Join(vs, ", "))
	var orgs []string
	for org := range shellBy {
		orgs = append(orgs, org)
	}
	sort.Strings(orgs)
	for _, org := range orgs {
		fmt.Fprintf(stdout, "SHELL %s: %d (%s)\n", org, len(shellBy[org]), strings.Join(shellBy[org], " "))
	}
	fmt.Fprintln(stdout, "not seen: repositories created after the inventory, non-default branches, workflows outside .github/workflows, checks not written as one of the four patterns")
	switch {
	case len(unread) > 0:
		return 2
	case counts[Shell] > 0:
		return 1
	}
	return 0
}
