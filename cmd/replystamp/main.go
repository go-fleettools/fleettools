// Command replystamp refuses a reply that does not carry the local date and
// time CLAUDE.md requires of every one.
//
// ⛔ A WRITTEN RULE WITH NO GATE IS BROKEN FOR A WHOLE SESSION AT A TIME.
// The rule is the first one in CLAUDE.md:
//
//	Start every reply with the local date and time, 24-hour clock:
//	**[2026-11-01 21:05 CET]**, never am/pm. Take it from the latest
//	PostToolUse hook line « Current date/time: … », never from the session
//	start date. Re-read this after every compaction.
//
// Measured on 2026-11-11, in this session: EVERY reply from the start of the
// session until the moment the rule was re-read after a compaction carried no
// stamp at all. Nothing said so. The rule had been written, read, and silently
// dropped, because the only thing enforcing it was my own memory of it — and a
// compaction is precisely the event that empties that memory. The rule even
// says "re-read this after every compaction", which is an instruction to
// remember to remember.
//
// So the check runs instead of being remembered. It is a Stop hook: the reply
// is already written when Stop fires, the transcript holds it, and a hook that
// answers `{"decision":"block"}` sends it back to be written again with the
// stamp on it.
//
// # What it judges, and what it deliberately does not
//
// It judges the FIRST text block of the turn — the start of the reply, which is
// what the rule is about. Text written between tool calls later in the same
// turn is not a reply's beginning and is not asked to carry a stamp.
//
// The four findings are each a distinct way the rule has been broken:
//
//	missing   no **[...]** at the start at all      (this session, every reply)
//	am/pm     a 12-hour clock                       (the rule names it)
//	hour      an hour of 24 or more
//	stale     a stamp far from the clock NOW        (the session start date)
//
// ⛔ IT CANNOT CHECK THE ONE THING THE RULE ASKS FOR MOST DIRECTLY: that the
// time came from the latest PostToolUse hook line rather than from somewhere
// else. A stamp is a string; where it was copied from leaves no trace. What
// staleness measures is the CONSEQUENCE of the forbidden source — a session
// start date is hours or days from now — and that is the part a program can
// see. A stamp inside the tolerance that was nevertheless invented passes.
//
// # Why it allows whenever it cannot read
//
// Same rule as recur's gate, for the same reason: a refusal is not a verdict.
// An unreadable transcript, a turn that ended on a tool call, a payload that is
// not a Stop payload — each of those is a question that could not be put, and
// blocking the session on one would make the gate's own breakage look like a
// finding. It says so on stderr and gets out of the way.
//
// # Why it stops blocking after a while
//
// A Stop hook that blocks is re-entered when the next reply ends, so a model
// that cannot satisfy it loops. --max-blocks bounds that: after this many
// consecutive blocks in one session it allows, saying that it has given up. The
// count is per session, kept beside the other durable state, and is cleared the
// moment a stamped reply goes through.
//
// Exit status, outside --hook: 0 the reply is stamped, 1 it is not, 2 the
// question could not be put. Inside --hook the status belongs to the hook
// protocol, not to that convention: it is 0 whatever the verdict, and the
// verdict travels in the JSON on stdout, so that a crash of this program can
// never be read as a refusal of the reply.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

const longHelp = `Refuses a reply that does not open with the local date and time.

CLAUDE.md's first rule asks every reply to start with ` + "`**[2026-11-01 21:05 CET]**`" + `,
on a 24-hour clock, taken from the latest PostToolUse hook line. Nothing
enforced it, and on 2026-11-11 every reply of a session went out without one.

Run as a Stop hook it reads the transcript, finds the first text block of the
reply that just ended, and answers the hook protocol: a reply with no stamp is
blocked and written again.

  replystamp --transcript <file>    report on the last reply in a transcript
  replystamp --hook                 answer a Stop payload read on stdin

It ALLOWS whenever it cannot read -- an unreadable transcript, a turn with no
text in it, a payload that is not a Stop payload. A refusal is not a verdict.

Exit status: 0 stamped, 1 not stamped, 2 the question could not be put. Under
--hook the status is the hook protocol's and is always 0; the verdict is the
JSON on stdout.`

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// now is the seam the tests move, because every verdict about staleness is a
// comparison against it.
var now = time.Now

// stateDir is the seam the tests redirect, so that a test run cannot touch the
// block counts of a live session.
var stateDir = defaultStateDir

func defaultStateDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "fleettools", "replystamp"), nil
}

type options struct {
	transcript string
	hook       bool
	tolerance  time.Duration
	maxBlocks  int
}

// RootCmd builds the command.
func RootCmd() *cobra.Command {
	var o options
	c := cli.New("replystamp [--transcript <file> | --hook]",
		"refuse a reply that does not open with the local date and time", longHelp)
	c.Flags().StringVar(&o.transcript, "transcript", "", "transcript .jsonl to read the last reply from")
	c.Flags().BoolVar(&o.hook, "hook", false, "read a Stop hook payload on stdin and answer the hook protocol")
	// ⛔ THREE HOURS, NOT FIFTEEN MINUTES, and the width is the point.
	//
	// The stamp is written at the START of a reply and this runs at its END,
	// with every tool call in between: a prwait on a slow lane, a clone, a
	// toolchain install. A tolerance tight enough to be a clock would refuse
	// correct replies, and each refusal costs a whole turn. What it is actually
	// looking for is a stamp from the session start -- hours or days away -- so
	// three hours catches that and leaves honest long turns alone.
	c.Flags().DurationVar(&o.tolerance, "tolerance", 3*time.Hour, "how far from now a stamp may be before it reads as stale")
	c.Flags().IntVar(&o.maxBlocks, "max-blocks", 2, "consecutive blocks in one session before it gives up and allows")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		return run(cmd, &o)
	}
	return c
}

func run(cmd *cobra.Command, o *options) error {
	if cli.HelpRequested(cmd) {
		return nil
	}
	if o.hook {
		return runHook(cmd, o)
	}
	if o.transcript == "" {
		return cli.Unanswerable("no transcript: pass --transcript <file>, or --hook to read a Stop payload on stdin")
	}
	f, err := findReply(o.transcript)
	if err != nil {
		return cli.Unanswerable("%v", err)
	}
	v := judge(f.text, judgedAgainst(f), o.tolerance)
	fmt.Fprintln(cli.Out(cmd), v.report(f))
	if v.kind == stamped {
		return nil
	}
	return cli.Found("the reply does not carry the stamp CLAUDE.md asks of every one")
}

// ---------------------------------------------------------------- the reply

// aReply is the text the rule is about: the first text block written after the
// last real turn the user took.
type aReply struct {
	text string
	uuid string
	// at is the instant the transcript recorded the reply. It is the clock the
	// stamp is judged against; see judgedAgainst.
	at     time.Time
	haveAt bool
}

type entry struct {
	Type    string          `json:"type"`
	IsMeta  bool            `json:"isMeta"`
	UUID    string          `json:"uuid"`
	Message json.RawMessage `json:"message"`
	// ⛔⛔ A USAGE-LIMIT MESSAGE IS NOT A REPLY, AND IT IS THE MOST COMMON LAST
	// TEXT IN A TRANSCRIPT. Measured over the 50 transcripts on this machine:
	// 14 of them end on `You've hit your 5-hour limit · resets 8:50pm`, text
	// the model never wrote, recorded as an ordinary assistant text entry.
	//
	// Judging it refuses a reply nobody composed — and the refusal lands
	// exactly when the session has no requests left, so the retry it asks for
	// walks straight back into the limit. It is the worst moment this gate
	// could choose to speak.
	//
	// The entry says so itself: isApiErrorMessage, beside apiError
	// ("usage_limit_reached") and apiErrorStatus (429). A FIELD, not a text
	// pattern — matching on the words would be a gate that breaks when the
	// message is reworded, and would refuse a real reply that quoted it.
	IsAPIErrorMessage bool `json:"isApiErrorMessage"`
	// A subagent's entries are written into the same transcript with this set.
	// Its replies are not this session's replies and carry no stamp.
	IsSidechain bool `json:"isSidechain"`
	// Timestamp is when the entry was recorded, RFC 3339.
	Timestamp string `json:"timestamp"`
}

type message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type block struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// findReply returns the first text an assistant wrote after the last turn the
// user took.
//
// ⛔ "THE LAST ASSISTANT MESSAGE" IS THE WRONG ANCHOR, and it was the first
// thing this did. A turn is many assistant entries -- thinking, tool calls,
// short notes written between them -- and its LAST text block is a closing
// line, which the rule does not ask to carry a stamp. The rule is about where a
// reply BEGINS. So the anchor is the user's turn, and the reply is the first
// text after it.
//
// ⛔ AND MOST `type: "user"` ENTRIES ARE NOT TURNS. Tool results arrive as user
// entries whose content is a tool_result block; injected context arrives with
// isMeta set. Counting either as a turn would put the anchor in the middle of a
// turn and judge a note between two tool calls. Measured in a real transcript:
// of the user entries in the last stretch of one session, the turns were the
// ones with a string or a text block and no tool_result.
func findReply(path string) (aReply, error) {
	f, err := os.Open(path)
	if err != nil {
		return aReply{}, err
	}
	defer f.Close()
	return readReply(f)
}

func readReply(r io.Reader) (aReply, error) {
	sc := bufio.NewScanner(r)
	// A transcript line holds a whole message and the default 64 KiB limit is
	// nowhere near enough; a line that overran it would be dropped silently and
	// the anchor would move.
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)

	var (
		reply     aReply
		haveReply bool
	)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e entry
		if err := json.Unmarshal(line, &e); err != nil {
			// A transcript is append-only and a half-written last line is
			// ordinary; it is not a reason to refuse to judge the rest.
			continue
		}
		var m message
		if len(e.Message) > 0 {
			_ = json.Unmarshal(e.Message, &m)
		}
		// A subagent writes into this same file. Nothing it says is this
		// session's reply, and its turns must not move the anchor either.
		if e.IsSidechain {
			continue
		}
		switch {
		case e.Type == "user" && isUserTurn(e, m):
			// A new turn: whatever reply we were holding belonged to the
			// previous one.
			reply, haveReply = aReply{}, false
		case e.Type == "assistant" && !haveReply && !e.IsAPIErrorMessage:
			if t, ok := firstText(m.Content); ok {
				r := aReply{text: t, uuid: e.UUID}
				if at, err := time.Parse(time.RFC3339, e.Timestamp); err == nil {
					r.at, r.haveAt = at.Local(), true
				}
				reply, haveReply = r, true
			}
		}
	}
	if err := sc.Err(); err != nil {
		return aReply{}, err
	}
	if !haveReply {
		return aReply{}, fmt.Errorf("no reply text after the last turn the user took: the turn ended on a tool call, or the transcript holds no turn")
	}
	return reply, nil
}

func isUserTurn(e entry, m message) bool {
	if e.IsMeta || m.Role != "user" {
		return false
	}
	if len(m.Content) == 0 {
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
	for _, b := range bs {
		if b.Type == "tool_result" {
			return false
		}
	}
	for _, b := range bs {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			return true
		}
	}
	return false
}

// judgedAgainst returns the clock a reply's stamp is compared with: the
// instant the transcript recorded that reply.
//
// ⛔⛔ time.Now() WAS THE WRONG CLOCK, and it made the verdict a function of
// when you happened to run the command. Measured over the 50 transcripts on
// this machine: 8 reported STALE, by 18 hours to 19 days — every one of them a
// reply that was correctly stamped WHEN IT WAS WRITTEN, in a session that has
// since ended. The command was answering "is this reply's stamp close to now",
// and the rule is "was it stamped with the time it was written".
//
// Under the hook the two agree, because Stop fires seconds after the reply —
// which is exactly why the defect was invisible there and obvious the moment
// the same binary was pointed at an old file. Reading the entry's own
// timestamp makes the answer the same on both paths and reproducible on a
// transcript of any age, which is what a baseline has to be.
//
// It falls back to the clock when the entry carries no usable timestamp,
// because a reply with no recorded time still has to be judged on something.
func judgedAgainst(f aReply) time.Time {
	if f.haveAt {
		return f.at
	}
	return now()
}

func firstText(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if strings.TrimSpace(s) == "" {
			return "", false
		}
		return s, true
	}
	var bs []block
	if json.Unmarshal(raw, &bs) != nil {
		return "", false
	}
	for _, b := range bs {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			return b.Text, true
		}
	}
	return "", false
}

// ---------------------------------------------------------------- the verdict

type kind int

const (
	stamped kind = iota
	missing
	twelveHour
	impossibleHour
	stale
)

type verdict struct {
	kind kind
	// at is the instant the stamp names, when one could be read.
	at time.Time
	// off is how far that is from the clock, for a stale verdict.
	off time.Duration
	// got is the opening of the reply, quoted back so the reason names what
	// was actually there rather than what was missing.
	got string
}

// ⛔ THE LEFT ANCHOR IS THE WHOLE CHECK. Without `^` this matches a stamp
// written anywhere in the reply, and "start every reply with" is exactly the
// part of the rule that was broken.
var stampRe = regexp.MustCompile(`^\*\*\[(\d{4})-(\d{2})-(\d{2}) (\d{1,2}):(\d{2})(?::(\d{2}))? ([A-Za-z][A-Za-z0-9/_+-]{1,9})\]\*\*`)

// twelveHourRe is checked FIRST, because a 12-hour stamp fails stampRe and
// would otherwise be reported as "no stamp at all" -- a true statement that
// names the wrong defect and sends the next reply looking in the wrong place.
var twelveHourRe = regexp.MustCompile(`(?i)^\*\*\[[^\]]*\b\d{1,2}:\d{2}(:\d{2})?\s*(am|pm)\b`)

func judge(text string, clock time.Time, tolerance time.Duration) verdict {
	// Leading whitespace only: a stamp under a blank line is still the opening
	// of the reply, and refusing it would be pedantry rather than the rule.
	t := strings.TrimLeft(text, " \t\r\n")
	got := opening(t)

	if twelveHourRe.MatchString(t) {
		return verdict{kind: twelveHour, got: got}
	}
	m := stampRe.FindStringSubmatch(t)
	if m == nil {
		return verdict{kind: missing, got: got}
	}
	year, _ := strconv.Atoi(m[1])
	month, _ := strconv.Atoi(m[2])
	day, _ := strconv.Atoi(m[3])
	hour, _ := strconv.Atoi(m[4])
	minute, _ := strconv.Atoi(m[5])
	if hour > 23 {
		return verdict{kind: impossibleHour, got: got}
	}
	// ⛔ READ IN THE MACHINE'S OWN ZONE, not in the stamp's abbreviation.
	// time.Parse gives an unknown abbreviation a zero offset, which would read
	// a correct CET stamp as an hour off and spend the tolerance on nothing.
	// The zone token is required to be PRESENT -- the rule's example has one --
	// and its value is not judged, because the one thing it would catch (a
	// stamp in another zone) is already inside any honest tolerance.
	at := time.Date(year, time.Month(month), day, hour, minute, 0, 0, clock.Location())
	off := clock.Sub(at)
	if off < 0 {
		off = -off
	}
	if off > tolerance {
		return verdict{kind: stale, at: at, off: off, got: got}
	}
	return verdict{kind: stamped, at: at, off: off, got: got}
}

// opening quotes the first line of the reply, shortened, so that a reason can
// show what was there.
func opening(t string) string {
	line := t
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	line = strings.TrimSpace(line)
	const max = 60
	if len(line) > max {
		// Cut on a rune boundary; a reply can open on any script.
		r := []rune(line)
		if len(r) > max {
			r = r[:max]
		}
		line = string(r) + "…"
	}
	if line == "" {
		return "(an empty line)"
	}
	return line
}

const wanted = "**[2026-11-01 21:05 CET]**"

// reason is what the model is told when the reply is sent back. It names the
// defect, not the rule in general, because a reason that only repeats the rule
// produces a second reply with the same defect.
func (v verdict) reason() string {
	switch v.kind {
	case missing:
		return fmt.Sprintf("Your reply did not start with the local date and time. It started with: %s\n"+
			"CLAUDE.md's first rule: start EVERY reply with %s -- 24-hour clock, taken from the latest PostToolUse hook line « Current date/time: … ».\n"+
			"Write the reply again, with the stamp on the first line.", v.got, wanted)
	case twelveHour:
		return fmt.Sprintf("Your reply opened on a 12-hour clock: %s\n"+
			"CLAUDE.md asks for a 24-hour one and names this: never am/pm. Convert it (9:21 pm is 21:21) and write the reply again.", v.got)
	case impossibleHour:
		return fmt.Sprintf("Your reply opened with an hour of 24 or more: %s\n"+
			"Take the time from the latest PostToolUse hook line « Current date/time: … » and write the reply again.", v.got)
	case stale:
		return fmt.Sprintf("Your reply is stamped %s, which is %s from the clock: %s\n"+
			"CLAUDE.md: take it from the latest PostToolUse hook line « Current date/time: … », never from the session start date. Write the reply again with the current time.",
			v.at.Format("2006-01-02 15:04 MST"), roundly(v.off), v.got)
	}
	return ""
}

func roundly(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	case d >= time.Hour:
		return fmt.Sprintf("%.1f hours", d.Hours())
	default:
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	}
}

func (v verdict) report(f aReply) string {
	switch v.kind {
	case stamped:
		return fmt.Sprintf("STAMPED  %s  (%s from the clock)", v.at.Format("2006-01-02 15:04"), roundly(v.off))
	case missing:
		return fmt.Sprintf("MISSING  the reply opens on %q, not on %s", v.got, wanted)
	case twelveHour:
		return fmt.Sprintf("AM/PM    the reply opens on a 12-hour clock: %q", v.got)
	case impossibleHour:
		return fmt.Sprintf("HOUR     the reply opens on an hour of 24 or more: %q", v.got)
	case stale:
		return fmt.Sprintf("STALE    stamped %s, %s from the clock", v.at.Format("2006-01-02 15:04"), roundly(v.off))
	}
	return ""
}

// ---------------------------------------------------------------- the hook

type stopPayload struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	StopHookActive bool   `json:"stop_hook_active"`
	HookEventName  string `json:"hook_event_name"`
}

type hookAnswer struct {
	Decision string `json:"decision,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// runHook answers a Stop payload. It writes the hook's JSON on stdout and
// returns nil whatever it decides: under the hook protocol a non-zero status
// is itself a refusal, so letting this command's own failures travel as one
// would turn a bug here into a blocked session.
func runHook(cmd *cobra.Command, o *options) error {
	out, errOut := cli.Out(cmd), cli.Err(cmd)
	raw, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return allow(out, errOut, "cannot read the hook payload: %v", err)
	}
	var p stopPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return allow(out, errOut, "the hook payload is not JSON: %v", err)
	}
	if p.TranscriptPath == "" {
		return allow(out, errOut, "the hook payload names no transcript")
	}
	f, err := findReply(p.TranscriptPath)
	if err != nil {
		return allow(out, errOut, "%v", err)
	}
	v := judge(f.text, judgedAgainst(f), o.tolerance)
	if v.kind == stamped {
		clearBlocks(p.SessionID)
		return allow(out, errOut, "")
	}
	// ⛔ BOUND THE LOOP, AND BOUND IT IN TOTAL. A blocked reply ends in another
	// Stop, so a model that cannot satisfy this would be sent back for ever.
	// After --max-blocks it stands down for the rest of the session and says
	// so.
	//
	// ⛔ IT MUST NOT CLEAR THE COUNT WHEN IT GIVES UP, which is what the first
	// version did: the count went back to zero, the gate re-armed on the very
	// next reply, and the loop came back as block, block, allow, block, block,
	// allow — bounded only in threes. Its test measured 4 refusals in 6
	// replies. Only a STAMPED reply clears the count, which is the right
	// re-arming: the thing the gate is for has happened.
	n := blocksSoFar(p.SessionID) + 1
	if n > o.maxBlocks {
		recordBlock(p.SessionID, n)
		return allow(out, errOut, "the reply is still unstamped after %d refusals; standing down until a stamped reply comes through", o.maxBlocks)
	}
	recordBlock(p.SessionID, n)
	return answer(out, hookAnswer{Decision: "block", Reason: v.reason()})
}

func allow(out, errOut io.Writer, format string, a ...any) error {
	if format != "" {
		fmt.Fprintf(errOut, "replystamp: allowing: "+format+"\n", a...)
	}
	return answer(out, hookAnswer{})
}

func answer(out io.Writer, a hookAnswer) error {
	b, err := json.Marshal(a)
	if err != nil {
		return nil
	}
	fmt.Fprintln(out, string(b))
	return nil
}

// ------------------------------------------------------- the block counter

func countPath(session string) (string, bool) {
	if session == "" {
		return "", false
	}
	dir, err := stateDir()
	if err != nil {
		return "", false
	}
	// A session id arrives from outside this program; it must not be able to
	// name a path of its own choosing.
	if strings.ContainsAny(session, `/\.`) {
		return "", false
	}
	return filepath.Join(dir, session+".blocks"), true
}

func blocksSoFar(session string) int {
	p, ok := countPath(session)
	if !ok {
		return 0
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0
	}
	return n
}

func recordBlock(session string, n int) {
	p, ok := countPath(session)
	if !ok {
		return
	}
	if os.MkdirAll(filepath.Dir(p), 0o755) != nil {
		return
	}
	_ = os.WriteFile(p, []byte(strconv.Itoa(n)+"\n"), 0o644)
}

func clearBlocks(session string) {
	if p, ok := countPath(session); ok {
		_ = os.Remove(p)
	}
}
