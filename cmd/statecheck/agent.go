package main

// The Agent-brief gate: `statecheck --agent-hook`, a PreToolUse hook for the
// Agent tool.
//
// ⛔ A BRIEF IS A CLAIM TOO. On 2026-11-26 21:15 a brief told a subagent to run
// qemu guests on « our GARM host, which has KVM », from an old note (64 vCPU).
// Live, the host was the controller downsized to 2 vCPU and 3.8 GB with no swap;
// the agent ran a 2 GB guest there for 48 minutes while a CI runner went down.
// The Stop hook reads REPLIES and could not see it: the claim was in a brief.
//
// It denies an Agent call whose prompt asserts the capacity of a host (« 64
// vCPU », « 16 GB of RAM », « has KVM ») in a sentence that speaks of a host,
// machine, server, node ... or names one (ssh user@host, an IPv4 address),
// unless a tool call EARLIER IN THE SAME TURN read that machine: a Bash command
// that holds the named host and a probe (nproc, free, lscpu, uptime,
// /proc/meminfo, sysctl ...). With no named target any probe of the turn answers.
//
// ⛔ Naming a machine WITHOUT a capacity is not refused. The first version also
// refused « ssh host ... run », and `statecheck sweep --agents` over the 50
// newest transcripts flagged about 270 sentences, nearly all descriptive
// (a user-granted host, a module version « zpkb@v0.82.0 » read as user@host).
// Requiring the stated SIZE, the thing that goes stale, left 4 of 4145. A sentence that itself tells the agent to read or verify
// (« re-read nproc before launching ») is not an assertion and is let through.
//
// It allows whenever it cannot read (unreadable transcript, a subagent's own
// payload, another tool), and a refusal is not a verdict: the deny names the
// sentence and the read that answers it.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/spf13/cobra"
)

type agentPayload struct {
	TranscriptPath string `json:"transcript_path"`
	ToolName       string `json:"tool_name"`
	AgentID        string `json:"agent_id"`
	ToolInput      struct {
		Prompt string `json:"prompt"`
	} `json:"tool_input"`
}

var (
	// sshTarget: `ssh [opts] [user@]host` (also scp/rsync user@host:).
	sshTarget = regexp.MustCompile(`(?i)\b(?:ssh|scp|rsync|sftp)\b(?:\s+-[A-Za-z]+(?:\s+[^\s@-][^\s@]*)?)*\s+([A-Za-z0-9_.-]+@)?([A-Za-z0-9][A-Za-z0-9.-]*[A-Za-z0-9])\b`)
	// atTarget: user@host with a dotted host or an IP, not an e-mail-looking
	// address at a well-known mail domain.
	atTarget    = regexp.MustCompile(`\b[a-z_][a-z0-9_-]*@([A-Za-z0-9][A-Za-z0-9-]*(?:\.[A-Za-z0-9-]+)*)\b`)
	version     = regexp.MustCompile(`^v?\d+\.\d+(\.\d+)?([-+.][0-9A-Za-z.+-]*)?$`)
	ipv4        = regexp.MustCompile(`\b((?:25[0-5]|2[0-4]\d|1?\d?\d)(?:\.(?:25[0-5]|2[0-4]\d|1?\d?\d)){3})\b`)
	sentenceEnd = regexp.MustCompile(`[.!?;]\s+`)
	// capacity: a stated size of a machine.
	capacity = regexp.MustCompile(`(?i)(\b\d+\s*(?:v?cpus?|cores?|threads)\b|\b\d+(?:\.\d+)?\s*(?:gb|gib|go)\s+(?:of\s+)?(?:ram|memory|mémoire|swap)\b|\b(?:has|have|with|a|avec|dispose\s+de|offers?)\s+kvm\b|\bkvm\s+(?:is\s+)?(?:available|enabled|présent|disponible)\b)`)
	// hostCtx: the sentence speaks of a machine that is not a guest.
	hostCtx = regexp.MustCompile(`(?i)\b(host|hôte|machine|server|serveur|node|nœud|controller|contrôleur|box|runner|bare[- ]metal|hypervisor)\b`)
	// readsWord: the sentence tells the agent to read, so it is no assertion.
	readsWord = regexp.MustCompile(`(?i)\b(re-?read|verify|verif\w*|vérif\w*|check|checks|relis|relire|contrôl\w*|measure|nproc|free\s+-|lscpu|unverified|non vérifié)\b`)
	// probe: a command that reads a machine's size.
	probe = regexp.MustCompile(`(?i)(\bnproc\b|\bfree\b|\blscpu\b|\buptime\b|/proc/(?:meminfo|cpuinfo)|\bsysctl\b|\bvmstat\b|\bhtop\b|\bgarm-cli\b|\bosvm\b|\bkvm-ok\b|/dev/kvm|\bvirsh\b|\bdf\b)`)
)

// agentFinding is one refused sentence of a brief.
type agentFinding struct {
	sentence string
	host     string // the named host, or "" for a capacity assertion alone
}

// briefSentences splits on sentence ends and newlines.
func briefSentences(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		for _, p := range sentenceEnd.Split(line, -1) {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

// namedHosts returns the machines a sentence directs work onto.
func namedHosts(sent string) []string {
	var hs []string
	seen := map[string]bool{}
	add := func(h string) {
		h = strings.ToLower(strings.TrimRight(h, "."))
		if h == "" || seen[h] || h == "localhost" || strings.HasPrefix(h, "127.") || h == "0.0.0.0" || h == "github.com" {
			return
		}
		seen[h] = true
		hs = append(hs, h)
	}
	for _, m := range sshTarget.FindAllStringSubmatch(sent, -1) {
		add(m[2])
	}
	for _, m := range atTarget.FindAllStringSubmatch(sent, -1) {
		// name@v0.82.0 is a module version, name@1.2.3 a package version.
		if (strings.Contains(m[1], ".") || ipv4.MatchString(m[1])) && !version.MatchString(m[1]) && !strings.HasSuffix(m[1], "noreply.github.com") {
			add(m[1])
		}
	}
	for _, m := range ipv4.FindAllStringSubmatch(sent, -1) {
		add(m[1])
	}
	return hs
}

// judgeBrief returns the sentences of a brief that direct work onto, or assert
// the size of, a machine no earlier call of the turn read. cmds is the text of
// every earlier tool call of the turn.
func judgeBrief(prompt string, cmds []string) []agentFinding {
	var fs []agentFinding
	for _, s := range briefSentences(prompt) {
		if readsWord.MatchString(s) {
			continue
		}
		hosts := namedHosts(s)
		// ⛔ A named machine alone is not an assertion: a brief that says
		// « ssh fractal, run the tests » carries what the user granted. It is
		// the SIZE stated for a host (« 64 vCPU », « has KVM ») that goes stale,
		// so a capacity is required; a named host only sharpens which read
		// answers it (measured over 50 transcripts: naming alone refused ~80 of
		// 272 briefs, nearly all descriptive).
		if !capacity.MatchString(s) || !(len(hosts) > 0 || hostCtx.MatchString(s)) {
			continue
		}
		if len(hosts) == 0 {
			if !anyProbe(cmds) {
				fs = append(fs, agentFinding{s, ""})
			}
			continue
		}
		for _, h := range hosts {
			if !readHost(cmds, h) {
				fs = append(fs, agentFinding{s, h})
				break
			}
		}
	}
	return fs
}

// readHost: an earlier command holds the host and a probe.
func readHost(cmds []string, host string) bool {
	for _, c := range cmds {
		if strings.Contains(strings.ToLower(c), host) && probe.MatchString(c) {
			return true
		}
	}
	return false
}

func anyProbe(cmds []string) bool {
	for _, c := range cmds {
		if probe.MatchString(c) {
			return true
		}
	}
	return false
}

func agentReason(fs []agentFinding) string {
	var b strings.Builder
	b.WriteString("statecheck: this brief states the state of a machine no call of this turn read.\n")
	for _, f := range fs {
		if f.host != "" {
			fmt.Fprintf(&b, "  - directs work onto %s: %q\n", f.host, clip(f.sentence, 200))
		} else {
			fmt.Fprintf(&b, "  - asserts a host's capacity: %q\n", clip(f.sentence, 200))
		}
	}
	b.WriteString("A note is history with no read time. Run the read now (ssh <host> 'nproc; free -m; uptime') and brief from its output, " +
		"or tell the agent to re-read the host before it launches anything.")
	return b.String()
}

// ------------------------------------------------------------ transcript calls

type toolBlock struct {
	Type  string          `json:"type"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// callText is the searchable text of a tool call that can read a machine: the
// command of a Bash call, the raw input of an MCP call. Other calls give "".
func callText(name string, input json.RawMessage) string {
	if name == "Bash" {
		var in struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(input, &in) == nil {
			return in.Command
		}
		return ""
	}
	if strings.HasPrefix(name, "mcp__") {
		return string(input)
	}
	return ""
}

// callsOf returns the texts of the tool calls of each user turn, in order.
// For a brief, replay(t) is the slice of calls before it.
func callsOf(m message) []toolBlock {
	var bs []toolBlock
	if json.Unmarshal(m.Content, &bs) != nil {
		return nil
	}
	return bs
}

// lastTurnCalls reads the tool-call texts of the newest user turn, from the end
// of the file in doubling windows (as lastTurnOfFile).
func lastTurnCalls(path string) ([]string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("transcript: %w", err)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	size := st.Size()
	for i := 0; i < 7; i++ {
		win := int64(1<<20) << uint(i)
		if win > size {
			win = size
		}
		buf := make([]byte, win)
		n, err := f.ReadAt(buf, size-win)
		if err != nil && err != io.EOF {
			return nil, err
		}
		buf = buf[:n]
		whole := win == size
		if !whole {
			if j := bytes.IndexByte(buf, '\n'); j >= 0 {
				buf = buf[j+1:]
			} else {
				buf = nil
			}
		}
		cmds, sawUser, err := callsOfLastTurn(bytes.NewReader(buf))
		if err != nil {
			return nil, err
		}
		if sawUser {
			return cmds, nil
		}
		if whole {
			break
		}
	}
	return nil, fmt.Errorf("no user turn in the transcript")
}

func callsOfLastTurn(r io.Reader) (cmds []string, sawUser bool, err error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		e, m, ok := decode(sc.Bytes())
		if !ok {
			continue
		}
		switch {
		case e.Type == "user" && isUserTurn(e, m):
			cmds, sawUser = nil, true
		case e.Type == "assistant" && sawUser:
			for _, b := range callsOf(m) {
				if b.Type == "tool_use" {
					if t := callText(b.Name, b.Input); t != "" {
						cmds = append(cmds, t)
					}
				}
			}
		}
	}
	return cmds, sawUser, sc.Err()
}

// ------------------------------------------------------------ the hook

func agentDecision(cmd *cobra.Command, decision, reason string) error {
	b, _ := json.Marshal(map[string]any{"hookSpecificOutput": map[string]string{
		"hookEventName":            "PreToolUse",
		"permissionDecision":       decision,
		"permissionDecisionReason": reason,
	}})
	fmt.Fprintln(cli.Out(cmd), string(b))
	return nil
}

func allowAgent(cmd *cobra.Command, format string, a ...any) error {
	if format != "" {
		fmt.Fprintf(cli.Err(cmd), "statecheck: allowing: "+format+"\n", a...)
	}
	return answer(cmd, hookAnswer{})
}

func runAgentHook(cmd *cobra.Command) error {
	raw, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 4<<20))
	if err != nil {
		return allowAgent(cmd, "cannot read the hook payload: %v", err)
	}
	var p agentPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return allowAgent(cmd, "the hook payload is not JSON: %v", err)
	}
	if p.ToolName != "Agent" && p.ToolName != "Task" {
		return allowAgent(cmd, "the payload is for tool %q, not Agent", p.ToolName)
	}
	if p.AgentID != "" {
		return allowAgent(cmd, "the payload is a subagent's")
	}
	if p.TranscriptPath == "" {
		return allowAgent(cmd, "the hook payload names no transcript")
	}
	// A brief with nothing to judge needs no transcript: do not read it.
	if len(judgeBrief(p.ToolInput.Prompt, nil)) == 0 {
		return allowAgent(cmd, "")
	}
	cmds, err := lastTurnCalls(p.TranscriptPath)
	if err != nil {
		return allowAgent(cmd, "%v", err)
	}
	fs := judgeBrief(p.ToolInput.Prompt, cmds)
	if len(fs) == 0 {
		return allowAgent(cmd, "")
	}
	return agentDecision(cmd, "deny", agentReason(fs))
}

// sweepAgents replays judgeBrief over the Agent calls of old transcripts, with
// the calls that preceded each in its turn, and returns (briefs, refused).
func sweepAgents(r io.Reader, show func(agentFinding)) (briefs, refused int, err error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	var cmds []string
	for sc.Scan() {
		e, m, ok := decode(sc.Bytes())
		if !ok {
			continue
		}
		switch {
		case e.Type == "user" && isUserTurn(e, m):
			cmds = nil
		case e.Type == "assistant":
			for _, b := range callsOf(m) {
				if b.Type != "tool_use" {
					continue
				}
				if b.Name == "Agent" || b.Name == "Task" {
					var in struct {
						Prompt string `json:"prompt"`
					}
					if json.Unmarshal(b.Input, &in) != nil {
						continue
					}
					briefs++
					if fs := judgeBrief(in.Prompt, cmds); len(fs) > 0 {
						refused++
						for _, f := range fs {
							show(f)
						}
					}
				} else if t := callText(b.Name, b.Input); t != "" {
					cmds = append(cmds, t)
				}
			}
		}
	}
	return briefs, refused, sc.Err()
}
