package main

import (
	"strings"
	"testing"
)

const (
	okGobin = "GOBIN=~/.local/bin go install github.com/go-agentsync/agentscope/cmd/agentscope@v0.15.2"
	okCheck = "go version -m ~/.local/bin/agentscope"
)

func TestInstallGate(t *testing.T) {
	// The brief shape of 2026-11-28 (WebM agent), which was followed to the letter.
	webm := "Implement WebM muxing in agentscope and release it.\nbibliography: https://www.webmproject.org/docs/container/\nprior-art: go-agentsync/agentscope\n" +
		"Ship with prwait then ghrelease. Install it and check `go version -m`, and quote the mod line."
	for _, tc := range []struct {
		name     string
		subtype  string
		desc     string
		prompt   string
		deny     bool
		mentions []string
	}{
		{"WebM brief of 2026-11-28", "implementer", "Add WebM", webm, true, []string{"GOBIN=~/.local/bin", "go version -m ~/.local/bin/<cmd>"}},
		{"private GOBIN only", "implementer", "Add x", "Run GOBIN=/Users/x/localhost/agentscope-bin go install a/b/cmd/c@v1\ngo version -m /Users/x/localhost/agentscope-bin/c", true, []string{"GOBIN=~/.local/bin", "go version -m ~/.local/bin/<cmd>"}},
		{"gobin named, check missing", "general-purpose", "Ship x", okGobin, true, []string{"go version -m ~/.local/bin/<cmd>"}},
		{"check named, gobin missing", "claude", "Ship x", "go install a/b/cmd/c@v1\n" + okCheck, true, []string{"GOBIN=~/.local/bin"}},
		{"both lines", "implementer", "Add x", okGobin + "\n" + okCheck, false, nil},
		{"absolute path", "claude", "Ship x", "GOBIN=/Users/x/.local/bin go install a/b/cmd/c@v1\ngo version -m /Users/x/.local/bin/c", false, nil},
		{"$HOME and quotes", "", "Ship x", "GOBIN=\"$HOME/.local/bin\" go install a/b/cmd/c@v1\ngo version -m $HOME/.local/bin/c", false, nil},
		{"lookalike dir is no PATH dir", "claude", "Ship x", "GOBIN=~/.local/bin-old go install a/b/cmd/c@v1\ngo version -m ~/.local/bin/c", true, []string{"GOBIN=~/.local/bin"}},
		{"installed binary phrase", "general-purpose", "Ship x", "Then verify the installed binary.", true, nil},
		{"install the tool", "", "Ship x", "Install the tool afterwards.", true, nil},
		{"do not install", "implementer", "Add x", "Do not install anything.", false, nil},
		{"never go install", "general-purpose", "Ship x", "never go install in this task", false, nil},
		{"don't install it", "claude", "Ship x", "Don't install it, only build.", false, nil},
		{"prose without an ask", "implementer", "Add x", "The installation guide is in README; the installer package is separate.", false, nil},
		{"install in another word", "implementer", "Add x", "Fix the uninstall path and reinstall hooks.", false, nil},
		{"exempt agent type", "checker", "Ship x", "go install a/b/cmd/c@v1", false, nil},
		{"fork is exempt", "fork", "Ship x", "go install a/b/cmd/c@v1", false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := bibPayload(tc.subtype, tc.desc, tc.prompt, "")
			if tc.subtype == "implementer" {
				p.ToolInput.Prompt += "\nbibliography: none — own format\nprior-art: none — own format"
			}
			v, _ := decideAll(p, noon)
			if v.deny != tc.deny {
				t.Fatalf("deny = %v, want %v: %s", v.deny, tc.deny, v.reason)
			}
			for _, m := range tc.mentions {
				if !strings.Contains(v.reason, m) {
					t.Errorf("refusal lacks %q: %s", m, v.reason)
				}
			}
			if tc.deny && !strings.Contains(v.reason, installLines) {
				t.Errorf("refusal does not quote the two lines verbatim: %s", v.reason)
			}
		})
	}
}

// A refusal for both gates names both.
func TestInstallGateJoinsBibliography(t *testing.T) {
	p := bibPayload("implementer", "Add x", "Add x and go install it", "")
	v, ws := decideAll(p, noon)
	if !v.deny || ws != nil || !strings.Contains(v.reason, "bibliography:") || !strings.Contains(v.reason, "GOBIN=~/.local/bin") {
		t.Fatalf("want one refusal naming both gates, got %+v %v", v, ws)
	}
}

// A model refusal still wins over the install gate.
func TestInstallGateModelWins(t *testing.T) {
	p := bibPayload("claude", "Review x", "go install a/b/cmd/c@v1", "")
	if v, _ := decideAll(p, noon); !v.deny || !strings.Contains(v.reason, "class review") {
		t.Fatalf("model refusal expected first: %s", v.reason)
	}
}

// End to end through the hook, as Claude Code calls it.
func TestInstallGateHook(t *testing.T) {
	out, _, _ := hook(t, mustJSON(t, "Add x", "Install it and check go version -m", "claude", "sonnet"))
	d, r := decision(t, out)
	if d != "deny" || !strings.Contains(r, "go version -m ~/.local/bin/<cmd>") {
		t.Fatalf("decision %q reason %q", d, r)
	}
}
