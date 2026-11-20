// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

// ctxgate cuts token spend without a human step and without thinning what the
// model reasons from.
//
//	ctxgate --precompact          # a PreCompact hook: what to keep, what to drop
//	ctxgate --hook [--max N]      # a PreToolUse hook on SendMessage
//	ctxgate report [--since 168h] # the context-size distribution of the calls
//
// ⛔ WHY IT EXISTS. Measured 2026-11-20 over 7 days and 104,263 API calls: cost
// is the re-reading of huge contexts. Calls carrying >= 400k tokens are 36 % of
// the calls and 64 % of every cache-read token; sonnet sub-agents carry 23 % of
// the cache reads in those buckets, because a resumed sub-agent keeps its whole
// history and the rule "follow-ups go to the SAME agent" resumes it again and
// again. One agent accumulated more than 900k. No hook can trigger a compaction,
// so the two levers are the text a compaction is told to preserve, and a refusal
// to resume an agent that has grown past a cap.
//
// A refusal is not a verdict: whatever ctxgate cannot read, it allows.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

// precompactCap is Claude Code's cap on the stdout of a PreCompact hook.
const precompactCap = 10000

// precompactText is what a compaction is told. It names what the next turns
// reason from and what only costs.
const precompactText = `COMPACTION INSTRUCTIONS (ctxgate). Write the summary so that work can resume with no loss of reasoning. Keep the identifiers EXACT, as they were read, never reconstructed or paraphrased; drop what only costs tokens.

KEEP VERBATIM
- Open decisions awaiting the user: the question, the options offered, and who is waiting on whom.
- The user's latest instructions and stated preferences, in their words, with the order they were given in. A preference stated earlier and never withdrawn still stands.
- Every pull request, issue and tag number in play, with its repository and state (open, merged, released, red, green), exactly as last read.
- Commit SHAs exactly as read from git or the API (never retyped or extended from a prefix), with the branch each belongs to.
- Branch names, clone and worktree paths, and the repository each one belongs to.
- The leases and claims held (agentsync claim: repository, holder, expiry) and the ones being waited on.
- file:line references that are in play, with one line on why each matters.
- Agent ids and names, each with its model, its pending task, and whether it has reported. Do not carry a finished agent's transcript, only its conclusion.
- Versions installed or released, and the command that proves each (a measurement, a test name, a check run).
- Failures not yet explained: the exact error text, the command that produced it, and what has already been ruled out.
- Facts measured in this session, with the number, the unit and the command that gave it.

DROP
- Raw tool outputs, directory listings, grep results and logs: keep the one line that was concluded from them.
- File contents already read and acted on: keep the path and the line that mattered.
- Superseded plans, abandoned approaches and earlier drafts: keep only the current plan and, in one line, why an alternative was rejected.
- Repeated hand-back reports from sub-agents: keep the final state once.
- Progress narration, acknowledgements and restated instructions.

FORMAT
Sections in this order: current goal; decisions awaiting the user; state (PR, branch, SHA, tag, leases); agents; open problems; next steps. Short lines, identifiers in code spans. If a fact is uncertain say so and name how to re-check it; never fill a gap with a guess.`

// precompactOutput is the text for a PreCompact hook: the fixed text, then the
// user's own custom_instructions, cut so that the whole stays within the cap.
func precompactOutput(custom string) string {
	custom = strings.ToValidUTF8(strings.TrimSpace(custom), "")
	if custom != "" {
		// room is positive: a test pins the base text under half the cap.
		head := precompactText + "\n\nADDITIONAL INSTRUCTIONS (from the compaction request):\n"
		return head + cutBytes(custom, precompactCap-len(head))
	}
	return precompactText
}

// cutBytes cuts s to at most n bytes, never in the middle of a character.
func cutBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

type precompactPayload struct {
	Trigger            string `json:"trigger"`
	CustomInstructions string `json:"custom_instructions"`
}

func precompactMode(out io.Writer, stdin io.Reader) int {
	var p precompactPayload
	// Bounded: a hook payload is small. Anything unreadable still gets the base text.
	if b, err := io.ReadAll(io.LimitReader(stdin, 1<<20)); err == nil {
		_ = json.Unmarshal(b, &p)
	}
	fmt.Fprintln(out, precompactOutput(p.CustomInstructions))
	return 0
}

// hookPayload is the part of a PreToolUse payload that matters here.
type hookPayload struct {
	ToolName       string `json:"tool_name"`
	TranscriptPath string `json:"transcript_path"`
	ToolInput      struct {
		To      string `json:"to"`
		Message string `json:"message"`
	} `json:"tool_input"`
}

var agentIDRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

var waiverRE = regexp.MustCompile(`(?m)^[ \t]*ctx-waiver:[ \t]*(\S.*?)[ \t]*$`)

// agentID extracts the id from a SendMessage target: "a8f6...", "agent-a8f6..."
// or either followed by " [ref]". A name, or anything that could escape the
// directory, is not an id.
func agentID(to string) (string, bool) {
	to, _, _ = strings.Cut(strings.TrimSpace(to), " [")
	to = strings.TrimPrefix(to, "agent-")
	return to, agentIDRE.MatchString(to)
}

// subagentsDir is where the current session's sub-agent transcripts live.
func subagentsDir(transcript string) string {
	d := filepath.Dir(transcript)
	if filepath.Base(d) == "subagents" {
		return d // the caller is itself a sub-agent
	}
	return strings.TrimSuffix(transcript, ".jsonl") + string(filepath.Separator) + "subagents"
}

// tailLimit is the most of a transcript's tail the hook will read.
const tailLimit = 128 << 20

type verdict struct {
	deny   bool
	reason string
	note   string // a stderr notice for an allowance worth stating
}

func decide(p hookPayload, max int64) verdict {
	if p.ToolName != "SendMessage" || p.TranscriptPath == "" {
		return verdict{}
	}
	id, ok := agentID(p.ToolInput.To)
	if !ok {
		return verdict{} // a name: another session, a teammate, "main"
	}
	path := filepath.Join(subagentsDir(p.TranscriptPath), "agent-"+id+".jsonl")
	if _, err := os.Stat(path); err != nil {
		return verdict{} // unknown to this session
	}
	n, err := lastContext(path, tailLimit)
	if err != nil {
		return verdict{note: fmt.Sprintf("ctxgate: could not read the context of agent %s (%v); allowing (a refusal is not a verdict)", id, err)}
	}
	if n <= max {
		return verdict{}
	}
	if m := waiverRE.FindStringSubmatch(p.ToolInput.Message); m != nil {
		return verdict{note: fmt.Sprintf("ctxgate: resume of agent %s (%dk) waived: %s", id, n/1000, m[1])}
	}
	return verdict{deny: true, reason: fmt.Sprintf(
		"ctxgate refuses this SendMessage: agent %s carries %dk of context (limit %dk); spawn a fresh agent with a handoff brief (goal, state: branch/PR/SHA, what is done, what is left) instead of resuming. Every resumed turn re-reads all %dk. If the history is truly needed, put a line \"ctx-waiver: <reason>\" in the message.",
		id, n/1000, max/1000, n/1000)}
}

func hookMode(out, errOut io.Writer, stdin io.Reader, max int64) int {
	b, err := io.ReadAll(io.LimitReader(stdin, 16<<20))
	if err != nil {
		fmt.Fprintln(errOut, "ctxgate: could not read the hook payload; allowing (a refusal is not a verdict)")
		return 0
	}
	var p hookPayload
	if err := json.Unmarshal(b, &p); err != nil {
		fmt.Fprintf(errOut, "ctxgate: could not decode the hook payload (%v); allowing (a refusal is not a verdict)\n", err)
		return 0
	}
	v := decide(p, max)
	if v.note != "" {
		fmt.Fprintln(errOut, v.note)
	}
	if v.deny {
		j, _ := json.Marshal(map[string]any{"hookSpecificOutput": map[string]string{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       "deny",
			"permissionDecisionReason": v.reason,
		}})
		fmt.Fprintln(out, string(j))
	}
	return 0
}

var longHelp = `ctxgate cuts token spend automatically and keeps what reasoning needs.

  ctxgate --precompact           PreCompact hook: print compaction instructions that
                                 keep decisions, PR/issue/tag numbers, SHAs, branches,
                                 leases, file:line, agent ids and the user's latest
                                 instructions verbatim, and drop raw outputs, listings,
                                 dumps, superseded plans and repeated reports. The text
                                 stays under 10,000 characters (Claude Code's cap);
                                 custom_instructions is appended, cut to fit.
  ctxgate --hook [--max N]       PreToolUse hook on SendMessage: refuse to resume a
                                 sub-agent of this session whose last context is above
                                 N tokens (default 200000), and say to spawn a fresh
                                 agent with a handoff brief. A "ctx-waiver: <reason>"
                                 line in the message allows it, the reason echoed.
  ctxgate report [--since 168h]  the context-size distribution of the API calls under
                                 ~/.claude/projects: calls and share of cache-read
                                 tokens per bucket, for all, main, sub-agents and each
                                 model, to measure the effect before and after.

ALLOWED: another tool than SendMessage, a target that is a name or an agent this
session has no transcript for (another session, a teammate), a transcript that
cannot be read, a payload that cannot be decoded. A refusal is not a verdict.

Context size of a call = input + cache-read + cache-creation tokens of the last
assistant entry of the agent's transcript. Entries sharing a message id are one
API call. Lines above 16 MiB are skipped and counted.

Exit status: 0 for --precompact and --hook (the answer is the stdout); report
exits 2 when it could read no transcript.`

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// RootCmd builds the command. Tests drive it through run.
func RootCmd() *cobra.Command {
	var hook, pre bool
	var max int64
	c := cli.New("ctxgate --precompact | --hook [--max N] | report [--since D]",
		"Cut token spend: compaction instructions, a cap on resumed sub-agents, a context report", longHelp)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		switch {
		case pre:
			return cli.FromStatus(precompactMode(cli.Out(cmd), cmd.InOrStdin()))
		case hook:
			return cli.FromStatus(hookMode(cli.Out(cmd), cli.Err(cmd), cmd.InOrStdin(), max))
		}
		return cli.Unanswerable("nothing to do: give --precompact, --hook or the report subcommand")
	}
	c.Flags().BoolVar(&pre, "precompact", false, "print compaction instructions (a PreCompact hook)")
	c.Flags().BoolVar(&hook, "hook", false, "answer a PreToolUse payload: deny SendMessage to an agent above --max")
	c.Flags().Int64Var(&max, "max", 200000, "context size (tokens) above which resuming an agent is refused")
	c.AddCommand(reportCmd())
	return c
}

func run(args []string, out, errOut io.Writer) int {
	return cli.Drive(RootCmd(), args, out, errOut)
}
