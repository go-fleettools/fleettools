package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// step is one thing that happens in a fixture turn.
type step struct {
	text    string // an assistant text block
	command string // a Bash call ...
	output  string // ... and what it printed
}

func jsonl(t *testing.T, entries ...map[string]any) string {
	t.Helper()
	var b strings.Builder
	for _, e := range entries {
		line, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// transcriptOf builds a transcript: an earlier turn, the user's last message,
// then the steps.
func transcriptOf(t *testing.T, steps ...step) string {
	t.Helper()
	es := []map[string]any{
		{"type": "user", "uuid": "u0", "message": map[string]any{"role": "user", "content": "earlier"}},
		{"type": "assistant", "uuid": "a0", "message": map[string]any{"role": "assistant",
			"content": []any{map[string]any{"type": "text", "text": "PR #1 is merged."}}}},
		{"type": "user", "uuid": "u1", "message": map[string]any{"role": "user", "content": "go on"}},
	}
	for i, s := range steps {
		id := fmt.Sprintf("toolu_%d", i)
		switch {
		case s.text != "":
			es = append(es, map[string]any{"type": "assistant", "uuid": fmt.Sprintf("a%d", i+1),
				"message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": s.text}}}})
		case s.command != "":
			es = append(es,
				map[string]any{"type": "assistant", "uuid": fmt.Sprintf("a%d", i+1),
					"message": map[string]any{"role": "assistant", "content": []any{
						map[string]any{"type": "tool_use", "id": id, "name": "Bash", "input": map[string]any{"command": s.command}}}}},
				map[string]any{"type": "user", "uuid": fmt.Sprintf("r%d", i+1),
					"message": map[string]any{"role": "user", "content": []any{
						map[string]any{"type": "tool_result", "tool_use_id": id, "content": s.output}}}})
		}
	}
	return jsonl(t, es...)
}

func judgeText(t *testing.T, content string) string {
	t.Helper()
	tt, sawUser, err := turnOf(strings.NewReader(content))
	if err != nil || !sawUser {
		t.Fatalf("turnOf: %v %v", sawUser, err)
	}
	return tt.judge()
}

func TestJudge(t *testing.T) {
	viewMerged := step{command: "gh pr view 12 -R go-x/y --json state", output: `{"state":"MERGED"}`}
	cases := []struct {
		name   string
		steps  []step
		refuse bool
		want   string // a substring of the reason
	}{
		{"claim + read MERGED", []step{viewMerged, {text: "PR #12 is merged."}}, false, ""},
		{"claim + read MERGED, number from output", []step{
			{command: "gh pr view --json number,state", output: `{"number":12,"state":"MERGED"}`},
			{text: "go-x/y#12 is merged."}}, false, ""},
		{"claim + read MERGED, jq", []step{
			{command: "gh pr view 12 --json state -q .state", output: "MERGED\n"},
			{text: "PR 12 is merged."}}, false, ""},
		{"claim + gh api merged true", []step{
			{command: "gh api repos/go-x/y/pulls/12", output: `{"number":12,"state":"closed","merged":true}`},
			{text: "go-x/y#12 is merged."}}, false, ""},
		{"claim + ghmerge", []step{
			{command: "ghmerge go-x/y 12", output: "merged\n"},
			{text: "PR #12 est fusionnée."}}, false, ""},
		{"claim + ghrelease already merged", []step{
			{command: "ghrelease go-x/y 12 v0.3.0", output: "go-x/y#12 is already merged\n"},
			{text: "#12 is merged and tagged."}}, false, ""},
		{"claim + read OPEN", []step{
			{command: "gh pr view 12 --json state", output: `{"state":"OPEN"}`},
			{text: "PR #12 is merged."}}, true, "OPEN"},
		{"OPEN then ghmerge: the later read wins", []step{
			{command: "gh pr view 12 --json state", output: `{"state":"OPEN"}`},
			{command: "ghmerge go-x/y 12", output: "merged\n"},
			{text: "PR #12 is merged."}}, false, ""},
		{"MERGED then reopened read", []step{
			viewMerged,
			{command: "gh pr view 12 --json state", output: `{"state":"OPEN"}`},
			{text: "PR #12 is merged."}}, true, "OPEN"},
		{"claim with no read", []step{{text: "PR #12 is merged."}}, true, "no call of this turn read"},
		{"claim with no read, French", []step{{text: "La PR #12 a été fusionnée."}}, true, "no call of this turn read"},
		{"claim naming #12 while the turn read #13", []step{
			{command: "gh pr view 13 --json state", output: `{"state":"MERGED"}`},
			{text: "PR #12 is merged."}}, true, "#12"},
		{"wrong repo", []step{
			{command: "gh pr view 12 -R go-x/other --json state", output: `{"state":"MERGED"}`},
			{text: "go-x/y#12 is merged."}}, true, "go-x/y#12"},
		{"prwait is not a read", []step{
			{command: "prwait -repo go-x/y 12", output: "PASS SUCCESS:3\nstate MERGED\n"},
			{text: "PR #12 is merged."}}, true, "prwait"},
		{"an echo of MERGED is not a read", []step{
			{command: `echo '{"state":"MERGED"}'`, output: `{"state":"MERGED"}`},
			{text: "PR #12 is merged."}}, true, "no call"},
		{"a read before the user's last message does not count", nil, true, ""},
		{"no number, exactly one merged read", []step{viewMerged, {text: "C'est fusionné."}}, false, ""},
		{"no number, nothing read", []step{{text: "It is merged."}}, true, "without naming"},
		{"no number, two merged reads", []step{
			viewMerged,
			{command: "gh pr view 13 --json state", output: `{"state":"MERGED"}`},
			{text: "It is merged."}}, true, "2 pull request"},
		{"not merged yet", []step{{text: "PR #12 is not merged yet."}}, false, ""},
		{"pas encore fusionnée", []step{{text: "La PR #12 n'est pas encore fusionnée."}}, false, ""},
		{"future", []step{{text: "PR #12 sera fusionnée après la CI."}}, false, ""},
		{"condition", []step{{text: "Once PR #12 is merged, I tag."}}, false, ""},
		{"question", []step{{text: "Is PR #12 merged?"}}, false, ""},
		{"quoted", []step{{text: `The gate refuses "PR #12 is merged" with no read.`}}, false, ""},
		{"code span", []step{{text: "It greps for `PR #12 merged` in the log."}}, false, ""},
		{"fenced code", []step{{text: "Output:\n```\nPR #12 merged\n```\nDone."}}, false, ""},
		{"block quote", []step{{text: "> PR #12 merged\nThat is what it said."}}, false, ""},
		{"adjective", []step{{text: "The merged result is in out.txt."}}, false, ""},
		{"git flag", []step{{text: "I ran git branch --merged on it."}}, false, ""},
		{"closes an issue is not the claimed PR", []step{
			viewMerged,
			{text: "PR #12 is merged, closes #5."}}, false, ""},
		{"no claim at all", []step{{text: "PR #12 is green."}}, false, ""},
		{"merged in an earlier text of the turn", []step{
			{text: "PR #12 is merged."},
			{command: "ls", output: "x"},
			{text: "Now the tag."}}, true, "no call"},
		{"MERGED in capitals", []step{{text: "PR #12: MERGED"}}, true, "no call"},
		{"squash-merged", []step{{text: "Squash-merged PR #12."}}, true, "no call"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			steps := c.steps
			var content string
			if c.name == "a read before the user's last message does not count" {
				content = jsonl(t,
					map[string]any{"type": "user", "uuid": "u0", "message": map[string]any{"role": "user", "content": "first"}},
					map[string]any{"type": "assistant", "uuid": "a0", "message": map[string]any{"role": "assistant", "content": []any{
						map[string]any{"type": "tool_use", "id": "t0", "name": "Bash", "input": map[string]any{"command": "gh pr view 12 --json state"}}}}},
					map[string]any{"type": "user", "uuid": "r0", "message": map[string]any{"role": "user", "content": []any{
						map[string]any{"type": "tool_result", "tool_use_id": "t0", "content": `{"state":"MERGED"}`}}}},
					map[string]any{"type": "user", "uuid": "u1", "message": map[string]any{"role": "user", "content": "and now?"}},
					map[string]any{"type": "assistant", "uuid": "a1", "message": map[string]any{"role": "assistant", "content": []any{
						map[string]any{"type": "text", "text": "PR #12 is merged."}}}})
			} else {
				content = transcriptOf(t, steps...)
			}
			got := judgeText(t, content)
			if (got != "") != c.refuse {
				t.Fatalf("refused=%v, want %v\nreason: %s", got != "", c.refuse, got)
			}
			if c.want != "" && !strings.Contains(got, c.want) {
				t.Fatalf("reason lacks %q:\n%s", c.want, got)
			}
		})
	}
}

func TestSubagentAndAPIErrorAreNotTheSessionsReply(t *testing.T) {
	content := jsonl(t,
		map[string]any{"type": "user", "uuid": "u1", "message": map[string]any{"role": "user", "content": "go"}},
		map[string]any{"type": "assistant", "uuid": "a1", "isSidechain": true, "message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "text", "text": "PR #12 is merged."}}}},
		map[string]any{"type": "assistant", "uuid": "a2", "isApiErrorMessage": true, "message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "text", "text": "PR #12 is merged. You've hit your limit"}}}},
		map[string]any{"type": "assistant", "uuid": "a3", "message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "text", "text": "Working on it."}}}})
	if got := judgeText(t, content); got != "" {
		t.Fatalf("refused a reply the session did not write: %s", got)
	}
}

func hook(t *testing.T, dir, session, transcript string, active bool, agent string) (stdout, stderr string) {
	t.Helper()
	stateDir = func() (string, error) { return filepath.Join(dir, "state"), nil }
	p, _ := json.Marshal(map[string]any{"session_id": session, "transcript_path": transcript, "stop_hook_active": active, "agent_id": agent})
	var out, errb bytes.Buffer
	c := RootCmd()
	c.SetIn(bytes.NewReader(p))
	c.SetOut(&out)
	c.SetErr(&errb)
	c.SetArgs([]string{"--hook"})
	if err := c.Execute(); err != nil {
		t.Fatalf("hook: %v", err)
	}
	return out.String(), errb.String()
}

func TestHookProtocol(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.jsonl")
	good := filepath.Join(dir, "good.jsonl")
	if err := os.WriteFile(bad, []byte(transcriptOf(t, step{text: "PR #12 is merged."})), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(good, []byte(transcriptOf(t,
		step{command: "gh pr view 12 --json state", output: `{"state":"MERGED"}`},
		step{text: "PR #12 is merged."})), 0o600); err != nil {
		t.Fatal(err)
	}

	if out, _ := hook(t, dir, "s1", good, false, ""); strings.TrimSpace(out) != "{}" {
		t.Fatalf("a read claim must be allowed, got %s", out)
	}
	out, _ := hook(t, dir, "s1", bad, false, "")
	if !strings.Contains(out, `"decision":"block"`) {
		t.Fatalf("an unread claim must be blocked, got %s", out)
	}
	// Once per reply.
	if out, errs := hook(t, dir, "s1", bad, false, ""); strings.TrimSpace(out) != "{}" || !strings.Contains(errs, "already refused") {
		t.Fatalf("a reply is refused once: %s / %s", out, errs)
	}
	// A Stop that follows a block stands down.
	if out, _ := hook(t, dir, "s2", bad, true, ""); strings.TrimSpace(out) != "{}" {
		t.Fatalf("stop_hook_active must allow, got %s", out)
	}
	// A subagent's payload, no session to count in, an unreadable transcript.
	if out, _ := hook(t, dir, "s3", bad, false, "agent-1"); strings.TrimSpace(out) != "{}" {
		t.Fatalf("a subagent payload must allow, got %s", out)
	}
	if out, _ := hook(t, dir, "", bad, false, ""); strings.TrimSpace(out) != "{}" {
		t.Fatalf("no session id must allow, got %s", out)
	}
	if out, _ := hook(t, dir, "s4", filepath.Join(dir, "absent.jsonl"), false, ""); strings.TrimSpace(out) != "{}" {
		t.Fatalf("an unreadable transcript must allow, got %s", out)
	}
}

func TestHookModeFromRawArgv(t *testing.T) {
	for _, c := range []struct {
		args []string
		want bool
	}{
		{[]string{"--hook"}, true},
		{[]string{"--hook=yes"}, true}, // unparseable still means hook
		{[]string{"--hook=true", "--bogus"}, true},
		{[]string{"--hook=false"}, false},
		{[]string{"--transcript", "x"}, false},
	} {
		if got := hookMode(c.args); got != c.want {
			t.Errorf("hookMode(%v) = %v, want %v", c.args, got, c.want)
		}
	}
	// A malformed flag under --hook answers {} and exits normally.
	var out, errb bytes.Buffer
	serve([]string{"--hook", "--bogus"}, &out, &errb, func() { t.Fatal("plain mode") })
	if strings.TrimSpace(out.String()) != "{}" {
		t.Fatalf("got %q", out.String())
	}
}

func TestTranscriptMode(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.jsonl")
	if err := os.WriteFile(bad, []byte(transcriptOf(t, step{text: "PR #12 is merged."})), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	c := RootCmd()
	c.SetOut(&out)
	c.SetErr(&out)
	c.SetArgs([]string{"--transcript", bad})
	if err := c.Execute(); err == nil || !strings.Contains(out.String(), "#12") {
		t.Fatalf("want a refusal naming #12, got err=%v out=%s", err, out.String())
	}
}

func TestLargeTranscriptTailIsCutOnALine(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "big.jsonl")
	filler := strings.Repeat(`{"type":"system","x":"`+strings.Repeat("a", 1000)+`"}`+"\n", 40000) // ~40 MB
	body := filler + transcriptOf(t, step{text: "PR #12 is merged."})
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	// The user's turn is in the window, so the verdict is the same as for a small file.
	tt, err := lastTurnOfFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if tt.judge() == "" {
		t.Fatal("an unread claim at the end of a large transcript was allowed")
	}
}
