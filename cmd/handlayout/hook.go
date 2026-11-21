// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
)

type payload struct {
	ToolName  string `json:"tool_name"`
	Cwd       string `json:"cwd"`
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
}

var segmentSplit = regexp.MustCompile(`&&|\|\||[;&|\n]`)
var redirect = regexp.MustCompile(`^\d*[<>]`)
var envAssign = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// pushDir returns the directory a push in command runs in, and whether the
// command pushes at all: `gitpush ...` or `git push ...`, after any `cd`.
func pushDir(command, cwd string) (string, bool) {
	dir := cwd
	for _, seg := range segmentSplit.Split(command, -1) {
		f := strings.Fields(seg)
		for len(f) > 0 && (envAssign.MatchString(f[0]) || redirect.MatchString(f[0])) {
			f = f[1:]
		}
		if len(f) == 0 {
			continue
		}
		prog := filepath.Base(f[0])
		switch {
		case prog == "cd" && len(f) > 1:
			d := strings.Trim(f[1], `"'`)
			if filepath.IsAbs(d) {
				dir = d
			} else {
				dir = filepath.Join(dir, d)
			}
		case prog == "gitpush":
			return dir, true
		case prog == "git":
			d := dir
			for i := 1; i < len(f); i++ {
				switch w := f[i]; {
				case w == "-C" && i+1 < len(f):
					i++
					if p := strings.Trim(f[i], `"'`); filepath.IsAbs(p) {
						d = p
					} else {
						d = filepath.Join(d, p)
					}
				case w == "push":
					return d, true
				}
			}
		}
	}
	return dir, false
}

// decision is a hook answer.
type decision struct {
	deny   bool
	reason string // the refusal, or the notice of an allowance; "" = silent allow
}

// decide is the whole policy. Only a definite, unwaived finding denies.
func decide(p payload) decision {
	if p.ToolName != "Bash" {
		return decision{}
	}
	dir, ok := pushDir(p.ToolInput.Command, p.Cwd)
	if !ok {
		return decision{}
	}
	top, err := git(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return decision{reason: "handlayout: could not find the repository of this push (" + oneLine(err) + "); allowing, not counted as clean"}
	}
	top = strings.TrimSpace(top)
	base, err := pushBase(top)
	if err != nil {
		// Whether this is a consumer is still worth knowing: say nothing about
		// a repository that is not one.
		if c, _, _ := moduleKind(top); !c {
			return decision{}
		}
		return decision{reason: "handlayout: " + oneLine(err) + "; allowing the push, not counted as clean"}
	}
	r, err := checkRange(top, base)
	if err != nil {
		return decision{reason: "handlayout: could not read the pushed range (" + oneLine(err) + "); allowing, not counted as clean"}
	}
	var b strings.Builder
	for _, f := range r.Added {
		fmt.Fprintf(&b, "  %s\n", f)
	}
	switch {
	case len(r.Added) > 0 && len(r.Waivers) > 0:
		return decision{reason: fmt.Sprintf("handlayout: %d finding(s) WAIVED: %s\n%s", len(r.Added), strings.Join(r.Waivers, " | "), b.String())}
	case len(r.Added) > 0:
		seen := map[string]bool{}
		var adv strings.Builder
		for _, f := range r.Added {
			if !seen[f.Detector] {
				seen[f.Detector] = true
				fmt.Fprintf(&adv, "\n%s: %s.", f.Detector, advice[f.Detector])
			}
		}
		return decision{deny: true, reason: fmt.Sprintf(
			"handlayout refuses this push: it adds %d hand-written layout/drawing site(s) to a go-widgets application (%s):\n%s%s\nIf this is deliberate, put a line \"handlayout-waiver: <reason>\" in a commit message of the range; the reason is printed.",
			len(r.Added), counts(r.Added), b.String(), adv.String())}
	case len(r.Unreadly) > 0:
		return decision{reason: "handlayout: could not read " + strings.Join(r.Unreadly, "; ") + "; allowing, not counted as clean"}
	}
	return decision{}
}

func oneLine(err error) string {
	return strings.Join(strings.Fields(err.Error()), " ")
}

// hookMode answers a PreToolUse payload; everything it cannot read allows and
// says so.
func hookMode(out, errOut io.Writer, stdin io.Reader) int {
	b, err := io.ReadAll(stdin)
	if err != nil {
		fmt.Fprintln(errOut, "handlayout: could not read the hook payload; allowing (a refusal is not a verdict)")
		return 0
	}
	var p payload
	if err := json.Unmarshal(b, &p); err != nil {
		fmt.Fprintf(errOut, "handlayout: could not decode the hook payload (%v); allowing (a refusal is not a verdict)\n", err)
		return 0
	}
	d := decide(p)
	switch {
	case d.deny:
		writeDecision(out, "deny", d.reason)
	case d.reason != "":
		fmt.Fprintln(errOut, d.reason)
		writeDecision(out, "allow", d.reason)
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
