// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ⛔ THE BIBLIOGRAPHY AND PRIOR-ART GATE. "Bibliography before implementation"
// was a memory (feedback-bibliographie-avant) and was broken again on
// 2026-11-26: two days of a Windows-on-OpenStack image chain written from
// memory, briefs saying "priorart first" although priorart, run on ONE repo,
// reads neither the external reference (cloudbase/windows-openstack-imaging-
// tools) nor the other fleet repos that already covered the subject
// (cloud-boot/windows-image, cloud-boot/iso, go-diskimages).
//
// An implementation brief must carry TWO lines:
//
//	bibliography: <URL or existing file path of an EXTERNAL source that was opened>
//	prior-art:    <owner/repo of the fleet repositories read, or a URL or file>
//
// or, for either, "<key>: none — <reason>" (reason required, LEDGERED).
// Applies to subagent_type "implementer" and to untyped or "general-purpose"
// calls whose task class is implement; every other type is exempt.
// Tool input fields (prompt, description, subagent_type, model) are those of
// the Agent tool in https://code.claude.com/docs/en/hooks (PreToolUse input).

// ownerRepoRE is a GitHub repository name such as go-fleettools/fleettools.
var ownerRepoRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9._-]+$`)

// noneRE is "none", a separator (dash, en/em dash, colon) and a reason.
var noneRE = regexp.MustCompile(`(?i)^none[ \t]*(?:—|–|-+|:)[ \t]*(\S.*)$`)

// citation is one required line of a brief.
type citation struct {
	key    string
	repoOK bool // an owner/repo name counts as a source
	help   string
}

var citations = []citation{
	{"bibliography", false, `"bibliography: <URL or file of the spec, RFC or reference tool you OPENED>", e.g. "bibliography: https://github.com/cloudbase/windows-openstack-imaging-tools (README, Convert-WindowsImage)". priorart reads only our own repos and tracker, never the external reference`},
	{"prior-art", true, `"prior-art: <owner/repo of the fleet repositories you READ>", e.g. "prior-art: cloud-boot/windows-image, go-diskimages/diskimages". priorart reads one repository at a time, so search the other orgs (memory org-capability-map) too`},
}

// cites returns the text of every "<key>:" line of the prompt.
func cites(prompt, key string) []string {
	re := regexp.MustCompile(`(?mi)^[ \t]*` + regexp.QuoteMeta(key) + `:[ \t]*(.*?)[ \t]*$`)
	var out []string
	for _, m := range re.FindAllStringSubmatch(prompt, -1) {
		out = append(out, m[1])
	}
	return out
}

// bibliographyApplies says whether the gate covers this call.
func bibliographyApplies(p payload) bool {
	switch strings.TrimSpace(p.ToolInput.SubagentType) {
	case "implementer":
		return true
	case "", "general-purpose":
		m := classify(subject(p))
		return m != nil && m.class.name == "implement"
	}
	return false
}

// sourceOpened reports whether a free-text citation names a URL, an existing
// file (absolute, ~/..., or relative to cwd) or, when repoOK, an owner/repo.
func sourceOpened(cite, cwd string, repoOK bool) bool {
	for _, f := range strings.Fields(cite) {
		f = strings.TrimRight(strings.Trim(f, "()[]<>,;\"'`"), ".:")
		path := f
		if strings.HasPrefix(path, "~/") {
			home, _ := os.UserHomeDir()
			path = filepath.Join(home, path[2:])
		} else if cwd != "" && !filepath.IsAbs(path) {
			path = filepath.Join(cwd, path)
		}
		_, err := os.Stat(path)
		switch {
		case strings.HasPrefix(f, "https://") && len(f) > len("https://"):
			return true
		case strings.HasPrefix(f, "http://") && len(f) > len("http://"):
			return true
		case repoOK && ownerRepoRE.MatchString(f):
			return true
		case f != "" && filepath.IsAbs(path) && err == nil:
			return true
		}
	}
	return false
}

// waiver is a "none — reason" accepted for one citation key.
type waiver struct{ key, reason string }

// bibliographyVerdict applies the gate: every citation must be satisfied by a
// source or waived with a reason; the refusal names everything missing.
func bibliographyVerdict(p payload) (verdict, []waiver) {
	if !bibliographyApplies(p) {
		return verdict{}, nil
	}
	var waivers []waiver
	var missing []string
	for _, c := range citations {
		lines := cites(p.ToolInput.Prompt, c.key)
		ok := false
		for _, l := range lines {
			// a waiver's reason is prose: "internal/tooling" is not a source
			ok = ok || (!noneRE.MatchString(l) && sourceOpened(l, p.Cwd, c.repoOK))
		}
		if ok {
			continue
		}
		reason := ""
		for _, l := range lines {
			if n := noneRE.FindStringSubmatch(l); n != nil {
				reason = n[1]
			}
		}
		if reason == "" {
			missing = append(missing, c.help)
			continue
		}
		waivers = append(waivers, waiver{c.key, reason})
	}
	if len(missing) > 0 {
		return verdict{deny: true, reason: "modelgate refuses this implementation brief: add " +
			strings.Join(missing, "; and ") + `. If there is genuinely none, write "<key>: none — <why>" (the reason is required and goes in the waiver ledger).`}, nil
	}
	var notes []string
	for _, w := range waivers {
		notes = append(notes, w.key+" waived: "+w.reason)
	}
	if len(notes) == 0 {
		return verdict{}, nil
	}
	return verdict{reason: "modelgate: " + strings.Join(notes, "; ")}, waivers
}

// ledgerPath is where waivers are appended (os.UserConfigDir based); tests
// replace it. "" means the directory could not be determined.
var ledgerPath = func() string {
	d, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(d, "modelgate", "bibliography-waivers.jsonl")
}

// appendLedger records a waiver: timestamp, key, reason, first prompt line.
func appendLedger(now time.Time, w waiver, prompt string) error {
	path := ledgerPath()
	first, _, _ := strings.Cut(strings.TrimSpace(prompt), "\n")
	b, _ := json.Marshal(map[string]string{
		"time": now.Format(time.RFC3339), "kind": w.key, "reason": w.reason, "prompt": first})
	// Branch-free on purpose: a failed MkdirAll or OpenFile (and an empty path)
	// surfaces as the OpenFile error, and Write/Close on the nil *os.File it
	// leaves return os.ErrInvalid, documented behaviour of a nil receiver.
	merr := os.MkdirAll(filepath.Dir(path), 0o700)
	f, oerr := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	_, werr := f.Write(append(b, '\n'))
	return errors.Join(merr, oerr, werr, f.Close())
}

// decideAll is the model policy, then the bibliography gate. A model refusal
// wins; the notices of both allowances are joined.
func decideAll(p payload, now time.Time) (verdict, []waiver) {
	v := decide(p, now)
	if v.deny || p.ToolName != "Agent" || p.ToolInput.SubagentType == "fork" {
		return v, nil
	}
	bv, waivers := bibliographyVerdict(p)
	if iv := installVerdict(p); iv.deny {
		if bv.deny {
			bv.reason += "\n" + iv.reason
			return bv, nil
		}
		return iv, nil
	}
	if bv.reason != "" && v.reason != "" {
		bv.reason = v.reason + "; " + bv.reason
	}
	if bv.deny || bv.reason != "" {
		return bv, waivers
	}
	return v, nil
}
