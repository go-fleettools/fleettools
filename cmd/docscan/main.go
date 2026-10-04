package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/go-fleettools/fleettools/internal/fleet"
)

// gh runs the CLI, retrying only what waiting can fix. Same brake as the other
// scanners: the SECONDARY limit lifts in seconds, the PRIMARY one is an hourly
// budget no short backoff reaches.

// file is one path's contents, or "" when it is not there.
//
// ⛔ A read that fails and a file that is absent are NOT the same, and this
// deliberately flattens them — then counts the surfaces it actually read, so a
// whole organisation whose landing could not be fetched is reported as
// unreadable rather than as one advertising nothing.
func file(repo, path string) string {
	if s, ok := treeFile(repo, path); ok {
		return s
	}
	b, err := fleet.GH("api", "repos/"+repo+"/contents/"+path, "--jq", ".content")
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
	if names, ok := treeDir(repo, path); ok {
		return names
	}
	b, err := fleet.GH("api", "repos/"+repo+"/contents/"+path, "--jq", ".[].name")
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
	Disk     int    `json:"diskUsage"`
}

// finding is one organisation's answer.
type finding struct {
	org       string
	unlisted  map[string][]string // repo -> the surfaces that DO name it
	stale     map[string][]string // surface -> names it lists that are gone
	archived  map[string]bool     // of those, the ones that are archived, not gone
	empty     []string            // repositories with nothing in them
	quiet     map[string]string   // repo -> why it is deliberately not advertised
	badAllow  string              // the .docs-unlisted file is malformed
	modules   int
	surfaces  []string // the surfaces that were readable
	silent    bool     // no surface at all: this org advertises nothing
	readError string
}

func main() {
	fleet.WarnIfStale(os.Stderr)
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("docscan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	only := fs.String("orgs", "", "comma-separated organisations instead of the whole fleet")
	workers := fs.Int("workers", 6, "concurrent organisations")
	// ⛔ For the per-organisation check, which runs IN the organisation and
	// must stop a merge. The fleet-wide report deliberately does not fail:
	// being unlisted is sometimes a choice, and a weekly red job teaches
	// people to ignore it.
	strict := fs.Bool("fail-on-drift", false, "exit non-zero when anything is unlisted or stale")
	fs.Var(treeFlag{}, "tree", "owner/repo=dir: read this repository's surfaces from a\ncheckout instead of the API, so a pull request is judged on\nwhat it proposes rather than on its default branch")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	// ⛔ A result is only as good as what was read, so SAY what was read. A
	// green run whose tree flag was ignored looks exactly like a green run that
	// read the branch, and the difference is the whole point of the flag.
	for _, repo := range slices.Sorted(maps.Keys(trees)) {
		fmt.Fprintf(stdout, "tree: %s read from %s, not from the API\n", repo, trees[repo])
		if err := readUnlisted(orgOf(repo), trees[repo]); err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
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
	code := report(found, stdout)
	if *strict && code == 0 {
		for _, f := range found {
			if f.drifted() {
				return 1
			}
		}
	}
	return code
}

// classify sorts an organisation's repositories into what the report needs:
// the names it OWNS, the ones that are MODULES, the empty ones, and the
// archived ones.
//
// ⛔ It is a function of its own so that it can be tested. It used to be the
// top of scan(), which reaches GitHub, so nothing exercised it — and when the
// archived/gone distinction was added, deleting the two lines that fill
// f.archived left the whole suite green. A control nothing can reach is a
// control that will be deleted by accident.
func classify(org string, all []ghRepo, f *finding) (have map[string]bool, modules []string) {
	have = map[string]bool{}
	for _, r := range all {
		// ⛔ An archived repository is not unlisted-by-mistake, it is finished.
		// Reporting it would bury the real answers under every tombstone the
		// fleet keeps.
		if r.Archived || r.Fork {
			// ⛔ ARCHIVED IS NOT GONE, and the difference decides what a
			// reader does about it. Told a surface names something "the org
			// no longer has", they delete the row; told it is archived, they
			// mark it — which is the honest answer, because the repository is
			// still there and a link to it still resolves.
			if r.Archived {
				f.archived[r.Name] = true
			}
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
		// ⛔ An EMPTY repository is not a module nobody advertised; it is a
		// repository with nothing in it, and "add it to the landing" is the
		// wrong remedy -- a row pointing at nothing is worse than no row.
		// Five of the fleet's sixty unadvertised names were empty, and one of
		// them, openweft/weft-loom-pub, is what made me look: it has no
		// README, no language and no commits since June.
		if r.Disk == 0 {
			f.empty = append(f.empty, r.Name)
			continue
		}
		modules = append(modules, r.Name)
	}
	return have, modules
}

// drifted is the ONE definition of drift, for the headline count and for the
// exit status alike.
//
// ⛔ They used to be two expressions, and they disagreed twice.
//
// An EMPTY repository counted in the headline and not in the gate, so the
// report ended "4 organisations with drift" naming four whose only finding was
// a line that says, in the same report, "empty, so not a module to advertise".
// The count contradicted the remedy printed above it.
//
// A MALFORMED .docs-unlisted counted in the headline and not in the gate, which
// is the dangerous way round: the file that grants exemptions could be
// unparseable and the check would still pass. I wrote that one while adding the
// file, in the same change where I said an allowance must never be silent.
// silentOrg reports whether an organisation advertises nothing while having
// something to advertise.
//
// Split out of scan so a test can reach it: scan talks to GitHub, and the
// three lines that decide this would otherwise be covered by nothing — the
// same shape as the two lines filling f.archived, which once went missing with
// the whole suite still green.
func silentOrg(surfaces []string, modules int) bool {
	return len(surfaces) == 0 && modules > 0
}

func (f finding) drifted() bool {
	return len(f.unlisted) > 0 || len(f.stale) > 0 || f.badAllow != ""
}

// scan asks one organisation what it has and what it says.
func scan(org string) finding {
	f := finding{org: org, unlisted: map[string][]string{}, stale: map[string][]string{}, quiet: map[string]string{}, archived: map[string]bool{}}

	b, err := fleet.GH("repo", "list", org, "--limit", "200", "--json", "name,isArchived,isFork,diskUsage")
	if err != nil {
		f.readError = err.Error()
		return f
	}
	var all []ghRepo
	if err := json.Unmarshal(b, &all); err != nil {
		f.readError = err.Error()
		return f
	}
	have, modules := classify(org, all, &f)
	f.modules = len(modules)
	if len(modules) == 0 {
		return f
	}

	sort.Strings(modules)
	site := org + "/" + org + ".github.io"
	// ⛔ NOT a write to the shared map: scan runs one goroutine per
	// organisation, and the tree-read allowances were parsed before any of
	// them started. Everything after this point uses the local copy.
	allow := unlisted[org]
	if len(allow) == 0 {
		parsed, err := parseUnlisted(file(site, UnlistedFile))
		if err != nil {
			// A malformed allowance counts as drift. It must not read as no
			// allowance asked for, which is the silent direction.
			f.badAllow = err.Error()
		}
		allow = parsed
	}
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
		// ⛔ An INDEX names most of what the organisation has. A surface that
		// names a handful is a product page, and asking why it omits the rest
		// is asking the wrong question of it.
		//
		// The first threshold here was "at least one", and it was too weak by
		// a wide margin: openweft's landing is a page about weft that happens
		// to name nine of seventy modules, so the report carried 44 lines of
		// "only on profile" -- true, expected, and enough to bury the 17
		// modules that really were named nowhere. go-widgets' landing names
		// one of nineteen.
		//
		// Half is not a tuned constant. It is the point where a listing stops
		// being a sample and starts being a list.
		if n*2 < len(modules) {
			continue
		}
		kept = append(kept, s)
		f.surfaces = append(f.surfaces, s.Name)
	}
	surfaces = kept
	if len(f.surfaces) == 0 {
		// Not drift: with no surface there is nothing to compare the
		// organisation against, and the comparison is what this tool does.
		//
		// ⛔ BUT IT IS NOT NOTHING EITHER, and reporting it as nothing is how
		// 24 organisations came to sit under a headline reading "0
		// organisations with drift" — among them openstack-terraform-modules
		// with eight repositories and go-sicp with seven, advertised nowhere
		// at all. An organisation that says nothing was indistinguishable
		// from one that says everything correctly.
		//
		// So it is counted and printed, separately, and it does NOT move the
		// drift count or the exit status: there is no defect here to fix in a
		// pull request, only a page nobody has written.
		f.silent = silentOrg(f.surfaces, f.modules)
		return f
	}

	for _, m := range modules {
		// ⛔ A deliberate omission is still PRINTED, with its reason. The point
		// of the file is to move a repository from invisible to accounted for,
		// not to remove it from the report — a silent allowance would be the
		// check switched off one line at a time.
		if why, ok := allow[m]; ok {
			f.quiet[m] = why
			continue
		}
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
		if len(f.unlisted) == 0 && len(f.stale) == 0 && len(f.empty) == 0 && len(f.quiet) == 0 && f.badAllow == "" {
			continue
		}
		// An organisation whose only news is a deliberate omission has no
		// drift, and still gets its paragraph so the omission stays visible.
		if f.drifted() {
			drift++
		}
		fmt.Fprintf(w, "\n%s — %d modules, surfaces: %s\n", f.org, f.modules,
			strings.Join(f.surfaces, " "))
		if f.badAllow != "" {
			fmt.Fprintf(w, "  %s\n", f.badAllow)
		}
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
		for _, m := range slices.Sorted(maps.Keys(f.quiet)) {
			fmt.Fprintf(w, "  %-22s unlisted on purpose: %s\n", m, f.quiet[m])
		}
		if len(f.empty) > 0 {
			sort.Strings(f.empty)
			fmt.Fprintf(w, "  empty, so not a module to advertise: %s\n", strings.Join(f.empty, " "))
		}
		for s, gone := range f.stale {
			var missing, frozen []string
			for _, n := range gone {
				if f.archived[n] {
					frozen = append(frozen, n)
				} else {
					missing = append(missing, n)
				}
			}
			if len(missing) > 0 {
				fmt.Fprintf(w, "  %s still lists, and the org no longer has: %s\n", s, strings.Join(missing, " "))
			}
			if len(frozen) > 0 {
				fmt.Fprintf(w, "  %s lists as current, and the org has ARCHIVED: %s\n", s, strings.Join(frozen, " "))
			}
		}
	}
	var silent []string
	for _, f := range found {
		if f.silent {
			silent = append(silent, fmt.Sprintf("%s (%d)", f.org, f.modules))
		}
	}
	if len(silent) > 0 {
		sort.Strings(silent)
		fmt.Fprintf(w, "\n%d organisation(s) advertise NOTHING: no profile README, no landing page.\n", len(silent))
		fmt.Fprintf(w, "Not drift — there is no surface to compare — but not nothing either. The\n")
		fmt.Fprintf(w, "number in brackets is how many repositories are invisible as a result.\n")
		for _, o := range silent {
			fmt.Fprintf(w, "      %s\n", o)
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
	b, err := fleet.GH("api", "user/orgs", "--paginate", "--jq", ".[].login")
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
