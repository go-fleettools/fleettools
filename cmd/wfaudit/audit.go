package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Severity orders findings. A HIGH is a path from somebody else's pull request
// to this repository's secrets or write token; a MEDIUM widens what a
// compromised step could do; an INFO is a fact the reader needs to judge the
// rest (what a workflow without a permissions block actually gets depends on a
// repository setting this file cannot see).
type Severity int

const (
	Info Severity = iota
	Medium
	High
)

func (s Severity) String() string {
	switch s {
	case High:
		return "HIGH"
	case Medium:
		return "MEDIUM"
	}
	return "INFO"
}

// Finding is one rule matched at one place.
type Finding struct {
	Severity Severity
	Rule     string
	File     string
	Line     int
	Message  string
}

func (f Finding) String() string {
	return fmt.Sprintf("%-6s %s:%d %s: %s", f.Severity, f.File, f.Line, f.Rule, f.Message)
}

// privilegedTriggers run in the context of the BASE repository, with its
// secrets and a write-capable token, whoever opened the pull request that set
// them off. That is the whole of the "pwn request" class.
var privilegedTriggers = map[string]bool{
	"pull_request_target": true,
	"workflow_run":        true,
}

// prHeadRE recognises an expression naming the pull request's own code: a
// checkout of it, under a privileged trigger, runs a stranger's code with the
// base repository's secrets.
var prHeadRE = regexp.MustCompile(`github\.event\.pull_request\.head\.(sha|ref)|github\.head_ref|github\.event\.workflow_run\.head_(sha|branch)|refs/pull/`)

// prCheckoutRunRE is the same act from a shell step.
var prCheckoutRunRE = regexp.MustCompile(`gh pr checkout|git (fetch|checkout)[^\n]*(pull/|head_ref|pull_request\.head)`)

// workspacePathRE is an artifact path that takes the whole checkout, .git
// included -- and with it the token actions/checkout leaves in .git/config
// unless told not to.
var workspacePathRE = regexp.MustCompile(`^\s*(\.|\./|\$\{\{\s*github\.workspace\s*\}\})\s*$`)

var secretsRE = regexp.MustCompile(`secrets\.[A-Za-z_][A-Za-z0-9_]*`)

// Audit reads one workflow file and returns what it finds there.
//
// It deliberately leaves template injection (an untrusted ${{ }} inside run:)
// to actionlint, which already reports it; repeating a check in two tools
// makes them disagree the day one is fixed and the other is not.
func Audit(file string, src []byte) ([]Finding, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	if len(doc.Content) == 0 {
		return nil, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: not a mapping at the top", file)
	}

	var out []Finding
	add := func(s Severity, rule string, line int, msg string) {
		out = append(out, Finding{s, rule, file, line, msg})
	}

	onKey, on := get(root, "on")
	if on == nil {
		// YAML 1.1 reads a bare `on` as the boolean true, and so does this
		// parser's map form; the node keeps the text, so look for it too.
		onKey, on = get(root, "true")
	}
	triggers := triggerNames(on)
	var privileged []string
	for _, t := range triggers {
		if privilegedTriggers[t] {
			privileged = append(privileged, t)
		}
	}
	sort.Strings(privileged)

	_, wfPerms := get(root, "permissions")
	if wfPerms != nil {
		checkPermissions(wfPerms, "the workflow", add)
	}

	_, jobs := get(root, "jobs")
	if jobs == nil || jobs.Kind != yaml.MappingNode {
		return out, nil
	}

	checksOutPR := false
	firstPRCheckout := 0
	jobsWithoutPerms := 0
	for i := 0; i+1 < len(jobs.Content); i += 2 {
		name, job := jobs.Content[i].Value, jobs.Content[i+1]
		if job.Kind != yaml.MappingNode {
			continue
		}
		_, jobPerms := get(job, "permissions")
		if jobPerms != nil {
			checkPermissions(jobPerms, "job "+name, add)
		} else if wfPerms == nil {
			jobsWithoutPerms++
		}
		_, steps := get(job, "steps")
		if steps == nil || steps.Kind != yaml.SequenceNode {
			continue
		}

		var unpersisted []*yaml.Node // checkouts that keep the token in .git
		for _, step := range steps.Content {
			if step.Kind != yaml.MappingNode {
				continue
			}
			_, uses := get(step, "uses")
			_, with := get(step, "with")
			_, run := get(step, "run")
			usesName := ""
			if uses != nil {
				usesName = strings.SplitN(uses.Value, "@", 2)[0]
			}
			if usesName == "actions/checkout" {
				if _, ref := get(with, "ref"); ref != nil && prHeadRE.MatchString(ref.Value) {
					checksOutPR = true
					if firstPRCheckout == 0 {
						firstPRCheckout = ref.Line
					}
				}
				if _, pc := get(with, "persist-credentials"); pc == nil || pc.Value != "false" {
					unpersisted = append(unpersisted, step)
				}
			}
			if run != nil && prCheckoutRunRE.MatchString(run.Value) {
				checksOutPR = true
				if firstPRCheckout == 0 {
					firstPRCheckout = run.Line
				}
			}
			if len(unpersisted) > 0 && (usesName == "actions/upload-artifact" || usesName == "actions/upload-pages-artifact") {
				if _, p := get(with, "path"); p != nil && coversWorkspace(p.Value) {
					add(High, "artipacked", p.Line, fmt.Sprintf(
						"job %s uploads the whole checkout (path %q) after actions/checkout left its token in .git/config (line %d); set persist-credentials: false on the checkout, or upload only what is needed",
						name, strings.TrimSpace(p.Value), unpersisted[0].Line))
				}
			}
			if len(privileged) > 0 {
				for _, n := range []*yaml.Node{run, with} {
					if n == nil {
						continue
					}
					if m := secretsRE.FindString(text(n)); m != "" {
						add(Medium, "secrets-under-privileged-trigger", n.Line, fmt.Sprintf(
							"job %s reads %s in a workflow triggered by %s, which runs for pull requests from forks with this repository's secrets",
							name, m, strings.Join(privileged, ", ")))
						break
					}
				}
			}
		}
	}

	if len(privileged) > 0 {
		line := 1
		if onKey != nil {
			line = onKey.Line
		}
		if checksOutPR {
			add(High, "dangerous-trigger", firstPRCheckout, fmt.Sprintf(
				"%s runs with this repository's secrets and write token, and this workflow checks out the pull request's own code: anyone who opens a pull request runs code here",
				strings.Join(privileged, ", ")))
		} else {
			add(Medium, "dangerous-trigger", line, fmt.Sprintf(
				"%s runs with this repository's secrets and write token for pull requests from forks; no checkout of the pull request's code was seen, so keep it that way",
				strings.Join(privileged, ", ")))
		}
	}

	if wfPerms == nil && jobsWithoutPerms > 0 {
		add(Info, "no-permissions", 1, fmt.Sprintf(
			"%d job(s) declare no permissions and the workflow sets none: they get the repository's default token, read-only or read-write depending on a setting this file cannot show (wfaudit -api reads it)",
			jobsWithoutPerms))
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Severity != out[j].Severity {
			return out[i].Severity > out[j].Severity
		}
		return out[i].Line < out[j].Line
	})
	return out, nil
}

// checkPermissions reports a grant that gives every step more than it needs.
func checkPermissions(n *yaml.Node, where string, add func(Severity, string, int, string)) {
	switch n.Kind {
	case yaml.ScalarNode:
		if n.Value == "write-all" {
			add(High, "excessive-permissions", n.Line, where+" grants write-all: every scope, to every step")
		}
	case yaml.MappingNode:
		if where != "the workflow" {
			return // a job-level grant is the narrow form this rule asks for
		}
		var writes []string
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i+1].Value == "write" {
				writes = append(writes, n.Content[i].Value)
			}
		}
		if len(writes) > 0 {
			sort.Strings(writes)
			add(Medium, "excessive-permissions", n.Line, fmt.Sprintf(
				"the workflow grants %s: write to every job; grant it on the one job that needs it",
				strings.Join(writes, ", ")+" write"))
		}
	}
}

// triggerNames lists the events in an `on:` value, whichever of its three
// spellings is used: a name, a list of names, or a map keyed by name.
func triggerNames(on *yaml.Node) []string {
	if on == nil {
		return nil
	}
	switch on.Kind {
	case yaml.ScalarNode:
		return []string{on.Value}
	case yaml.SequenceNode:
		var out []string
		for _, c := range on.Content {
			out = append(out, c.Value)
		}
		return out
	case yaml.MappingNode:
		var out []string
		for i := 0; i < len(on.Content); i += 2 {
			out = append(out, on.Content[i].Value)
		}
		return out
	}
	return nil
}

// coversWorkspace reports whether an upload path (one per line) takes the
// whole checkout.
func coversWorkspace(path string) bool {
	for _, l := range strings.Split(path, "\n") {
		if workspacePathRE.MatchString(l) {
			return true
		}
	}
	return false
}

// get returns the key and value nodes of a mapping entry, or nils.
func get(m *yaml.Node, key string) (*yaml.Node, *yaml.Node) {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil, nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i], m.Content[i+1]
		}
	}
	return nil, nil
}

// text flattens a node's scalars, so a secret named anywhere under `with:` is
// seen.
func text(n *yaml.Node) string {
	if n.Kind == yaml.ScalarNode {
		return n.Value
	}
	var b strings.Builder
	for _, c := range n.Content {
		b.WriteString(text(c))
		b.WriteByte('\n')
	}
	return b.String()
}
