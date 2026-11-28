// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"regexp"
	"strings"
)

// ⛔ THE INSTALL-PATH GATE. On 2026-11-28 an agent was told "Install it and
// check `go version -m`", twice. Both times it ran
// `GOBIN=…/go-agentsync-agentscope-bin go install …`, checked THAT copy and
// truthfully reported "installed v0.15.x" while ~/.local/bin/agentscope, the
// binary on PATH, still held the previous build (memory
// an-install-check-must-name-the-path-binary). "Installed" means "what the
// user runs is this version"; a check on another copy agrees by construction.
//
// A brief that asks for a binary to be installed must therefore carry both
//
//	GOBIN=~/.local/bin go install <module>/cmd/<c>@<tag>
//	go version -m ~/.local/bin/<c>
//
// (an absolute path ending in /.local/bin counts). Applies to subagent_type
// implementer, claude, general-purpose and untyped calls, whatever the task
// class: the request to install is the signal, not the verb of the description.
// A line that forbids installing ("do not install", "never install") is not a
// request. The PreToolUse input fields are those of the Agent tool in
// https://code.claude.com/docs/en/hooks.

var (
	// installAskRE is a request to put a binary in place.
	installAskRE = regexp.MustCompile(`(?i)\bgo[ \t]+install\b|\binstall[ \t]+(?:it|them|this|that|the[ \t]+(?:binary|binaries|tool|release|build|cli|program))\b|\binstalled[ \t]+(?:binary|binaries|build|version)\b`)
	// installNoRE is a line that forbids installing; it is not a request.
	installNoRE = regexp.MustCompile(`(?i)\b(?:do[ \t]+not|don't|dont|never|must[ \t]+not|no|not|without)[ \t]+(?:\w+[ \t]+)?(?:go[ \t]+)?install`)
	// pathGobinRE is GOBIN pointed at ~/.local/bin ($HOME, ~ or absolute).
	pathGobinRE = regexp.MustCompile(`GOBIN=["']?(?:~|\$HOME|\$\{HOME\}|/[^\s"']*?)/\.local/bin["']?(?:[\s;&)\x60]|$)`)
	// pathCheckRE is go version -m on a binary under .local/bin.
	pathCheckRE = regexp.MustCompile(`go[ \t]+version[ \t]+-m[ \t]+["']?(?:~|\$HOME|\$\{HOME\}|/[^\s"']*?)/\.local/bin/[A-Za-z0-9._-]+`)
)

// installApplies says whether the gate covers this call.
func installApplies(p payload) bool {
	switch strings.TrimSpace(p.ToolInput.SubagentType) {
	case "implementer", "claude", "general-purpose", "":
		return true
	}
	return false
}

// asksInstall reports whether some line of the prompt requests an install.
func asksInstall(prompt string) bool {
	for _, l := range strings.Split(prompt, "\n") {
		if installAskRE.MatchString(l) && !installNoRE.MatchString(l) {
			return true
		}
	}
	return false
}

// installLines are the two lines a refusal asks to add, verbatim.
const installLines = "GOBIN=~/.local/bin go install <module>/cmd/<cmd>@<tag>\n" +
	"go version -m ~/.local/bin/<cmd>"

// installVerdict applies the gate.
func installVerdict(p payload) verdict {
	if !installApplies(p) || !asksInstall(p.ToolInput.Prompt) {
		return verdict{}
	}
	pr := p.ToolInput.Prompt
	var missing []string
	if !pathGobinRE.MatchString(pr) {
		missing = append(missing, "GOBIN=~/.local/bin")
	}
	if !pathCheckRE.MatchString(pr) {
		missing = append(missing, "go version -m ~/.local/bin/<cmd>")
	}
	if len(missing) == 0 {
		return verdict{}
	}
	return verdict{deny: true, reason: "modelgate refuses this brief: it asks for a binary to be installed but does not name the PATH binary (missing: " +
		strings.Join(missing, ", ") + "). An agent that installs into a private GOBIN and checks that copy reports \"installed\" while the binary the user runs is older (2026-11-28, twice). Add these two lines to the prompt, with the real module, command and tag:\n" +
		installLines + "\nand ask the report to QUOTE the mod line."}
}
