package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
)

// clock is the instant every test judges against, so that "stale" is a
// property of the fixture and not of the day the suite runs.
var clock = time.Date(2026, 11, 11, 21, 30, 0, 0, time.Local)

func stamp(t time.Time) string {
	return t.Format("**[2006-01-02 15:04 MST]**")
}

func TestJudgeNamesEachWayTheRuleIsBroken(t *testing.T) {
	cases := []struct {
		name string
		text string
		want kind
	}{
		{"the rule obeyed", stamp(clock) + "\n\nrecur is green.", stamped},
		{"with seconds", "**[2026-11-11 21:29:41 CET]**\n\nok", stamped},
		{"a few minutes of tool calls", stamp(clock.Add(-40 * time.Minute)), stamped},
		{"blank lines before it", "\n\n" + stamp(clock) + "\nok", stamped},

		{"this session, every reply", "recur is green (211 read, 211 accounted).", missing},
		{"a bare date", "2026-11-11 21:30\n\nok", missing},
		{"no brackets", "**2026-11-11 21:30 CET**\n\nok", missing},
		{"no zone", "**[2026-11-11 21:30]**\n\nok", missing},

		{"am/pm, which the rule names", "**[2026-11-11 9:21 pm CET]**\n\nok", twelveHour},
		{"AM/PM upper", "**[2026-11-11 09:21 AM CET]**\n\nok", twelveHour},

		{"an hour that does not exist", "**[2026-11-11 25:30 CET]**\n\nok", impossibleHour},

		{"the session start date", "**[2026-11-09 21:30 CET]**\n\nok", stale},
		{"this morning", "**[2026-11-11 09:24 CET]**\n\nok", stale},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := judge(c.text, clock, 3*time.Hour)
			if got.kind != c.want {
				t.Fatalf("judge(%q) = %v, want %v (report: %s)", c.text, got.kind, c.want, got.report(aReply{}))
			}
			if c.want != stamped && got.reason() == "" {
				t.Errorf("a refused reply must carry a reason saying what to do; got none")
			}
		})
	}
}

// ⛔ THE LEFT ANCHOR IS THE CHECK. "Start every reply with" is the part of the
// rule that was broken, so a stamp written further down must NOT pass. Remove
// the `^` from stampRe and this test is the one that goes red.
func TestAStampFurtherDownIsNotAReplyThatStartsWithOne(t *testing.T) {
	text := "Let me look at the settings first.\n\n" + stamp(clock) + "\n\nok"
	if got := judge(text, clock, 3*time.Hour); got.kind != missing {
		t.Fatalf("a stamp on the third line counted as a reply starting with one: %v", got.kind)
	}
}

// ⛔ A 12-HOUR STAMP ALSO FAILS stampRe, so the order of the two patterns
// decides which defect is named. Reported as "missing" it sends the next reply
// looking for something that is already there.
func TestTheTwelveHourClockIsNamedAsItselfNotAsMissing(t *testing.T) {
	v := judge("**[2026-11-11 9:21 pm CET]**\n\nok", clock, 3*time.Hour)
	if v.kind != twelveHour {
		t.Fatalf("kind = %v, want twelveHour", v.kind)
	}
	if !strings.Contains(v.reason(), "24-hour") {
		t.Errorf("the reason must say what to write instead; got %q", v.reason())
	}
}

func TestToleranceIsTheOnlyThingThatDecidesStale(t *testing.T) {
	text := "**[2026-11-11 19:00 CET]**\n\nok" // 2h30 before the clock
	if v := judge(text, clock, 3*time.Hour); v.kind != stamped {
		t.Errorf("2h30 under a 3h tolerance = %v, want stamped", v.kind)
	}
	if v := judge(text, clock, 1*time.Hour); v.kind != stale {
		t.Errorf("2h30 under a 1h tolerance = %v, want stale", v.kind)
	}
}

// A stamp from the future is as wrong as one from the past, and an unsigned
// comparison would have read it as zero.
func TestAStampFromTheFutureIsStaleToo(t *testing.T) {
	if v := judge("**[2026-11-12 21:30 CET]**\n\nok", clock, 3*time.Hour); v.kind != stale {
		t.Fatalf("a stamp a day ahead = %v, want stale", v.kind)
	}
}

// ---------------------------------------------------------------- transcript

type line map[string]any

func userTurn(text string) line {
	return line{"type": "user", "uuid": "u", "message": line{"role": "user",
		"content": []line{{"type": "text", "text": text}}}}
}

func toolResult(text string) line {
	return line{"type": "user", "uuid": "tr", "message": line{"role": "user",
		"content": []line{{"type": "tool_result", "content": text}}}}
}

func metaUser(text string) line {
	return line{"type": "user", "uuid": "meta", "isMeta": true, "message": line{"role": "user",
		"content": text}}
}

func assistantText(uuid, text string) line {
	return line{"type": "assistant", "uuid": uuid, "message": line{"role": "assistant",
		"content": []line{{"type": "text", "text": text}}}}
}

func apiErrorText(uuid, text string) line {
	return line{"type": "assistant", "uuid": uuid, "isApiErrorMessage": true,
		"apiError": "usage_limit_reached", "apiErrorStatus": 429,
		"message": line{"role": "assistant",
			"content": []line{{"type": "text", "text": text}}}}
}

func sidechainUser(text string) line {
	return line{"type": "user", "uuid": "su", "isSidechain": true, "message": line{"role": "user",
		"content": []line{{"type": "text", "text": text}}}}
}

func sidechainAssistant(uuid, text string) line {
	return line{"type": "assistant", "uuid": uuid, "isSidechain": true, "message": line{"role": "assistant",
		"content": []line{{"type": "text", "text": text}}}}
}

func stampedAt(uuid, text string, at time.Time) line {
	l := assistantText(uuid, text)
	l["timestamp"] = at.UTC().Format(time.RFC3339)
	return l
}

func assistantToolUse(uuid string) line {
	return line{"type": "assistant", "uuid": uuid, "message": line{"role": "assistant",
		"content": []line{{"type": "tool_use", "name": "Bash"}}}}
}

func assistantThinking(uuid string) line {
	return line{"type": "assistant", "uuid": uuid, "message": line{"role": "assistant",
		"content": []line{{"type": "thinking", "thinking": "…"}}}}
}

func transcript(t *testing.T, lines ...line) string {
	t.Helper()
	var b strings.Builder
	for _, l := range lines {
		raw, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	p := filepath.Join(t.TempDir(), "t.jsonl")
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// ⛔ THE ANCHOR IS THE USER'S TURN, NOT THE LAST ASSISTANT MESSAGE. A turn
// writes several texts; only the first one opens the reply. Judging the last
// would refuse every turn that ends on a closing line.
func TestTheReplyIsTheFirstTextOfTheTurnNotTheLast(t *testing.T) {
	p := transcript(t,
		userTurn("proceed"),
		assistantThinking("a1"),
		assistantText("a2", stamp(clock)+"\n\nLooking at the settings."),
		assistantToolUse("a3"),
		toolResult("ok"),
		assistantText("a4", "The settings confirm it."),
	)
	r, err := findReply(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.uuid != "a2" {
		t.Fatalf("anchored on %s, want a2 (%q)", r.uuid, r.text)
	}
	if judge(r.text, clock, 3*time.Hour).kind != stamped {
		t.Errorf("a stamped turn was refused because the wrong text was judged")
	}
}

// ⛔ A TOOL RESULT IS A `type: "user"` ENTRY AND IS NOT A TURN. Counting one
// would move the anchor into the middle of the turn, onto a note between two
// tool calls, and refuse a reply that was correctly stamped.
func TestAToolResultIsNotATurn(t *testing.T) {
	p := transcript(t,
		userTurn("proceed"),
		assistantText("a1", stamp(clock)+"\n\nfirst"),
		assistantToolUse("a2"),
		toolResult("out"),
		assistantText("a3", "a note between two calls"),
	)
	r, err := findReply(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.uuid != "a1" {
		t.Fatalf("a tool result moved the anchor: got %s (%q)", r.uuid, r.text)
	}
}

// Injected context arrives as a user entry with isMeta set. Same hazard.
func TestInjectedContextIsNotATurn(t *testing.T) {
	p := transcript(t,
		userTurn("proceed"),
		assistantText("a1", stamp(clock)+"\n\nfirst"),
		metaUser("Another Claude session sent a message: …"),
		assistantText("a2", "unstamped note"),
	)
	r, err := findReply(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.uuid != "a1" {
		t.Fatalf("injected context moved the anchor: got %s", r.uuid)
	}
}

// A compaction hands the session a user entry whose content is a plain string.
// It IS a turn: the reply after it is the one the rule is about, and it is
// exactly the reply a compaction makes it easy to write unstamped.
func TestAStringUserEntryIsATurn(t *testing.T) {
	p := transcript(t,
		userTurn("proceed"),
		assistantText("a1", stamp(clock)+"\n\nfirst"),
		line{"type": "user", "uuid": "u2", "message": line{"role": "user",
			"content": "This session is being continued from a previous conversation…"}},
		assistantText("a2", "Continuing where we left off."),
	)
	r, err := findReply(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.uuid != "a2" {
		t.Fatalf("the continuation was not read as a turn: anchored on %s", r.uuid)
	}
	if judge(r.text, clock, 3*time.Hour).kind != missing {
		t.Errorf("the unstamped reply after a compaction must be refused")
	}
}

// ⛔⛔ A USAGE-LIMIT MESSAGE IS NOT A REPLY. Measured over the 50 transcripts
// on this machine: 14 end on `You've hit your 5-hour limit · resets 8:50pm`,
// text the model never wrote, recorded as an ordinary assistant text entry.
// Refusing it blocks a reply nobody composed, at the one moment the session
// has no requests left to satisfy the refusal with.
func TestAUsageLimitMessageIsNotAReply(t *testing.T) {
	p := transcript(t,
		userTurn("proceed"),
		apiErrorText("a1", "You've hit your 5-hour limit · resets 8:50pm (Europe/Paris)"),
	)
	if _, err := findReply(p); err == nil {
		t.Fatal("the limit message was judged as a reply")
	}
}

// And when the limit lands mid-turn, the real reply is still the first text:
// the error must not displace it, in either direction.
func TestALimitMessageAfterARealReplyDoesNotDisplaceIt(t *testing.T) {
	p := transcript(t,
		userTurn("proceed"),
		assistantText("a1", stamp(clock)+"\n\nStarting the sweep."),
		assistantToolUse("a2"),
		toolResult("ok"),
		apiErrorText("a3", "You've hit your 5-hour limit · resets 8:50pm (Europe/Paris)"),
	)
	r, err := findReply(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.uuid != "a1" {
		t.Fatalf("anchored on %s (%q)", r.uuid, r.text)
	}
}

// ⛔ A SUBAGENT WRITES INTO THE SAME TRANSCRIPT. Its prompt is a user entry
// with text in it and its answer is an assistant text, so without the
// sidechain test the anchor lands inside the subagent and refuses the parent
// session for a reply it did not write. Its turns must be skipped too, not
// only its replies.
func TestASubagentIsNotThisSessionsReply(t *testing.T) {
	p := transcript(t,
		userTurn("proceed"),
		assistantText("a1", stamp(clock)+"\n\nSpawning a reviewer."),
		assistantToolUse("a2"),
		sidechainUser("Review PR #256 and report back."),
		sidechainAssistant("s1", "Two concrete false refusals already."),
		toolResult("agent finished"),
		assistantText("a3", "The reviewer found two."),
	)
	r, err := findReply(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.uuid != "a1" {
		t.Fatalf("anchored on %s (%q)", r.uuid, r.text)
	}
}

// ⛔⛔ THE CLOCK IS THE REPLY'S OWN TIMESTAMP, NOT time.Now(). Measured over
// the 50 transcripts on this machine, judging against the wall clock: 8
// reported STALE by 18 hours to 19 days, every one a reply that was correctly
// stamped when it was written, in a session that has since ended.
func TestAnOldTranscriptIsJudgedAtTheTimeItWasWritten(t *testing.T) {
	written := clock.Add(-19 * 24 * time.Hour)
	p := transcript(t,
		userTurn("proceed"),
		stampedAt("a1", stamp(written)+"\n\nMerged and released.", written),
	)
	r, err := findReply(p)
	if err != nil {
		t.Fatal(err)
	}
	if v := judge(r.text, judgedAgainst(r), 3*time.Hour); v.kind != stamped {
		t.Fatalf("a reply stamped correctly 19 days ago reads as %v", v.kind)
	}
}

// The staleness check must still catch what it is for: a stamp that does not
// match the moment the reply was actually written.
func TestAStampFromAnotherDayIsStillStale(t *testing.T) {
	written := clock
	p := transcript(t,
		userTurn("proceed"),
		stampedAt("a1", stamp(clock.Add(-48*time.Hour))+"\n\nok", written),
	)
	r, err := findReply(p)
	if err != nil {
		t.Fatal(err)
	}
	if v := judge(r.text, judgedAgainst(r), 3*time.Hour); v.kind != stale {
		t.Fatalf("a stamp two days off its own entry reads as %v", v.kind)
	}
}

// With no recorded timestamp there is still a reply to judge, so it falls back
// to the clock rather than refusing to answer.
func TestWithoutATimestampItFallsBackToTheClock(t *testing.T) {
	p := transcript(t, userTurn("proceed"), assistantText("a1", stamp(clock)+"\n\nok"))
	r, err := findReply(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.haveAt {
		t.Fatal("the fixture was supposed to carry no timestamp")
	}
	savedNow := now
	now = func() time.Time { return clock }
	t.Cleanup(func() { now = savedNow })
	if v := judge(r.text, judgedAgainst(r), 3*time.Hour); v.kind != stamped {
		t.Fatalf("kind = %v", v.kind)
	}
}

func TestATurnThatEndedOnAToolCallCannotBeJudged(t *testing.T) {
	p := transcript(t, userTurn("proceed"), assistantToolUse("a1"))
	if _, err := findReply(p); err == nil {
		t.Fatal("want an error: there is no reply text to judge")
	}
}

func TestAHalfWrittenLastLineDoesNotHideTheReply(t *testing.T) {
	p := transcript(t, userTurn("proceed"), assistantText("a1", stamp(clock)+"\n\nok"))
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"type":"assis`); err != nil {
		t.Fatal(err)
	}
	f.Close()
	r, err := findReply(p)
	if err != nil {
		t.Fatalf("a truncated last line made the whole transcript unreadable: %v", err)
	}
	if r.uuid != "a1" {
		t.Fatalf("anchored on %s", r.uuid)
	}
}

// ---------------------------------------------------------------- the hook

func drive(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	dir := t.TempDir()
	saved := stateDir
	stateDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { stateDir = saved })

	savedNow := now
	now = func() time.Time { return clock }
	t.Cleanup(func() { now = savedNow })

	c := RootCmd()
	c.SetIn(strings.NewReader(stdin))
	var out, errOut bytes.Buffer
	code := cli.Drive(c, args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func payload(t *testing.T, transcriptPath, session string) string {
	t.Helper()
	raw, err := json.Marshal(stopPayload{
		SessionID:      session,
		TranscriptPath: transcriptPath,
		HookEventName:  "Stop",
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func decision(t *testing.T, stdout string) hookAnswer {
	t.Helper()
	var a hookAnswer
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &a); err != nil {
		t.Fatalf("the hook must answer with JSON on stdout; got %q: %v", stdout, err)
	}
	return a
}

func TestTheHookBlocksAnUnstampedReply(t *testing.T) {
	p := transcript(t, userTurn("proceed"), assistantText("a1", "recur is green."))
	code, stdout, _ := drive(t, payload(t, p, "sess1"), "--hook")
	if code != 0 {
		t.Fatalf("a hook must exit 0 whatever it decides; got %d", code)
	}
	a := decision(t, stdout)
	if a.Decision != "block" {
		t.Fatalf("decision = %q, want block", a.Decision)
	}
	if !strings.Contains(a.Reason, "**[") {
		t.Errorf("the reason must show the stamp to write; got %q", a.Reason)
	}
}

func TestTheHookLetsAStampedReplyThrough(t *testing.T) {
	p := transcript(t, userTurn("proceed"), assistantText("a1", stamp(clock)+"\n\nok"))
	code, stdout, _ := drive(t, payload(t, p, "sess1"), "--hook")
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	if a := decision(t, stdout); a.Decision != "" {
		t.Fatalf("decision = %q, want none", a.Decision)
	}
}

// ⛔ IT ALLOWS WHENEVER IT CANNOT READ. A refusal is not a verdict, and a gate
// that blocks on its own breakage stops ~25 sessions at once.
func TestItAllowsWhateverItCannotRead(t *testing.T) {
	cases := []struct{ name, stdin string }{
		{"not JSON", "this is not a payload"},
		{"no transcript named", `{"session_id":"s","hook_event_name":"Stop"}`},
		{"transcript missing", `{"session_id":"s","transcript_path":"/nowhere/at/all.jsonl"}`},
		{"empty", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, stdout, errOut := drive(t, c.stdin, "--hook")
			if code != 0 {
				t.Fatalf("code = %d", code)
			}
			if a := decision(t, stdout); a.Decision != "" {
				t.Fatalf("it refused a reply on its own blindness: %q", a.Decision)
			}
			if !strings.Contains(errOut, "allowing") {
				t.Errorf("it must say why it stood aside; stderr was %q", errOut)
			}
		})
	}
}

// ⛔ BOUND THE LOOP WHEN THERE IS NO UUID TO KEY ON. Refusing one reply once
// handles the ordinary case; an entry carrying no uuid cannot be recognised
// the second time, so --max-blocks is what stops it looping.
func TestItGivesUpRatherThanLoop(t *testing.T) {
	noUUID := line{"type": "assistant", "message": line{"role": "assistant",
		"content": []line{{"type": "text", "text": "no stamp"}}}}
	p := transcript(t, userTurn("proceed"), noUUID)
	dir := t.TempDir()
	saved := stateDir
	stateDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { stateDir = saved })
	savedNow := now
	now = func() time.Time { return clock }
	t.Cleanup(func() { now = savedNow })

	blocked := 0
	for i := 0; i < 6; i++ {
		c := RootCmd()
		c.SetIn(strings.NewReader(payload(t, p, "loopy")))
		var out, errOut bytes.Buffer
		if code := cli.Drive(c, []string{"--hook", "--max-blocks", "2"}, &out, &errOut); code != 0 {
			t.Fatalf("code = %d", code)
		}
		var a hookAnswer
		if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &a); err != nil {
			t.Fatal(err)
		}
		if a.Decision == "block" {
			blocked++
		}
	}
	if blocked != 2 {
		t.Fatalf("blocked %d times out of 6 with --max-blocks 2; want 2", blocked)
	}
}

// ⛔⛔ ONE REFUSAL PER REPLY. Found by this gate firing on its own author:
// blocking at Stop does not start a new user turn, so the anchor does not
// move, and the next Stop judges the SAME text — which is already written and
// cannot be edited. The first version refused it again, and again, asking each
// time for a message that no longer exists to be rewritten.
func TestTheSameReplyIsRefusedOnlyOnce(t *testing.T) {
	dir := t.TempDir()
	saved := stateDir
	stateDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { stateDir = saved })
	savedNow := now
	now = func() time.Time { return clock }
	t.Cleanup(func() { now = savedNow })

	p := transcript(t, userTurn("proceed"), assistantText("a1", "no stamp"))
	blocked := 0
	for i := 0; i < 4; i++ {
		c := RootCmd()
		c.SetIn(strings.NewReader(payload(t, p, "same")))
		var out, errOut bytes.Buffer
		cli.Drive(c, []string{"--hook"}, &out, &errOut)
		var a hookAnswer
		if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &a); err != nil {
			t.Fatal(err)
		}
		if a.Decision == "block" {
			blocked++
		}
	}
	if blocked != 1 {
		t.Fatalf("the same unchangeable reply was refused %d times; want 1", blocked)
	}
}

// ⛔ A refusal must not ask for the impossible: the text it names is already
// written, so the only actionable instruction is about the NEXT reply.
func TestTheReasonAsksForSomethingThatCanStillBeDone(t *testing.T) {
	r := verdict{kind: missing, got: "The reviewer hit the 5-hour limit…"}.reason()
	if strings.Contains(r, "Write the reply again") {
		t.Error("the reason asks for a message that no longer exists to be rewritten")
	}
	if !strings.Contains(r, "NEXT turn") {
		t.Errorf("the reason does not say what can still be done: %q", r)
	}
	if !strings.Contains(r, "FIRST text block") {
		t.Errorf("the reason does not say which text is judged: %q", r)
	}
}

// A DIFFERENT unstamped reply is still refused — standing down is per reply,
// not for the session.
func TestANewUnstampedReplyIsStillRefused(t *testing.T) {
	dir := t.TempDir()
	saved := stateDir
	stateDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { stateDir = saved })
	savedNow := now
	now = func() time.Time { return clock }
	t.Cleanup(func() { now = savedNow })

	first := transcript(t, userTurn("proceed"), assistantText("a1", "no stamp"))
	second := transcript(t, userTurn("proceed"), assistantText("a2", "still no stamp"))
	decide := func(p string) string {
		c := RootCmd()
		c.SetIn(strings.NewReader(payload(t, p, "sess")))
		var out, errOut bytes.Buffer
		cli.Drive(c, []string{"--hook"}, &out, &errOut)
		var a hookAnswer
		_ = json.Unmarshal([]byte(strings.TrimSpace(out.String())), &a)
		return a.Decision
	}
	if d := decide(first); d != "block" {
		t.Fatalf("first = %q, want block", d)
	}
	if d := decide(first); d != "" {
		t.Fatalf("same reply again = %q, want no decision", d)
	}
	if d := decide(second); d != "block" {
		t.Fatalf("a different unstamped reply = %q, want block", d)
	}
}

func TestAStampedReplyClearsTheCount(t *testing.T) {
	dir := t.TempDir()
	saved := stateDir
	stateDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { stateDir = saved })
	savedNow := now
	now = func() time.Time { return clock }
	t.Cleanup(func() { now = savedNow })

	bad := transcript(t, userTurn("proceed"), assistantText("a1", "no stamp"))
	good := transcript(t, userTurn("proceed"), assistantText("a1", stamp(clock)+"\n\nok"))

	run := func(p string) {
		c := RootCmd()
		c.SetIn(strings.NewReader(payload(t, p, "sess")))
		var out, errOut bytes.Buffer
		cli.Drive(c, []string{"--hook", "--max-blocks", "2"}, &out, &errOut)
	}
	run(bad)
	run(good)
	if n, _ := blocksSoFar("sess"); n != 0 {
		t.Fatalf("a stamped reply left the count at %d, want 0", n)
	}
	if _, err := os.Stat(filepath.Join(dir, "sess.blocks")); !os.IsNotExist(err) {
		t.Errorf("the count file outlived the stamped reply")
	}
}

// A session id arrives from outside and must not be able to name a path.
func TestASessionIdCannotNameAPath(t *testing.T) {
	for _, s := range []string{"../../etc/passwd", "a/b", `a\b`, "a.b", ""} {
		if _, ok := countPath(s); ok {
			t.Errorf("countPath(%q) accepted a name it should refuse", s)
		}
	}
}

// ---------------------------------------------------------------- the CLI

func TestTheCommandReportsOnATranscript(t *testing.T) {
	p := transcript(t, userTurn("proceed"), assistantText("a1", stamp(clock)+"\n\nok"))
	code, stdout, _ := drive(t, "", "--transcript", p)
	if code != 0 {
		t.Fatalf("code = %d, stdout %q", code, stdout)
	}
	if !strings.Contains(stdout, "STAMPED") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestAnUnstampedTranscriptExitsOne(t *testing.T) {
	p := transcript(t, userTurn("proceed"), assistantText("a1", "no stamp"))
	code, stdout, _ := drive(t, "", "--transcript", p)
	if code != 1 {
		t.Fatalf("code = %d, want 1 (a finding); stdout %q", code, stdout)
	}
	if !strings.Contains(stdout, "MISSING") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestAnUnreadableTranscriptExitsTwo(t *testing.T) {
	code, _, errOut := drive(t, "", "--transcript", filepath.Join(t.TempDir(), "nope.jsonl"))
	if code != 2 {
		t.Fatalf("code = %d, want 2 (the question could not be put); stderr %q", code, errOut)
	}
}

func TestNoArgumentsIsNotACleanResult(t *testing.T) {
	code, _, _ := drive(t, "")
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
}
