package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeIn(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func bash(cmd string) map[string]any {
	return map[string]any{"type": "tool_use", "name": "Bash", "id": "b1", "input": map[string]any{"command": cmd}}
}

func agentCall(prompt string) map[string]any {
	return map[string]any{"type": "tool_use", "name": "Agent", "id": "g1", "input": map[string]any{"prompt": prompt}}
}

const incident = "Where: our GARM host, which has KVM. It runs 64 vCPU."

func TestJudgeBrief(t *testing.T) {
	cases := []struct {
		name, brief string
		cmds        []string
		host        string // "" with refused = a capacity alone
		refused     bool
	}{
		{"the 2026-11-26 incident", "Where: our GARM host, which has KVM", nil, "", true},
		{"stale cores", "It hosts the GARM runner pool (98/128 cores in use per an earlier estimate)", nil, "", true},
		{"ram of a server", "The build server has 16 GB of RAM, run the build there.", nil, "", true},
		{"named ssh host with capacity", "On `ssh root@10.0.0.5` (a host with 64 vCPU) run the VM.", nil, "10.0.0.5", true},
		{"named fqdn host", "Use builder@big.example.org, a machine with 32 cores.", nil, "big.example.org", true},
		{"named ssh bare host", "ssh bighost, the machine has 8 cores.", nil, "bighost", true},
		{"probe of the turn answers", "Our host has 64 vCPU.", []string{"ssh h nproc"}, "", false},
		{"probe of another host does not answer a named host", "On root@10.0.0.5 the host has 64 vCPU.", []string{"ssh root@10.0.0.9 nproc"}, "10.0.0.5", true},
		{"probe of the named host answers", "On root@10.0.0.5 the host has 64 vCPU.", []string{"ssh root@10.0.0.5 'nproc; free -m'"}, "", false},
		{"a host in a non-probe command does not answer", "On root@10.0.0.5 the host has 64 vCPU.", []string{"ssh root@10.0.0.5 ls"}, "10.0.0.5", true},
		{"the brief tells the agent to re-read", "Our host has 64 vCPU, re-read nproc before launching.", nil, "", false},
		{"naming alone is not refused", "Run the tests on `ssh fractal`.", nil, "", false},
		{"ssh user@host run, no size", "ssh me@big.example.org and run the build.", nil, "", false},
		{"guest capacity is not a host", "Boot a VM with 4 vCPU and 8 GB of RAM.", nil, "", false},
		{"module version is not a host", "run `go install github.com/x/y/cmd/z@v0.82.0` on the host with 2 cores", nil, "", true},
		{"module version alone", "go install a/b@v0.82.0 then `zpkb@1.2.3`", nil, "", false},
		{"noreply is not a host", "commit as me@users.noreply.github.com", nil, "", false},
		{"localhost is not a host", "ssh root@localhost, a machine with 4 cores", nil, "", true},
		{"empty", "", nil, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := judgeBrief(c.brief, c.cmds)
			if !c.refused {
				if len(fs) != 0 {
					t.Fatalf("refused %q: %+v", c.brief, fs)
				}
				return
			}
			if len(fs) != 1 {
				t.Fatalf("want one finding in %q, got %+v", c.brief, fs)
			}
			if fs[0].host != c.host {
				t.Fatalf("host = %q, want %q", fs[0].host, c.host)
			}
		})
	}
}

func TestNamedHosts(t *testing.T) {
	got := namedHosts("ssh -o X=y -p 22 me@A.Example.org and 10.1.2.3, 10.1.2.3, 127.0.0.1, 0.0.0.0 and x@github.com and ssh other. ")
	want := []string{"a.example.org", "other", "10.1.2.3"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestAgentReason(t *testing.T) {
	r := agentReason([]agentFinding{{"host which has KVM", ""}, {strings.Repeat("é", 300), "h1"}})
	for _, w := range []string{"asserts a host's capacity", "directs work onto h1", "nproc", "…", "re-read"} {
		if !strings.Contains(r, w) {
			t.Errorf("reason lacks %q:\n%s", w, r)
		}
	}
}

func TestCallText(t *testing.T) {
	if callText("Bash", json.RawMessage(`{"command":"nproc"}`)) != "nproc" {
		t.Error("a Bash command is its text")
	}
	if callText("Bash", json.RawMessage(`[1]`)) != "" || callText("Bash", nil) != "" {
		t.Error("a malformed Bash input has no text")
	}
	if callText("mcp__x__y", json.RawMessage(`{"a":"nproc"}`)) != `{"a":"nproc"}` {
		t.Error("an MCP call is its raw input")
	}
	if callText("Read", json.RawMessage(`{"file_path":"/x"}`)) != "" {
		t.Error("a Read is no read of a machine")
	}
	if callsOf(message{Content: json.RawMessage(`"text"`)}) != nil {
		t.Error("string content has no calls")
	}
}

func TestLastTurnCalls(t *testing.T) {
	body := user(t, "one") + assistant(t, "a0", bash("nproc-old")) + toolResult(t) +
		user(t, "two") + assistant(t, "a1", bash("ssh h nproc"), tool("Read"), map[string]any{"type": "tool_use", "name": "mcp__x__y", "input": map[string]any{"q": "free"}}) + toolResult(t)
	cmds, err := lastTurnCalls(write(t, "t.jsonl", body))
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 2 || cmds[0] != "ssh h nproc" || strings.Contains(strings.Join(cmds, ""), "old") {
		t.Fatalf("only the calls of the newest turn: %v", cmds)
	}
	// across windows: a turn far larger than the first window
	var b strings.Builder
	b.WriteString(user(t, "hello"))
	b.WriteString(assistant(t, "r", bash("ssh h nproc")))
	pad := strings.Repeat("x", 1500)
	for i := 0; i < 1500; i++ {
		b.WriteString(assistant(t, "p", text(pad)))
	}
	cmds, err = lastTurnCalls(write(t, "big.jsonl", b.String()))
	if err != nil || len(cmds) != 1 {
		t.Fatalf("the call must be found across windows: %v %v", cmds, err)
	}
	if _, err := lastTurnCalls(write(t, "nl.jsonl", strings.Repeat("a", 3<<20))); err == nil {
		t.Fatal("no user turn must be an error")
	}
	if _, err := lastTurnCalls(write(t, "e.jsonl", "")); err == nil {
		t.Fatal("an empty transcript must be an error")
	}
	if _, err := lastTurnCalls(t.TempDir() + "/missing"); err == nil {
		t.Fatal("a missing file must error")
	}
	if _, err := lastTurnCalls(t.TempDir()); err == nil {
		t.Fatal("a directory must error")
	}
}

func agentHookOn(t *testing.T, payload string, args ...string) (stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	c := RootCmd()
	c.SetIn(strings.NewReader(payload))
	c.SetOut(&out)
	c.SetErr(&errb)
	c.SetArgs(append([]string{"--agent-hook"}, args...))
	if err := c.Execute(); err != nil {
		t.Fatal(err)
	}
	return out.String(), errb.String()
}

func agentBody(t *testing.T, transcript, tool, agentID, prompt string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"transcript_path": transcript, "tool_name": tool, "agent_id": agentID,
		"tool_input": map[string]any{"prompt": prompt}})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAgentHook(t *testing.T) {
	unread := write(t, "unread.jsonl", user(t, "go on")+assistant(t, "a1", text("ok")))
	read := write(t, "read.jsonl", user(t, "go on")+assistant(t, "a1", bash("ssh garm 'nproc; free -m'"))+toolResult(t))
	brief := "Where: our GARM host, which has KVM"

	out, _ := agentHookOn(t, agentBody(t, unread, "Agent", "", brief))
	if !strings.Contains(out, `"permissionDecision":"deny"`) || !strings.Contains(out, "PreToolUse") || !strings.Contains(out, "nproc") {
		t.Fatalf("an unread brief must be denied with the read named: %s", out)
	}
	out, _ = agentHookOn(t, agentBody(t, read, "Agent", "", brief))
	if strings.TrimSpace(out) != "{}" {
		t.Fatalf("a brief after a read must pass: %s", out)
	}
	out, _ = agentHookOn(t, agentBody(t, read, "Task", "", brief))
	if strings.TrimSpace(out) != "{}" {
		t.Fatalf("Task is an Agent call: %s", out)
	}

	for name, c := range map[string]struct{ body, why string }{
		"another tool":     {agentBody(t, unread, "Bash", "", brief), "not Agent"},
		"a subagent":       {agentBody(t, unread, "Agent", "sub1", brief), "subagent"},
		"no transcript":    {agentBody(t, "", "Agent", "", brief), "names no transcript"},
		"unreadable":       {agentBody(t, unread+".missing", "Agent", "", brief), "transcript:"},
		"no user turn":     {agentBody(t, write(t, "nu.jsonl", "{}\n"), "Agent", "", brief), "no user turn"},
		"nothing to judge": {agentBody(t, unread+".missing", "Agent", "", "Write a test."), ""},
		"not JSON":         {"{", "not JSON"},
	} {
		out, errs := agentHookOn(t, c.body)
		if strings.TrimSpace(out) != "{}" {
			t.Errorf("%s: must allow, got %s", name, out)
		}
		// an allow that is not a verdict says why; a brief with nothing to judge
		// is allowed in silence, without even reading the transcript
		if c.why == "" && errs != "" || c.why != "" && !strings.Contains(errs, c.why) {
			t.Errorf("%s: stderr %q, want %q", name, errs, c.why)
		}
	}
}

func TestAgentHookStdinError(t *testing.T) {
	var out, errb bytes.Buffer
	c := RootCmd()
	c.SetIn(errReader{})
	c.SetOut(&out)
	c.SetErr(&errb)
	c.SetArgs([]string{"--agent-hook"})
	if err := c.Execute(); err != nil || strings.TrimSpace(out.String()) != "{}" || !strings.Contains(errb.String(), "cannot read the hook payload") {
		t.Fatalf("an unreadable payload is allowed, and says so: %v %q %q", err, out.String(), errb.String())
	}
}

func TestHookModeAgentHook(t *testing.T) {
	for _, a := range []string{"--agent-hook", "-agent-hook", "--agent-hook=true"} {
		if !hookMode([]string{a}) {
			t.Errorf("%s must be hook mode: a malformed run must not exit 2", a)
		}
	}
	if hookMode([]string{"--agent-hook=false"}) {
		t.Error("--agent-hook=false is not hook mode")
	}
}

func TestSweepAgents(t *testing.T) {
	brief := "Where: our GARM host, which has KVM"
	body := user(t, "one") + assistant(t, "a1", agentCall(brief)) + toolResult(t) +
		user(t, "two") + assistant(t, "b1", bash("ssh garm nproc")) + toolResult(t) + assistant(t, "b2", agentCall(brief)) + toolResult(t) +
		assistant(t, "b3", map[string]any{"type": "tool_use", "name": "Agent", "input": "bad"})
	var shown []agentFinding
	n, refused, err := sweepAgents(strings.NewReader(body), func(f agentFinding) { shown = append(shown, f) })
	if err != nil || n != 2 || refused != 1 || len(shown) != 1 {
		t.Fatalf("briefs %d refused %d shown %v err %v: only the unread brief is refused", n, refused, shown, err)
	}
	// the sweep command counts briefs, and prints the refused sentence
	dir := t.TempDir()
	writeIn(t, dir, "proj/s.jsonl", body)
	writeIn(t, dir, "proj/bad.jsonl", strings.Repeat("a", 70<<20)+"\n")
	var out bytes.Buffer
	c := RootCmd()
	c.SetOut(&out)
	c.SetErr(&out)
	c.SetArgs([]string{"sweep", "--agents", "--show", dir})
	if err := c.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "briefs judged 2, refused 1") || !strings.Contains(out.String(), "GARM host") || !strings.Contains(out.String(), "unreadable 1") {
		t.Fatalf("sweep --agents output: %s", out.String())
	}
}
