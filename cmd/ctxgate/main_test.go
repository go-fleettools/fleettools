// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// asst builds one assistant transcript line.
func asst(id, model string, in, cr, cc int64, ts string) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":{"id":%q,"model":%q,"content":[{"type":"text","text":"x"}],"usage":{"input_tokens":%d,"cache_read_input_tokens":%d,"cache_creation_input_tokens":%d,"output_tokens":5}}}`,
		ts, id, model, in, cr, cc)
}

func writeLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// session lays out <dir>/s.jsonl and returns its path and the subagents dir.
func session(t *testing.T) (transcript, subs string) {
	t.Helper()
	d := t.TempDir()
	transcript = filepath.Join(d, "s.jsonl")
	writeLines(t, transcript, asst("m0", "claude-opus-5-5", 1, 10, 10, "2026-11-20T09:00:00Z"))
	return transcript, filepath.Join(d, "s", "subagents")
}

func payload(tool, to, transcript, msg string) string {
	b, _ := json.Marshal(map[string]any{
		"tool_name": tool, "transcript_path": transcript,
		"tool_input": map[string]string{"to": to, "message": msg},
	})
	return string(b)
}

func hookRun(t *testing.T, in string, args ...string) (out, errOut string) {
	t.Helper()
	var o, e bytes.Buffer
	cmd := RootCmd()
	cmd.SetIn(strings.NewReader(in))
	cmd.SetOut(&o)
	cmd.SetErr(&e)
	cmd.SetArgs(append([]string{"--hook"}, args...))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("hook returned %v", err)
	}
	return o.String(), e.String()
}

func TestResumeIsRefusedOnlyAboveTheCap(t *testing.T) {
	tr, subs := session(t)
	for _, c := range []struct {
		name string
		ctx  int64
		deny bool
	}{
		{"just under", 199_999, false},
		{"at the cap", 200_000, false},
		{"just over", 200_001, true},
		{"huge", 930_000, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			writeLines(t, filepath.Join(subs, "agent-abc123.jsonl"),
				asst("m1", "claude-sonnet-5-5", 1, 50, 50, "2026-11-20T09:00:00Z"),
				asst("m2", "claude-sonnet-5-5", 1, c.ctx-1, 0, "2026-11-20T09:01:00Z"))
			out, _ := hookRun(t, payload("SendMessage", "abc123", tr, "go on"))
			if c.deny != (out != "") {
				t.Fatalf("deny=%v want %v, out=%q", out != "", c.deny, out)
			}
			if c.deny {
				var r struct {
					H struct {
						D string `json:"permissionDecision"`
						R string `json:"permissionDecisionReason"`
					} `json:"hookSpecificOutput"`
				}
				if err := json.Unmarshal([]byte(out), &r); err != nil || r.H.D != "deny" {
					t.Fatalf("bad answer %q (%v)", out, err)
				}
				want := fmt.Sprintf("agent abc123 carries %dk of context", c.ctx/1000)
				if !strings.Contains(r.H.R, want) || !strings.Contains(r.H.R, "spawn a fresh agent with a handoff brief") {
					t.Fatalf("reason %q lacks %q", r.H.R, want)
				}
			}
		})
	}
}

func TestMaxFlagMovesTheCap(t *testing.T) {
	tr, subs := session(t)
	writeLines(t, filepath.Join(subs, "agent-abc.jsonl"), asst("m", "x", 1, 99, 0, "2026-11-20T09:00:00Z"))
	if out, _ := hookRun(t, payload("SendMessage", "abc", tr, ""), "--max", "100"); out != "" {
		t.Fatalf("at the cap: %q", out)
	}
	if out, _ := hookRun(t, payload("SendMessage", "abc", tr, ""), "--max", "99"); out == "" {
		t.Fatal("over a lowered cap must deny")
	}
}

func TestAgentPrefixAndRefAreAccepted(t *testing.T) {
	tr, subs := session(t)
	writeLines(t, filepath.Join(subs, "agent-abc.jsonl"), asst("m", "x", 1, 300_000, 0, "2026-11-20T09:00:00Z"))
	for _, to := range []string{"abc", "agent-abc", "abc [3fa9c1]"} {
		if out, _ := hookRun(t, payload("SendMessage", to, tr, "")); out == "" {
			t.Errorf("target %q not resolved", to)
		}
	}
}

func TestEverythingUnreadableOrForeignIsAllowed(t *testing.T) {
	tr, subs := session(t)
	big := asst("m", "x", 1, 900_000, 0, "2026-11-20T09:00:00Z")
	writeLines(t, filepath.Join(subs, "agent-big.jsonl"), big)
	writeLines(t, filepath.Join(subs, "agent-empty.jsonl"), `{"type":"user"}`)
	if err := os.MkdirAll(filepath.Join(subs, "agent-isdir.jsonl"), 0o755); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"non-SendMessage tool": payload("Agent", "big", tr, ""),
		"unknown agent":        payload("SendMessage", "nosuch", tr, ""),
		"a name":               payload("SendMessage", "researcher", tr, ""),
		"main":                 payload("SendMessage", "main", tr, ""),
		"traversal":            payload("SendMessage", "../../s", tr, ""),
		"no transcript path":   payload("SendMessage", "big", "", ""),
		"missing transcript":   payload("SendMessage", "big", filepath.Join(t.TempDir(), "gone.jsonl"), ""),
		"no usage in file":     payload("SendMessage", "empty", tr, ""),
		"unreadable (a dir)":   payload("SendMessage", "isdir", tr, ""),
		"not json":             "{{{",
		"empty stdin":          "",
	}
	for name, in := range cases {
		if out, _ := hookRun(t, in); out != "" {
			t.Errorf("%s: denied: %s", name, out)
		}
	}
	// control: the same big agent IS refused when reachable
	if out, _ := hookRun(t, payload("SendMessage", "big", tr, "")); out == "" {
		t.Fatal("control: the big agent must be refused")
	}
}

func TestUnreadableTranscriptSaysSoOnStderr(t *testing.T) {
	tr, subs := session(t)
	if err := os.MkdirAll(filepath.Join(subs, "agent-isdir.jsonl"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, e := hookRun(t, payload("SendMessage", "isdir", tr, ""))
	if !strings.Contains(e, "allowing") {
		t.Fatalf("stderr %q", e)
	}
}

func TestWaiverAllowsAndIsEchoed(t *testing.T) {
	tr, subs := session(t)
	writeLines(t, filepath.Join(subs, "agent-w.jsonl"), asst("m", "x", 1, 500_000, 0, "2026-11-20T09:00:00Z"))
	out, e := hookRun(t, payload("SendMessage", "w", tr, "continue\nctx-waiver: needs the open file list"))
	if out != "" || !strings.Contains(e, "needs the open file list") {
		t.Fatalf("out=%q err=%q", out, e)
	}
}

func TestSubagentCallerResolvesItsOwnDirectory(t *testing.T) {
	_, subs := session(t)
	writeLines(t, filepath.Join(subs, "agent-peer.jsonl"), asst("m", "x", 1, 500_000, 0, "2026-11-20T09:00:00Z"))
	self := filepath.Join(subs, "agent-me.jsonl")
	writeLines(t, self, asst("m", "x", 1, 5, 0, "2026-11-20T09:00:00Z"))
	if out, _ := hookRun(t, payload("SendMessage", "peer", self, "")); out == "" {
		t.Fatal("a sub-agent's own transcript_path must find its siblings")
	}
}

func TestASplitMessageIsOneCall(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "a.jsonl")
	// three entries (one per content block) of the same API call, then another call
	writeLines(t, p,
		asst("m1", "x", 1, 100, 0, "2026-11-20T09:00:00Z"),
		asst("m1", "x", 1, 100, 0, "2026-11-20T09:00:01Z"),
		asst("m1", "x", 1, 100, 0, "2026-11-20T09:00:02Z"),
		`{"type":"user","message":{"usage":{"input_tokens":9}}}`,
		asst("m2", "x", 2, 200, 3, "2026-11-20T09:01:00Z"))
	calls, _, err := readCalls(p)
	if err != nil || len(calls) != 2 {
		t.Fatalf("calls=%d err=%v", len(calls), err)
	}
	if calls[0].ctx != 101 || calls[1].ctx != 205 || calls[1].cacheRead != 200 {
		t.Fatalf("%+v", calls)
	}
}

func TestDecodeLineRejects(t *testing.T) {
	for name, l := range map[string]string{
		"empty":          "",
		"not json":       `"usage" nonsense`,
		"user":           `{"type":"user","message":{"usage":{"input_tokens":5}}}`,
		"all zero":       asst("m", "<synthetic>", 0, 0, 0, "2026-11-20T09:00:00Z"),
		"negative":       `{"type":"assistant","message":{"usage":{"input_tokens":-5,"cache_read_input_tokens":10}}}`,
		"no usage":       `{"type":"assistant","message":{"id":"m"}}`,
		"wrong type":     `{"type":"assistant","message":{"usage":{"input_tokens":"many"}}}`,
		"truncated line": asst("m", "x", 1, 1, 1, "2026-11-20T09:00:00Z")[:60],
	} {
		if _, ok := decodeLine([]byte(l)); ok {
			t.Errorf("%s accepted", name)
		}
	}
	if c, ok := decodeLine([]byte(asst("m", "x", 1, 2, 3, "2026-11-20T09:00:00Z"))); !ok || c.ctx != 6 {
		t.Fatalf("control: %+v %v", c, ok)
	}
}

func TestOversizeLinesAreSkippedAndCounted(t *testing.T) {
	var in strings.Builder
	in.WriteString("one\n")
	in.WriteString(strings.Repeat("x", 200_000) + "\n")
	in.WriteString("two\n")
	in.WriteString(strings.Repeat("y", 150_000)) // no newline at the end, also too long
	var got []string
	over, err := eachLine(strings.NewReader(in.String()), 100_000, func(b []byte) { got = append(got, string(b)) })
	if err != nil || over != 2 || strings.Join(got, ",") != "one,two" {
		t.Fatalf("over=%d n=%d err=%v", over, len(got), err)
	}
	// a line just inside the bound is kept
	got = nil
	over, _ = eachLine(strings.NewReader(strings.Repeat("z", 100_000)+"\nend"), 100_001, func(b []byte) { got = append(got, string(b)) })
	if over != 0 || len(got) != 2 || got[1] != "end" {
		t.Fatalf("over=%d got=%d", over, len(got))
	}
}

func TestLastContextReadsBackThroughTheTail(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "a.jsonl")
	junk := `{"type":"user","message":{"content":"` + strings.Repeat("j", 100_000) + `"}}`
	lines := []string{asst("m1", "x", 1, 777, 0, "2026-11-20T09:00:00Z")}
	for range 30 { // 3 MB of lines with no usage after the last call
		lines = append(lines, junk)
	}
	writeLines(t, p, lines...)
	n, err := lastContext(p, 64<<20)
	if err != nil || n != 778 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if _, err := lastContext(p, 1<<20); err == nil {
		t.Fatal("a window too small to reach the call must be an error, not a number")
	}
	if _, err := lastContext(filepath.Join(d, "gone"), 1<<20); err == nil {
		t.Fatal("missing file")
	}
}

func TestPrecompactStaysUnderTheCap(t *testing.T) {
	if n := utf8.RuneCountInString(precompactText); n >= precompactCap/2 {
		t.Fatalf("base text is %d chars: no room left for custom_instructions", n)
	}
	for name, custom := range map[string]string{
		"none":      "",
		"short":     "focus on the PR",
		"huge":      strings.Repeat("a", 50_000),
		"multibyte": strings.Repeat("é€😀", 20_000),
	} {
		got := precompactOutput(custom)
		if utf8.RuneCountInString(got) > precompactCap || len(got) > precompactCap || !utf8.ValidString(got) {
			t.Errorf("%s: %d chars, %d bytes, valid=%v", name, utf8.RuneCountInString(got), len(got), utf8.ValidString(got))
		}
		if !strings.HasPrefix(got, precompactText) {
			t.Errorf("%s: base text altered", name)
		}
	}
	if !strings.Contains(precompactOutput("focus on the PR"), "focus on the PR") {
		t.Fatal("custom instructions not appended")
	}
}

func TestPrecompactNamesWhatToKeepAndDrop(t *testing.T) {
	for _, w := range []string{"Open decisions", "pull request, issue and tag", "Commit SHAs", "Branch names", "leases", "file:line", "Agent ids", "latest instructions", "Raw tool outputs", "directory listings", "Superseded plans", "hand-back reports"} {
		if !strings.Contains(precompactText, w) {
			t.Errorf("missing %q", w)
		}
	}
}

func TestPrecompactReadsTheHookPayload(t *testing.T) {
	var o, e bytes.Buffer
	cmd := RootCmd()
	cmd.SetIn(strings.NewReader(`{"hook_event_name":"PreCompact","trigger":"auto","custom_instructions":"keep the Lyon notes"}`))
	cmd.SetOut(&o)
	cmd.SetErr(&e)
	cmd.SetArgs([]string{"--precompact"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(o.String(), "keep the Lyon notes") || !strings.HasPrefix(o.String(), "COMPACTION INSTRUCTIONS") {
		t.Fatalf("%q", o.String())
	}
	// garbage on stdin still yields the base text
	o.Reset()
	cmd = RootCmd()
	cmd.SetIn(strings.NewReader("{{{"))
	cmd.SetOut(&o)
	cmd.SetArgs([]string{"--precompact"})
	if err := cmd.Execute(); err != nil || !strings.HasPrefix(o.String(), "COMPACTION INSTRUCTIONS") {
		t.Fatalf("%v %q", err, o.String())
	}
}

func TestReportCountsBucketsKindsAndTheWindow(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	old := now.Add(-time.Hour).Format(time.RFC3339)
	ancient := now.Add(-30 * 24 * time.Hour).Format(time.RFC3339)
	writeLines(t, filepath.Join(root, "proj", "s.jsonl"),
		asst("a", "claude-opus-5-5", 1, 50_000, 0, old),
		asst("a", "claude-opus-5-5", 1, 50_000, 0, old), // split: one call
		asst("b", "claude-opus-5-5", 1, 450_000, 0, old),
		asst("c", "claude-opus-5-5", 1, 999_999, 0, ancient)) // outside the window
	writeLines(t, filepath.Join(root, "proj", "s", "subagents", "agent-x.jsonl"),
		asst("d", "claude-sonnet-5-5", 1, 800_000, 0, old),
		asst("e", "claude-sonnet-5-5", 1, 150_000, 0, old))
	c, err := scan(root, now.Add(-168*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	all := c.groups["all"]
	want := [5]int64{1, 1, 0, 1, 1}
	for i, w := range want {
		if all.buckets[i].calls != w {
			t.Errorf("bucket %s: %d calls, want %d", bucketNames[i], all.buckets[i].calls, w)
		}
	}
	if got := c.groups["sub claude-sonnet-5-5"].total().calls; got != 2 {
		t.Errorf("sub sonnet calls %d", got)
	}
	if got := c.groups["main"].total().calls; got != 2 {
		t.Errorf("main calls %d", got)
	}
	if all.total().cacheRead != 50_000+450_000+800_000+150_000 {
		t.Errorf("cache read %d", all.total().cacheRead)
	}
}

func TestReportThroughTheCommand(t *testing.T) {
	root := t.TempDir()
	clock = func() time.Time { return time.Now() }
	ts := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	writeLines(t, filepath.Join(root, "p", "s.jsonl"), asst("a", "claude-opus-5-5", 1, 450_000, 0, ts))
	var o, e bytes.Buffer
	if st := run([]string{"report", "--projects", root, "--since", "1h"}, &o, &e); st != 0 {
		t.Fatalf("status %d: %s", st, e.String())
	}
	for _, w := range []string{"1 calls", "400-700k", "main claude-opus-5-5", "100.0%"} {
		if !strings.Contains(o.String(), w) {
			t.Errorf("output lacks %q:\n%s", w, o.String())
		}
	}
	if st := run([]string{"report", "--projects", filepath.Join(root, "nope")}, &o, &e); st != 2 {
		t.Errorf("missing root: status %d", st)
	}
	if st := run([]string{"report", "--projects", t.TempDir()}, &o, &e); st != 2 {
		t.Errorf("no transcripts: status %d", st)
	}
	if st := run([]string{"report", "--projects", root, "--since", "0s"}, &o, &e); st != 2 {
		t.Errorf("zero window: status %d", st)
	}
}

func TestNothingToDoIsStatus2(t *testing.T) {
	var o, e bytes.Buffer
	if st := run(nil, &o, &e); st != 2 {
		t.Fatalf("status %d", st)
	}
}

func TestHelpStaysOffStdout(t *testing.T) {
	var o, e bytes.Buffer
	if st := run([]string{"--help"}, &o, &e); st != 0 || o.Len() != 0 || !strings.Contains(e.String(), "--precompact") {
		t.Fatalf("status %d stdout %q", st, o.String())
	}
}

func FuzzDecodeLine(f *testing.F) {
	f.Add([]byte(asst("m", "x", 1, 2, 3, "2026-11-20T09:00:00Z")))
	f.Add([]byte(`{"type":"assistant","message":{"usage":{"input_tokens":9223372036854775807,"cache_read_input_tokens":9223372036854775807}}}`))
	f.Add([]byte(`"usage"`))
	f.Fuzz(func(t *testing.T, in []byte) {
		c, ok := decodeLine(in)
		if ok && (c.ctx <= 0 || c.cacheRead < 0) {
			t.Fatalf("accepted %+v", c)
		}
	})
}

func FuzzHook(f *testing.F) {
	f.Add([]byte(`{"tool_name":"SendMessage","transcript_path":"/nonexistent/s.jsonl","tool_input":{"to":"a1"}}`))
	f.Add([]byte(`{"tool_name":"SendMessage","transcript_path":"/x","tool_input":{"to":"../../etc/passwd"}}`))
	f.Fuzz(func(t *testing.T, in []byte) {
		var o, e bytes.Buffer
		if st := hookMode(&o, &e, bytes.NewReader(in), 200000); st != 0 {
			t.Fatalf("status %d", st)
		}
	})
}
