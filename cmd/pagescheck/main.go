// pagescheck asks whether a GitHub Pages site serves what its repository
// builds.
//
//	pagescheck -orgs go-ooxml,go-richdoc
//	pagescheck -fail-on-drift
//
// ⛔ A GREEN DEPLOY IS NOT A SERVED PAGE. go-ooxml.github.io served the
// default Jekyll theme's rendering of README.md -- 5 512 bytes of it -- while
// its deploy-pages workflow built the Hugo site, uploaded the artefact and
// reported success three times. Nothing was broken: the build was correct, the
// upload was correct, and the setting that decides which of the two is
// published lives in the API rather than in any file, so no file was wrong and
// no check looked at the URL.
//
// That is why this reads the PAGE and not the repository. A workflow can only
// say what it did; the URL says what a reader gets.
//
// ⛔ The source of the failure is `build_type`. "workflow" publishes the
// artefact a workflow uploaded; "legacy" builds the branch with Jekyll and
// throws the artefact away. A repository with a deploy workflow and a legacy
// build type is the defect, and it is invisible from the repository's files.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-fleettools/fleettools/internal/fleet"
)

// headBytes is how much of a page to read. A generator meta is in the head,
// and a landing with a data URL in it can be megabytes: a sweep that pulled
// every page whole would spend its time on bytes it does not look at.
const headBytes = 64 << 10

// fetch is the HTTP seam. A test replaces it; nothing else should.
//
// ⛔ It is NOT fleet.GH. Reading a published page is not an API call, costs
// nothing against the hourly budget Renovate shares, and must not be charged
// to it -- a sweep that spends budget on public HTML is a sweep somebody has
// to schedule around.
var fetch = func(url string) (string, error) {
	c := &http.Client{Timeout: 20 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s", resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, headBytes))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// api is the gh seam, so a test can answer without a network or a token.
var api = func(args ...string) ([]byte, error) { return fleet.GH(args...) }

// repos is the repository-listing seam.
var repos = fleet.Repos

func main() {
	fleet.WarnIfStale(os.Stderr)
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("pagescheck", flag.ContinueOnError)
	fs.SetOutput(errOut)
	only := fs.String("orgs", "", "comma-separated organisations instead of the whole fleet")
	failOnDrift := fs.Bool("fail-on-drift", false, "exit non-zero when a page is not the one its repository builds")
	workers := fs.Int("workers", 6, "concurrent organisations")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	orgs, err := organisations(*only)
	if err != nil {
		fmt.Fprintln(errOut, "pagescheck:", err)
		return 2
	}
	if *only != "" {
		fmt.Fprintln(out, "NARROWED to the organisations named: this says nothing about the others.")
	}
	fmt.Fprintf(out, "orgs: %d\n\n", len(orgs))

	found := sweep(orgs, *workers)
	return report(found, out, *failOnDrift)
}

// organisations is the list to sweep: the ones named, or every one the account
// belongs to.
func organisations(only string) ([]string, error) {
	if only != "" {
		var out []string
		for _, o := range strings.Split(only, ",") {
			if o = strings.TrimSpace(o); o != "" {
				out = append(out, o)
			}
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("-orgs was given and names nothing")
		}
		return out, nil
	}
	return fleet.Orgs()
}

// a site is one Pages deployment and what it is actually serving.
type site struct {
	Repo      string // owner/name
	URL       string
	BuildType string
	Generator string
	Source    string // the branch Pages serves, when it serves a branch
	Verdict   string
	Note      string
}

// the verdicts, in the order report prints them.
const (
	drift      = "DRIFT"
	watch      = "WATCH"
	unreadable = "UNREADABLE"
	ok         = "OK"
)

func sweep(orgs []string, workers int) []site {
	if workers < 1 {
		workers = 1
	}
	var (
		mu    sync.Mutex
		found []site
		wg    sync.WaitGroup
	)
	sem := make(chan struct{}, workers)
	for _, org := range orgs {
		wg.Add(1)
		go func(org string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			got := scan(org)
			mu.Lock()
			found = append(found, got...)
			mu.Unlock()
		}(org)
	}
	wg.Wait()
	sort.Slice(found, func(i, j int) bool { return found[i].Repo < found[j].Repo })
	return found
}

// scan is every Pages site one organisation serves.
//
// ⛔ has_pages comes WITH the repository listing, so finding the sites costs
// no call of its own. Asking each repository whether it has a page would be
// one call per repository across 294 organisations, and a sweep that expensive
// is one that starves the budget Renovate runs on.
func scan(org string) []site {
	rs, err := repos(org)
	if err != nil {
		return []site{{Repo: org + "/?", Verdict: unreadable, Note: err.Error()}}
	}
	var out []site
	for _, r := range rs {
		if !r.HasPages {
			continue
		}
		out = append(out, look(r.FullName, r.DefaultBranch))
	}
	return out
}

// pagesInfo is the part of the Pages configuration that decides what is
// published.
//
// ⛔ The SOURCE BRANCH matters as much as the build type. A branch source is
// not automatically a defect: mkdocs and mike push a built site to gh-pages
// and let GitHub serve it, which is a published artefact under another name.
// What is a defect is serving the branch the source lives on -- there the
// published site is GitHub's rendering of the source, and whatever a workflow
// built is discarded.
type pagesInfo struct {
	BuildType string `json:"build_type"`
	HTMLURL   string `json:"html_url"`
	Source    struct {
		Branch string `json:"branch"`
		Path   string `json:"path"`
	} `json:"source"`
}

// look is one repository's verdict. defaultBranch is the branch the source
// lives on, which the repository listing already gave us.
func look(full, defaultBranch string) site {
	s := site{Repo: full}
	b, err := api("api", "repos/"+full+"/pages")
	if err != nil {
		// has_pages said yes and the configuration is unreadable: that is a
		// permission or a race, not a verdict about the page.
		s.Verdict = unreadable
		s.Note = "the Pages configuration is unreadable: " + firstLine(err.Error())
		return s
	}
	var info pagesInfo
	if err := json.Unmarshal(b, &info); err != nil {
		s.Verdict = unreadable
		s.Note = "the Pages configuration does not parse: " + err.Error()
		return s
	}
	s.BuildType, s.URL, s.Source = info.BuildType, info.HTMLURL, info.Source.Branch
	if s.URL == "" {
		s.Verdict = unreadable
		s.Note = "Pages is enabled and names no URL"
		return s
	}
	body, err := fetch(s.URL)
	if err != nil {
		s.Verdict = unreadable
		s.Note = "the page is not readable: " + firstLine(err.Error())
		return s
	}
	// ⛔ A documentation site's root is often a REDIRECT and not the page.
	// mike writes a stub at / that refreshes to /latest/, and that stub has no
	// generator meta at all -- so a check reading the root alone reported "no
	// generator" for a site MkDocs built, which is crying wolf at the whole
	// fleet's documentation.
	if to := refreshTarget(body, s.URL); to != "" {
		if next, err := fetch(to); err == nil {
			s.URL, body = to, next
		}
	}
	s.Generator = generatorOf(body)
	s.Verdict, s.Note = judge(full, s.BuildType, s.Source, defaultBranch, s.Generator)
	return s
}

// refreshTarget is where a <meta http-equiv=refresh> sends a reader, resolved
// against the page's own URL, or "" when the page is not a redirect.
func refreshTarget(page, from string) string {
	rest := page
	for {
		i := strings.Index(strings.ToLower(rest), "<meta")
		if i < 0 {
			return ""
		}
		rest = rest[i+len("<meta"):]
		end := strings.IndexByte(rest, '>')
		if end < 0 {
			return ""
		}
		tag := rest[:end]
		rest = rest[end+1:]
		a := attrs(tag)
		if !strings.EqualFold(a["http-equiv"], "refresh") {
			continue
		}
		// content is "0; url=./latest/" -- the delay, then the target.
		_, after, found := strings.Cut(a["content"], "=")
		if !found {
			return ""
		}
		target := strings.Trim(strings.TrimSpace(after), `"'`)
		base, err := url.Parse(from)
		if err != nil {
			return ""
		}
		ref, err := url.Parse(target)
		if err != nil {
			return ""
		}
		return base.ResolveReference(ref).String()
	}
}

// judge is the whole rule, in one place so that the reasons are readable
// together.
//
// ⛔ Jekyll is the signal and not the crime. GitHub builds a branch with
// Jekyll by default, so a page whose generator is Jekyll is either a Jekyll
// site somebody wrote -- which has a _config.yml -- or GitHub's own rendering
// of a README that nobody chose. The _config.yml is what tells them apart, and
// it is read ONLY in that case, so the common path costs no extra call.
func judge(full, buildType, sourceBranch, defaultBranch, generator string) (verdict, note string) {
	jekyll := strings.Contains(strings.ToLower(generator), "jekyll")
	built := generator
	if built == "" {
		built = "no generator meta"
	}
	switch {
	case jekyll && !hasJekyllConfig(full):
		return drift, "serving GitHub's own Jekyll build of README.md, not what this repository builds" +
			" (build_type=" + buildType + "; it must be \"workflow\")"
	case jekyll:
		return ok, "a Jekyll site, deliberately: it has a _config.yml"
	case buildType == "workflow":
		return ok, "published from the workflow (" + built + ")"
	// ⛔ A branch source is a defect only when the branch is the one the
	// SOURCE lives on: there what is served is GitHub's rendering of the
	// repository, and whatever a workflow built is thrown away. A build pushed
	// to another branch -- gh-pages, as mkdocs and mike do -- is an artefact
	// under another name, and calling it drift would fail most of the fleet's
	// documentation and teach everybody to ignore this tool.
	case sourceBranch != "" && sourceBranch == defaultBranch:
		return watch, "served from " + sourceBranch + ", the branch the source lives on" +
			" (build_type=" + buildType + "): if a workflow builds this site, its output is discarded (" + built + ")"
	case sourceBranch != "":
		return ok, "served from the " + sourceBranch + " branch, where a workflow pushes the built site (" + built + ")"
	}
	return watch, "build_type=" + buildType + " and no source branch, which this check cannot interpret (" + built + ")"
}

// hasJekyllConfig reports whether the repository holds a _config.yml, which is
// what a deliberate Jekyll site has and GitHub's default rendering does not.
func hasJekyllConfig(full string) bool {
	_, err := api("api", "repos/"+full+"/contents/_config.yml")
	return err == nil
}

// generatorOf is the content of the generator meta, or "".
//
// ⛔ It must read MINIFIED html. `hugo --minify` drops the quotes around an
// attribute name, so the served page says `name=generator content="Hugo
// 0.167.0"` while the source says `name="generator"`. A matcher written from
// the source finds nothing on every minified page in the fleet, which is all
// of them -- and a check that can only fail is not a check.
func generatorOf(page string) string {
	rest := page
	for {
		i := strings.Index(strings.ToLower(rest), "<meta")
		if i < 0 {
			return ""
		}
		rest = rest[i+len("<meta"):]
		end := strings.IndexByte(rest, '>')
		if end < 0 {
			return ""
		}
		tag := rest[:end]
		rest = rest[end+1:]
		a := attrs(tag)
		if strings.EqualFold(a["name"], "generator") {
			return strings.TrimSpace(a["content"])
		}
	}
}

// attrs reads a tag's attributes, quoted or bare.
func attrs(tag string) map[string]string {
	out := map[string]string{}
	i := 0
	for i < len(tag) {
		for i < len(tag) && (tag[i] == ' ' || tag[i] == '\t' || tag[i] == '\n' || tag[i] == '\r' || tag[i] == '/') {
			i++
		}
		start := i
		for i < len(tag) && tag[i] != '=' && tag[i] != ' ' && tag[i] != '\t' && tag[i] != '\n' && tag[i] != '\r' {
			i++
		}
		name := strings.ToLower(tag[start:i])
		if name == "" {
			// Nothing left but separators: the tag is read.
			return out
		}
		if i >= len(tag) || tag[i] != '=' {
			// A valueless attribute, such as `async`.
			out[name] = ""
			continue
		}
		i++ // the '='
		if i < len(tag) && (tag[i] == '"' || tag[i] == '\'') {
			q := tag[i]
			i++
			start = i
			for i < len(tag) && tag[i] != q {
				i++
			}
			out[name] = tag[start:i]
			if i < len(tag) {
				i++ // the closing quote
			}
			continue
		}
		start = i
		for i < len(tag) && tag[i] != ' ' && tag[i] != '\t' && tag[i] != '\n' && tag[i] != '\r' {
			i++
		}
		out[name] = tag[start:i]
	}
	return out
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

// report prints what was found, worst first, and decides the exit status.
func report(found []site, w io.Writer, failOnDrift bool) int {
	byVerdict := map[string][]site{}
	for _, s := range found {
		byVerdict[s.Verdict] = append(byVerdict[s.Verdict], s)
	}
	for _, v := range []string{drift, watch, unreadable, ok} {
		for _, s := range byVerdict[v] {
			fmt.Fprintf(w, "%-11s %s\n", v, s.Repo)
			fmt.Fprintf(w, "            %s\n", s.Note)
			if s.URL != "" {
				fmt.Fprintf(w, "            %s\n", s.URL)
			}
		}
	}
	fmt.Fprintf(w, "\n%d page(s): %d serving something else, %d to watch, %d unreadable, %d as built\n",
		len(found), len(byVerdict[drift]), len(byVerdict[watch]),
		len(byVerdict[unreadable]), len(byVerdict[ok]))
	if failOnDrift && len(byVerdict[drift]) > 0 {
		return 1
	}
	return 0
}
