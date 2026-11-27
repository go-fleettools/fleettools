// Command mergedcheck refuses a reply that tells the user a pull request is
// merged when no tool call of that turn read the pull request's state from
// GitHub and found it MERGED.
//
// ⛔ A MERGE IS SAID FROM MEMORY MORE OFTEN THAN IT IS READ. On 2026-11-22 at
// 19:40 a session told the user a pull request was merged without having read
// it; the lesson was written and the recur ledger records the recurrence. A
// written lesson is not a gate, so this is the gate: a Stop hook, modelled on
// replystamp --hook and statecheck --hook, that reads the transcript and
// answers the hook protocol.
//
// # What it judges
//
// The text of the turn (every assistant text block after the user's last
// message). A sentence is a CLAIM when it carries "merged", "mergé(e)(s)" or
// "fusionné(e)(s)", unless it is a question, sits in code, a quotation or a
// block quote, is negated or hypothetical ("not merged yet", "pas encore
// fusionnée", "once merged", "sera fusionnée"), or uses the word as an
// adjective ("the merged result").
//
// The claim is bound to the pull request it NAMES: owner/repo#N, "PR 12",
// "pull request #12", ".../pull/12", or a bare #12 that is not an issue being
// closed. A claim naming no number is accepted only when the turn read exactly
// one pull request as MERGED.
//
// # What counts as a read
//
// A Bash call of the turn whose OUTPUT shows the state of THAT number:
// `gh pr view N --json state` (or `number,state`, `-q .state`, the text form),
// `gh pr list --json number,state`, `gh api repos/o/r/pulls/N`, and the output
// of ghmerge or ghrelease reporting the merge. `prwait` is NOT a read of the
// merge state. The latest read of a pull request wins: an OPEN read followed
// by a ghmerge that reports "merged" is a MERGED read.
//
// # Why it allows whenever it cannot read
//
// Same rule as the other hooks: a refusal is not a verdict. An unreadable
// transcript, a payload that names none, a subagent's payload, a session it
// cannot keep a count for: it says so on stderr and allows. A reply is refused
// once (by its last text's uuid), a Stop that follows a block stands down, and
// the exit status under --hook is always 0.
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/go-fleettools/fleettools/internal/transcript"
	"github.com/spf13/cobra"
)

const longHelp = `Refuses a reply that says a pull request is merged without a read of it.

  mergedcheck --transcript <file>   judge the last turn of a transcript
  mergedcheck --hook                answer a Stop payload read on stdin

A reply that says "merged" / "fusionné(e)" about a pull request is allowed only
when, in the same turn (after the user's last message), a Bash call printed that
pull request's state and it was MERGED: gh pr view N --json state, gh pr list
--json number,state, gh api repos/o/r/pulls/N, or ghmerge / ghrelease reporting
the merge. prwait is not a read of the merge state. The claim is bound to the
number named in the reply (N, owner/repo#N, PR N, .../pull/N); a claim naming
none needs exactly one pull request read as MERGED in the turn.

Not a claim: a question, code, a quotation, a block quote, a negation ("not
merged yet", "pas encore fusionnée"), a hypothesis ("once merged", "sera
fusionnée") or the adjective ("the merged result").

Wire it beside replystamp in the Stop hooks: ~/.local/bin/mergedcheck --hook

Like the other hooks it allows whatever it cannot read, refuses a given reply
once, stands down on stop_hook_active, and is always exit 0 under --hook.
Outside --hook: exit 0 allowed, 1 refused, 2 the question could not be put.`

func main() {
	serve(os.Args[1:], os.Stdout, os.Stderr, func() {
		fleet.WarnIfStale(os.Stderr) // not in hook mode: it is a network call
		cli.Run(RootCmd())
	})
}

// serve answers a hook invocation itself, and hands anything else to plain. A
// command that fails under --hook still answers "{}": a status other than 0 is
// a block.
func serve(args []string, stdout, stderr io.Writer, plain func()) {
	if !hookMode(args) {
		plain()
		return
	}
	if code := cli.Drive(RootCmd(), args, stdout, stderr); code != 0 {
		fmt.Fprintf(stderr, "mergedcheck: allowing: the command itself failed (status %d)\n", code)
		fmt.Fprintln(stdout, "{}")
	}
}

// hookMode reads --hook from the raw argv, before cobra: a malformed flag must
// not become the exit status 2 a Stop hook reads as a block. An unparseable
// value still means "hook"; only an explicit false does not.
func hookMode(args []string) bool {
	for _, a := range args {
		name, value, _ := strings.Cut(a, "=")
		if name != "--hook" && name != "-hook" {
			continue
		}
		if on, err := strconv.ParseBool(value); err != nil || on {
			return true
		}
	}
	return false
}

// stdin and stateDir are the seams the tests move.
var stdin io.Reader = os.Stdin

var stateDir = func() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "fleettools", "mergedcheck"), nil
}

// RootCmd builds the command.
func RootCmd() *cobra.Command {
	var tr string
	var hook bool
	c := cli.New("mergedcheck [--transcript <file> | --hook]",
		"refuse a reply that says a pull request is merged without a read of it", longHelp)
	c.Flags().StringVar(&tr, "transcript", "", "transcript .jsonl to judge the last turn of")
	c.Flags().BoolVar(&hook, "hook", false, "read a Stop hook payload on stdin and answer the hook protocol")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if hook {
			return runHook(cmd)
		}
		if tr == "" {
			return cli.Unanswerable("no transcript: pass --transcript <file>, or --hook to read a Stop payload on stdin")
		}
		t, err := lastTurnOfFile(tr)
		if err != nil {
			return cli.Unanswerable("%v", err)
		}
		why := t.judge()
		if why == "" {
			fmt.Fprintln(cli.Out(cmd), "CLEAN")
			return nil
		}
		fmt.Fprintln(cli.Out(cmd), why)
		return cli.Found("the reply says a pull request is merged with no read of it in the turn")
	}
	return c
}

// ------------------------------------------------------------ the transcript

// turn is the last turn of a session: what the assistant said and what its
// Bash calls printed.
type turn struct {
	text     string // every text block of the turn, one per line
	lastUUID string // the uuid of the last text-bearing entry
	calls    []call
}

// maxTail bounds how much of a transcript is read: the turn is at the end.
const maxTail = 32 << 20

func lastTurnOfFile(path string) (turn, error) {
	f, err := os.Open(path)
	if err != nil {
		return turn{}, err
	}
	defer f.Close()
	var r io.Reader = f
	// A file longer than maxTail is read from its last maxTail bytes; a shorter
	// one cannot be seeked that far back and is read whole.
	if pos, err := f.Seek(-maxTail, io.SeekEnd); err == nil && pos > 0 {
		// The first line of the window is cut in the middle: drop it.
		br := bufio.NewReader(f)
		_, _ = br.ReadBytes('\n')
		r = br
	}
	t, sawUser, err := turnOf(r)
	if err != nil {
		return turn{}, err
	}
	if !sawUser {
		return turn{}, fmt.Errorf("no turn the user took in the last %d MiB of the transcript", maxTail>>20)
	}
	if t.text == "" {
		return turn{}, fmt.Errorf("no reply text after the last turn the user took")
	}
	return t, nil
}

// turnOf reads the transcript and returns its last turn. ⛔ A turn starts at a
// genuine user entry (a tool_result is not one); a usage-limit message and a
// subagent's entries are not the session's replies.
func turnOf(r io.Reader) (t turn, sawUser bool, err error) {
	pending := map[string]int{} // tool_use id -> index in t.calls
	var texts []string
	err = transcript.Scan(r, func(e transcript.Entry, m transcript.Message) {
		switch {
		case e.Type == "user" && transcript.IsUserTurn(e, m):
			t, texts, sawUser = turn{}, nil, true
			pending = map[string]int{}
		case e.Type == "user":
			for _, b := range transcript.Blocks(m.Content) {
				if b.Type != "tool_result" {
					continue
				}
				if i, ok := pending[b.ToolUseID]; ok {
					t.calls[i].output = transcript.ResultText(b)
				}
			}
		case e.Type == "assistant" && !e.IsAPIErrorMessage:
			for _, b := range transcript.Blocks(m.Content) {
				switch {
				case b.Type == "text" && strings.TrimSpace(b.Text) != "":
					texts = append(texts, b.Text)
					t.lastUUID = e.UUID
				case b.Type == "tool_use" && b.Name == "Bash":
					var in struct {
						Command string `json:"command"`
					}
					_ = json.Unmarshal(b.Input, &in)
					pending[b.ID] = len(t.calls)
					t.calls = append(t.calls, call{command: in.Command})
				}
			}
		}
		t.text = strings.Join(texts, "\n")
	})
	return t, sawUser, err
}

// ------------------------------------------------------------ the verdict

// judge returns why the turn's text must be refused, or "" when it may stand.
func (t turn) judge() string {
	claims := claimsIn(t.text)
	if len(claims) == 0 {
		return ""
	}
	// The latest read of each pull request wins.
	var reads []read
	for _, c := range t.calls {
		reads = append(reads, readsOf(c)...)
	}
	latest := func(want ref) (read, bool) {
		for i := len(reads) - 1; i >= 0; i-- {
			if reads[i].ref.matches(want) {
				return reads[i], true
			}
		}
		return read{}, false
	}

	var claimed []ref
	seen := map[ref]bool{}
	for _, c := range claims {
		for _, r := range c.refs {
			if !seen[r] {
				seen[r] = true
				claimed = append(claimed, r)
			}
		}
	}
	if len(claimed) == 0 {
		// The claim names no number in its own sentence: take the numbers the
		// reply names anywhere.
		for _, r := range refsOf(prose(t.text)) {
			if !seen[r] {
				seen[r] = true
				claimed = append(claimed, r)
			}
		}
	}

	quote := ""
	for _, c := range claims {
		quote = clip(c.sentence, 140)
		break
	}
	if len(claimed) == 0 {
		// Unnamed: exactly one pull request read as MERGED in the turn.
		merged := map[ref]bool{}
		for _, r := range reads {
			if r.state == "MERGED" {
				merged[r.ref] = true
			}
		}
		if len(merged) == 1 {
			return ""
		}
		return fmt.Sprintf("The reply says a pull request is merged (%q) without naming which one, and this turn read %d pull request(s) as MERGED (exactly one is needed).\n%s",
			quote, len(merged), howToRead(ref{}))
	}
	var problems []string
	for _, want := range claimed {
		r, ok := latest(want)
		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf("%s: no call of this turn read its state.\n%s", label(want), howToRead(want)))
		case r.state != "MERGED":
			problems = append(problems, fmt.Sprintf("%s: this turn read it (%s) and GitHub said %s, not MERGED.", label(want), r.via, r.state))
		}
	}
	if len(problems) == 0 {
		return ""
	}
	return fmt.Sprintf("The reply says a pull request is merged (%q), but the turn did not read it as MERGED:\n- %s\nA merge is read, not remembered: a ghmerge/ghrelease that printed \"merged\", or a gh pr view of that number whose output says MERGED, in THIS turn. prwait is not a read of the merge state.",
		quote, strings.Join(problems, "\n- "))
}

func label(r ref) string {
	if r.repo != "" {
		return fmt.Sprintf("%s#%d", r.repo, r.number)
	}
	return fmt.Sprintf("#%d", r.number)
}

func howToRead(r ref) string {
	n, repo := "<N>", "<owner/repo>"
	if r.number > 0 {
		n = strconv.Itoa(r.number)
	}
	if r.repo != "" {
		repo = r.repo
	}
	return fmt.Sprintf("Read it: gh pr view %s -R %s --json number,state   (then restate the claim, or correct it)", n, repo)
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// ------------------------------------------------------------ the hook

type stopPayload struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	StopHookActive bool   `json:"stop_hook_active"`
	AgentID        string `json:"agent_id"`
}

type hookAnswer struct {
	Decision string `json:"decision,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

func allow(cmd *cobra.Command, format string, a ...any) error {
	if format != "" {
		fmt.Fprintf(cli.Err(cmd), "mergedcheck: allowing: "+format+"\n", a...)
	}
	return answer(cmd, hookAnswer{})
}

func answer(cmd *cobra.Command, a hookAnswer) error {
	b, _ := json.Marshal(a)
	fmt.Fprintln(cli.Out(cmd), string(b))
	return nil
}

func runHook(cmd *cobra.Command) error {
	raw, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 1<<20))
	if err != nil {
		return allow(cmd, "cannot read the hook payload: %v", err)
	}
	var p stopPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return allow(cmd, "the hook payload is not JSON: %v", err)
	}
	if p.TranscriptPath == "" {
		return allow(cmd, "the hook payload names no transcript")
	}
	if p.AgentID != "" {
		return allow(cmd, "the payload is a subagent's")
	}
	t, err := lastTurnOfFile(p.TranscriptPath)
	if err != nil {
		return allow(cmd, "%v", err)
	}
	why := t.judge()
	if why == "" {
		return allow(cmd, "")
	}
	// ⛔ IF IT CANNOT REMEMBER, IT MUST NOT REFUSE: a refusal it cannot count is
	// one it cannot stop.
	path, ok := statePath(p.SessionID)
	if !ok {
		return allow(cmd, "cannot keep state for this session, so cannot bound a refusal")
	}
	if p.StopHookActive {
		return allow(cmd, "this Stop follows a block, and the reply is already written: standing down")
	}
	id := t.lastUUID
	if id == "" {
		h := sha256.Sum256([]byte(t.text))
		id = hex.EncodeToString(h[:8])
	}
	if b, _ := os.ReadFile(path); strings.TrimSpace(string(b)) == id {
		return allow(cmd, "already refused this reply: standing down for it")
	}
	if os.MkdirAll(filepath.Dir(path), 0o755) != nil || os.WriteFile(path, []byte(id+"\n"), 0o644) != nil {
		return allow(cmd, "cannot record the refusal, so cannot bound it")
	}
	return answer(cmd, hookAnswer{Decision: "block", Reason: why})
}

func statePath(session string) (string, bool) {
	if session == "" || strings.ContainsAny(session, `/\.`) {
		return "", false
	}
	dir, err := stateDir()
	if err != nil {
		return "", false
	}
	return filepath.Join(dir, session+".last"), true
}
