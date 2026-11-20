// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

// modelgate refuses a sub-agent launched with the wrong model for its task.
//
//	modelgate --hook                      # a PreToolUse hook on the Agent tool
//	modelgate --explain "Review PR 12"    # which class a description falls in
//
// ⛔ WHY IT EXISTS. The routing rule — haiku for search, inventory and page
// checks; sonnet for anything that implements, releases or maintains; opus only
// for bibliography, design and review — was REMEMBERED and never enforced.
// Measured 2026-11-19 over 48 hours and 14,607 API calls: haiku got 8 calls, an
// opus agent reviewed a Renovate config PR, and 509 sub-agent calls ran on an
// outdated claude-opus-5. A rule that lives only in a prompt is followed when
// the prompt is in view and forgotten when it is not.
//
// # The decision, in the order it is taken
//
//	not the Agent tool            allow, silently
//	payload cannot be decoded     allow, with a notice (a refusal is not a verdict)
//	subagent_type "fork"          allow: a fork ignores the model
//	"model-waiver: <reason>"      allow, the reason echoed as a stamped notice
//	model missing                 REFUSE: the environment default is sonnet,
//	                              which hides the choice
//	task unclassified             allow, with any explicit model
//	model family != class's       REFUSE, naming class, matched word and model
//
// # How a task is classified
//
// From the description (3 to 5 words), or the first line of the prompt when
// there is no description. Both are lowered and cut into words, and the
// EARLIEST word that appears in the table decides: "Fix review findings" is an
// implement task, "Review the fix" a review. A word matches WHOLE, so "fix"
// does not match "prefix" nor "review" "preview". Words in no class leave the
// task unclassified, and an unclassified task is never refused on its model —
// ambiguity must not turn into a false refusal.
//
// The table is in longHelp, generated from the same variable the code reads.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

// A class is a kind of task and the model family that should run it.
type class struct {
	name   string
	model  string   // the expected family: haiku, sonnet or opus
	phrase []string // words or space-separated phrases, lower case
}

// classes is THE table. Order is irrelevant: the earliest word in the text wins,
// not the first class.
var classes = []class{
	{"review", "opus", []string{
		"review", "reviews", "revue", "audit", "bibliography", "design", "spec",
		"second opinion", "critique",
	}},
	{"implement", "sonnet", []string{
		"implement", "implementation", "fix", "fixes", "build", "add", "port",
		"release", "bump", "migrate", "land", "repair", "bring", "rework",
		"update", "adopt", "finish", "close", "create", "rewrite", "refactor",
		"docs", "landing", "bind", "integrate", "translate", "align", "enable",
		"reduce", "refuse", "smooth", "switch", "link", "patch",
	}},
	{"check", "haiku", []string{
		"check", "verify", "inventory", "list", "find", "search", "read",
		"count", "fetch", "survey", "wait",
	}},
}

// match is a word of the text that the table knows.
type match struct {
	class *class
	word  string
}

// tokens cuts text into lower-case words: runs of letters, digits, '-' and '_'.
//
// ⛔ A HYPHEN BINDS. "avkit#113 MP4 box-count bound" is a fix, and cut at the
// hyphen it contained the check word "count" and was refused for running on
// sonnet; "fact-check" and "pre-fix" are likewise one word each, found in no
// class, so a compound never matches the word it ends in.
func tokens(text string) []string {
	f := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_'
	})
	out := f[:0]
	for _, w := range f {
		if w = strings.Trim(w, "-_"); w != "" {
			out = append(out, w)
		}
	}
	return out
}

// classify returns the class of the EARLIEST word of text found in the table,
// or nil. A phrase ("second opinion") matches when its words are consecutive.
func classify(text string) *match {
	toks := tokens(text)
	for i := range toks {
		for ci := range classes {
			for _, p := range classes[ci].phrase {
				pw := strings.Fields(p)
				if i+len(pw) > len(toks) {
					continue
				}
				ok := true
				for j, w := range pw {
					if toks[i+j] != w {
						ok = false
						break
					}
				}
				if ok {
					return &match{&classes[ci], p}
				}
			}
		}
	}
	return nil
}

// family names the model family of a model string ("claude-opus-5-5" → opus),
// or "" when it is none of the three.
func family(model string) string {
	m := strings.ToLower(model)
	for _, f := range []string{"haiku", "sonnet", "opus"} {
		if strings.Contains(m, f) {
			return f
		}
	}
	return ""
}

// payload is the part of a PreToolUse payload that matters here.
type payload struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Description  string `json:"description"`
		Prompt       string `json:"prompt"`
		SubagentType string `json:"subagent_type"`
		Model        string `json:"model"`
	} `json:"tool_input"`
}

var waiverRE = regexp.MustCompile(`(?m)^[ \t]*model-waiver:[ \t]*(\S.*?)[ \t]*$`)

// subject is the text a task is classified from.
func subject(p payload) string {
	if d := strings.TrimSpace(p.ToolInput.Description); d != "" {
		return d
	}
	first, _, _ := strings.Cut(strings.TrimSpace(p.ToolInput.Prompt), "\n")
	return first
}

type verdict struct {
	deny   bool
	reason string // the refusal, or the stamped notice of an allowance
}

// decide is the whole policy.
func decide(p payload, now time.Time) verdict {
	if p.ToolName != "Agent" {
		return verdict{}
	}
	in := p.ToolInput
	if in.SubagentType == "fork" {
		return verdict{}
	}
	if m := waiverRE.FindStringSubmatch(in.Prompt); m != nil {
		return verdict{reason: fmt.Sprintf("modelgate: model waived at %s: %s",
			now.Format("2006-01-02 15:04 MST"), m[1])}
	}
	got := family(in.Model)
	m := classify(subject(p))
	if strings.TrimSpace(in.Model) == "" {
		hint := ""
		if m != nil {
			hint = fmt.Sprintf(" This task is class %s (matched %q), which expects model %q.", m.class.name, m.word, m.class.model)
		}
		return verdict{deny: true, reason: "modelgate refuses this Agent call: no model given. The environment default is sonnet, which hides the choice; name one." + hint + waiverHelp}
	}
	if m == nil || got == m.class.model {
		return verdict{}
	}
	shown := got
	if shown == "" {
		shown = "an unrecognised model"
	}
	return verdict{deny: true, reason: fmt.Sprintf(
		"modelgate refuses this Agent call: class %s (matched the word %q) expects model %q, got %q (%s). Relaunch with model: %q.%s",
		m.class.name, m.word, m.class.model, in.Model, shown, m.class.model, waiverHelp)}
}

const waiverHelp = ` If the table is wrong for this task, put a line "model-waiver: <reason>" in the prompt; the reason is required and is echoed as a notice.`

// decodePayload reads a hook payload; any failure is an error, never a verdict.
func decodePayload(b []byte) (payload, error) {
	var p payload
	err := json.Unmarshal(b, &p)
	return p, err
}

var clock = time.Now

// hookMode answers a PreToolUse payload. Only a definite finding denies:
// everything it cannot read allows and says so.
func hookMode(out, errOut io.Writer, stdin io.Reader) int {
	b, err := io.ReadAll(stdin)
	if err != nil {
		fmt.Fprintln(errOut, "modelgate: could not read the hook payload; allowing (a refusal is not a verdict)")
		return 0
	}
	p, err := decodePayload(b)
	if err != nil {
		fmt.Fprintf(errOut, "modelgate: could not decode the hook payload (%v); allowing (a refusal is not a verdict)\n", err)
		return 0
	}
	v := decide(p, clock())
	switch {
	case v.deny:
		writeDecision(out, "deny", v.reason)
	case v.reason != "":
		fmt.Fprintln(errOut, v.reason)
		writeDecision(out, "allow", v.reason)
	}
	return 0
}

func writeDecision(w io.Writer, decision, reason string) {
	b, _ := json.Marshal(map[string]any{"hookSpecificOutput": map[string]string{
		"hookEventName":            "PreToolUse",
		"permissionDecision":       decision,
		"permissionDecisionReason": reason,
	}})
	fmt.Fprintln(w, string(b))
}

// tableHelp renders the table for --help from the variable the code reads.
func tableHelp() string {
	var b strings.Builder
	for _, c := range classes {
		fmt.Fprintf(&b, "  %-9s -> %-6s %s\n", c.name, c.model, strings.Join(c.phrase, ", "))
	}
	return b.String()
}

var longHelp = `modelgate refuses a sub-agent launched with the wrong model for its task.

  modelgate --hook                      read a PreToolUse payload on stdin, answer allow or deny
  modelgate --explain "<description>"   say which class a description falls in

THE TABLE (whole words, case-insensitive; the EARLIEST word of the description
decides, so "Fix review findings" is implement and "Review the fix" is review;
the prompt's first line is used only when there is no description):

` + tableHelp() + `
REFUSED: the model is missing (the environment default is sonnet, which hides the
choice), or its family is not the class's. Each refusal names the class, the
matched word and the expected model.

ALLOWED: a task in no class (any explicit model), subagent_type "fork" (it
ignores the model), a line "model-waiver: <reason>" in the prompt (the reason is
required and is echoed as a stamped notice), another tool than Agent, and a
payload that cannot be decoded (a refusal is not a verdict).

Exit status: 0 always with --hook (the answer is the JSON on stdout); --explain
exits 1 when the description is unclassified.`

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// RootCmd builds the command. Tests drive it through run.
func RootCmd() *cobra.Command {
	var hook bool
	var explain string
	c := cli.New("modelgate --hook | --explain <description>",
		"Refuse a sub-agent launched with the wrong model for its task", longHelp)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		switch {
		case hook:
			return cli.FromStatus(hookMode(cli.Out(cmd), cli.Err(cmd), cmd.InOrStdin()))
		case cmd.Flags().Changed("explain"):
			m := classify(explain)
			if m == nil {
				fmt.Fprintf(cli.Out(cmd), "unclassified: no word of %q is in the table; any explicit model is allowed\n", explain)
				return cli.FromStatus(1)
			}
			fmt.Fprintf(cli.Out(cmd), "class %s (matched %q): expected model %s\n", m.class.name, m.word, m.class.model)
			return nil
		}
		return cli.Unanswerable("nothing to do: give --hook or --explain <description>")
	}
	c.Flags().BoolVar(&hook, "hook", false,
		"read a PreToolUse payload on stdin and answer it: deny an Agent call whose model does not fit its task")
	c.Flags().StringVar(&explain, "explain", "",
		"print the class of a description and the model it expects")
	return c
}

func run(args []string, out, errOut io.Writer) int {
	return cli.Drive(RootCmd(), args, out, errOut)
}
