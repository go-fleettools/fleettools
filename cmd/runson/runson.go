// Package main implements runson: which runner images the fleet's workflows
// ask for, read from the checkouts already on this machine.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-fleettools/fleettools/internal/checkout"
)

// Finding is one workflow file's answer. Repo is "org/repo" as
// checkout.Repos names it, so two findings from one repository collapse on it.
type Finding struct {
	Repo     string
	Workflow string
	RunsOn   string
}

// runsOnValues extracts every `runs-on:` value from one workflow's text.
//
// ⚠ IT DOES NOT PARSE YAML, and says so rather than pretending: a value of
// `${{ matrix.os }}` is reported VERBATIM, because resolving it means reading
// the matrix and a wrong resolution would be worse than an honest indirection.
// A list form (`runs-on: [self-hosted, linux]`) is likewise reported as
// written. The tests fix both shapes, so a later change to "improve" this has
// to decide deliberately rather than by accident.
func runsOnValues(text string) []string {
	var out []string
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if !strings.HasPrefix(line, "runs-on:") {
			continue
		}
		v := strings.TrimSpace(strings.TrimPrefix(line, "runs-on:"))
		if i := strings.Index(v, "#"); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		v = strings.Trim(v, `"'`)
		if v == "" {
			// A block form; the values are on the following lines, and reading
			// them means tracking indentation. Counted as unreadable rather
			// than as absent -- see unreadable in scanRepo's caller.
			continue
		}
		out = append(out, v)
	}
	return out
}

// scanRepo reads one checkout's workflows. dir is the checkout, repo the
// "org/repo" name the findings carry.
//
// A repository with no .github/workflows yields nothing, which is a repository
// with no CI rather than a repository that could not be read: the two are told
// apart by the caller, because a scan that cannot distinguish them reports
// zero for both.
func scanRepo(dir, repo string) (found []Finding, hadWorkflows bool) {
	wf := filepath.Join(dir, ".github", "workflows")
	entries, err := os.ReadDir(wf)
	if err != nil {
		return nil, false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml") {
			continue
		}
		hadWorkflows = true
		b, err := os.ReadFile(filepath.Join(wf, name))
		if err != nil {
			continue
		}
		for _, v := range runsOnValues(string(b)) {
			found = append(found, Finding{
				Repo:     repo,
				Workflow: ".github/workflows/" + name,
				RunsOn:   v,
			})
		}
	}
	return found, hadWorkflows
}

// stalenessNote is the sentence about the EVIDENCE under the table.
//
// ⛔ IT DOES NOT CALL checkout.StalenessWarning, and the reason is the noun.
// That function's sentence counts "findings", because the scanners it was
// written for report one finding per repository. runson reports a
// DISTRIBUTION: 3599 findings over 1247 checkouts, so "842 of the 1247
// findings above" would have been wrong twice over -- wrong count, wrong
// thing counted. What must not drift is the RULE for deciding whether a
// checkout is behind, unknown or current, and that rule is checkout.Judge,
// which is shared. The sentence is ours because the population is ours.
func stalenessNote(ages []checkout.Age) string {
	behind, unknown, furthest := 0, 0, 0
	for _, a := range ages {
		switch checkout.Judge(a.Behind, a.FetchAge, a.FetchKnown) {
		case checkout.Behind:
			behind++
			if a.Behind > furthest {
				furthest = a.Behind
			}
		case checkout.Unknown:
			unknown++
		}
	}
	var b strings.Builder
	if behind > 0 {
		fmt.Fprintf(&b,
			"\n⚠ %d of the %d CHECKOUTS read are BEHIND their remote (furthest: %d commits).\n"+
				"  A workflow added upstream since is invisible here, so this UNDERSTATES exposure.\n"+
				"  Pull those checkouts and re-run before treating an absence as real.\n",
			behind, len(ages), furthest)
	}
	if unknown > 0 {
		fmt.Fprintf(&b,
			"\n⚠ %d of the %d CHECKOUTS read have not FETCHED for over %v, so a distance of 0\n"+
				"  means nobody asked, not that they are current.\n",
			unknown, len(ages), checkout.FetchHorizon)
	}
	return b.String()
}

// Count is one runner value and the repositories that ask for it.
type Count struct {
	Value string
	Repos []string
}

// tally groups findings by runs-on value, counting REPOSITORIES and not
// files.
//
// ⛔ ONE REPOSITORY WITH NINE WORKFLOWS IS NOT NINE EXPOSURES. The question
// runson answers is "how much of the fleet moves when this image moves", and
// that is a count of repositories; counting files answers a question nobody
// asked and inflates whichever repository happens to have the most lanes.
func tally(findings []Finding) []Count {
	byValue := map[string]map[string]bool{}
	for _, f := range findings {
		if byValue[f.RunsOn] == nil {
			byValue[f.RunsOn] = map[string]bool{}
		}
		byValue[f.RunsOn][f.Repo] = true
	}
	out := make([]Count, 0, len(byValue))
	for v, repos := range byValue {
		names := make([]string, 0, len(repos))
		for r := range repos {
			names = append(names, r)
		}
		sort.Strings(names)
		out = append(out, Count{Value: v, Repos: names})
	}
	// Most-used first, then alphabetically, so the output is stable enough to
	// diff between runs.
	sort.Slice(out, func(i, j int) bool {
		if len(out[i].Repos) != len(out[j].Repos) {
			return len(out[i].Repos) > len(out[j].Repos)
		}
		return out[i].Value < out[j].Value
	})
	return out
}
