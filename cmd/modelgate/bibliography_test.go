package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func bibPayload(subtype, desc, prompt, cwd string) payload {
	var p payload
	p.ToolName, p.Cwd = "Agent", cwd
	p.ToolInput.SubagentType, p.ToolInput.Description, p.ToolInput.Prompt = subtype, desc, prompt
	p.ToolInput.Model = "sonnet"
	return p
}

// swapLedger points the ledger elsewhere and restores the real one afterwards.
func swapLedger(t *testing.T, f func() string) {
	t.Helper()
	orig := ledgerPath
	ledgerPath = f
	t.Cleanup(func() { ledgerPath = orig })
}

const pa = "\nprior-art: go-fleettools/fleettools, cloud-boot/iso"

func TestBibliographyGate(t *testing.T) {
	dir := t.TempDir()
	spec := filepath.Join(dir, "spec.md")
	if err := os.WriteFile(spec, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		p        payload
		deny     bool
		waivers  []waiver
		mentions []string
	}{
		{"url + fleet repos", bibPayload("implementer", "Add x", "Do it\nbibliography: https://example.org/spec (read)"+pa, dir), false, nil, nil},
		{"http url", bibPayload("implementer", "Add x", "bibliography: http://example.org/s"+pa, dir), false, nil, nil},
		{"bare scheme is no source", bibPayload("implementer", "Add x", "bibliography: https://"+pa, dir), true, nil, []string{"bibliography:"}},
		{"existing absolute file", bibPayload("implementer", "Add x", "bibliography: "+spec+pa, dir), false, nil, nil},
		{"existing relative file", bibPayload("implementer", "Add x", "bibliography: spec.md."+pa, dir), false, nil, nil},
		{"missing file", bibPayload("implementer", "Add x", "bibliography: "+filepath.Join(dir, "nope")+pa, dir), true, nil, []string{"bibliography:"}},
		{"no lines at all", bibPayload("implementer", "Add x", "Do it", dir), true, nil, []string{"bibliography:", "prior-art:"}},
		{"bibliography only", bibPayload("implementer", "Add x", "bibliography: https://e.org/s", dir), true, nil, []string{"prior-art:"}},
		{"prior-art only", bibPayload("implementer", "Add x", "prior-art: a/b", dir), true, nil, []string{"bibliography:"}},
		{"prior-art owner/repo is no bibliography", bibPayload("implementer", "Add x", "bibliography: a/b"+pa, dir), true, nil, []string{"bibliography:"}},
		{"prior-art word is no source", bibPayload("implementer", "Add x", "bibliography: https://e.org/s\nprior-art: nothing", dir), true, nil, []string{"prior-art:"}},
		{"prior-art url", bibPayload("implementer", "Add x", "bibliography: https://e.org/s\nprior-art: https://github.com/a/b", dir), false, nil, nil},
		{"none without reason", bibPayload("implementer", "Add x", "bibliography: none —"+pa, dir), true, nil, nil},
		{"none bare", bibPayload("implementer", "Add x", "bibliography: none"+pa, dir), true, nil, nil},
		{"none with reason", bibPayload("implementer", "Add x", "bibliography: none — internal tooling"+pa, dir), false, []waiver{{"bibliography", "internal tooling"}}, []string{"bibliography waived: internal tooling"}},
		{"none hyphen reason, any case", bibPayload("implementer", "Add x", "BIBLIOGRAPHY: none - internal"+pa, dir), false, []waiver{{"bibliography", "internal"}}, nil},
		{"prior-art none with reason", bibPayload("implementer", "Add x", "bibliography: https://e.org/s\nprior-art: none — new subject", dir), false, []waiver{{"prior-art", "new subject"}}, nil},
		{"a reason that looks like owner/repo is still a waiver", bibPayload("implementer", "Add x", "bibliography: https://e.org/s\nprior-art: none — internal/tooling", dir), false, []waiver{{"prior-art", "internal/tooling"}}, nil},
		{"both waived", bibPayload("implementer", "Add x", "bibliography: none — a\nprior-art: none — b", dir), false, []waiver{{"bibliography", "a"}, {"prior-art", "b"}}, nil},
		{"untyped implement refused", bibPayload("", "Fix the bug", "x", dir), true, nil, nil},
		{"general-purpose implement refused", bibPayload("general-purpose", "Fix the bug", "x", dir), true, nil, nil},
		{"untyped unclassified exempt", bibPayload("", "Deploy the bug", "x", dir), false, nil, nil},
		{"checker exempt", bibPayload("checker", "Add x", "x", dir), false, nil, nil},
		{"reviewer exempt", bibPayload("reviewer", "Add x", "x", dir), false, nil, nil},
		{"security exempt", bibPayload("security", "Add x", "x", dir), false, nil, nil},
		{"Explore exempt", bibPayload("Explore", "Add x", "x", dir), false, nil, nil},
		{"web-fetch exempt", bibPayload("web-fetch", "Add x", "x", dir), false, nil, nil},
		{"fork exempt", bibPayload("fork", "Add x", "x", dir), false, nil, nil},
		{"claude-code-guide exempt", bibPayload("claude-code-guide", "Add x", "x", dir), false, nil, nil},
		{"statusline-setup exempt", bibPayload("statusline-setup", "Add x", "x", dir), false, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, ws := decideAll(tc.p, noon)
			if v.deny != tc.deny || len(ws) != len(tc.waivers) {
				t.Fatalf("deny %v waivers %v: %q", v.deny, ws, v.reason)
			}
			for i := range ws {
				if ws[i] != tc.waivers[i] {
					t.Errorf("waiver %d: %v, want %v", i, ws[i], tc.waivers[i])
				}
			}
			if !tc.deny && len(tc.waivers) == 0 && tc.mentions == nil && v.reason != "" {
				t.Errorf("a clean allowance is silent: %q", v.reason)
			}
			if tc.deny && !strings.Contains(v.reason, "none —") {
				t.Errorf("refusal must say how to waive: %q", v.reason)
			}
			for _, m := range tc.mentions {
				if !strings.Contains(v.reason, m) {
					t.Errorf("reason %q lacks %q", v.reason, m)
				}
			}
		})
	}
}

func TestTheDefaultLedgerIsUnderTheUserConfigDir(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("HOME", cfg)
	t.Setenv("XDG_CONFIG_HOME", cfg)
	if got := ledgerPath(); filepath.Base(got) != "bibliography-waivers.jsonl" || !strings.HasPrefix(got, cfg) {
		t.Errorf("ledger path %q", got)
	}
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	if got := ledgerPath(); got != "" {
		t.Errorf("no config dir must give no path, got %q", got)
	}
}

func TestATildePathIsExpanded(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, "ref.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !sourceOpened("~/ref.md", "", false) {
		t.Error("~/ref.md exists under HOME")
	}
	if sourceOpened("~/absent.md", "", false) {
		t.Error("~/absent.md does not exist")
	}
}

func TestAModelRefusalWinsAndWaiversAreJoined(t *testing.T) {
	p := bibPayload("implementer", "Review x", "bibliography: none — r\nprior-art: none — s", "")
	p.ToolInput.Model = "haiku"
	if v, ws := decideAll(p, noon); !v.deny || !strings.Contains(v.reason, "class review") || ws != nil {
		t.Errorf("model refusal expected: %+v %v", v, ws)
	}
	p = bibPayload("implementer", "Add x", "model-waiver: why\nbibliography: none — r\nprior-art: none — s", "")
	v, ws := decideAll(p, noon)
	if v.deny || len(ws) != 2 || !strings.Contains(v.reason, "model waived") || !strings.Contains(v.reason, "bibliography waived: r") {
		t.Errorf("joined notices expected: %+v %v", v, ws)
	}
	// a model waiver alone, with both citations given, keeps its own notice
	p = bibPayload("implementer", "Add x", "model-waiver: why\nbibliography: https://e.org/s\nprior-art: a/b", "")
	if v, ws := decideAll(p, noon); v.deny || ws != nil || !strings.Contains(v.reason, "model waived") {
		t.Errorf("model notice must survive: %+v %v", v, ws)
	}
	p.ToolName = "Bash"
	if v, ws := decideAll(p, noon); v.deny || v.reason != "" || ws != nil {
		t.Errorf("another tool: %+v", v)
	}
}

func TestHookWritesTheLedgerOnAWaiver(t *testing.T) {
	ledger := filepath.Join(t.TempDir(), "sub", "w.jsonl")
	swapLedger(t, func() string { return ledger })
	clock = func() time.Time { return noon }
	t.Cleanup(func() { clock = time.Now })

	out, _, code := hook(t, mustJSON(t, "Add x", "Add the thing\nbibliography: none — internal tooling\nprior-art: none — new subject", "implementer", "sonnet"))
	if code != 0 {
		t.Fatal(code)
	}
	if d, _ := decision(t, out); d != "allow" {
		t.Errorf("decision %q", d)
	}
	b, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{`"reason":"internal tooling"`, `"kind":"bibliography"`, `"kind":"prior-art"`, `"reason":"new subject"`, `"prompt":"Add the thing"`, `"time":"2026-11-19T12:05`} {
		if !strings.Contains(string(b), w) {
			t.Errorf("ledger %q lacks %s", b, w)
		}
	}
	if n := strings.Count(string(b), "\n"); n != 2 {
		t.Errorf("one line per waiver, got %d", n)
	}

	// a refusal writes nothing
	out, _, _ = hook(t, mustJSON(t, "Add x", "no line", "implementer", "sonnet"))
	if d, r := decision(t, out); d != "deny" || !strings.Contains(r, "bibliography:") {
		t.Errorf("refusal expected: %q %q", d, r)
	}
	if b2, _ := os.ReadFile(ledger); string(b2) != string(b) {
		t.Errorf("a refusal must not write the ledger")
	}
}

func TestLedgerFailuresStillAllowAndSayWhy(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	adir := t.TempDir() // a directory cannot be opened for append
	for name, path := range map[string]string{
		"no config dir": "",
		"mkdir fails":   filepath.Join(blocker, "x", "w.jsonl"),
		"open fails":    adir,
	} {
		t.Run(name, func(t *testing.T) {
			swapLedger(t, func() string { return path })
			out, errOut, _ := hook(t, mustJSON(t, "Add x", "bibliography: none — r\nprior-art: a/b", "implementer", "sonnet"))
			if d, _ := decision(t, out); d != "allow" || !strings.Contains(errOut, "could not record the bibliography waiver") {
				t.Errorf("%q %q", d, errOut)
			}
			if err := appendLedger(noon, waiver{"k", "r"}, "p"); err == nil {
				t.Error("appendLedger must report the failure")
			}
		})
	}
}
