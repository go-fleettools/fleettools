// gorequires answers "which of our modules require module X, and at which
// version?" -- the reverse question nothing in the fleet could answer.
//
//	gorequires -module github.com/ajroetker/go-highway
//	gorequires -module github.com/ajroetker/go-highway -version v0.0.12
//
// It reads every active repository of fleetfind's inventory (the dated cache
// `fleetfind -refresh` writes) and fetches its root go.mod from
// raw.githubusercontent.com -- plain HTTPS, not the REST API, so a sweep of two
// thousand repositories costs Renovate nothing.
//
// Written 2026-10-12 to bring go-highway back from v0.0.12, whose go.mod
// requires an unpublished hwy/goat v0.0.0 (ajroetker/go-highway#73), to v0.0.11
// everywhere it appears. By minimal version selection, ONE go.mod left at
// v0.0.12 anywhere in a graph keeps the whole graph there -- so the list has to
// be complete, and the report says what it could not read rather than leaving
// a gap that reads as "does not require it".
//
// Limits, printed with every answer: the inventory's date (a repository created
// since is not seen), root go.mod only (a nested module is not seen), and the
// default branch only.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/mod/modfile"

	"github.com/go-fleettools/fleettools/internal/fleet"
)

func main() {
	o, code, proceed := parseArgs(os.Args[1:], os.Stderr)
	if !proceed {
		os.Exit(code)
	}
	fleet.WarnIfStale(os.Stderr)
	os.Exit(run(o, os.Stdout, os.Stderr, fetchGoMod))
}

type options struct {
	module, version, inventory string
	jobs                       int
	archived                   bool
}

func parseArgs(args []string, stderr io.Writer) (options, int, bool) {
	fs := flag.NewFlagSet("gorequires", flag.ContinueOnError)
	fs.SetOutput(stderr)
	o := options{}
	fs.StringVar(&o.module, "module", "", "the module path to look for (required)")
	fs.StringVar(&o.version, "version", "", "only report go.mod files requiring exactly this version")
	fs.StringVar(&o.inventory, "inventory", defaultInventory(), "fleetfind's inventory (fleetfind -refresh writes it)")
	fs.IntVar(&o.jobs, "j", 8, "go.mod files fetched at once")
	fs.BoolVar(&o.archived, "archived", false, "include archived repositories")
	switch err := fs.Parse(args); {
	case errors.Is(err, flag.ErrHelp):
		return o, 0, false
	case err != nil:
		return o, 2, false
	}
	if o.module == "" || fs.NArg() > 0 || o.jobs < 1 {
		fmt.Fprintln(stderr, "usage: gorequires -module PATH [-version vX.Y.Z] [-inventory FILE] [-j N] [-archived]")
		return o, 2, false
	}
	return o, 0, true
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

// errNoGoMod is a 404. ⛔ It means EITHER no root go.mod OR a private
// repository: raw.githubusercontent.com answers 404 to an anonymous request for
// a private file, exactly as for a missing one, and this tool sends no token
// (a token never goes in a URL, a command line or an environment variable
// here). The report counts these together and names the ambiguity.
var errNoGoMod = errors.New("404: no root go.mod, or a private repository")

// Hit is one go.mod that requires the module.
type Hit struct {
	Repo, Module, Version string
	Indirect              bool
}

// Requires reports whether a go.mod requires mod, and at which version.
func Requires(gomod []byte, mod string) (Hit, bool, error) {
	f, err := modfile.ParseLax("go.mod", gomod, nil)
	if err != nil {
		return Hit{}, false, err
	}
	for _, r := range f.Require {
		if r.Mod.Path == mod {
			h := Hit{Version: r.Mod.Version, Indirect: r.Indirect}
			if f.Module != nil {
				h.Module = f.Module.Mod.Path
			}
			return h, true, nil
		}
	}
	return Hit{}, false, nil
}

func run(o options, stdout, stderr io.Writer, fetch func(org, repo string) ([]byte, error)) int {
	b, err := os.ReadFile(o.inventory)
	if err != nil {
		fmt.Fprintln(stderr, "gorequires: no inventory:", err, "-- run `fleetfind -refresh` first")
		return 2
	}
	var inv inventory
	if err := json.Unmarshal(b, &inv); err != nil || len(inv.Repos) == 0 {
		fmt.Fprintln(stderr, "gorequires: the inventory is empty or unreadable")
		return 2
	}

	type result struct {
		repo string
		hit  Hit
		has  bool
		err  error
	}
	var repos []string
	for _, r := range inv.Repos {
		if r.Archived && !o.archived {
			continue
		}
		repos = append(repos, r.Org+"/"+r.Name)
	}
	results := make([]result, len(repos))
	var wg sync.WaitGroup
	sem := make(chan struct{}, o.jobs)
	for i, full := range repos {
		wg.Add(1)
		go func(i int, full string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			org, name, _ := strings.Cut(full, "/")
			gm, err := fetch(org, name)
			if err != nil {
				results[i] = result{repo: full, err: err}
				return
			}
			h, has, err := Requires(gm, o.module)
			h.Repo = full
			results[i] = result{repo: full, hit: h, has: has, err: err}
		}(i, full)
	}
	wg.Wait()

	var hits []Hit
	var unread []string
	goMods, notGo := 0, 0
	for _, r := range results {
		switch {
		case errors.Is(r.err, errNoGoMod):
			notGo++
		case r.err != nil:
			unread = append(unread, r.repo+": "+r.err.Error())
		default:
			goMods++
			if r.has && (o.version == "" || r.hit.Version == o.version) {
				hits = append(hits, r.hit)
			}
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Repo < hits[j].Repo })
	for _, h := range hits {
		kind := "direct"
		if h.Indirect {
			kind = "indirect"
		}
		fmt.Fprintf(stdout, "%-48s %-10s %-8s %s\n", h.Repo, h.Version, kind, h.Module)
	}
	sort.Strings(unread)
	for _, u := range unread {
		fmt.Fprintln(stderr, "gorequires: unread:", u)
	}
	want := o.module
	if o.version != "" {
		want += "@" + o.version
	}
	fmt.Fprintf(stdout, "\n%d repositories (inventory of %s), %d root go.mod read, %d answered 404 (no root go.mod, or private), %d UNREAD: %d require %s\n",
		len(repos), inv.Taken.Format("2006-01-02 15:04"), goMods, notGo, len(unread), len(hits), want)
	fmt.Fprintln(stdout, "not seen: private repositories, repositories created after the inventory, nested modules, non-default branches")
	if len(unread) > 0 {
		return 2
	}
	return 0
}

var client = &http.Client{Timeout: 30 * time.Second}

// fetchGoMod reads a repository's root go.mod on its default branch, retrying
// a transient failure: one attempt is a flake detector, not a reader.
func fetchGoMod(org, repo string) ([]byte, error) {
	url := "https://raw.githubusercontent.com/" + org + "/" + repo + "/HEAD/go.mod"
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 2 * time.Second)
		}
		resp, err := client.Get(url)
		if err != nil {
			last = err
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		switch {
		case err != nil:
			last = err
		case resp.StatusCode == http.StatusOK:
			return body, nil
		case resp.StatusCode == http.StatusNotFound:
			return nil, errNoGoMod
		default:
			last = fmt.Errorf("HTTP %d", resp.StatusCode)
		}
	}
	return nil, last
}
