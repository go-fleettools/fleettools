package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// goCmd runs the go command in dir (replaced in tests) and returns its
// STDOUT alone: `go run` writes "go: … switching to go1.27.3" on stderr,
// and wave 2 found that line pasted into five hugo.toml files as if it
// were part of the brand block, which broke their builds.
var goCmd = func(dir string, args ...string) ([]byte, error) {
	c := exec.Command("go", args...)
	c.Dir = dir
	c.Env = append(os.Environ(), "GOWORK=off")
	var stderr strings.Builder
	c.Stderr = &stderr
	out, err := c.Output()
	if err != nil {
		return out, fmt.Errorf("go %s: %v\n%s%s", strings.Join(args, " "), err, out, stderr.String())
	}
	return out, nil
}

// listRepos is the organisation's public, non-archived source repositories
// (replaced in tests), less its own .github, brand, docs and landing.
var listRepos = func(org string) ([]string, error) {
	c := exec.Command("gh", "repo", "list", org, "--limit", "100", "--no-archived", "--source", "--visibility", "public", "--json", "name", "--jq", ".[].name")
	out, err := c.Output()
	if err != nil {
		return nil, err
	}
	skip := map[string]bool{".github": true, "brand": true, "docs": true, org + ".github.io": true}
	var names []string
	for _, n := range strings.Fields(string(out)) {
		if !skip[n] {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names, nil
}

// report is what a migration did, for its pull request and for verify.
type report struct {
	Org       string     `json:"org"`
	Kit       string     `json:"kit"`
	Shape     string     `json:"shape"`
	Fragments []fragment `json:"fragments"`
	Removed   []string   `json:"removed"`
	Written   []string   `json:"written"`
	Workflows []string   `json:"workflows"`
	Data      []string   `json:"data,omitempty"`
}

// copiedPartials are the kit's own partials, which a landing must not keep a
// copy of: a file of the same name overrides the module's, silently.
var copiedPartials = []string{"styles.html", "theme-toggle.html", "fork-ribbon.html", "icons/book.html", "icons/github.html", "icons/mark-github.html", "hero-carousel.html", "loading-carousel.html", "mantra-on.html"}

// migrate moves the landing checked out at dir onto the kit at version kit.
func migrate(dir, org, kit string, data []string) (report, error) {
	r := report{Org: org, Kit: kit, Data: data}
	read := func(p string) (string, error) {
		b, err := os.ReadFile(filepath.Join(dir, p))
		return string(b), err
	}
	write := func(p, s string) error {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755); err != nil {
			return err
		}
		r.Written = append(r.Written, p)
		return os.WriteFile(filepath.Join(dir, p), []byte(s), 0o644)
	}
	if gm, err := read("go.mod"); err == nil && strings.Contains(gm, "github.com/go-fleettools/landingkit") {
		return r, fmt.Errorf("%s already uses the kit: bump its go.mod instead", org)
	}
	src, err := read("layouts/index.html")
	if err != nil {
		return r, err
	}
	// A landing that copied the kit's own partials already has the kit's
	// layout: only the copies go, for the module (and its repeats hide).
	_, statErr := os.Stat(filepath.Join(dir, "layouts/partials/styles.html"))
	kitCopy := statErr == nil
	var l landing
	if kitCopy {
		r.Shape = "kit-copy"
	} else {
		if l, err = parseTemplate(src); err != nil {
			return r, fmt.Errorf("not migrated, a person should look: %v", err)
		}
		r.Shape = "template"
	}
	// every refusal before the first write: a checkout is changed whole or
	// not at all
	wfPatches, err := workflowPatches(dir)
	if err != nil {
		return r, err
	}
	// the logo: the header's, else the conventional one, else the favicon
	// (some template landings show no logo in the header at all)
	if l.Logo == "" && !kitCopy {
		for _, c := range []string{"/img/logo.svg", "/favicon.svg"} {
			if _, err := os.Stat(filepath.Join(dir, "static", c)); err == nil {
				l.Logo = c
				break
			}
		}
	}
	if l.Logo == "" && kitCopy {
		l.Logo = "/img/logo.svg" // the kit-copy shape reads its brand from there
	}
	if l.Logo == "" {
		return r, fmt.Errorf("no logo: none in the header, no static/img/logo.svg, no static/favicon.svg")
	}
	conf, err := read("hugo.toml")
	if err != nil {
		return r, err
	}
	conf = addModule(conf)
	if !strings.Contains(conf, "[params.brand]") {
		logo := "static" + l.Logo
		brand, err := goCmd(dir, "run", "github.com/go-fleettools/landingkit/cmd/landingkit@"+kit, "brand", logo)
		if err != nil {
			return r, fmt.Errorf("the brand could not be read from %s: %v", logo, err)
		}
		conf = strings.TrimRight(conf, "\n") + "\n\n" + strings.TrimRight(string(brand), "\n") + "\n"
	}
	if kitCopy {
		index, frags := repeats(src)
		r.Fragments = frags
		if index != src {
			if err := write("layouts/index.html", index); err != nil {
				return r, err
			}
		}
	} else {
		// the repositories the page links to, for a terminal when there is
		// no snippet and no [[params.repos]] to range over; when it links to
		// none, the organisation's own list (go-deltasync's terminal said
		// only `gh repo list`)
		l.Repos = linkedRepos(src, org)
		if len(l.Repos) == 0 {
			l.Repos, _ = listRepos(org)
		}
		index, terminal := render(l, org)
		index, r.Fragments = repeats(index)
		if err := write("layouts/index.html", index); err != nil {
			return r, err
		}
		if err := write("layouts/partials/hero-terminal.html", terminal); err != nil {
			return r, err
		}
	}
	for _, p := range copiedPartials {
		full := filepath.Join(dir, "layouts/partials", p)
		if _, err := os.Stat(full); err == nil {
			if err := os.Remove(full); err != nil {
				return r, err
			}
			r.Removed = append(r.Removed, "layouts/partials/"+p)
		}
	}
	// the Octicons licence goes with the last Octicon
	if icons, _ := filepath.Glob(filepath.Join(dir, "layouts/partials/icons/*.html")); len(icons) == 0 {
		lic := filepath.Join(dir, "layouts/partials/icons/OCTICONS-LICENSE")
		if _, err := os.Stat(lic); err == nil {
			if err := os.Remove(lic); err != nil {
				return r, err
			}
			r.Removed = append(r.Removed, "layouts/partials/icons/OCTICONS-LICENSE")
		}
	}

	// the module
	// the repository's name, not the checkout's: a worktree is named freely
	module := "github.com/" + org + "/" + org + ".github.io"
	if _, err := read("go.mod"); err == nil {
		// a landing with a module already (its playground's, say): add the
		// kit to it, never write over it
		if _, err := goCmd(dir, "get", "github.com/go-fleettools/landingkit@"+kit); err != nil {
			return r, err
		}
		r.Written = append(r.Written, "go.mod")
	} else if err := write("go.mod", "module "+module+"\n\ngo 1.27.3\n\nrequire github.com/go-fleettools/landingkit "+kit+"\n"); err != nil {
		return r, err
	}
	if err := write("kit.go", kitGo); err != nil {
		return r, err
	}
	if _, err := goCmd(dir, "mod", "tidy"); err != nil {
		return r, err
	}
	r.Written = append(r.Written, "go.sum")

	// hugo.toml (read and given its import and brand above): the data chips
	if len(data) > 0 {
		items := []string{`"LICENSE=BSD-3-Clause"`, `"CGO_ENABLED=0"`}
		for _, d := range data {
			items = append(items, fmt.Sprintf("%q", d))
		}
		conf = strings.TrimRight(conf, "\n") + "\n\n# The strip under the top bar: the code's licence and cgo, and the data\n# shipped under its own licence (read from its licence files).\n[params.mantra]\n  items = [" + strings.Join(items, ", ") + "]\n"
	}
	if err := write("hugo.toml", conf); err != nil {
		return r, err
	}

	for _, p := range wfPatches {
		if err := write(p[0], p[1]); err != nil {
			return r, err
		}
		r.Workflows = append(r.Workflows, p[0])
	}

	if rd, err := read("README.md"); err == nil && !strings.Contains(rd, "landingkit") {
		if err := write("README.md", strings.TrimRight(rd, "\n")+"\n\n"+readmeNote); err != nil {
			return r, err
		}
	}
	return r, nil
}

// workflowPatches are the workflows that install hugo, given the go command
// they now need to fetch the module, as (path, content) pairs.
func workflowPatches(dir string) ([][2]string, error) {
	wfs, _ := filepath.Glob(filepath.Join(dir, ".github/workflows/*.yml"))
	var out [][2]string
	runsHugo := false
	for _, wf := range wfs {
		b, err := os.ReadFile(wf)
		if err != nil {
			return nil, err
		}
		s := string(b)
		// a workflow that installs hugo another way is not touched: the
		// pull request's build then fails, loudly, which is the signal
		if !installsHugo(s) {
			continue
		}
		runsHugo = true
		patched, ok := addSetupGo(s)
		if !ok {
			return nil, fmt.Errorf("%s: no step found around the hugo install: patch it by hand", filepath.Base(wf))
		}
		if patched != s {
			rel, _ := filepath.Rel(dir, wf)
			out = append(out, [2]string{rel, patched})
		}
	}
	if !runsHugo {
		return nil, fmt.Errorf("no workflow installs hugo (peaceiris/actions-hugo or a release .deb): how is this site built?")
	}
	return out, nil
}

const kitGo = `//go:build tools

// This file never builds. A Hugo site has no Go code, so ` + "`go mod tidy`" + ` --
// which Renovate runs after every bump -- would find nothing that uses
// github.com/go-fleettools/landingkit and delete its requirement from go.mod.
// Importing it here keeps it. See the landingkit README.
package site

import _ "github.com/go-fleettools/landingkit"
`

const readmeNote = `## The design is not here

It comes from the shared Hugo module
[` + "`go-fleettools/landingkit`" + `](https://github.com/go-fleettools/landingkit),
pinned in ` + "`go.mod`" + ` (Renovate bumps it); ` + "`kit.go`" + ` keeps ` + "`go mod tidy`" + ` from
deleting that requirement. ` + "`[params.brand]`" + ` in ` + "`hugo.toml`" + ` is read from the logo by
` + "`landingkit brand`" + `. A file under ` + "`layouts/partials/`" + ` with the name of one of the
kit's would silently override it.
`

var tableRe = regexp.MustCompile(`(?m)^\[`)

// addModule puts the kit's import before the first table of a hugo.toml.
func addModule(conf string) string {
	if strings.Contains(conf, "github.com/go-fleettools/landingkit") {
		return conf
	}
	block := "# The design comes from the landing kit, a Hugo module pinned in go.mod.\n[module]\n  [[module.imports]]\n    path = \"github.com/go-fleettools/landingkit\"\n\n"
	loc := tableRe.FindStringIndex(conf)
	if loc == nil {
		return strings.TrimRight(conf, "\n") + "\n\n" + block
	}
	return conf[:loc[0]] + block + conf[loc[0]:]
}

// installsHugo: the two ways the fleet's workflows install hugo, the
// peaceiris action and the official template's release .deb.
func installsHugo(s string) bool {
	return strings.Contains(s, "peaceiris/actions-hugo") || strings.Contains(s, "gohugoio/hugo/releases/download")
}

var rootGoVersion = regexp.MustCompile(`(?m)go-version-file:\s*["']?(\./)?go\.mod["']?\s*$`)

// addSetupGo inserts actions/setup-go right after the checkout step of a
// workflow that installs hugo.
func addSetupGo(wf string) (string, bool) {
	// already done: a setup-go reading the ROOT go.mod. One reading a
	// playground's or a figure's module (go-numerics, go-statistics) may
	// come after the hugo build and is not ours to rely on.
	if rootGoVersion.MatchString(wf) {
		return wf, true
	}
	if !installsHugo(wf) {
		return wf, false
	}
	lines := strings.SplitAfter(wf, "\n")
	// setup-go reads go.mod, so it goes right after the checkout, wherever
	// the hugo install is: the official template installs hugo BEFORE it
	// checks the repository out, and a setup-go there finds no go.mod
	at := -1
	for i, ln := range lines {
		if strings.Contains(ln, "actions/checkout") && !strings.HasPrefix(strings.TrimSpace(ln), "#") {
			at = i
			break
		}
	}
	if at < 0 {
		return wf, false
	}
	indentOf := func(s string) int { return len(s) - len(strings.TrimLeft(s, " ")) }
	step := -1 // the checkout step's "- " line
	for i := at; i >= 0; i-- {
		t := strings.TrimSpace(lines[i])
		if strings.HasPrefix(t, "- ") && (i == at || indentOf(lines[i]) < indentOf(lines[at])) {
			step = i
			break
		}
	}
	if step < 0 {
		return wf, false
	}
	// the step after it starts at the next "- " line of the same indent, or
	// at the first line less indented (the end of the steps)
	start := len(lines)
	for i := step + 1; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if t == "" || strings.HasPrefix(t, "#") && indentOf(lines[i]) > indentOf(lines[step]) {
			continue
		}
		if indentOf(lines[i]) < indentOf(lines[step]) || indentOf(lines[i]) == indentOf(lines[step]) {
			start = i
			break
		}
	}
	// a comment block right above the next step stays with it
	for start > step+1 && strings.HasPrefix(strings.TrimSpace(lines[start-1]), "#") {
		start--
	}
	indent := strings.Repeat(" ", indentOf(lines[step]))
	setup := indent + "# Hugo resolves the landingkit module (hugo.toml [module]) with the go\n" +
		indent + "# command, at the version go.mod pins.\n" +
		indent + "- uses: actions/setup-go@v7\n" +
		indent + "  with:\n" +
		indent + "    go-version-file: go.mod\n" +
		indent + "    cache: false\n"
	head := strings.Join(lines[:start], "")
	if !strings.HasSuffix(head, "\n") {
		head += "\n" // a checkout on the file's last line, with no newline
	}
	return head + setup + strings.Join(lines[start:], ""), true
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}
