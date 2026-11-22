package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
)

func classOf(text string) string {
	if m := classify(text); m != nil {
		return m.class.name
	}
	return "unclassified"
}

// ⛔ A WITNESS ON EACH SIDE OF EVERY BOUNDARY. A table of words is wrong in two
// directions: too narrow (a task is let through) and too broad ("fix" inside
// "prefix" refuses a haiku search). The accepting rows are the ones that catch
// the broad word.
func TestClassification(t *testing.T) {
	for _, tc := range []struct {
		text, class, word string
	}{
		// each class, on the word the brief names
		{"Review replystamp PR #256", "review", "review"},
		{"Audit the Pages workflow", "review", "audit"},
		{"Font selection bibliography", "review", "bibliography"},
		{"Design shared video codec kernels", "review", "design"},
		{"PS2 hybrid bank clean-room spec", "review", "spec"},
		{"Get a second opinion on the plan", "review", "second opinion"},
		{"Revue indépendante de la PR 122", "review", "revue"},
		{"Implement stun + TURN server", "implement", "implement"},
		{"Fix avkit#120 Matroska lacing bound", "implement", "fix"},
		{"Build the modfloor gate", "implement", "build"},
		{"Add turnd mint-credentials", "implement", "add"},
		{"Port the Kv routine", "implement", "port"},
		{"Release v0.4.0 of the tool", "implement", "release"},
		{"Bump fileshare to hfsplus v0.12.0", "implement", "bump"},
		{"Migrate Jekyll Pages to Hugo", "implement", "migrate"},
		{"Land gather kernels in go-simd/floats", "implement", "land"},
		{"Repair go-slides/vscode", "implement", "repair"},
		{"Bring go-turn org to fleet conformance", "implement", "bring"},
		{"Check curl CVEs fixed after 8.17.0", "check", "check"},
		{"Verify Ford dealers near Les Ulis", "check", "verify"},
		{"Inventory: SMB share + automount", "check", "inventory"},
		{"List the open PRs", "check", "list"},
		{"Find FreeType's signing key fingerprint", "check", "find"},
		{"Search the tracker for modelgate", "check", "search"},
		{"Read CTAP 2.1 spec sections", "check", "read"},
		{"Count the failing lanes", "check", "count"},

		// the EARLIEST word decides, in both directions
		{"Fix review findings on special PR #70", "implement", "fix"},
		{"Review the fix for #70", "review", "review"},
		{"Update design PR #75 to as-built", "implement", "update"},
		{"Design note for hfsplus#45", "review", "design"},
		{"Check docs#74 and landing#93 claims", "check", "check"},
		{"Docs and landing for v0.26.1", "implement", "docs"},
		{"Read the spec for the design", "check", "read"},

		// whole words: a word inside another is not a match
		{"Prefix table for the parser", "unclassified", ""},
		{"Preview the landing page layout", "implement", "landing"}, // "preview" is not review; landing is
		{"Preview of the page", "unclassified", ""},
		{"Reviewer roster", "unclassified", ""},
		{"Address the portal", "unclassified", ""}, // add in address, port in portal
		{"Listing of bundles", "unclassified", ""},
		{"Builder pattern", "unclassified", ""},
		{"Inspect the deployment", "unclassified", ""}, // "spec" inside inspect
		{"Specification of the wire format", "unclassified", ""},
		{"Designer handoff", "unclassified", ""},
		{"Readme tweaks", "unclassified", ""},
		{"Landing", "implement", "landing"},
		{"Fixture generator", "unclassified", ""},
		{"Closest match", "unclassified", ""},
		{"Wait", "check", "wait"},
		{"Waiter", "unclassified", ""},

		// a hyphen binds: the compound is not the word it ends in
		{"avkit#113 MP4 box-count bound", "unclassified", ""},
		{"Fact-check go-fft#159 doc claims", "unclassified", ""},
		{"pre-fix the table", "unclassified", ""},
		{"Third-party review of #430 rework", "review", "review"},
		{"Adversarial review of #429 round-2 fixes", "review", "review"},

		// case and punctuation do not matter
		{"REVIEW: PR 12", "review", "review"},
		{"(fix) the thing", "implement", "fix"},
		{"fix,review", "implement", "fix"},

		// nothing in the table
		{"", "unclassified", ""},
		{"turnd: host handover (#19)", "unclassified", ""},
		{"Measure cache reuse across deploys", "unclassified", ""},
		{"Write the testwhy -skipped gate", "unclassified", ""},
	} {
		m := classify(tc.text)
		got, word := "unclassified", ""
		if m != nil {
			got, word = m.class.name, m.word
		}
		if got != tc.class || word != tc.word {
			t.Errorf("classify(%q) = %s/%q, want %s/%q", tc.text, got, word, tc.class, tc.word)
		}
	}
}

// A phrase must be duplicate-free across classes, or class order would decide
// silently; and every word is lower case, or it could never match.
func TestTableIsWellFormed(t *testing.T) {
	seen := map[string]string{}
	for _, c := range classes {
		if family(c.model) != c.model {
			t.Errorf("class %s expects %q, which is not a model family", c.name, c.model)
		}
		for _, p := range c.phrase {
			if p != strings.ToLower(p) || strings.TrimSpace(p) != p {
				t.Errorf("%q in %s is not lower case and trimmed", p, c.name)
			}
			if o, dup := seen[p]; dup {
				t.Errorf("%q is in both %s and %s", p, o, c.name)
			}
			seen[p] = c.name
		}
	}
	if len(classes) != 4 {
		t.Fatalf("%d classes, want review, implement, check, security", len(classes))
	}
}

func TestFamily(t *testing.T) {
	for in, want := range map[string]string{
		"opus": "opus", "sonnet": "sonnet", "haiku": "haiku",
		"claude-opus-5": "opus", "claude-opus-5-5": "opus", "claude-sonnet-5-5": "sonnet",
		"claude-haiku-5-5": "haiku", "Claude-Opus-4-8": "opus",
		"fable": "fable", "claude-fable-5-2": "fable", "Claude-Fable-6": "fable",
		"": "", "inherit": "", "gpt-5": "",
	} {
		if got := family(in); got != want {
			t.Errorf("family(%q) = %q, want %q", in, got, want)
		}
	}
}

var noon = time.Date(2026, 11, 19, 12, 5, 0, 0, time.FixedZone("CET", 3600))

func agent(desc, prompt, subtype, model string) payload {
	var p payload
	p.ToolName = "Agent"
	p.ToolInput.Description = desc
	p.ToolInput.Prompt = prompt
	p.ToolInput.SubagentType = subtype
	p.ToolInput.Model = model
	return p
}

// The rule boundaries, one accepting and one refusing witness each.
func TestDecide(t *testing.T) {
	for _, tc := range []struct {
		name     string
		p        payload
		deny     bool
		contains []string // all must appear in the reason
	}{
		// (a) model missing
		{"missing model, classified", agent("Review PR 5", "", "", ""), true, []string{"no model given", "class review", `"review"`, `"opus"`}},
		{"missing model, unclassified", agent("turnd: host handover", "", "", ""), true, []string{"no model given", "sonnet"}},
		{"model given, unclassified", agent("turnd: host handover", "", "", "haiku"), false, nil},
		{"whitespace model is missing", agent("Review PR 5", "", "", "  "), true, []string{"no model given"}},

		// (b) the model does not match the class
		{"opus on implement", agent("Fix the parser", "", "", "opus"), true, []string{"class implement", `"fix"`, `"sonnet"`, `"opus"`}},
		{"sonnet on implement", agent("Fix the parser", "", "", "sonnet"), false, nil},
		{"haiku on implement", agent("Fix the parser", "", "", "haiku"), true, []string{"class implement", `"sonnet"`}},
		{"sonnet on check", agent("Check the lanes", "", "", "sonnet"), true, []string{"class check", `"check"`, `"haiku"`}},
		{"opus on check", agent("Check the lanes", "", "", "claude-opus-5-5"), true, []string{"class check", `"haiku"`}},
		{"haiku on check", agent("Check the lanes", "", "", "haiku"), false, nil},
		{"haiku on review", agent("Review PR 5", "", "", "haiku"), true, []string{"class review", `"opus"`}},
		{"sonnet on review", agent("Review PR 5", "", "", "sonnet"), true, []string{"class review", `"opus"`}},
		{"opus on review", agent("Review PR 5", "", "", "opus"), false, nil},
		{"full id of the right family", agent("Review PR 5", "", "", "claude-opus-4-8"), false, nil},
		{"unknown model on a classified task", agent("Review PR 5", "", "", "gpt-5"), true, []string{"unrecognised model"}},
		{"unknown model, unclassified task", agent("turnd: host handover", "", "", "gpt-5"), false, nil},

		// fable, the tier above opus
		{"fable on review, no waiver", agent("Review PR 5", "", "", "fable"), true, []string{"class review", "tier ABOVE opus", `"opus"`, "Weekly Fable", "model-waiver"}},
		{"full fable id on design", agent("Design the API", "", "", "claude-fable-5-2"), true, []string{"tier ABOVE opus"}},
		{"fable on review, waived", agent("Review PR 5", "model-waiver: opus missed the race twice\n", "", "fable"), false, []string{"waived", "opus missed the race twice"}},
		{"fable on implement", agent("Fix the parser", "", "", "fable"), true, []string{"class implement", `"sonnet"`, `"fable"`}},
		{"fable on check", agent("Check the lanes", "", "", "claude-fable-5-2"), true, []string{"class check", `"haiku"`}},
		{"fable on an unclassified task", agent("turnd: host handover", "", "", "fable"), false, nil},

		// the security class expects fable
		{"fable on security", agent("Security review of the parser", "", "", "fable"), false, nil},
		{"full fable id on a vulnerability", agent("Vulnerability triage", "", "", "claude-fable-5-2"), false, nil},
		{"opus on security", agent("Security review of the parser", "", "", "opus"), true, []string{"class security", `"security"`, `expects model "fable"`}},
		{"sonnet on a CVE", agent("CVE triage", "", "", "sonnet"), true, []string{"class security", `expects model "fable"`}},
		{"haiku on a threat-model", agent("threat-model of the daemon", "", "", "haiku"), true, []string{"class security", `"threat-model"`}},
		{"attack surface phrase", agent("Map the attack surface", "", "", "opus"), true, []string{"class security", `"attack surface"`}},
		{"security waived", agent("Security review of the parser", "model-waiver: budget\n", "", "opus"), false, []string{"waived", "budget"}},
		{"missing model on security", agent("Security review of the parser", "", "", ""), true, []string{"no model given", "class security", `"fable"`}},
		{"security bug fix stays implement", agent("Fix the security bug", "", "", "sonnet"), false, nil},
		{"fable on the security bug fix", agent("Fix the security bug", "", "", "fable"), true, []string{"class implement"}},
		{"review the security of X stays review", agent("Review the security of X", "", "", "opus"), false, nil},
		{"fable on review the security of X", agent("Review the security of X", "", "", "fable"), true, []string{"class review", "tier ABOVE opus"}},
		{"audit is not security", agent("Audit the fuzz harness", "", "", "opus"), false, nil},

		// the prompt's first line stands in for a missing description
		{"prompt fallback, refused", agent("", "Fix the parser\nlong body about review", "", "opus"), true, []string{"class implement"}},
		{"prompt fallback, ignores later lines", agent("", "turnd handover\nreview this", "", "haiku"), false, nil},
		{"description wins over the prompt", agent("Check the lanes", "Fix everything", "", "haiku"), false, nil},

		// fork ignores the model
		{"fork without a model", agent("Review PR 5", "", "fork", ""), false, nil},
		{"fork with the wrong model", agent("Check it", "", "fork", "opus"), false, nil},
		{"another subagent type is judged", agent("Check it", "", "general-purpose", "opus"), true, []string{"class check"}},

		// waiver
		{"waiver with a reason", agent("Check it", "model-waiver: needs judgement over the diff\nbody", "", "opus"), false, []string{"waived", "needs judgement over the diff", "2026-11-19 12:05 CET"}},
		{"waiver covers a missing model", agent("Check it", "x\nmodel-waiver: owner asked\n", "", ""), false, []string{"owner asked"}},
		{"waiver needs a reason", agent("Check it", "model-waiver:\nbody", "", "opus"), true, []string{"class check"}},
		{"waiver of blanks is no reason", agent("Check it", "model-waiver:    \n", "", "opus"), true, nil},
		{"waiver must start its line", agent("Check it", "see model-waiver: because", "", "opus"), true, nil},
		{"indented waiver counts", agent("Check it", "  model-waiver: because", "", "opus"), false, []string{"because"}},

		// other tools
		{"another tool", payload{ToolName: "Bash"}, false, nil},
		{"no tool", payload{}, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := decide(tc.p, noon)
			if v.deny != tc.deny {
				t.Fatalf("deny = %v, want %v (reason %q)", v.deny, tc.deny, v.reason)
			}
			for _, s := range tc.contains {
				if !strings.Contains(v.reason, s) {
					t.Errorf("reason %q lacks %q", v.reason, s)
				}
			}
		})
	}
}

func hook(t *testing.T, stdin string) (out, errOut string, code int) {
	t.Helper()
	var o, e bytes.Buffer
	c := RootCmd()
	c.SetIn(strings.NewReader(stdin))
	code = cli.Drive(c, []string{"--hook"}, &o, &e)
	return o.String(), e.String(), code
}

func decision(t *testing.T, out string) (string, string) {
	t.Helper()
	var r struct {
		H struct {
			Event    string `json:"hookEventName"`
			Decision string `json:"permissionDecision"`
			Reason   string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("stdout is not hook JSON: %q (%v)", out, err)
	}
	if r.H.Event != "PreToolUse" {
		t.Errorf("hookEventName = %q", r.H.Event)
	}
	return r.H.Decision, r.H.Reason
}

const agentJSON = `{"session_id":"s","tool_name":"Agent","tool_input":{"description":%q,"prompt":%q,"subagent_type":%q,"model":%q}}`

func TestHookAnswers(t *testing.T) {
	clock = func() time.Time { return noon }
	t.Cleanup(func() { clock = time.Now })

	// refusal: JSON on stdout, exit 0, nothing on stderr
	out, errOut, code := hook(t, mustJSON(t, "Review the config PR", "", "", "sonnet"))
	if code != 0 || errOut != "" {
		t.Fatalf("code %d stderr %q", code, errOut)
	}
	if d, r := decision(t, out); d != "deny" || !strings.Contains(r, "class review") || !strings.Contains(r, `"opus"`) {
		t.Errorf("decision %q reason %q", d, r)
	}

	// plain allowance: silent
	out, errOut, code = hook(t, mustJSON(t, "Review the config PR", "", "", "opus"))
	if out != "" || errOut != "" || code != 0 {
		t.Errorf("an allowed call must be silent: %q %q %d", out, errOut, code)
	}

	// another tool: silent
	out, errOut, code = hook(t, `{"tool_name":"Bash","tool_input":{"command":"ls"}}`)
	if out != "" || errOut != "" || code != 0 {
		t.Errorf("another tool must be silent: %q %q %d", out, errOut, code)
	}

	// waiver: allow with a stamped notice on both channels
	out, errOut, code = hook(t, mustJSON(t, "Check it", "model-waiver: judgement needed", "", "opus"))
	if d, r := decision(t, out); d != "allow" || !strings.Contains(r, "judgement needed") || !strings.Contains(r, "2026-11-19 12:05 CET") {
		t.Errorf("waiver: %q %q", d, r)
	}
	if !strings.Contains(errOut, "judgement needed") || code != 0 {
		t.Errorf("waiver notice on stderr: %q %d", errOut, code)
	}
}

func mustJSON(t *testing.T, desc, prompt, subtype, model string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"tool_name": "Agent",
		"tool_input": map[string]string{
			"description": desc, "prompt": prompt, "subagent_type": subtype, "model": model,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A refusal is not a verdict: whatever it cannot read, it allows and says so.
func TestUndecodablePayloadAllows(t *testing.T) {
	for name, in := range map[string]string{
		"empty":            "",
		"not json":         "Review the PR",
		"truncated":        `{"tool_name":"Agent","tool_input":{"description":"Rev`,
		"wrong type":       `{"tool_name":"Agent","tool_input":{"description":42}}`,
		"array":            `[1,2]`,
		"tool_input wrong": `{"tool_name":"Agent","tool_input":"x"}`,
		"nul bytes":        "\x00\x00\x00",
	} {
		t.Run(name, func(t *testing.T) {
			out, errOut, code := hook(t, in)
			if out != "" || code != 0 {
				t.Errorf("out %q code %d: must allow", out, code)
			}
			if !strings.Contains(errOut, "allowing (a refusal is not a verdict)") {
				t.Errorf("no notice on stderr: %q", errOut)
			}
		})
	}
	// Valid JSON with unknown or missing fields decodes: nothing to judge, allowed.
	if out, _, code := hook(t, `{}`); out != "" || code != 0 {
		t.Errorf("{} : %q %d", out, code)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, os.ErrClosed }

func TestUnreadableStdinAllows(t *testing.T) {
	var o, e bytes.Buffer
	if code := hookMode(&o, &e, failingReader{}); code != 0 || o.Len() != 0 || !strings.Contains(e.String(), "allowing") {
		t.Errorf("code %d out %q err %q", code, o.String(), e.String())
	}
}

func TestExplainAndUsage(t *testing.T) {
	var o, e bytes.Buffer
	if code := run([]string{"--explain", "Fix the parser"}, &o, &e); code != 0 || !strings.Contains(o.String(), "class implement") || !strings.Contains(o.String(), "sonnet") {
		t.Errorf("explain: %d %q", code, o.String())
	}
	o.Reset()
	if code := run([]string{"--explain", "turnd handover"}, &o, &e); code != 1 || !strings.Contains(o.String(), "unclassified") {
		t.Errorf("explain unclassified: %d %q", code, o.String())
	}
	e.Reset()
	if code := run(nil, &o, &e); code != 2 || !strings.Contains(e.String(), "--hook") {
		t.Errorf("no flags must be unanswerable: %d %q", code, e.String())
	}
	e.Reset()
	if code := run([]string{"-h"}, &o, &e); code != 0 {
		t.Errorf("-h = %d", code)
	}
	for _, want := range []string{"THE TABLE", "review", "implement", "check", "model-waiver", "fork"} {
		if !strings.Contains(e.String(), want) {
			t.Errorf("--help lacks %q", want)
		}
	}
	// the help prints the very table the code reads
	for _, c := range classes {
		for _, p := range c.phrase {
			if !strings.Contains(e.String(), p) {
				t.Errorf("--help lacks the table word %q", p)
			}
		}
	}
}

// ⛔ THE REAL DESCRIPTIONS. testdata/real-descriptions.tsv holds every distinct
// Agent description in `agentscope snapshot --horizon 168h --max-agents 200`
// on 2026-11-19 -- 534 of them, with the model each ran on -- so the table is
// refined against what is written and not against what was imagined.
//
// The brief asked for "every real description lands in a class or in
// unclassified": that is true of any function, so the test also pins HOW MANY
// land where. A change to the table that moves a real description is then a
// visible edit of these numbers, with the new words to defend.
func TestRealDescriptions(t *testing.T) {
	b, err := os.ReadFile("testdata/real-descriptions.tsv")
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	bad := map[string]int{}
	var refusedExamples []string
	n := 0
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		model, desc, ok := strings.Cut(l, "\t")
		if !ok || desc == "" {
			t.Fatalf("malformed line %q", l)
		}
		n++
		cl := classOf(desc)
		counts[cl]++
		v := decide(agent(desc, "", "", model), noon)
		if v.deny {
			bad[cl]++
			if len(refusedExamples) < 3 {
				refusedExamples = append(refusedExamples, v.reason)
			}
		}
	}
	// The security class (fleettools, 2026-11-22) took four rows: three
	// "Security review ..." rows that were review (opus) and "ghrepo verb:
	// private vuln reporting", which was unclassified.
	want := map[string]int{"review": 195, "implement": 122, "check": 39, "security": 4, "unclassified": 174}
	for k, w := range want {
		if counts[k] != w {
			t.Errorf("class %s: %d real descriptions, want %d", k, counts[k], w)
		}
	}
	if n != 534 {
		t.Errorf("%d descriptions, want 534", n)
	}
	// What the gate would have refused over those 534 calls: reviews run on a
	// cheaper model, and one inventory that was really a profile run.
	if bad["unclassified"] != 0 {
		t.Errorf("an unclassified task was refused: %v", bad)
	}
	// All four security rows ran below fable (three opus, one sonnet).
	if bad["check"] != 1 || bad["implement"] != 0 || bad["review"] != 22 || bad["security"] != 4 {
		t.Errorf("would-be refusals by class = %v, want review 22, check 1, implement 0, security 4", bad)
	}
	t.Logf("%d descriptions: %v; refused %v", n, counts, bad)
	for _, r := range refusedExamples {
		t.Log(r)
	}

	// Hand-read witnesses: each is a real description, and the class is what a
	// reader decides, not what the function says.
	for desc, want := range map[string]string{
		"Check curl CVEs fixed after 8.17.0":                 "check",
		"Read the OpenType spec on default instances":        "check",
		"Find GNU gawk 5.3.2 announcement":                   "check",
		"Wait for guardmut":                                  "check",
		"Build the modfloor gate":                            "implement",
		"Fix agentsync#27 mem-add/mem-verify":                "implement",
		"Bring go-turn org to fleet conformance":             "implement",
		"18 patch releases go 1.27.4":                        "implement",
		"Wait placeholder":                                   "check",
		"Review replystamp PR #256":                          "review",
		"Review Renovate config fix PR":                      "review",
		"Bibliography: passwordless SMB, non-domain Windows": "review",
		"Design: extract loom-server hosting":                "review",
		"Fix review findings on special PR #70":              "implement",
		"Update design PR #75 to as-built":                   "implement",
		"avkit#113 MP4 box-count bound":                      "unclassified",
		"turnd: relay grace + session renewal":               "unclassified",
		"Build modelgate Agent-call hook":                    "implement",
		"Security review of go-avkit/worker#1":               "security",
		"ghrepo verb: private vuln reporting":                "security",
	} {
		if got := classOf(desc); got != want {
			t.Errorf("%q: %s, want %s", desc, got, want)
		}
	}
}

// The decoder must never panic, and the answer must be one of three: nothing,
// a JSON decision, or a stderr notice -- with exit status 0 in every case.
func FuzzHook(f *testing.F) {
	for _, s := range []string{
		`{"tool_name":"Agent","tool_input":{"description":"Review PR 5","model":"haiku"}}`,
		`{"tool_name":"Agent","tool_input":{"description":"Check it","prompt":"model-waiver: x","model":"opus"}}`,
		`{"tool_name":"Agent","tool_input":{"subagent_type":"fork"}}`,
		`{"tool_name":"Agent","tool_input":{"prompt":"\n\n"}}`,
		`{"tool_name":"Bash"}`, `{"tool_input":null}`, `[]`, ``, `null`, "\xff\xfe",
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		var o, e bytes.Buffer
		if code := hookMode(&o, &e, bytes.NewReader(in)); code != 0 {
			t.Fatalf("exit %d", code)
		}
		if o.Len() > 0 {
			var v struct {
				H struct {
					D string `json:"permissionDecision"`
				} `json:"hookSpecificOutput"`
			}
			if err := json.Unmarshal(o.Bytes(), &v); err != nil || (v.H.D != "deny" && v.H.D != "allow") {
				t.Fatalf("stdout %q is not a decision", o.String())
			}
		}
		if _, err := decodePayload(in); err != nil && o.Len() != 0 {
			t.Fatalf("an undecodable payload produced a decision: %q", o.String())
		}
	})
}
