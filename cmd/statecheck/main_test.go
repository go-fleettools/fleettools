package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDetect(t *testing.T) {
	cases := []struct {
		name, text, kind string // kind "" = no finding
	}{
		// the two real cases of 2026-11-25
		{"quota blocking fr", "L'hôte GARM était bloqué sur le quota RAM.", "asserts"},
		{"quota blocking fr 2", "Le quota RAM est bloquant.", "asserts"},
		{"install the app fr", "Installez l'App sur go-authn.", "asks"},
		{"app to install fr", "L'App GitHub à installer sur go-authn.", "asks"},
		// English
		{"quota exhausted en", "The GitHub API quota is exhausted until 15:49.", "asserts"},
		{"please install en", "Please install the GitHub App on the org.", "asks"},
		{"need to enable en", "You need to enable the runner in the pool.", "asks"},
		{"imperative en", "Raise the quota of the host.", "asks"},
		{"bullet imperative en", "- Install the GitHub App first.", "asks"},
		{"weekly limit en", "The weekly limit is reached.", "asserts"},
		// not refused
		{"unverified fr", "Le quota est bloquant, non vérifié.", ""},
		{"unverified en", "The quota is exhausted (not checked).", ""},
		{"hypothetical fr", "Si le quota est bloquant, installez l'App.", ""},
		{"hypothetical en", "If the quota is exhausted we would wait.", ""},
		{"would", "The runner would be blocked by the quota.", ""},
		{"cites a read", "Selon gh api, le quota est épuisé.", ""},
		{"verified", "J'ai vérifié : le quota est épuisé.", ""},
		{"question", "Le quota est-il épuisé ?", ""},
		{"inline code", "Le champ `quota exhausted` vient du journal.", ""},
		{"fenced", "```\nthe quota is exhausted\n```", ""},
		{"block quote", "> the quota is exhausted", ""},
		{"quoted fr", "Il a écrit « le quota est épuisé » hier.", ""},
		{"quoted en", `He wrote "the quota is exhausted" yesterday.`, ""},
		{"context limit", "Son contexte atteint 166 000 tokens, près de la limite.", ""},
		{"crypto key", "La clé enregistrée est bloquée par un PIN.", ""},
		{"no noun", "Le test est bloqué.", ""},
		{"no predicate", "Le quota GitHub est une ressource partagée.", ""},
		{"empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := detect(c.text)
			if c.kind == "" {
				if len(fs) != 0 {
					t.Fatalf("refused %q: %+v", c.text, fs)
				}
				return
			}
			if len(fs) != 1 || fs[0].kind != c.kind {
				t.Fatalf("want one %s finding in %q, got %+v", c.kind, c.text, fs)
			}
			if fs[0].read == "" || fs[0].sentence == "" {
				t.Fatalf("finding must name the sentence and the read: %+v", fs[0])
			}
		})
	}
}

func TestReadHintByNoun(t *testing.T) {
	for in, want := range map[string]string{
		"the quota":           "quota query",
		"the GitHub App":      "installations",
		"the github token":    "ghscopes",
		"the runner":          "runner list",
		"la vm":               "status or list command",
		"something unrelated": "lists that system",
	} {
		if got := readHint(in); !strings.Contains(got, want) {
			t.Errorf("readHint(%q) = %q, want it to contain %q", in, got, want)
		}
	}
}

func TestReasonNamesSentenceAndRead(t *testing.T) {
	fs := detect("Le quota est bloquant. Installez l'App sur go-authn. Le quota est épuisé. Le quota est atteint.")
	r := reason(fs)
	for _, w := range []string{"Le quota est bloquant.", "the read that answers it", "non vérifié", "and 1 more", "asks the user"} {
		if !strings.Contains(r, w) {
			t.Errorf("reason lacks %q:\n%s", w, r)
		}
	}
	long := reason([]finding{{sentence: strings.Repeat("é", 300), kind: "asserts", read: "x"}})
	if !strings.Contains(long, "…") {
		t.Error("a long sentence must be clipped")
	}
}

// ------------------------------------------------------------ transcripts

func line(t *testing.T, v map[string]any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b) + "\n"
}

func user(t *testing.T, text string) string {
	return line(t, map[string]any{"type": "user", "uuid": "u-" + text[:3], "message": map[string]any{"role": "user", "content": text}})
}

func assistant(t *testing.T, uuid string, blocks ...map[string]any) string {
	return line(t, map[string]any{"type": "assistant", "uuid": uuid, "message": map[string]any{"role": "assistant", "content": blocks}})
}

func text(s string) map[string]any { return map[string]any{"type": "text", "text": s} }
func tool(name string) map[string]any {
	return map[string]any{"type": "tool_use", "name": name, "id": "t1"}
}

func toolResult(t *testing.T) string {
	return line(t, map[string]any{"type": "user", "message": map[string]any{"role": "user",
		"content": []map[string]any{{"type": "tool_result", "content": "ok"}}}})
}

func write(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const stale = "Le quota RAM est bloquant."

func TestTurnJudging(t *testing.T) {
	cases := []struct {
		name, body string
		refused    bool
	}{
		{"no tool call", user(t, "hello") + assistant(t, "a1", text(stale)), true},
		{"a Bash read excuses", user(t, "hello") + assistant(t, "a1", tool("Bash")) + toolResult(t) + assistant(t, "a2", text(stale)), false},
		{"an MCP read excuses", user(t, "hello") + assistant(t, "a1", tool("mcp__x__y")) + toolResult(t) + assistant(t, "a2", text(stale)), false},
		{"a Read of a note does not", user(t, "hello") + assistant(t, "a1", tool("Read")) + toolResult(t) + assistant(t, "a2", text(stale)), true},
		{"Grep does not", user(t, "hello") + assistant(t, "a1", tool("Grep")) + toolResult(t) + assistant(t, "a2", text(stale)), true},
		{"a read of the PREVIOUS turn does not", user(t, "one") + assistant(t, "a0", tool("Bash")) + toolResult(t) + assistant(t, "a0b", text("ok")) + user(t, "two") + assistant(t, "a1", text(stale)), true},
		{"only the last text counts", user(t, "hello") + assistant(t, "a1", text(stale)) + assistant(t, "a2", tool("Bash")) + toolResult(t) + assistant(t, "a3", text("done")), false},
		{"clean text", user(t, "hello") + assistant(t, "a1", text("all good")), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tn, err := lastTurnOfFile(write(t, "t.jsonl", c.body))
			if err != nil {
				t.Fatal(err)
			}
			if got := len(tn.judge()) > 0; got != c.refused {
				t.Fatalf("refused = %v, want %v", got, c.refused)
			}
		})
	}
}

func TestTranscriptEntriesSkipped(t *testing.T) {
	apiErr := line(t, map[string]any{"type": "assistant", "uuid": "e1", "isApiErrorMessage": true,
		"message": map[string]any{"role": "assistant", "content": []map[string]any{text(stale)}}})
	side := line(t, map[string]any{"type": "assistant", "uuid": "s1", "isSidechain": true,
		"message": map[string]any{"role": "assistant", "content": []map[string]any{text(stale)}}})
	meta := line(t, map[string]any{"type": "user", "isMeta": true, "message": map[string]any{"role": "user", "content": "meta"}})
	body := user(t, "hello") + assistant(t, "a1", text("fine")) + apiErr + side + meta + "not json\n" + "\n"
	tn, err := lastTurnOfFile(write(t, "t.jsonl", body))
	if err != nil {
		t.Fatal(err)
	}
	if tn.last != "fine" || len(tn.judge()) != 0 {
		t.Fatalf("an API error, a sidechain entry and meta context must not be judged: %+v", tn)
	}
}

func TestStringContentAndBlocksWithoutText(t *testing.T) {
	raw := line(t, map[string]any{"type": "assistant", "uuid": "a1", "message": map[string]any{"role": "assistant", "content": stale}})
	tn, err := lastTurnOfFile(write(t, "t.jsonl", user(t, "hello")+raw))
	if err != nil || len(tn.judge()) != 1 {
		t.Fatalf("a string content is a reply: %v %+v", err, tn)
	}
	if _, err := lastTurnOfFile(write(t, "t.jsonl", user(t, "hello")+assistant(t, "a1", tool("Bash")))); err == nil {
		t.Fatal("a turn with no text has nothing to judge")
	}
	if _, err := lastTurnOfFile(write(t, "t.jsonl", "")); err == nil {
		t.Fatal("an empty transcript has nothing to judge")
	}
	if _, err := lastTurnOfFile(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("a missing file must error")
	}
	if _, err := lastTurnOfFile(t.TempDir()); err == nil {
		t.Fatal("a directory must error")
	}
	// user content as blocks: a text block is a turn, a tool_result is not
	blocks := line(t, map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": []map[string]any{{"type": "text", "text": "go"}}}})
	tn, err = lastTurnOfFile(write(t, "t.jsonl", blocks+assistant(t, "a1", text(stale))))
	if err != nil || len(tn.judge()) != 1 {
		t.Fatalf("a text block opens a turn: %v %+v", err, tn)
	}
}

func TestTailWindowsFindTheTurnFromTheEnd(t *testing.T) {
	// a turn far larger than the first window: the scan must double until a user turn is in it
	pad := strings.Repeat("x", 1500)
	var b strings.Builder
	b.WriteString(user(t, "hello"))
	for i := 0; i < 1500; i++ {
		b.WriteString(assistant(t, "p", tool("Read"), text(pad)))
	}
	b.WriteString(assistant(t, "z", text(stale)))
	tn, err := lastTurnOfFile(write(t, "big.jsonl", b.String()))
	if err != nil {
		t.Fatal(err)
	}
	if len(tn.judge()) != 1 {
		t.Fatalf("want the stale claim found across windows: %+v", tn.last)
	}
	// a window that starts mid-line with no newline in it, and no user turn: unanswerable
	if _, err := lastTurnOfFile(write(t, "nl.jsonl", strings.Repeat("a", 3<<20))); err == nil {
		t.Fatal("no turn must be an error")
	}
}

// ------------------------------------------------------------ the hook

func runHookOn(t *testing.T, payload string) (stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	c := RootCmd()
	c.SetIn(strings.NewReader(payload))
	c.SetOut(&out)
	c.SetErr(&errb)
	c.SetArgs([]string{"--hook"})
	if err := c.Execute(); err != nil {
		t.Fatal(err)
	}
	return out.String(), errb.String()
}

func withState(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	old := stateDir
	stateDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { stateDir = old })
}

func payload(t *testing.T, v map[string]any) string {
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestHookBlocksOnceThenStandsDown(t *testing.T) {
	withState(t)
	tr := write(t, "t.jsonl", user(t, "hello")+assistant(t, "a1", text(stale)))
	p := payload(t, map[string]any{"session_id": "s1", "transcript_path": tr})
	out, _ := runHookOn(t, p)
	var a hookAnswer
	if err := json.Unmarshal([]byte(out), &a); err != nil || a.Decision != "block" {
		t.Fatalf("want a block, got %q (%v)", out, err)
	}
	if !strings.Contains(a.Reason, stale) {
		t.Fatalf("the reason must name the sentence: %s", a.Reason)
	}
	// the same reply is refused ONCE
	out, errOut := runHookOn(t, p)
	if strings.TrimSpace(out) != "{}" || !strings.Contains(errOut, "already refused") {
		t.Fatalf("second Stop on the same reply must allow: %q %q", out, errOut)
	}
	// a different reply is judged again
	tr2 := write(t, "t2.jsonl", user(t, "hello")+assistant(t, "a2", text(stale)))
	out, _ = runHookOn(t, payload(t, map[string]any{"session_id": "s1", "transcript_path": tr2}))
	if !strings.Contains(out, `"block"`) {
		t.Fatalf("a new reply is judged: %q", out)
	}
}

func TestHookAllowsWhatItCannotRead(t *testing.T) {
	withState(t)
	good := write(t, "t.jsonl", user(t, "hello")+assistant(t, "a1", text(stale)))
	for name, p := range map[string]string{
		"not json":         "{",
		"no transcript":    `{"session_id":"s"}`,
		"missing file":     payload(t, map[string]any{"session_id": "s", "transcript_path": filepath.Join(t.TempDir(), "nope")}),
		"subagent":         payload(t, map[string]any{"session_id": "s", "transcript_path": good, "agent_id": "x"}),
		"no session":       payload(t, map[string]any{"transcript_path": good}),
		"dotted session":   payload(t, map[string]any{"session_id": "../x", "transcript_path": good}),
		"stop_hook_active": payload(t, map[string]any{"session_id": "s", "transcript_path": good, "stop_hook_active": true}),
		"clean reply":      payload(t, map[string]any{"session_id": "s", "transcript_path": write(t, "c.jsonl", user(t, "hello")+assistant(t, "a1", text("fine")))}),
	} {
		out, _ := runHookOn(t, p)
		if strings.TrimSpace(out) != "{}" {
			t.Errorf("%s: want an allow, got %q", name, out)
		}
	}
}

func TestHookAllowsWhenStateCannotBeKept(t *testing.T) {
	good := write(t, "t.jsonl", user(t, "hello")+assistant(t, "a1", text(stale)))
	p := payload(t, map[string]any{"session_id": "s", "transcript_path": good})
	old := stateDir
	t.Cleanup(func() { stateDir = old })
	stateDir = func() (string, error) { return "", os.ErrNotExist }
	if out, _ := runHookOn(t, p); strings.TrimSpace(out) != "{}" {
		t.Fatalf("a refusal it cannot count is one it cannot stop: %q", out)
	}
	// a state directory that is a file cannot be written
	f := write(t, "file", "x")
	stateDir = func() (string, error) { return f, nil }
	if out, _ := runHookOn(t, p); strings.TrimSpace(out) != "{}" {
		t.Fatalf("an unwritable state must allow: %q", out)
	}
}

func TestHookUsesAHashWhenTheEntryHasNoUUID(t *testing.T) {
	withState(t)
	raw := line(t, map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant", "content": []map[string]any{text(stale)}}})
	tr := write(t, "t.jsonl", user(t, "hello")+raw)
	p := payload(t, map[string]any{"session_id": "s", "transcript_path": tr})
	if out, _ := runHookOn(t, p); !strings.Contains(out, `"block"`) {
		t.Fatalf("want a block: %q", out)
	}
	if out, _ := runHookOn(t, p); strings.TrimSpace(out) != "{}" {
		t.Fatalf("the hash must identify the reply: %q", out)
	}
}

func TestHookReadError(t *testing.T) {
	var out, errb bytes.Buffer
	c := RootCmd()
	c.SetIn(errReader{})
	c.SetOut(&out)
	c.SetErr(&errb)
	c.SetArgs([]string{"--hook"})
	if err := c.Execute(); err != nil || strings.TrimSpace(out.String()) != "{}" {
		t.Fatalf("an unreadable payload allows: %v %q", err, out.String())
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, os.ErrClosed }

func TestHookModeFromRawArgv(t *testing.T) {
	for args, want := range map[string]bool{
		"--hook":         true,
		"--hook=1":       true,
		"--hook=TRUE":    true,
		"--hook=yes":     true, // unparseable still means hook: fail open
		"-hook":          true,
		"--hook=false":   false,
		"--transcript x": false,
		"":               false,
	} {
		if got := hookMode(strings.Fields(args)); got != want {
			t.Errorf("hookMode(%q) = %v, want %v", args, got, want)
		}
	}
}

// ------------------------------------------------------------ the command line

func exec(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	var out, errb bytes.Buffer
	c := RootCmd()
	c.SetOut(&out)
	c.SetErr(&errb)
	c.SetArgs(args)
	err := c.Execute()
	return out.String(), errb.String(), err
}

func TestTranscriptFlag(t *testing.T) {
	bad := write(t, "bad.jsonl", user(t, "hello")+assistant(t, "a1", text(stale)))
	out, _, err := exec(t, "--transcript", bad)
	if err == nil || !strings.Contains(out, "the read that answers it") {
		t.Fatalf("want a finding: %q %v", out, err)
	}
	good := write(t, "good.jsonl", user(t, "hello")+assistant(t, "a1", text("fine")))
	if out, _, err := exec(t, "--transcript", good); err != nil || strings.TrimSpace(out) != "CLEAN" {
		t.Fatalf("want CLEAN: %q %v", out, err)
	}
	if _, _, err := exec(t); err == nil {
		t.Fatal("no transcript is unanswerable")
	}
	if _, _, err := exec(t, "--transcript", filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("a missing transcript is unanswerable")
	}
	if _, _, err := exec(t, "--help"); err != nil {
		t.Fatal(err)
	}
}

func TestSweep(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "proj")
	if err := os.MkdirAll(filepath.Join(proj, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := user(t, "one") + assistant(t, "a1", text(stale)) + user(t, "two") + assistant(t, "a2", text("fine"))
	for _, n := range []string{"a.jsonl", "b.jsonl"} {
		if err := os.WriteFile(filepath.Join(proj, n), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// a subagent transcript, deeper: skipped
	if err := os.WriteFile(filepath.Join(proj, "sub", "c.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, err := exec(t, "sweep", "--last", "5", "--show", root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "transcripts 2, unreadable 0, replies judged 4, refused 2") || !strings.Contains(out, "asserts") {
		t.Fatalf("unexpected sweep output:\n%s", out)
	}
	out, _, _ = exec(t, "sweep", "--last", "1", root)
	if !strings.Contains(out, "transcripts 1,") || strings.Contains(out, "asserts") {
		t.Fatalf("--last 1 without --show:\n%s", out)
	}
	if _, _, err := exec(t, "sweep", t.TempDir()); err == nil {
		t.Fatal("a directory with no transcript is unanswerable")
	}
	// an unreadable transcript is counted, never a silent zero
	bad := filepath.Join(proj, "d.jsonl")
	if err := os.Mkdir(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	out, _, err = exec(t, "sweep", root)
	if err != nil || !strings.Contains(out, "unreadable 1") {
		t.Fatalf("an unreadable transcript must be counted: %q %v", out, err)
	}
	// the default directory is under the home directory
	t.Setenv("HOME", root)
	if err := os.MkdirAll(filepath.Join(root, ".claude", "projects", "p"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := exec(t, "sweep"); err == nil {
		t.Fatal("an empty default directory is unanswerable")
	}
}

// ------------------------------------------------------------ fuzz

func FuzzDetect(f *testing.F) {
	for _, s := range []string{stale, "Installez l'App.", "```\nx", "« quota exhausted", "\"quota", "a.b? c!\nd;", "", "\xff\xfe", "?", "- 1. Install the app"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		fs := detect(s)
		again := detect(s)
		if len(fs) != len(again) {
			t.Fatal("detect must be deterministic")
		}
		for _, x := range fs {
			if x.sentence == "" || x.read == "" || (x.kind != "asserts" && x.kind != "asks") {
				t.Fatalf("malformed finding %+v", x)
			}
		}
		r := reason(fs)
		if len(fs) > 0 && !utf8.ValidString(r) && utf8.ValidString(s) {
			t.Fatal("a reason built from valid text must be valid text")
		}
	})
}

func FuzzTranscript(f *testing.F) {
	f.Add(`{"type":"user","message":{"role":"user","content":"hi"}}` + "\n" + `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Le quota est épuisé."}]}}`)
	f.Add("{\n\x00\n[]\n")
	f.Fuzz(func(t *testing.T, body string) {
		ts, _, err := turnsOf(strings.NewReader(body))
		if err != nil {
			return
		}
		for _, tn := range ts {
			_ = tn.judge()
		}
	})
}

func TestIsUserTurnBoundaries(t *testing.T) {
	blocks := func(bs ...map[string]any) json.RawMessage {
		b, _ := json.Marshal(bs)
		return b
	}
	cases := []struct {
		name string
		e    entry
		m    message
		want bool
	}{
		{"plain user text", entry{}, message{Role: "user", Content: json.RawMessage(`"go"`)}, true},
		{"meta user text", entry{IsMeta: true}, message{Role: "user", Content: json.RawMessage(`"go"`)}, false},
		{"assistant role", entry{}, message{Role: "assistant", Content: json.RawMessage(`"go"`)}, false},
		{"blank string", entry{}, message{Role: "user", Content: json.RawMessage(`"  "`)}, false},
		{"text block", entry{}, message{Role: "user", Content: blocks(text("go"))}, true},
		{"tool_result then text", entry{}, message{Role: "user", Content: blocks(map[string]any{"type": "tool_result"}, text("go"))}, false},
		{"blank text block", entry{}, message{Role: "user", Content: blocks(text(" "))}, false},
		{"not json", entry{}, message{Role: "user", Content: json.RawMessage(`12`)}, false},
	}
	for _, c := range cases {
		if got := isUserTurn(c.e, c.m); got != c.want {
			t.Errorf("%s: isUserTurn = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestClipBoundary(t *testing.T) {
	if got := clip("abc", 3); got != "abc" {
		t.Fatalf("len == n must not clip: %q", got)
	}
	if got := clip("abcd", 3); got != "abc…" {
		t.Fatalf("len == n+1 must clip: %q", got)
	}
}

// Each allow names WHY on stderr: assert the reason, so a guard that is
// removed (and lets another guard answer the same "{}") is still seen.
func TestHookAllowReasons(t *testing.T) {
	withState(t)
	good := write(t, "t.jsonl", user(t, "hello")+assistant(t, "a1", text(stale)))
	cases := []struct{ name, payload, want string }{
		{"not json", "{", "not JSON"},
		{"no transcript", `{"session_id":"s"}`, "names no transcript"},
		{"subagent", payload(t, map[string]any{"session_id": "s", "transcript_path": good, "agent_id": "x"}), "subagent"},
		{"no session", payload(t, map[string]any{"transcript_path": good}), "cannot keep state"},
	}
	for _, c := range cases {
		out, errOut := runHookOn(t, c.payload)
		if strings.TrimSpace(out) != "{}" || !strings.Contains(errOut, c.want) {
			t.Errorf("%s: want an allow citing %q, got %q %q", c.name, c.want, out, errOut)
		}
	}
	// a transcript that cannot be read says why
	_, errOut := runHookOn(t, payload(t, map[string]any{"session_id": "s", "transcript_path": filepath.Join(t.TempDir(), "nope")}))
	if !strings.Contains(errOut, "allowing: ") || strings.Contains(errOut, "no reply text") {
		t.Fatalf("a missing transcript must be reported as such: %q", errOut)
	}
	// an unreadable payload
	var out, errb bytes.Buffer
	cmd := RootCmd()
	cmd.SetIn(errReader{})
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	cmd.SetArgs([]string{"--hook"})
	if err := cmd.Execute(); err != nil || !strings.Contains(errb.String(), "cannot read the hook payload") {
		t.Fatalf("read error: %v %q", err, errb.String())
	}
	// a state directory that cannot be found
	old := stateDir
	t.Cleanup(func() { stateDir = old })
	stateDir = func() (string, error) { return "", os.ErrNotExist }
	if _, ok := statePath("s"); ok {
		t.Fatal("no state dir means no state path")
	}
}

func TestStateDirNeedsAConfigDir(t *testing.T) {
	for _, k := range []string{"HOME", "XDG_CONFIG_HOME", "APPDATA"} {
		t.Setenv(k, "")
	}
	if d, err := stateDir(); err == nil {
		t.Fatalf("no config dir must be an error, got %q", d)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	if d, err := stateDir(); err != nil || !strings.HasSuffix(d, filepath.Join("fleettools", "statecheck")) {
		t.Fatalf("stateDir = %q, %v", d, err)
	}
}

func TestCommandLineMessages(t *testing.T) {
	if _, _, err := exec(t); err == nil || !strings.Contains(err.Error(), "pass --transcript") {
		t.Fatalf("no transcript must say how to pass one: %v", err)
	}
	if _, _, err := exec(t, "--transcript", ""); err == nil || !strings.Contains(err.Error(), "pass --transcript") {
		t.Fatalf("an empty transcript is no transcript: %v", err)
	}
	// a RunE reached with --help set must do nothing, not judge or fail
	c := RootCmd()
	c.InitDefaultHelpFlag()
	if err := c.Flags().Set("help", "true"); err != nil {
		t.Fatal(err)
	}
	if err := c.RunE(c, nil); err != nil {
		t.Fatalf("--help must succeed without work: %v", err)
	}
}

func TestSweepErrorMessages(t *testing.T) {
	if _, _, err := exec(t, "sweep", "["); err == nil || !strings.Contains(err.Error(), "syntax error") {
		t.Fatalf("a malformed directory pattern must say so: %v", err)
	}
	if _, err := newest("[", 1); err == nil {
		t.Fatal("newest must return the pattern error")
	}
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	if _, _, err := exec(t, "sweep"); err == nil || strings.Contains(err.Error(), "no transcript under") {
		t.Fatalf("no home must be its own error: %v", err)
	}
}

func TestTranscriptOpenAndScanErrors(t *testing.T) {
	if _, err := lastTurnOfFile(filepath.Join(t.TempDir(), "nope")); !errors.Is(err, fs.ErrNotExist) || !strings.HasPrefix(err.Error(), "transcript: ") {
		t.Fatalf("a missing file must report why, and name the transcript: %v", err)
	}
	if _, err := lastTurnOfFile(t.TempDir()); err == nil || strings.Contains(err.Error(), "no reply text") {
		t.Fatalf("a directory fails to READ, it does not merely lack text: %v", err)
	}
	if runtime.GOOS != "windows" && os.Getuid() != 0 {
		p := write(t, "locked.jsonl", user(t, "hello")+assistant(t, "a1", text(stale)))
		if err := os.Chmod(p, 0); err != nil {
			t.Fatal(err)
		}
		if _, err := lastTurnOfFile(p); !errors.Is(err, fs.ErrPermission) {
			t.Fatalf("an unreadable file must report why: %v", err)
		}
	}
	// one 64 MiB line: the scanner's token limit is an error, not "no text"
	p := filepath.Join(t.TempDir(), "long.jsonl")
	if err := os.WriteFile(p, bytes.Repeat([]byte("a"), 64<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := lastTurnOfFile(p); err == nil || !strings.Contains(err.Error(), "too long") {
		t.Fatalf("a line past the scanner limit must report it: %v", err)
	}
}

func TestHookRunDecidesFromTheRawArgv(t *testing.T) {
	var out, errb bytes.Buffer
	plain := 0
	serve([]string{"--transcript", "x"}, &out, &errb, func() { plain++ })
	if plain != 1 || out.Len() != 0 {
		t.Fatal("a plain invocation is handed to the plain path, not served as a hook")
	}
	serve([]string{"--hook", "--bogus"}, &out, &errb, func() { plain++ })
	if plain != 1 || strings.TrimSpace(out.String()) != "{}" ||
		!strings.Contains(errb.String(), "the command itself failed") {
		t.Fatalf("a hook whose command fails must still allow: %q %q", out.String(), errb.String())
	}
}
