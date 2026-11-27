// Command gofloor says, for every Go repository of one or more organisations,
// which Go toolchain it can be built with and whether CI would notice a
// vulnerable one.
//
//	gofloor go-authn
//	gofloor --floor 1.27.3 go-authn go-pkgx
//
// # WHY IT EXISTS
//
// A module's `go` line is its FLOOR: the oldest toolchain a consumer may build
// it with. On 2026-10-31 go-authn/directory declared `go 1.27.1`, its CI named
// `1.27.2` in five jobs, and nothing ran govulncheck: the library could be
// built with a standard library carrying three known vulnerabilities, and
// nothing said so. Fourteen of nineteen sibling repositories were the same.
// The floor is named in more places than go.mod, so this reads all of them:
//
//   - every go.mod in the repository, nested modules included (a module under
//     interop/ has its own go line, and CI failed on one);
//   - every actions/setup-go step: `go-version-file` follows go.mod and
//     cannot drift, a hard-coded version can, and `stable` is a channel that
//     moves with no commit (see gopin);
//   - every govulncheck step, and whether it can FAIL on a finding.
//
// # A govulncheck STEP IS NOT A GATE
//
// ⛔ `-format json`, `sarif` and `openvex` make govulncheck exit 0 whatever it
// found (vulngate exists for that), `|| true` and `continue-on-error: true`
// do the same, and `-mode binary` or `extract` does not scan the source. A job
// with any of them is "present" and gates nothing, so it is reported as such,
// not as clean. A step that runs `vulngate` is the judge and counts — in the
// SAME run: block, because a step is judged on its own text.
//
// # A LANE CAN BE IN ANOTHER REPOSITORY
//
// ⛔ A job can be a `uses:` instead of steps, and then the lane is a reusable
// workflow somewhere else. Walking steps sees nothing there, so a repository
// calling a shared lane used to read exactly like one that scans nothing —
// measured on go-richdoc, where extracting one inline lane into an org-wide
// `workflow_call` turned all ELEVEN rows from `gates` to `none` while every
// lane kept running green. The failure is not only the missing row: `none` on
// a repository that already calls a shared lane argues for pasting an inline
// copy back into each caller, which is the duplication the shared definition
// removes.
//
// So a `uses:` job is followed ONE level: that file is read and audited, and
// its verdict is attributed to the caller (`ci.yml -> org/.github/...@main`).
// The result is cached per called file, because an organisation usually has
// one shared lane that every module calls. A call that cannot be read, or
// cannot be parsed into owner/repo/path@ref, is UNREADABLE — never clean. A
// called workflow that calls another in turn is reported rather than followed:
// an unbounded follow is a sweep nobody asked for.
//
// # ERRORS ARE VALUES
//
// ⛔ A repository this could not read is reported UNREADABLE and the run
// exits 2; it is never counted clean. That includes a truncated tree listing
// (GitHub caps it), a go.mod or workflow it failed to fetch, and a workflow
// that is not YAML. One tree call plus one contents call per file: no GraphQL.
//
// Exit status: 0 every repository clean, 1 something found, 2 something could
// not be read (2 outranks 1: the answer is incomplete).
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v4"
)

const longHelp = `gofloor reads, through the REST API and the default branch, every go.mod and
every workflow of each repository of the given organisations, and reports:

  GO    each go.mod's go directive; below --floor is a finding
  CI    each actions/setup-go step: go-version-file (follows go.mod), or a
        hard-coded version / a channel such as stable (a finding)
  VULN  whether a govulncheck step exists AND can fail the build on a finding
        (-format json|sarif|openvex, ||true, continue-on-error, -mode binary
        make it a step that gates nothing; vulngate counts as the judge, in
        the same run: block)

A job that is a "uses:" rather than steps is a lane in ANOTHER repository, and
it is followed one level: that workflow is read and its verdict attributed to
the caller. Without this, a repository calling an organisation's shared lane
read exactly like one that scans nothing — and the obvious way to satisfy the
tool was to paste an inline copy back into every caller.

A repository it could not read is UNREADABLE and is never counted clean. So is
a called workflow that could not be read or parsed; one that calls another in
turn is reported rather than followed.

Two subcommands change things rather than read them:

  gofloor fix [--max N] ORG   open one PR per repository raising every go
                              directive below --floor (default 1.27.4); it
                              clones, edits, builds, pushes, opens, and waits
                              for nothing
  gofloor merge ORG           merge those PRs whose checks are green, list the
                              others with their state

Exit status: 0 clean, 1 findings, 2 something could not be read.`

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// RootCmd builds the command with the real GitHub reads behind it.
func RootCmd() *cobra.Command { return command(fleet.GH, fleet.Repos) }

func command(gh ghFunc, repos reposFunc) *cobra.Command {
	var floor string
	c := cli.New("gofloor [flags] ORG...", "Go floor, CI toolchain and govulncheck of every repository", longHelp)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			fmt.Fprintln(cli.Err(cmd), "usage: gofloor [--floor "+defaultFloor+"] ORG... | gofloor fix ORG | gofloor merge ORG")
			return cli.FromStatus(2)
		}
		if _, ok := parseVersion(floor); !ok {
			fmt.Fprintf(cli.Err(cmd), "gofloor: --floor %q is not a Go version (1.27.3)\n", floor)
			return cli.FromStatus(2)
		}
		return cli.FromStatus(sweep(cli.Out(cmd), cli.Err(cmd), gh, repos, floor, args))
	}
	c.Flags().StringVar(&floor, "floor", defaultFloor,
		"the lowest go directive that is not a finding; move it with the standard library's fixes")
	addFixCommands(c, realEnv(gh, repos))
	return c
}

type (
	ghFunc    func(args ...string) ([]byte, error)
	reposFunc func(org string) ([]fleet.Repo, error)
)

// A Report is what is known about one repository.
type Report struct {
	Repo       string
	GoMods     []GoMod  // every go.mod, by path
	Setups     []Setup  // every actions/setup-go step
	Vulns      []Vuln   // every govulncheck step
	Workflows  int      // how many workflow files were read
	Findings   []string // what is wrong
	Unreadable []string // what could not be read
}

// A GoMod is one module's directive.
type GoMod struct{ Path, Directive string }

// A Setup is one actions/setup-go step.
type Setup struct {
	File, Job, Source string // Source: "go-version-file", "hard-coded 1.27.2", "stable", "expression"
	Bad               bool
}

// A Vuln is one govulncheck step and whether it can fail the build.
type Vuln struct {
	File, Job string
	Gates     bool
	Why       string // why it does not
}

func sweep(out, errOut io.Writer, gh ghFunc, repos reposFunc, floor string, orgs []string) int {
	found, unread, n := 0, 0, 0
	for _, org := range orgs {
		rs, err := repos(org)
		if err != nil {
			// The whole organisation: nothing was read, and that is not clean.
			fmt.Fprintf(errOut, "gofloor: listing %s: %v\n", org, err)
			unread++
			continue
		}
		for _, r := range rs {
			rep := Check(gh, r, floor)
			if rep == nil {
				continue // not a Go module
			}
			n++
			fmt.Fprint(out, rep.Line())
			for _, f := range rep.Findings {
				fmt.Fprintf(out, "    FOUND  %s\n", f)
			}
			for _, u := range rep.Unreadable {
				fmt.Fprintf(out, "    UNREADABLE  %s\n", u)
			}
			if len(rep.Findings) > 0 {
				found++
			}
			if len(rep.Unreadable) > 0 {
				unread++
			}
		}
	}
	fmt.Fprintf(out, "\n%d Go repositories: %d with findings, %d unreadable\n", n, found, unread)
	switch {
	case unread > 0:
		return 2
	case found > 0:
		return 1
	case n == 0:
		// A sweep that read nothing is not a clean bill.
		fmt.Fprintln(errOut, "gofloor: no Go module found in "+strings.Join(orgs, ", "))
		return 2
	}
	return 0
}

// Line is the one-line summary of a report.
func (r *Report) Line() string {
	var mods []string
	for _, m := range r.GoMods {
		if m.Path == "go.mod" {
			mods = append(mods, m.Directive)
		} else {
			mods = append(mods, path.Dir(m.Path)+"="+m.Directive)
		}
	}
	srcs := map[string]bool{}
	for _, s := range r.Setups {
		srcs[s.Source] = true
	}
	var ci []string
	for s := range srcs {
		ci = append(ci, s)
	}
	sort.Strings(ci)
	if len(ci) == 0 {
		ci = []string{"-"}
	}
	vuln := "none"
	if len(r.Vulns) > 0 {
		vuln = "gates"
		for _, v := range r.Vulns {
			if !v.Gates {
				vuln = "present, gates nothing"
			}
		}
	}
	return fmt.Sprintf("%-40s go %-22s ci %-26s govulncheck: %s\n",
		r.Repo, strings.Join(mods, " "), strings.Join(ci, ","), vuln)
}

type treeEntry struct {
	Path string `json:"path"`
	Type string `json:"type"`
}

// Check reads one repository. It returns nil for a repository with no go.mod.
func Check(gh ghFunc, r fleet.Repo, floor string) *Report {
	rep := &Report{Repo: r.FullName}
	branch := r.DefaultBranch
	if branch == "" {
		branch = "HEAD"
	}
	raw, err := gh("api", "repos/"+r.FullName+"/git/trees/"+branch+"?recursive=1")
	if err != nil {
		rep.Unreadable = append(rep.Unreadable, "the file tree: "+err.Error())
		return rep
	}
	var tree struct {
		Tree      []treeEntry `json:"tree"`
		Truncated bool        `json:"truncated"`
	}
	if err := json.Unmarshal(raw, &tree); err != nil {
		rep.Unreadable = append(rep.Unreadable, "the file tree is not JSON: "+err.Error())
		return rep
	}
	if tree.Truncated {
		// GitHub cuts a large listing; a go.mod beyond the cut would read as absent.
		rep.Unreadable = append(rep.Unreadable, "the file tree is truncated, so a nested go.mod or workflow may be missing from it")
		return rep
	}
	var mods, flows []string
	for _, e := range tree.Tree {
		if e.Type != "blob" {
			continue
		}
		switch {
		case path.Base(e.Path) == "go.mod" && !strings.Contains(e.Path, "testdata/") && !strings.HasPrefix(e.Path, "vendor/"):
			mods = append(mods, e.Path)
		case path.Dir(e.Path) == ".github/workflows" && (strings.HasSuffix(e.Path, ".yml") || strings.HasSuffix(e.Path, ".yaml")):
			flows = append(flows, e.Path)
		}
	}
	if len(mods) == 0 {
		return nil
	}
	sort.Strings(mods)
	sort.Strings(flows)
	read := func(p string) (string, bool) {
		b, err := gh("api", "-H", "Accept: application/vnd.github.raw+json",
			"repos/"+r.FullName+"/contents/"+p+"?ref="+branch)
		if err != nil {
			rep.Unreadable = append(rep.Unreadable, p+": "+err.Error())
			return "", false
		}
		return string(b), true
	}
	for _, p := range mods {
		src, ok := read(p)
		if !ok {
			continue
		}
		d := goDirective(src)
		rep.GoMods = append(rep.GoMods, GoMod{p, d})
		switch v, ok := parseVersion(d); {
		case d == "":
			rep.Findings = append(rep.Findings, p+" has no go directive")
		case !ok:
			rep.Findings = append(rep.Findings, p+": go "+d+" is not a version")
		default:
			if f, _ := parseVersion(floor); less(v, f) {
				rep.Findings = append(rep.Findings, fmt.Sprintf("%s: go %s is below the floor %s", p, d, floor))
			}
		}
	}
	if len(flows) == 0 {
		rep.Findings = append(rep.Findings, "no workflow: nothing builds or scans it")
	}
	var calls []Called
	for _, p := range flows {
		src, ok := read(p)
		if !ok {
			continue
		}
		rep.Workflows++
		setups, vulns, called, err := Audit(p, []byte(src))
		if err != nil {
			rep.Unreadable = append(rep.Unreadable, p+": "+err.Error())
			continue
		}
		rep.Setups = append(rep.Setups, setups...)
		rep.Vulns = append(rep.Vulns, vulns...)
		calls = append(calls, called...)
	}

	// ⛔ A CALLED WORKFLOW IS READ, OR THE REPOSITORY IS NOT CLEAN. The verdict
	// is in the called file, so this follows the call one level. Reading one
	// more file is the same kind of call this function already makes, and the
	// cache matters in practice: an organisation usually has ONE shared lane
	// that every module calls, so N modules would otherwise fetch it N times.
	//
	// What is NOT done here, deliberately: a called workflow that itself calls
	// another is not followed further. Going deeper needs a visited set and a
	// depth bound, and an unbounded follow is a sweep nobody asked for; the
	// second level is reported UNREAD instead, which is never clean.
	seen := map[string][]Vuln{}
	for _, c := range calls {
		slug, cpath, rev, ok := calledWorkflow(c.Ref)
		if !ok {
			rep.Unreadable = append(rep.Unreadable, fmt.Sprintf(
				"%s job %s: cannot read the called workflow %q", c.File, c.Job, c.Ref))
			continue
		}
		key := slug + "/" + cpath + "@" + rev
		vs, done := seen[key]
		if !done {
			var src string
			var got bool
			if slug == "" { // a local call: this repository, the branch read above
				src, got = read(cpath)
			} else {
				b, err := gh("api", "-H", "Accept: application/vnd.github.raw+json",
					"repos/"+slug+"/contents/"+cpath+"?ref="+rev)
				if err != nil {
					rep.Unreadable = append(rep.Unreadable, fmt.Sprintf(
						"%s job %s calls %s, which could not be read: %v", c.File, c.Job, c.Ref, err))
					seen[key] = nil
					continue
				}
				src, got = string(b), true
			}
			if !got {
				seen[key] = nil
				continue
			}
			_, cv, deeper, err := Audit(key, []byte(src))
			if err != nil {
				rep.Unreadable = append(rep.Unreadable, fmt.Sprintf(
					"%s job %s calls %s, which is not YAML: %v", c.File, c.Job, c.Ref, err))
				seen[key] = nil
				continue
			}
			for _, d := range deeper {
				rep.Unreadable = append(rep.Unreadable, fmt.Sprintf(
					"%s calls %s, which calls %s in turn; the second level is not followed", c.Ref, key, d.Ref))
			}
			seen[key], vs = cv, cv
		}
		for _, v := range vs {
			// Named by the CALLER, so a reader of the row knows which
			// repository is gated, with the called file kept for the trail.
			v.File = c.File + " -> " + v.File
			v.Job = c.Job + " -> " + v.Job
			rep.Vulns = append(rep.Vulns, v)
		}
	}
	for _, s := range rep.Setups {
		if s.Bad {
			rep.Findings = append(rep.Findings, fmt.Sprintf("%s job %s: setup-go %s (use go-version-file: go.mod)", s.File, s.Job, s.Source))
		}
	}
	// Not when a workflow could not be read: the step may be in it.
	if len(rep.Vulns) == 0 && len(rep.Unreadable) == 0 {
		rep.Findings = append(rep.Findings, "no govulncheck step")
	}
	for _, v := range rep.Vulns {
		if !v.Gates {
			rep.Findings = append(rep.Findings, fmt.Sprintf("%s job %s: govulncheck cannot fail the build (%s)", v.File, v.Job, v.Why))
		}
	}
	return rep
}

var directiveRe = regexp.MustCompile(`(?m)^go\s+(\S+)\s*$`)

func goDirective(gomod string) string {
	if m := directiveRe.FindStringSubmatch(gomod); m != nil {
		return m[1]
	}
	return ""
}

// parseVersion reads 1.27 or 1.27.3 (also 1.27rc1, as 1.27.0).
func parseVersion(s string) ([3]int, bool) {
	var v [3]int
	if s == "" {
		return v, false
	}
	parts := strings.Split(s, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return v, false
	}
	for i, p := range parts {
		if i == len(parts)-1 { // 1.27rc1 is a release candidate of 1.27.0
			end := 0
			for end < len(p) && p[end] >= '0' && p[end] <= '9' {
				end++
			}
			p = p[:end]
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

func less(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

type step struct {
	Name            string         `yaml:"name"`
	Uses            string         `yaml:"uses"`
	Run             string         `yaml:"run"`
	With            map[string]any `yaml:"with"`
	ContinueOnError any            `yaml:"continue-on-error"`
}

type job struct {
	ContinueOnError any    `yaml:"continue-on-error"`
	Steps           []step `yaml:"steps"`
	// ⛔ A JOB CAN BE A `uses:` TOO, and that is the whole of #203. A job that
	// calls a reusable workflow has NO steps, so every rule below that walks
	// Steps sees nothing and the repository reads as if it ran no lane at all.
	Uses string `yaml:"uses"`
}

// A Called is a job that delegates to a reusable workflow. Whether that
// workflow gates is not in this file and cannot be guessed from it.
type Called struct {
	File, Job string // where the call is
	Ref       string // owner/repo/.github/workflows/x.yml@ref, verbatim
}

// Audit reads one workflow file.
func Audit(file string, src []byte) ([]Setup, []Vuln, []Called, error) {
	var wf struct {
		Jobs map[string]job `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(src, &wf); err != nil {
		return nil, nil, nil, fmt.Errorf("not YAML: %w", err)
	}
	var setups []Setup
	var vulns []Vuln
	var called []Called
	names := make([]string, 0, len(wf.Jobs))
	for n := range wf.Jobs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, jn := range names {
		j := wf.Jobs[jn]
		if r := strings.TrimSpace(j.Uses); r != "" && len(j.Steps) == 0 {
			called = append(called, Called{File: file, Job: jn, Ref: r})
		}
		for _, s := range j.Steps {
			if strings.HasPrefix(s.Uses, "actions/setup-go") {
				setups = append(setups, classify(file, jn, s))
			}
			if v, ok := vulnStep(file, jn, j, s); ok {
				vulns = append(vulns, v)
			}
		}
	}
	return setups, vulns, called, nil
}

// calledWorkflow splits `owner/repo/.github/workflows/x.yml@ref` into the
// pieces needed to read that file. A local call (`./.github/workflows/x.yml`)
// has no owner and is read from the repository being audited.
func calledWorkflow(ref string) (slug, path, rev string, ok bool) {
	at := strings.LastIndex(ref, "@")
	if at < 0 {
		// A local call carries no @ref: it is this repository, this commit.
		if p := strings.TrimPrefix(ref, "./"); strings.HasPrefix(p, ".github/workflows/") {
			return "", p, "", true
		}
		return "", "", "", false
	}
	left, rev := ref[:at], ref[at+1:]
	if rev == "" {
		return "", "", "", false
	}
	parts := strings.SplitN(left, "/", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" ||
		!strings.HasPrefix(parts[2], ".github/workflows/") {
		return "", "", "", false
	}
	return parts[0] + "/" + parts[1], parts[2], rev, true
}

func classify(file, jn string, s step) Setup {
	out := Setup{File: file, Job: jn}
	if f, ok := s.With["go-version-file"]; ok && f != nil {
		out.Source = "go-version-file"
		return out
	}
	v := strings.TrimSpace(fmt.Sprint(s.With["go-version"]))
	switch {
	case s.With["go-version"] == nil:
		out.Source = "default"
		out.Bad = true
	case strings.Contains(v, "${{"):
		out.Source = "expression"
	case v == "stable" || v == "oldstable":
		out.Source, out.Bad = v, true
	default:
		out.Source, out.Bad = "hard-coded "+v, true
	}
	return out
}

func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return strings.TrimSpace(x) == "true" || strings.Contains(x, "${{")
	}
	return false
}

var (
	formatRe = regexp.MustCompile(`-format[ =]+(json|sarif|openvex)`)
	modeRe   = regexp.MustCompile(`-mode[ =]+(binary|extract)`)
)

// vulnStep decides whether a step runs govulncheck, and whether it can fail.
func vulnStep(file, jn string, j job, s step) (Vuln, bool) {
	v := Vuln{File: file, Job: jn, Gates: true}
	switch {
	case strings.HasPrefix(s.Uses, "golang/govulncheck-action"):
		if f := fmt.Sprint(s.With["output-format"]); s.With["output-format"] != nil && f != "text" {
			v.Gates, v.Why = false, "output-format "+f+" exits 0"
		}
	case strings.Contains(s.Run, "govulncheck"):
		run := s.Run
		if strings.Contains(run, "vulngate") {
			return v, true // judged by vulngate, which is the point of it
		}
		switch {
		case formatRe.MatchString(run):
			v.Gates, v.Why = false, formatRe.FindString(run)+" exits 0 whatever was found"
		case modeRe.MatchString(run):
			v.Gates, v.Why = false, modeRe.FindString(run)+" does not scan the source"
		case strings.Contains(run, "|| true") || strings.Contains(run, "||true"):
			v.Gates, v.Why = false, "|| true"
		}
	default:
		return v, false
	}
	if v.Gates && (truthy(s.ContinueOnError) || truthy(j.ContinueOnError)) {
		v.Gates, v.Why = false, "continue-on-error"
	}
	return v, true
}
