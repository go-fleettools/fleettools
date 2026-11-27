package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// realStateDir is the default seam, captured before a test replaces it.
var realStateDir = stateDir

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

// runWith executes the command on a payload reader and returns both streams.
func runWith(t *testing.T, in *bytes.Reader, args ...string) (out, errs string, err error) {
	t.Helper()
	var o, e bytes.Buffer
	c := RootCmd()
	if in != nil {
		c.SetIn(in)
	}
	c.SetOut(&o)
	c.SetErr(&e)
	c.SetArgs(args)
	err = c.Execute()
	return o.String(), e.String(), err
}

func write(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestHookAllowsAndSaysWhy asserts the reason of every allowance: an answer
// of "{}" alone cannot tell which guard let the reply through.
func TestHookAllowsAndSaysWhy(t *testing.T) {
	dir := t.TempDir()
	stateDir = func() (string, error) { return filepath.Join(dir, "state"), nil }
	bad := write(t, "bad.jsonl", transcriptOf(t, step{text: "PR #12 is merged."}))
	for _, c := range []struct {
		name    string
		payload string
		want    string
	}{
		{"not JSON", `not json`, "not JSON"},
		{"no transcript", `{"session_id":"s"}`, "names no transcript"},
		{"subagent", `{"session_id":"s","transcript_path":"` + bad + `","agent_id":"a"}`, "subagent"},
		{"absent file", `{"session_id":"s","transcript_path":"` + filepath.Join(dir, "none") + `"}`, "open "},
		{"no session", `{"transcript_path":"` + bad + `"}`, "cannot keep state"},
	} {
		out, errs, err := runWith(t, bytes.NewReader([]byte(c.payload)), "--hook")
		if err != nil || strings.TrimSpace(out) != "{}" || !strings.Contains(errs, c.want) {
			t.Errorf("%s: err=%v out=%q errs=%q, want {} and %q", c.name, err, out, errs, c.want)
		}
	}
	// A payload that cannot be read at all.
	var o, e bytes.Buffer
	c := RootCmd()
	c.SetIn(failingReader{})
	c.SetOut(&o)
	c.SetErr(&e)
	c.SetArgs([]string{"--hook"})
	if err := c.Execute(); err != nil || strings.TrimSpace(o.String()) != "{}" || !strings.Contains(e.String(), "cannot read the hook payload") {
		t.Errorf("unreadable payload: err=%v out=%q errs=%q", err, o.String(), e.String())
	}
	// A state directory that cannot be resolved.
	stateDir = func() (string, error) { return "", errors.New("no dir") }
	out, errs, err := runWith(t, bytes.NewReader([]byte(`{"session_id":"s","transcript_path":"`+bad+`"}`)), "--hook")
	if err != nil || strings.TrimSpace(out) != "{}" || !strings.Contains(errs, "cannot keep state") {
		t.Errorf("no state dir: err=%v out=%q errs=%q", err, out, errs)
	}
	// A refusal that cannot be recorded is not given: the state path is a file.
	blocker := write(t, "blocker", "x")
	stateDir = func() (string, error) { return blocker, nil }
	out, errs, err = runWith(t, bytes.NewReader([]byte(`{"session_id":"s","transcript_path":"`+bad+`"}`)), "--hook")
	if err != nil || strings.TrimSpace(out) != "{}" || !strings.Contains(errs, "cannot record") {
		t.Errorf("unrecordable: err=%v out=%q errs=%q", err, out, errs)
	}
}

func TestDefaultStateDirNeedsAHome(t *testing.T) {
	for _, k := range []string{"HOME", "XDG_CONFIG_HOME", "APPDATA"} {
		t.Setenv(k, "")
	}
	if d, err := realStateDir(); err == nil {
		t.Fatalf("no config dir is defined, yet got %q", d)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	d, err := realStateDir()
	if err != nil || filepath.Base(d) != "mergedcheck" {
		t.Fatalf("got %q, %v", d, err)
	}
}

func TestTranscriptModeAnswers(t *testing.T) {
	good := write(t, "good.jsonl", transcriptOf(t,
		step{command: "gh pr view 12 --json state", output: `{"state":"MERGED"}`},
		step{text: "PR #12 is merged."}))
	if out, _, err := runWith(t, nil, "--transcript", good); err != nil || strings.TrimSpace(out) != "CLEAN" {
		t.Errorf("a read claim: err=%v out=%q", err, out)
	}
	if _, errs, err := runWith(t, nil); err == nil || !strings.Contains(errs+err.Error(), "no transcript") {
		t.Errorf("no --transcript: err=%v errs=%q", err, errs)
	}
	if _, errs, err := runWith(t, nil, "--transcript", filepath.Join(t.TempDir(), "none")); err == nil || !strings.Contains(errs+err.Error(), "open ") {
		t.Errorf("absent file: err=%v errs=%q", err, errs)
	}
	// Asking for help is not a failure to name a transcript.
	if _, _, err := runWith(t, nil, "--help"); err != nil {
		t.Errorf("--help: %v", err)
	}
	// A directory opens but cannot be read.
	if _, errs, err := runWith(t, nil, "--transcript", t.TempDir()); err == nil || strings.Contains(errs+err.Error(), "no turn the user took") {
		t.Errorf("a directory is a read error, not an empty transcript: %v %q", err, errs)
	}
}

func TestServeRunsPlainWithoutHook(t *testing.T) {
	ran := false
	var o, e bytes.Buffer
	serve([]string{"--transcript", "x"}, &o, &e, func() { ran = true })
	if !ran || o.Len() != 0 {
		t.Fatalf("plain=%v out=%q", ran, o.String())
	}
}

func TestLastTurnNeedsAUserAndAText(t *testing.T) {
	noUser := write(t, "nouser.jsonl", jsonl(t, map[string]any{"type": "assistant", "uuid": "a",
		"message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "PR #1 is merged."}}}}))
	if _, err := lastTurnOfFile(noUser); err == nil || !strings.Contains(err.Error(), "no turn the user took") {
		t.Errorf("no user entry: %v", err)
	}
	noText := write(t, "notext.jsonl", jsonl(t, map[string]any{"type": "user", "uuid": "u",
		"message": map[string]any{"role": "user", "content": "go"}}))
	if _, err := lastTurnOfFile(noText); err == nil || !strings.Contains(err.Error(), "no reply text") {
		t.Errorf("no text: %v", err)
	}
}

// TestTailBoundary: a file of exactly maxTail bytes is read whole, a longer one
// from its last maxTail bytes with the cut line dropped.
func TestTailBoundary(t *testing.T) {
	turnLines := jsonl(t,
		map[string]any{"type": "user", "uuid": "u1", "message": map[string]any{"role": "user", "content": "go"}},
		map[string]any{"type": "assistant", "uuid": "a1", "message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "text", "text": "PR #12 is merged."}}}})
	pad := func(n int) string { // one JSON line of exactly n bytes, newline included
		s := `{"type":"system","x":"`
		return s + strings.Repeat("a", n-len(s)-len(`"}`)-1) + `"}` + "\n"
	}
	// Exactly maxTail: the turn starts the file and must not lose its first line.
	body := turnLines + pad(maxTail-len(turnLines))
	if len(body) != maxTail {
		t.Fatalf("fixture is %d bytes", len(body))
	}
	tt, err := lastTurnOfFile(write(t, "exact.jsonl", body))
	if err != nil || tt.text == "" {
		t.Fatalf("exact window: %v %q", err, tt.text)
	}
	// One byte more: the first line is cut and dropped, so the turn that began
	// there is gone and the user's later message is the last turn.
	long := write(t, "long.jsonl", "X"+turnLines+pad(maxTail-len(turnLines)))
	if _, err := lastTurnOfFile(long); err == nil {
		t.Fatal("the cut first line was not dropped")
	}
}

func TestLabelAndHowToRead(t *testing.T) {
	got := judgeText(t, transcriptOf(t, step{text: "owner/repo#12 is merged."}))
	if !strings.Contains(got, "owner/repo#12:") || !strings.Contains(got, "gh pr view 12 -R owner/repo ") {
		t.Errorf("named repo: %q", got)
	}
	got = judgeText(t, transcriptOf(t, step{text: "PR #12 is merged."}))
	if !strings.Contains(got, "#12:") || strings.Contains(got, "/#12") || !strings.Contains(got, "<owner/repo>") {
		t.Errorf("no repo: %q", got)
	}
	if got := judgeText(t, transcriptOf(t, step{text: "Nothing to report."})); got != "" {
		t.Errorf("no claim must stand: %q", got)
	}
}

func TestClip(t *testing.T) {
	for _, c := range []struct {
		in   string
		n    int
		want string
	}{
		{"abc", 3, "abc"},
		{"abcd", 3, "abc…"},
		{"éé", 2, "éé"},
		{"ééé", 2, "éé…"},
		{"", 0, ""},
	} {
		if got := clip(c.in, c.n); got != c.want {
			t.Errorf("clip(%q,%d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
}

func TestReadsOfStates(t *testing.T) {
	for _, c := range []struct {
		name    string
		command string
		output  string
		want    []read
	}{
		{"draft is open", "gh pr view 5", "draft\n", []read{{ref{"", 5}, "OPEN", "gh pr view"}}},
		{"merged bare", "gh pr view 5", "MERGED\n", []read{{ref{"", 5}, "MERGED", "gh pr view"}}},
		{"no state", "gh pr view 5", "nothing here", nil},
		{"list json", "gh pr list -R o/r --state all --json number,state",
			`[{"number":5,"state":"MERGED"},{"number":0,"state":"OPEN"},{"number":6,"state":""}]`,
			[]read{{ref{"o/r", 5}, "MERGED", "gh pr list"}}},
		{"list empty json", "gh pr list --json number,state", `[]`, nil},
		{"list tsv", "gh pr list --state all", "7\ttitle\tMERGED\t2026\n8\tother\tDRAFT\t2026\n",
			[]read{{ref{"", 7}, "MERGED", "gh pr list"}, {ref{"", 8}, "OPEN", "gh pr list"}}},
		{"api without a pull path", "gh api repos/o/r/issues/5", `{"merged":true}`, nil},
		{"api merged", "gh api repos/o/r/pulls/5", `{"merged":true}`, []read{{ref{"o/r", 5}, "MERGED", "gh api"}}},
		{"api state", "gh api repos/o/r/pulls/5", `{"merged":false,"state":"closed"}`, []read{{ref{"o/r", 5}, "CLOSED", "gh api"}}},
		{"ghmerge without a merge line", "ghmerge o/r 5", "waiting for checks\n", nil},
		{"ghmerge merged", "ghmerge o/r 5", "merged\n", []read{{ref{"o/r", 5}, "MERGED", "ghmerge"}}},
	} {
		got := readsOf(call{c.command, c.output})
		if len(got) != len(c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: got %v, want %v", c.name, got, c.want)
			}
		}
	}
}
