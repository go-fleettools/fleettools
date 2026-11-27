package main

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// The parsers read text the model and GitHub wrote. Whatever it is, they must
// not panic, must not hang, and must agree with themselves.

func FuzzClaims(f *testing.F) {
	for _, s := range []string{
		"PR #12 is merged.", "La PR #12 a été fusionnée.", "not merged yet", "pas encore fusionnée",
		"`merged`", "\"PR #1 merged\"", "« fusionné »", "> merged\n", "```\nmerged\n", "owner/repo#12 merged",
		"squash-merged", "--merged", "the merged result", "\x00#99999999999999999999 merged",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		a := claimsIn(s)
		b := claimsIn(s)
		if len(a) != len(b) {
			t.Fatal("claimsIn is not deterministic")
		}
		for _, c := range a {
			if !utf8.ValidString(c.sentence) && utf8.ValidString(s) {
				t.Fatalf("a claim sentence cut a rune: %q", c.sentence)
			}
			for _, r := range c.refs {
				if r.number < 0 {
					t.Fatalf("negative number %d", r.number)
				}
			}
		}
		var tt turn
		tt.text = s
		_ = tt.judge()
	})
}

func FuzzReads(f *testing.F) {
	f.Add("gh pr view 12 --json state", `{"state":"MERGED"}`)
	f.Add("ghmerge go-x/y 12", "merged\n")
	f.Add("gh api repos/o/r/pulls/3", `{"merged":true}`)
	f.Add("gh pr list -R o/r --json number,state", `[{"number":1,"state":"OPEN"}]`)
	f.Add("gh pr list", "12\ttitle\tbranch\tMERGED\t2026-01-01\n")
	f.Add("gh pr view", "state:\tMERGED\n")
	f.Add("'gh' 'pr' 'view' && ghrelease", "")
	f.Fuzz(func(t *testing.T, command, output string) {
		for _, r := range readsOf(call{command: command, output: output}) {
			if r.state != "MERGED" && r.state != "OPEN" && r.state != "CLOSED" && strings.ToUpper(r.state) != r.state {
				t.Fatalf("odd state %q", r.state)
			}
			if r.ref.number < 0 {
				t.Fatalf("negative number %d", r.ref.number)
			}
		}
	})
}

func FuzzTranscript(f *testing.F) {
	f.Add(`{"type":"user","message":{"role":"user","content":"hi"}}` + "\n" +
		`{"type":"assistant","uuid":"a","message":{"role":"assistant","content":[{"type":"text","text":"PR #1 merged"},{"type":"tool_use","id":"t","name":"Bash","input":{"command":"gh pr view 1 --json state"}}]}}` + "\n" +
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":[{"type":"text","text":"{\"state\":\"MERGED\"}"}]}]}}` + "\n")
	f.Add("{")
	f.Add("")
	f.Add(`{"type":"user","message":null}` + "\n" + `{"type":"assistant","message":{"content":"x"}}`)
	f.Fuzz(func(t *testing.T, s string) {
		tt, _, err := turnOf(strings.NewReader(s))
		if err != nil {
			return
		}
		_ = tt.judge()
	})
}
