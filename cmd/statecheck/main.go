// Command statecheck refuses a reply that states the CURRENT state of an
// external system, or asks the user to change it, when no tool call of that
// turn read it.
//
// ⛔ A SUMMARY OR A NOTE IS HISTORY WITH NO READ TIME. On 2026-11-25, 22:01 to
// 22:05, twice in a row: asked whether the OpenBSD VMs work with GARM, the
// reply said « quota RAM bloquant » from a compacted summary (the memory
// already said the quota had been raised); two minutes later it told the user
// to install a GitHub App on an organisation where it had been installed since
// the day before. One `gh api` call answered the second, and it was made only
// because the user asked « es-tu sûr ? ». Asking the user to act is the
// costliest form of the mistake: it spends THEIR action on a belief.
//
// Run as a Stop hook it reads the transcript, takes the reply that just ended,
// and refuses it when
//
//   - a sentence of it ASSERTS a state of an external system (a quota that is
//     blocked or reached, an App that is installed or not, a runner that is
//     registered, a token that is expired ...), or
//   - a sentence ASKS THE USER to change one (install, enable, raise, allow,
//     grant ...),
//
// and NO tool call of the turn could have read it. A read is a call of Bash,
// WebFetch, WebSearch, an MCP tool, an agent or a monitor; Read, Grep and Glob
// are NOT reads of the world, they read the very notes that went stale.
//
// # What it does not refuse
//
// A sentence that says it is unverified (« non vérifié », « not checked »), is
// hypothetical (si, if, would, devrait, peut-être), cites its read (« selon »,
// « vérifié », « according to »), is a question (for an assertion), or sits in
// code, a quotation or a block quote. French AND English. It judges only the
// LAST text of the turn: the claim that stands when the turn ends.
//
// ⛔ It is a heuristic over prose, and says so: every refusal names the
// sentence and the read that would answer it, and `statecheck sweep` replays
// the detector over old transcripts so its false positives are COUNTED rather
// than believed.
//
// Like replystamp it ALLOWS whenever it cannot read, refuses one reply once
// (by uuid), stands down on stop_hook_active, and under --hook always exits 0.
package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

const longHelp = `Refuses a reply that states, or asks the user to change, the current state of
an external system without a tool call of the turn having read it.

  statecheck --transcript <file>    report on the last reply of a transcript
  statecheck --hook                 answer a Stop payload read on stdin
  statecheck --agent-hook           answer a PreToolUse payload for the Agent tool:
                                    deny a brief that directs work onto a named
                                    machine (ssh user@host, an IP) or asserts a
                                    host's capacity (vCPU, GB of RAM, "has KVM")
                                    when no earlier call of the turn read that
                                    host (nproc, free, lscpu, uptime ...)
  statecheck sweep [--last N] [--show] [--agents] [dir]
                                    replay the detector over the N newest
                                    transcripts under dir (default
                                    ~/.claude/projects) and count refusals;
                                    --agents replays the Agent-brief gate

Wire it beside replystamp, in the Stop hooks of ~/.claude/settings.json:

  command: ~/.local/bin/statecheck --hook

and, in the PreToolUse hooks with matcher "Agent":

  command: ~/.local/bin/statecheck --agent-hook

A refusal names the sentence and the read that would answer it. The answer is
« run that read now and restate with its output », or say « non vérifié ».

It allows whenever it cannot read (unreadable transcript, no reply, a payload
of another event), refuses a given reply once, and stands down on
stop_hook_active. Under --hook the status is always 0; the verdict is the JSON.
Exit status otherwise: 0 clean, 1 a finding, 2 the question could not be put.`

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
		fmt.Fprintf(stderr, "statecheck: allowing: the command itself failed (status %d)\n", code)
		fmt.Fprintln(stdout, "{}")
	}
}

// hookMode reads --hook from the raw argv, before cobra, as replystamp does: a
// malformed flag must not become the exit status 2 a Stop hook reads as a block.
func hookMode(args []string) bool {
	for _, a := range args {
		name, value, _ := strings.Cut(a, "=")
		if name != "--hook" && name != "-hook" && name != "--agent-hook" && name != "-agent-hook" {
			continue
		}
		if on, err := strconv.ParseBool(value); err != nil || on {
			return true
		}
	}
	return false
}

var stdin io.Reader = os.Stdin

// stateDir is the seam tests redirect.
var stateDir = func() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "fleettools", "statecheck"), nil
}

// RootCmd builds the command.
func RootCmd() *cobra.Command {
	var transcript string
	var hook, agentHook bool
	c := cli.New("statecheck [--transcript <file> | --hook | --agent-hook | sweep]",
		"refuse a reply that states an external system's state without reading it", longHelp)
	c.Flags().StringVar(&transcript, "transcript", "", "transcript .jsonl to read the last reply from")
	c.Flags().BoolVar(&hook, "hook", false, "read a Stop hook payload on stdin and answer the hook protocol")
	c.Flags().BoolVar(&agentHook, "agent-hook", false, "read a PreToolUse payload for the Agent tool on stdin and deny a brief that states a machine's state unread")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if cli.HelpRequested(cmd) {
			return nil
		}
		if agentHook {
			return runAgentHook(cmd)
		}
		if hook {
			return runHook(cmd)
		}
		if transcript == "" {
			return cli.Unanswerable("no transcript: pass --transcript <file>, or --hook to read a Stop payload on stdin")
		}
		t, err := lastTurnOfFile(transcript)
		if err != nil {
			return cli.Unanswerable("%v", err)
		}
		fs := t.judge()
		if len(fs) == 0 {
			fmt.Fprintln(cli.Out(cmd), "CLEAN")
			return nil
		}
		fmt.Fprintln(cli.Out(cmd), reason(fs))
		return cli.Found("the reply states or requests an external state with no read of it in the turn")
	}
	c.AddCommand(sweepCmd())
	return c
}

// ------------------------------------------------------------ the transcript

type entry struct {
	Type              string          `json:"type"`
	IsMeta            bool            `json:"isMeta"`
	UUID              string          `json:"uuid"`
	Message           json.RawMessage `json:"message"`
	IsAPIErrorMessage bool            `json:"isApiErrorMessage"`
	IsSidechain       bool            `json:"isSidechain"`
}

type message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type block struct {
	Type string `json:"type"`
	Text string `json:"text"`
	Name string `json:"name"`
}

// turn is what a judgement needs of one user turn.
type turn struct {
	// last is the LAST assistant text of the turn: the claim standing when it ends.
	last     string
	lastUUID string
	// reads is how many tool calls of the turn could have read the world.
	reads int
}

// isRead reports whether a tool call can read an external system. Read, Grep,
// Glob and the editors read local files, which is where the stale note lives.
func isRead(name string) bool {
	switch name {
	case "Bash", "BashOutput", "WebFetch", "WebSearch", "Agent", "Task", "Monitor":
		return true
	}
	return strings.HasPrefix(name, "mcp__")
}

func decode(line []byte) (entry, message, bool) {
	var e entry
	if json.Unmarshal(line, &e) != nil || e.IsSidechain {
		return entry{}, message{}, false
	}
	var m message
	if len(e.Message) > 0 {
		_ = json.Unmarshal(e.Message, &m)
	}
	return e, m, true
}

// isUserTurn is replystamp's definition: a tool_result is not a turn, nor is
// injected (isMeta) context.
func isUserTurn(e entry, m message) bool {
	if e.IsMeta || m.Role != "user" {
		return false
	}
	var s string
	if json.Unmarshal(m.Content, &s) == nil {
		return strings.TrimSpace(s) != ""
	}
	var bs []block
	if json.Unmarshal(m.Content, &bs) != nil {
		return false
	}
	text := false
	for _, b := range bs {
		if b.Type == "tool_result" {
			return false
		}
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			text = true
		}
	}
	return text
}

// turnsOf scans a transcript forward and returns every turn that holds an
// assistant text. complete is false when no user turn was seen before the first
// assistant entry (a tail window that began mid-turn).
func turnsOf(r io.Reader) (turns []turn, sawUser bool, err error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	var cur *turn
	flush := func() {
		if cur != nil && cur.last != "" {
			turns = append(turns, *cur)
		}
	}
	for sc.Scan() {
		e, m, ok := decode(sc.Bytes())
		if !ok {
			continue
		}
		switch {
		case e.Type == "user" && isUserTurn(e, m):
			flush()
			cur, sawUser = &turn{}, true
		case e.Type == "assistant" && !e.IsAPIErrorMessage && cur != nil:
			t, reads := assistantParts(m.Content)
			cur.reads += reads
			if t != "" {
				cur.last, cur.lastUUID = t, e.UUID
			}
		}
	}
	flush()
	return turns, sawUser, sc.Err()
}

// assistantParts returns the text of an assistant message (its text blocks
// joined) and how many of its tool_use blocks are reads.
func assistantParts(raw json.RawMessage) (string, int) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s), 0
	}
	var bs []block
	if json.Unmarshal(raw, &bs) != nil {
		return "", 0
	}
	var parts []string
	reads := 0
	for _, b := range bs {
		switch b.Type {
		case "text":
			if strings.TrimSpace(b.Text) != "" {
				parts = append(parts, b.Text)
			}
		case "tool_use":
			if isRead(b.Name) {
				reads++
			}
		}
	}
	return strings.Join(parts, "\n"), reads
}

// lastTurnOfFile reads the newest turn from the END of the file, in doubling
// windows from 1 MiB to 64 MiB: a Stop hook must not scan a 900 MB transcript.
func lastTurnOfFile(path string) (turn, error) {
	st, err := os.Stat(path)
	if err != nil {
		return turn{}, fmt.Errorf("transcript: %w", err)
	}
	f, err := os.Open(path)
	if err != nil {
		return turn{}, err
	}
	defer f.Close()
	size := st.Size()
	for i := 0; i < 7; i++ { // 1 MiB << 6 == 64 MiB: bounded by construction
		win := int64(1<<20) << uint(i)
		if win > size {
			win = size
		}
		buf := make([]byte, win)
		n, err := f.ReadAt(buf, size-win)
		if err != nil && err != io.EOF {
			return turn{}, err
		}
		buf = buf[:n]
		whole := win == size
		if !whole {
			if i := bytes.IndexByte(buf, '\n'); i >= 0 {
				buf = buf[i+1:]
			} else {
				buf = nil
			}
		}
		ts, sawUser, err := turnsOf(bytes.NewReader(buf))
		if err != nil {
			return turn{}, err
		}
		if sawUser && len(ts) > 0 {
			return ts[len(ts)-1], nil
		}
		if whole {
			break
		}
	}
	return turn{}, fmt.Errorf("no reply text after a user turn in the transcript")
}

// ------------------------------------------------------------ the detector

type finding struct {
	sentence string
	kind     string // "asserts" or "asks"
	read     string // the read that would answer it
}

// judge is the verdict on a turn: nothing when the turn read the world.
func (t turn) judge() []finding {
	if t.reads > 0 {
		return nil
	}
	return detect(t.last)
}

var (
	fence      = regexp.MustCompile("(?s)```.*?```")
	inlineCode = regexp.MustCompile("`[^`\n]*`")
	quoted     = regexp.MustCompile(`«[^»]*»|“[^”]*”|"[^"\n]*"`)
)

// prose strips what is not the assistant speaking for itself: fenced and inline
// code, block quotes, quotations.
func prose(text string) string {
	text = fence.ReplaceAllString(text, " ")
	var keep []string
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), ">") {
			continue
		}
		keep = append(keep, l)
	}
	text = strings.Join(keep, "\n")
	text = inlineCode.ReplaceAllString(text, " ")
	return quoted.ReplaceAllString(text, " ")
}

// sentences splits on . ! ? ; followed by space, and on newlines, keeping the
// terminator so a question can be told from an assertion.
func sentences(text string) []string {
	var out []string
	var cur []rune
	rs := []rune(text)
	emit := func() {
		if s := strings.TrimSpace(string(cur)); s != "" {
			out = append(out, s)
		}
		cur = cur[:0]
	}
	for i, r := range rs {
		switch {
		case r == '\n':
			emit()
		case r == '.' || r == '!' || r == '?' || r == ';':
			cur = append(cur, r)
			if i+1 == len(rs) || rs[i+1] == ' ' || rs[i+1] == '\n' || rs[i+1] == ' ' {
				emit()
			}
		default:
			cur = append(cur, r)
		}
	}
	emit()
	return out
}

// word wraps alternatives so they match whole words, accented endings included
// (\b is ASCII-only in RE2 and would miss « bloqué »).
func word(alts string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}])(?:` + alts + `)(?:$|[^\p{L}\p{N}])`)
}

// The external systems, by the read that answers for each. The nouns are kept
// NARROW on purpose: a first sweep with « clé », « limit », « service », « app »
// and « page » refused 86 replies of 21666 and about 85 % were prose about code
// (a context limit, a cryptographic key, "the service blocks startup").
const (
	nQuota = `quotas?|rate[- ]limit\w*|limites? (?:hebdomadaire|d'usage|de session|de 5 ?h)\w*|(?:weekly|session|usage|5-hour|sonnet|opus) limit`
	nApp   = `github apps?|l['’]app|the app|apps? garm|garm`
	nToken = `(?:github|gh|api|access|oauth) tokens?|jetons? (?:github|d'accès)|ghscopes|scopes? (?:github|workflow|du token)`
	nRun   = `runners?`
	nHost  = `vms?|hôtes?|serveurs? (?:distant|garm)\w*`
)

var (
	// the external systems whose state lives outside this machine's notes
	system = word(nQuota + "|" + nApp + "|" + nToken + "|" + nRun + "|" + nHost)

	// a state asserted
	predicate = word(`bloqu(?:é|ée|és|ées)|bloquante?s?|blocked|blocking|install(?:é|ée|és|ées)|installed|not registered|pas enregistr[ée]\w*|désactiv[ée]e?s?|disabled|atteinte?s?|reached|exceeded|dépass(?:é|ée|és|ées)|exhausted|épuis(?:é|ée|és|ées)|expired|expir[ée]e?s?|down|unreachable|inaccessible|revoked|révoqu[ée]e?s?|rate[- ]limited`)

	// the user is asked to change a state
	askFr    = word(`installez|activez|augmentez|relevez|autorisez|accordez|approuvez|ajoutez|configurez|créez|validez|acceptez|à (?:installer|activer|augmenter|relever|autoriser|accorder|approuver|configurer)|doit être (?:installé|activé|relevé|autorisé)\w*|doivent être (?:installé|activé|relevé|autorisé)\w*`)
	askEnSub = word(`(?:please|you need to|you'll need to|you must|you should|can you|could you|will you|would you|pouvez-vous|peux-tu|merci de|veuillez|il faut|vous devez|tu dois|needs? (?:you )?to|needs? to be) (?:to )?(?:install|enable|raise|allow|grant|approve|add|configure|create|accept|increase|bump|installed|enabled|raised|allowed|granted|approved)`)
	askEnImp = regexp.MustCompile(`(?i)^(?:install|enable|raise|allow|grant|approve|add|configure|create|accept|increase|bump)\b`)

	// a sentence that is not a claim about now
	excuse = word(`si|if|whether|en général|generally|typically|usually|jamais|never|unless|would|could|might|may|should|maybe|perhaps|peut-être|probablement|probably|devrait|pourrait|serait|seraient|faudrait|supposons|suppose|hypoth\w+|non vérifié\w*|pas vérifié\w*|unverified|not verified|not checked|non contrôlé\w*|à vérifier|to check|to verify|à confirmer|je ne sais pas|i don't know|vérifié\w*|verified|checked|according to|selon|d'après|read at|lu à|output|sortie|returned|retourne`)
)

// reads names the read that would answer a sentence, from the system it names.
var readFor = []struct {
	re   *regexp.Regexp
	read string
}{
	{word(nQuota), "the quota query itself (`gh api rate_limit`, `aiquota status`, the provider's quota command), not a note about it"},
	{word(nApp), "`gh api orgs/<org>/installations` (or the repo's installation list)"},
	{word(nToken), "`ghscopes` / the token's scope listing"},
	{word(nRun), "`gh run list` / `prwait` / the runner list of the pool in question"},
	{word(nHost), "the host's own status or list command"},
}

func readHint(s string) string {
	for _, r := range readFor {
		if r.re.MatchString(s) {
			return r.read
		}
	}
	return "the command that lists that system's current state"
}

// detect returns the findings in a reply's text.
func detect(text string) []finding {
	var out []finding
	for _, s := range sentences(prose(text)) {
		if excuse.MatchString(s) || !system.MatchString(s) {
			continue
		}
		switch {
		case askFr.MatchString(s) || askEnSub.MatchString(s) || askEnImp.MatchString(strings.TrimLeft(s, "-*•0123456789. ")):
			out = append(out, finding{s, "asks", readHint(s)})
		case !strings.HasSuffix(s, "?") && predicate.MatchString(s):
			out = append(out, finding{s, "asserts", readHint(s)})
		}
	}
	return out
}

// reason is the text sent back to the model. It names each sentence and the
// read that answers it.
func reason(fs []finding) string {
	var b strings.Builder
	b.WriteString("This reply states or requests the CURRENT state of an external system, and no tool call of the turn read it. A note or a summary is history with no read time (memory a-state-stated-from-a-note-instead-of-read-live).\n")
	for i, f := range fs {
		if i == 3 {
			fmt.Fprintf(&b, "... and %d more.\n", len(fs)-3)
			break
		}
		what := "asserts a state"
		if f.kind == "asks" {
			what = "asks the user to change a state"
		}
		fmt.Fprintf(&b, "- %s: %q\n  the read that answers it: %s\n", what, clip(f.sentence, 200), f.read)
	}
	b.WriteString("Run that read NOW and restate with its output, or say « non vérifié ». That text is already written: do it in your NEXT turn. This reply will not be refused again.")
	return b.String()
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
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
		fmt.Fprintf(cli.Err(cmd), "statecheck: allowing: "+format+"\n", a...)
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
	fs := t.judge()
	if len(fs) == 0 {
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
		h := sha256.Sum256([]byte(t.last))
		id = hex.EncodeToString(h[:8])
	}
	if b, _ := os.ReadFile(path); strings.TrimSpace(string(b)) == id {
		return allow(cmd, "already refused this reply: standing down for it")
	}
	if os.MkdirAll(filepath.Dir(path), 0o755) != nil || os.WriteFile(path, []byte(id+"\n"), 0o644) != nil {
		return allow(cmd, "cannot record the refusal, so cannot bound it")
	}
	return answer(cmd, hookAnswer{Decision: "block", Reason: reason(fs)})
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

// ------------------------------------------------------------ the sweep

func sweepCmd() *cobra.Command {
	var last int
	var show, agents bool
	c := &cobra.Command{
		Use:           "sweep [dir]",
		Short:         "count the refusals the detector would have made over old transcripts",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.MaximumNArgs(1),
	}
	c.Flags().IntVar(&last, "last", 50, "how many of the newest transcripts to replay")
	c.Flags().BoolVar(&show, "show", false, "print every refused sentence")
	c.Flags().BoolVar(&agents, "agents", false, "replay the Agent-brief gate instead of the reply detector")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		dir := ""
		if len(args) == 1 {
			dir = args[0]
		} else {
			home, err := os.UserHomeDir()
			if err != nil {
				return cli.Unanswerable("%v", err)
			}
			dir = filepath.Join(home, ".claude", "projects")
		}
		files, err := newest(dir, last)
		if err != nil {
			return cli.Unanswerable("%v", err)
		}
		if len(files) == 0 {
			return cli.Unanswerable("no transcript under %s", dir)
		}
		var turnsN, refused, unreadable int
		out := cli.Out(cmd)
		for _, f := range files {
			fh, err := os.Open(f)
			if err != nil {
				unreadable++
				continue
			}
			if agents {
				b, r, err := sweepAgents(fh, func(x agentFinding) {
					if show {
						fmt.Fprintf(out, "%s\t%s\t%s\n", filepath.Base(f), x.host, clip(x.sentence, 240))
					}
				})
				fh.Close()
				if err != nil {
					unreadable++
					continue
				}
				turnsN, refused = turnsN+b, refused+r
				continue
			}
			ts, _, err := turnsOf(fh)
			fh.Close()
			if err != nil {
				unreadable++
				continue
			}
			for _, t := range ts {
				turnsN++
				if fs := t.judge(); len(fs) > 0 {
					refused++
					if show {
						for _, x := range fs {
							fmt.Fprintf(out, "%s\t%s\t%s\n", filepath.Base(f), x.kind, clip(x.sentence, 240))
						}
					}
				}
			}
		}
		what := "replies"
		if agents {
			what = "briefs"
		}
		fmt.Fprintf(out, "transcripts %d, unreadable %d, %s judged %d, refused %d\n", len(files), unreadable, what, turnsN, refused)
		return nil
	}
	return c
}

// newest returns the n most recently modified top-level transcripts of every
// project directory under dir (subagent files live deeper and are skipped).
func newest(dir string, n int) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*", "*.jsonl"))
	if err != nil {
		return nil, err
	}
	type fm struct {
		path string
		mod  time.Time
	}
	var all []fm
	for _, m := range matches {
		if st, err := os.Stat(m); err == nil {
			all = append(all, fm{m, st.ModTime()})
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].mod.After(all[j].mod) })
	var out []string
	for i := 0; i < len(all) && i < n; i++ {
		out = append(out, all[i].path)
	}
	return out, nil
}
