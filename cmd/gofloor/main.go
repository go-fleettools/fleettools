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
// not as clean. A step that runs `vulngate` is the judge and counts.
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
	"go.yaml.in/yaml/v3"
)

const longHelp = `gofloor reads, through the REST API and the default branch, every go.mod and
every workflow of each repository of the given organisations, and reports:

  GO    each go.mod's go directive; below --floor is a finding
  CI    each actions/setup-go step: go-version-file (follows go.mod), or a
        hard-coded version / a channel such as stable (a finding)
  VULN  whether a govulncheck step exists AND can fail the build on a finding
        (-format json|sarif|openvex, ||true, continue-on-error, -mode binary
        make it a step that gates nothing; vulngate counts as the judge)

A repository it could not read is UNREADABLE and is never counted clean.

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
			fmt.Fprintln(cli.Err(cmd), "usage: gofloor [--floor 1.27.3] ORG...")
			return cli.FromStatus(2)
		}
		if _, ok := parseVersion(floor); !ok {
			fmt.Fprintf(cli.Err(cmd), "gofloor: --floor %q is not a Go version (1.27.3)\n", floor)
			return cli.FromStatus(2)
		}
		return cli.FromStatus(sweep(cli.Out(cmd), cli.Err(cmd), gh, repos, floor, args))
	}
	c.Flags().StringVar(&floor, "floor", "1.27.3",
		"the lowest go directive that is not a finding; move it with the standard library's fixes")
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
	for _, p := range flows {
		src, ok := read(p)
		if !ok {
			continue
		}
		rep.Workflows++
		setups, vulns, err := Audit(p, []byte(src))
		if err != nil {
			rep.Unreadable = append(rep.Unreadable, p+": "+err.Error())
			continue
		}
		rep.Setups = append(rep.Setups, setups...)
		rep.Vulns = append(rep.Vulns, vulns...)
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
}

// Audit reads one workflow file.
func Audit(file string, src []byte) ([]Setup, []Vuln, error) {
	var wf struct {
		Jobs map[string]job `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(src, &wf); err != nil {
		return nil, nil, fmt.Errorf("not YAML: %w", err)
	}
	var setups []Setup
	var vulns []Vuln
	names := make([]string, 0, len(wf.Jobs))
	for n := range wf.Jobs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, jn := range names {
		j := wf.Jobs[jn]
		for _, s := range j.Steps {
			if strings.HasPrefix(s.Uses, "actions/setup-go") {
				setups = append(setups, classify(file, jn, s))
			}
			if v, ok := vulnStep(file, jn, j, s); ok {
				vulns = append(vulns, v)
			}
		}
	}
	return setups, vulns, nil
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
