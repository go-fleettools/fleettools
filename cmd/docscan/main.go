package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

// gh runs the CLI, retrying only what waiting can fix. Same brake as the other
// scanners: the SECONDARY limit lifts in seconds, the PRIMARY one is an hourly
// budget no short backoff reaches.
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
		if attempt < 5 && retryable(msg) {
			time.Sleep(backoff(attempt))
			continue
		}
		return nil, fmt.Errorf("%s", strings.TrimSpace(msg))
	}
}

func retryable(msg string) bool {
	if strings.Contains(msg, "API rate limit exceeded") {
		return false
	}
	return strings.Contains(msg, "secondary rate") ||
		strings.Contains(msg, "abuse") ||
		strings.Contains(msg, "too quickly")
}

var backoff = func(attempt int) time.Duration {
	return time.Duration(20*(attempt+1)) * time.Second
}

// file is one path's contents, or "" when it is not there.
//
// ⛔ A read that fails and a file that is absent are NOT the same, and this
// deliberately flattens them — then counts the surfaces it actually read, so a
// whole organisation whose landing could not be fetched is reported as
// unreadable rather than as one advertising nothing.
func file(repo, path string) string {
	b, err := gh("api", "repos/"+repo+"/contents/"+path, "--jq", ".content")
	if err != nil {
		return ""
	}
	dec, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(strings.TrimSpace(string(b)), "\n", ""))
	if err != nil {
		return ""
	}
	return string(dec)
}

// dir is the names in a directory, or nothing.
func dir(repo, path string) []string {
	b, err := gh("api", "repos/"+repo+"/contents/"+path, "--jq", ".[].name")
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

type ghRepo struct {
	Name     string `json:"name"`
	Archived bool   `json:"isArchived"`
	Fork     bool   `json:"isFork"`
}

// finding is one organisation's answer.
type finding struct {
	org       string
	unlisted  map[string][]string // repo -> the surfaces that DO name it
	stale     map[string][]string // surface -> names it lists that are gone
	modules   int
	surfaces  []string // the surfaces that were readable
	readError string
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("docscan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	only := fs.String("orgs", "", "comma-separated organisations instead of the whole fleet")
	workers := fs.Int("workers", 6, "concurrent organisations")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	orgs, err := organisations(*only)
	if err != nil {
		fmt.Fprintln(stderr, "orgs:", err)
		return 1
	}
	if *only != "" {
		fmt.Fprintln(stdout, "NARROWED to the organisations named: this says nothing about the others.")
	}
	fmt.Fprintf(stdout, "orgs: %d\n", len(orgs))

	var mu sync.Mutex
	var found []finding
	sem := make(chan struct{}, *workers)
	var wg sync.WaitGroup
	for _, o := range orgs {
		wg.Add(1)
		go func(org string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			f := scan(org)
			mu.Lock()
			found = append(found, f)
			mu.Unlock()
		}(o)
	}
	wg.Wait()
	sort.Slice(found, func(i, j int) bool { return found[i].org < found[j].org })
	return report(found, stdout)
}

// scan asks one organisation what it has and what it says.
func scan(org string) finding {
	f := finding{org: org, unlisted: map[string][]string{}, stale: map[string][]string{}}

	b, err := gh("repo", "list", org, "--limit", "200", "--json", "name,isArchived,isFork")
	if err != nil {
		f.readError = err.Error()
		return f
	}
	var all []ghRepo
	if err := json.Unmarshal(b, &all); err != nil {
		f.readError = err.Error()
		return f
	}
	have := map[string]bool{}
	var modules []string
	for _, r := range all {
		// ⛔ An archived repository is not unlisted-by-mistake, it is finished.
		// Reporting it would bury the real answers under every tombstone the
		// fleet keeps.
		if r.Archived || r.Fork {
			continue
		}
		// ⛔ have is "the organisation owns this name", which is a different
		// question from "this is a module". An infrastructure repository is
		// owned, so a profile linking to docs/ is not pointing at a tombstone;
		// it is simply not a module and does not belong in the unlisted count.
		have[r.Name] = true
		if infra(org, r.Name) {
			continue
		}
		modules = append(modules, r.Name)
	}
	f.modules = len(modules)
	if len(modules) == 0 {
		return f
	}

	sort.Strings(modules)
	site := org + "/" + org + ".github.io"
	var landing strings.Builder
	landing.WriteString(file(site, "hugo.toml"))
	for _, d := range dir(site, "data") {
		landing.WriteString("\n")
		landing.WriteString(file(site, "data/"+d))
	}
	surfaces := []Surface{
		{Name: "landing", Org: org, Body: landing.String(), Entry: landingEntry},
		{Name: "docs", Org: org, Body: file(org+"/docs", "mkdocs.yml"), Entry: docsEntry},
		{Name: "profile", Org: org, Body: file(org+"/.github", "profile/README.md"),
			Entry: profileEntryFor(org)},
	}
	// ⛔ A surface counts only if it is a module index AT ALL. Several
	// organisations document a single program rather than a module per page,
	// so their docs name none of the modules -- and asking "why is every
	// module missing from the docs" of such an organisation produces one noisy
	// line per module and hides the organisations that really did drift.
	//
	// So each surface is asked how many modules it names, and one that names
	// none is not treated as a listing that lost them.
	var kept []Surface
	for _, s := range surfaces {
		if s.Body == "" {
			continue
		}
		n := 0
		for _, m := range modules {
			if s.Entry(s.Body, m) {
				n++
			}
		}
		if n == 0 {
			continue
		}
		kept = append(kept, s)
		f.surfaces = append(f.surfaces, s.Name)
	}
	surfaces = kept
	if len(f.surfaces) == 0 {
		return f // nothing advertises here at all; not drift, just absence
	}

	for _, m := range modules {
		on := Advertised(surfaces, m)
		if len(on) < len(f.surfaces) {
			f.unlisted[m] = on
		}
	}
	for _, s := range surfaces {
		if s.Body == "" {
			continue
		}
		if gone := Stale(s, have); len(gone) > 0 {
			sort.Strings(gone)
			f.stale[s.Name] = gone
		}
	}
	return f
}

func report(found []finding, w io.Writer) int {
	var drift, unread int
	for _, f := range found {
		if f.readError != "" {
			unread++
			fmt.Fprintf(w, "%-28s UNREAD: %s\n", f.org, f.readError)
			continue
		}
		if len(f.unlisted) == 0 && len(f.stale) == 0 {
			continue
		}
		drift++
		fmt.Fprintf(w, "\n%s — %d modules, surfaces: %s\n", f.org, f.modules,
			strings.Join(f.surfaces, " "))
		var names []string
		for m := range f.unlisted {
			names = append(names, m)
		}
		sort.Strings(names)
		for _, m := range names {
			on := f.unlisted[m]
			where := "NOWHERE"
			if len(on) > 0 {
				where = "only on " + strings.Join(on, ", ")
			}
			fmt.Fprintf(w, "  %-22s %s\n", m, where)
		}
		for s, gone := range f.stale {
			fmt.Fprintf(w, "  %s still lists, and the org no longer has: %s\n", s, strings.Join(gone, " "))
		}
	}
	fmt.Fprintf(w, "\n%d organisations with drift, %d unreadable\n", drift, unread)
	if unread > 0 {
		// ⛔ An unreadable organisation is not a clean one. Saying so in the
		// exit status keeps a partial sweep from reading as a full pass.
		return 1
	}
	return 0
}

func organisations(only string) ([]string, error) {
	if only != "" {
		var out []string
		for _, o := range strings.Split(only, ",") {
			if o = strings.TrimSpace(o); o != "" {
				out = append(out, o)
			}
		}
		return out, nil
	}
	b, err := gh("api", "user/orgs", "--paginate", "--jq", ".[].login")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	sort.Strings(out)
	return out, nil
}
