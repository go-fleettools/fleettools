package main

import (
	"bytes"
	"encoding/json"
	"io"
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

// ⛔⛔ THE REFUSALS NOBODY COULD ACT ON. Found in review 2026-11-12 and
// measured with fixtures built from code points: a stamp preceded or
// interrupted by a Unicode space, or preceded by a byte order mark, was
// refused — and the refusal quoted it back LOOKING EXACTLY RIGHT, so there was
// nothing in it to act on.
//
// The characters are written as NUMBERS here. Spelling the escapes put a
// literal byte order mark into the source three times while this was written,
// and the compiler answered "illegal byte order mark" each time.
func TestAUnicodeSpaceDoesNotMakeAStampInvisible(t *testing.T) {
	var (
		nnbsp = string(rune(0x202F)) // narrow no-break space, routine in French
		nbsp  = string(rune(0x00A0))
		bom   = string(rune(0xFEFF))
	)
	good := stamp(clock)
	for _, tc := range []struct{ name, text string }{
		{"narrow no-break space before", nnbsp + good + "\n\nok"},
		{"no-break space before", nbsp + good + "\n\nok"},
		{"byte order mark before", bom + good + "\n\nok"},
		{"no-break space inside", strings.Replace(good, " ", nbsp, 1) + "\n\nok"},
		{"narrow no-break space before the zone", strings.Replace(good, " ", nnbsp, 2) + "\n\nok"},
		{"all of them", bom + nnbsp + strings.ReplaceAll(good, " ", nbsp) + "\n\nok"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if v := judge(tc.text, clock, 3*time.Hour); v.kind != stamped {
				t.Fatalf("kind = %v, want stamped (reason: %s)", v.kind, v.reason())
			}
		})
	}
}

// ⛔⛔ THE INVISIBLE CLASS IS `Cf`, NOT ONE CHARACTER. Naming U+FEFF alone left
// the class open: review measured U+200B, U+200F and U+2060 before a correct
// stamp all reported MISSING, with the reason showing a stamp that looks
// right. None is White_Space; all are FORMAT characters.
func TestEveryFormatCharacterIsInvisibleNotMissing(t *testing.T) {
	good := stamp(clock)
	for name, r := range map[string]rune{
		"zero width space":      0x200B,
		"right-to-left mark":    0x200F,
		"word joiner":           0x2060,
		"byte order mark":       0xFEFF,
		"zero width non-joiner": 0x200C,
		"soft hyphen":           0x00AD,
	} {
		t.Run(name, func(t *testing.T) {
			if v := judge(string(r)+good+"\n\nok", clock, 3*time.Hour); v.kind != stamped {
				t.Errorf("before the stamp: %v", v.kind)
			}
			// ...and inside the brackets, which is as invisible and was not
			// covered by trimming the front.
			inside := strings.Replace(good, " ", string(r)+" ", 1)
			if v := judge(inside+"\n\nok", clock, 3*time.Hour); v.kind != stamped {
				t.Errorf("inside the brackets: %v", v.kind)
			}
		})
	}
}

// ⛔ A refusal must SHOW an invisible character rather than print it. With %s
// the model is handed a stamp that looks correct and told it is absent.
func TestTheReasonQuotesWhatItFound(t *testing.T) {
	v := judge("plain text, no stamp", clock, 3*time.Hour)
	if !strings.Contains(v.reason(), `"plain text, no stamp"`) {
		t.Errorf("the reason does not quote the opening: %q", v.reason())
	}
}

// ⛔ A fallback verdict is about an EARLIER reply, and must not claim to be
// about the one that just ended.
func TestAFallbackVerdictSaysWhichReplyItIsAbout(t *testing.T) {
	v := judge("no stamp", clock, 3*time.Hour)
	if strings.Contains(v.reason(), "earlier reply") {
		t.Error("a normal verdict claimed to be a fallback")
	}
	v.fallback = true
	if !strings.Contains(v.reason(), "earlier reply") {
		t.Errorf("a fallback verdict did not say so: %q", v.reason())
	}
}

// ⛔ A ZONE NAME IS NOT TEN CHARACTERS. `Europe/Paris` is twelve, and the cap
// refused it.
func TestAnIANAZoneNameIsAZoneName(t *testing.T) {
	for _, zone := range []string{"CET", "UTC", "GMT+1", "Europe/Paris", "America/Argentina/Buenos_Aires"} {
		text := "**[2026-11-11 21:30 " + zone + "]**\n\nok"
		if v := judge(text, clock, 3*time.Hour); v.kind != stamped {
			t.Errorf("zone %q: kind = %v, want stamped", zone, v.kind)
		}
	}
}

// ...and the zone is still REQUIRED, so widening it did not empty the rule.
func TestWideningTheZoneDidNotMakeItOptional(t *testing.T) {
	if v := judge("**[2026-11-11 21:30]**\n\nok", clock, 3*time.Hour); v.kind != missing {
		t.Fatalf("a stamp with no zone = %v, want missing", v.kind)
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

// ⛔⛔ IF IT CANNOT COUNT, IT MUST NOT BLOCK. Measured in review: with
// session_id empty, countPath refuses the name, the count is always 0, and six
// runs produced six blocks — a wedged session, and the one place this
// command's invariant had inverted.
func TestItDoesNotRefuseWhatItCannotCount(t *testing.T) {
	p := transcript(t, userTurn("proceed"), assistantText("a1", "no stamp"))
	for i := 0; i < 3; i++ {
		code, stdout, errOut := drive(t, payload(t, p, ""), "--hook")
		if code != 0 {
			t.Fatalf("code = %d", code)
		}
		if a := decision(t, stdout); a.Decision != "" {
			t.Fatalf("run %d refused a reply it cannot count: %q", i, a.Decision)
		}
		if !strings.Contains(errOut, "cannot keep a block count") {
			t.Errorf("it must say why it stood aside; stderr was %q", errOut)
		}
	}
}

// ⛔ stop_hook_active is the protocol's own loop signal, and was parsed and
// never used. A Stop that FOLLOWS a block judges a reply already written.
func TestAStopThatFollowsABlockStandsDown(t *testing.T) {
	p := transcript(t, userTurn("proceed"), assistantText("a1", "no stamp"))
	raw, err := json.Marshal(stopPayload{
		SessionID: "s", TranscriptPath: p, HookEventName: "Stop", StopHookActive: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, stdout, _ := drive(t, string(raw), "--hook")
	if a := decision(t, stdout); a.Decision != "" {
		t.Fatalf("decision = %q, want none", a.Decision)
	}
}

// ⛔⛔ A TURN THAT PRODUCED NO TEXT MUST NOT DISCARD THE VERDICT ON THE ONE
// BEFORE IT. Measured in review over 187 turns: 22 ended on an injected entry
// — a task notification, a system prompt — with no assistant text after it, and
// each one ALLOWED the unstamped reply that came before.
func TestANotificationBehindAnUnstampedReplyDoesNotExcuseIt(t *testing.T) {
	p := transcript(t,
		userTurn("proceed"),
		assistantText("a1", "no stamp here"),
		line{"type": "user", "uuid": "n1", "turnOrigin": "task_notification",
			"message": line{"role": "user", "content": []line{
				{"type": "text", "text": "<task-notification>agent finished</task-notification>"}}}},
	)
	r, err := findReply(p)
	if err != nil {
		t.Fatalf("the notification excused the reply behind it: %v", err)
	}
	if r.uuid != "a1" {
		t.Fatalf("fell back to %s, want a1", r.uuid)
	}
	if judge(r.text, clock, 3*time.Hour).kind != missing {
		t.Error("the unstamped reply was not refused")
	}
}

// ...but a turn that genuinely ended on a tool call with nothing before it is
// still unanswerable, and still allows.
func TestTheFallbackDoesNotInventAReply(t *testing.T) {
	p := transcript(t, userTurn("proceed"), assistantToolUse("a1"))
	if _, err := findReply(p); err == nil {
		t.Fatal("want an error: there is no reply to fall back to")
	}
}

// ⛔⛔ A MALFORMED FLAG MUST NOT READ AS A REFUSAL. cobra never reaches RunE,
// so none of the loop logic runs: `--hook --tolerance=zzz` exited 2, which a
// Stop hook reads as a block whose reason is the flag error, unbounded.
func TestHookModeIsDecidedBeforeTheFlagsAreParsed(t *testing.T) {
	// ⛔ pflag parses a boolean value with strconv.ParseBool, so every
	// spelling it accepts must be recognised here. A hand-written list of
	// three missed --hook=1, --hook=t and --hook=TRUE, each of which exited 2
	// with a malformed --tolerance: the wedge, still open.
	for _, args := range [][]string{
		{"--hook"},
		{"--hook", "--tolerance=zzz"},
		{"--tolerance=zzz", "--hook"},
		{"--hook=true"},
		{"--hook=1"},
		{"--hook=t"},
		{"--hook=TRUE"},
		{"-hook=T"},
		// ⛔ A value pflag REJECTS still meant "hook": --hook=yes exited 2,
		// which the Stop protocol reads as a block. An unparseable value
		// fails open.
		{"--hook=yes"},
		{"--hook=on"},
		{"--hook=", "--tolerance=zzz"},
	} {
		if !hookMode(args) {
			t.Errorf("hookMode(%q) = false; a hook would then fail CLOSED", args)
		}
	}
	for _, args := range [][]string{
		{"--transcript", "x.jsonl"},
		{},
		{"--hooks"},
		// Only an explicit, parseable false asks for the command-line mode.
		{"--hook=false"},
		{"--hook=0"},
		{"--hook=F"},
	} {
		if hookMode(args) {
			t.Errorf("hookMode(%q) = true", args)
		}
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

// ------------------------------------------------------------ --pretool

func pretoolPayload(t *testing.T, transcriptPath, session, event string) string {
	t.Helper()
	raw, err := json.Marshal(stopPayload{SessionID: session, TranscriptPath: transcriptPath, HookEventName: event})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func turnUUID(uuid, text string) line {
	l := userTurn(text)
	l["uuid"] = uuid
	return l
}

// compactionTurn is what a resume after /compact looks like: a user entry whose
// content is a plain string.
func compactionTurn(uuid string) line {
	return line{"type": "user", "uuid": uuid, "isCompactSummary": true, "message": line{"role": "user",
		"content": "This session is being continued from a previous conversation... Resume directly."}}
}

func denied(t *testing.T, stdout string) (bool, preToolDeny) {
	t.Helper()
	var d preToolDeny
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &d); err != nil {
		t.Fatalf("the hook must answer with JSON on stdout; got %q: %v", stdout, err)
	}
	return d.HookSpecificOutput.PermissionDecision == "deny", d
}

func pretool(t *testing.T, p, session string, extra ...string) (int, string, string) {
	t.Helper()
	return drive(t, pretoolPayload(t, p, session, "PreToolUse"), append([]string{"--pretool"}, extra...)...)
}

func wantDeny(t *testing.T, name string, lines ...line) {
	t.Helper()
	code, stdout, _ := pretool(t, transcript(t, lines...), "s-"+name)
	if code != 0 {
		t.Fatalf("%s: exit %d, want 0", name, code)
	}
	ok, d := denied(t, stdout)
	if !ok {
		t.Fatalf("%s: allowed, want deny; got %q", name, stdout)
	}
	if d.HookSpecificOutput.HookEventName != "PreToolUse" ||
		!strings.Contains(d.HookSpecificOutput.PermissionDecisionReason, "**[") {
		t.Errorf("%s: malformed deny: %+v", name, d)
	}
}

func wantAllow(t *testing.T, name string, lines ...line) {
	t.Helper()
	code, stdout, _ := pretool(t, transcript(t, lines...), "s-"+name)
	if code != 0 {
		t.Fatalf("%s: exit %d, want 0", name, code)
	}
	if ok, _ := denied(t, stdout); ok {
		t.Fatalf("%s: denied, want allow; got %q", name, stdout)
	}
}

func TestPretoolDeniesATurnOpeningOnAToolCall(t *testing.T) {
	wantDeny(t, "opens-on-tool", turnUUID("t1", "proceed"), assistantThinking("a1"), assistantToolUse("a2"))
}

func TestPretoolAllowsTextThenTool(t *testing.T) {
	wantAllow(t, "text-first", turnUUID("t1", "proceed"), assistantText("a1", "Looking."), assistantToolUse("a2"))
}

// ⛔ INTERSTITIAL TEXT IS THE TURN'S FIRST TEXT: the test is "any text since the
// user turn", not "no tool calls this turn".
func TestPretoolAllowsTextBetweenTools(t *testing.T) {
	wantAllow(t, "between",
		turnUUID("t1", "proceed"), assistantToolUse("a1"), toolResult("ok"),
		assistantText("a2", "Now the second step."), assistantToolUse("a3"))
}

func TestPretoolDeniesAfterACompactionWhenNoText(t *testing.T) {
	wantDeny(t, "compaction",
		turnUUID("t0", "old"), assistantText("a0", "old reply"),
		compactionTurn("c1"), assistantToolUse("a1"))
}

func TestPretoolTextBeforeACompactionDoesNotExcuseTheNewTurn(t *testing.T) {
	// The only text is from the previous turn; the continuation is the anchor.
	wantDeny(t, "compaction-old-text",
		turnUUID("t0", "old"), assistantText("a0", "old reply"), compactionTurn("c1"))
}

func TestPretoolAToolResultIsNotAUserTurn(t *testing.T) {
	// Text, tool, tool_result, tool: the tool_result must not move the anchor
	// to after the text, or this would deny.
	wantAllow(t, "toolresult",
		turnUUID("t1", "go"), assistantText("a1", "first"), assistantToolUse("a2"),
		toolResult("out"), assistantToolUse("a3"))
}

func TestPretoolAllowsWhatItCannotRead(t *testing.T) {
	good := transcript(t, turnUUID("t1", "go"), assistantToolUse("a1"))
	cases := []struct{ name, stdin string }{
		{"unreadable path", pretoolPayload(t, "/nowhere/at/all.jsonl", "s", "PreToolUse")},
		{"wrong event", pretoolPayload(t, good, "s", "Stop")},
		{"no event", pretoolPayload(t, good, "s", "")},
		{"malformed JSON", "{not json"},
		{"empty stdin", ""},
		{"no transcript named", `{"session_id":"s","hook_event_name":"PreToolUse"}`},
		{"no turn located", pretoolPayload(t, transcript(t, assistantToolUse("a1")), "s", "PreToolUse")},
		{"no session to count under", pretoolPayload(t, good, "", "PreToolUse")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, stdout, errOut := drive(t, c.stdin, "--pretool")
			if code != 0 {
				t.Fatalf("exit %d, want 0", code)
			}
			if ok, _ := denied(t, stdout); ok {
				t.Fatalf("it refused on its own blindness: %q", stdout)
			}
			if !strings.Contains(errOut, "allowing") {
				t.Errorf("it must say why it stood aside; stderr was %q", errOut)
			}
		})
	}
}

// ⛔ A BAD FLAG MUST NEVER BE A BLOCK. cobra exits 2 before RunE; under the
// PreToolUse protocol 2 blocks the tool call, in front of EVERY tool.
func TestPretoolBadFlagStillExitsZeroAndAllows(t *testing.T) {
	for _, args := range [][]string{
		{"--pretool", "--tolerance=zzz"},
		{"--pretool", "--nonsense"},
		{"--pretool=yes", "--max-blocks=abc"},
		{"-pretool", "--bogus"},
	} {
		if !hookMode(args) {
			t.Fatalf("hookMode(%q) = false; a bad flag would then reach cobra", args)
		}
		var out, errOut bytes.Buffer
		driveHook(args, &out, &errOut)
		if got := strings.TrimSpace(out.String()); got != "{}" {
			t.Errorf("%q: stdout = %q, want {} (an allow)", args, got)
		}
		if !strings.Contains(errOut.String(), "allowing") {
			t.Errorf("%q: stderr must say why: %q", args, errOut.String())
		}
	}
}

func TestPretoolRefusalIsBoundedPerTurn(t *testing.T) {
	dir := t.TempDir()
	saved := stateDir
	stateDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { stateDir = saved })
	run := func(p string) bool {
		c := RootCmd()
		c.SetIn(strings.NewReader(pretoolPayload(t, p, "sess", "PreToolUse")))
		var out, errOut bytes.Buffer
		if code := cli.Drive(c, []string{"--pretool"}, &out, &errOut); code != 0 {
			t.Fatalf("exit %d", code)
		}
		ok, _ := denied(t, out.String())
		return ok
	}
	turn1 := transcript(t, turnUUID("t1", "go"), assistantToolUse("a1"))
	got := []bool{run(turn1), run(turn1), run(turn1), run(turn1)}
	want := []bool{true, true, false, false}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("denials over one turn = %v, want %v", got, want)
		}
	}
	// A new turn re-arms the gate: the bound is per turn, not per session.
	turn2 := transcript(t, turnUUID("t1", "go"), assistantToolUse("a1"), turnUUID("t2", "next"), assistantToolUse("a2"))
	if !run(turn2) {
		t.Fatalf("a new turn must be refused again")
	}
}

func TestPretoolMaxBlocksIsHonoured(t *testing.T) {
	p := transcript(t, turnUUID("t1", "go"), assistantToolUse("a1"))
	_, stdout, _ := pretool(t, p, "mb", "--max-blocks=0")
	if ok, _ := denied(t, stdout); ok {
		t.Fatalf("--max-blocks=0 must never refuse")
	}
}

func TestPretoolUnstampedTextIsTheStopHooksBusiness(t *testing.T) {
	wantAllow(t, "unstamped-text", turnUUID("t1", "go"), assistantText("a1", "no stamp here"), assistantToolUse("a2"))
}

// ------------------------------------------------- review fixes on --pretool

type countingReaderAt struct {
	r io.ReaderAt
	n int64
}

func (c *countingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	n, err := c.r.ReadAt(p, off)
	c.n += int64(n)
	return n, err
}

// bigTranscript writes `before` filler assistant entries, the given tail lines,
// and returns the path and size.
func bigTranscript(t *testing.T, before int, tail ...line) (string, int64) {
	t.Helper()
	filler, err := json.Marshal(assistantText("f", strings.Repeat("x", 900)))
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	for i := 0; i < before; i++ {
		b.Write(filler)
		b.WriteByte('\n')
	}
	for _, l := range tail {
		raw, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	p := filepath.Join(t.TempDir(), "big.jsonl")
	if err := os.WriteFile(p, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p, int64(b.Len())
}

// ⛔ IT READS THE TAIL, NOT THE FILE: a 948 MB transcript cost 2.1 s per call.
func TestPretoolReadsOnlyTheTail(t *testing.T) {
	p, size := bigTranscript(t, 40000, // ~36 MB of filler, no user turn in it
		turnUUID("t1", "go"), assistantThinking("a1"), assistantToolUse("a2"))
	if size < 30<<20 {
		t.Fatalf("fixture too small: %d", size)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	c := &countingReaderAt{r: f}
	s, n, err := lastTurnAt(c, size)
	if err != nil {
		t.Fatal(err)
	}
	if !s.found || s.hasText || s.uuid != "t1" {
		t.Fatalf("turn = %+v, want found, no text, t1", s)
	}
	if n != c.n {
		t.Errorf("reported %d bytes, reader saw %d", n, c.n)
	}
	if n > size/10 {
		t.Errorf("read %d of %d bytes: it must stay far under the file size", n, size)
	}
}

func TestPretoolFindsATurnBeyondTheFirstWindow(t *testing.T) {
	// ~3.6 MB of filler AFTER the turn: needs two doublings, and the turn's own
	// text-less state must survive the window boundary.
	p, size := bigTranscript(t, 0, turnUUID("t1", "go"))
	_ = size
	filler, _ := json.Marshal(assistantToolUse("f"))
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	pad := bytes.Repeat(append(filler, '\n'), 40000)
	if _, err := f.Write(pad); err != nil {
		t.Fatal(err)
	}
	f.Close()
	s, err := lastTurn(p)
	if err != nil {
		t.Fatal(err)
	}
	if !s.found || s.hasText || s.uuid != "t1" {
		t.Fatalf("turn = %+v", s)
	}
}

func TestPretoolAllowsWhenNoTurnIsWithinTheCap(t *testing.T) {
	// The only user turn is further back than maxTail: cannot locate, allow.
	p, size := bigTranscript(t, 0, turnUUID("t1", "go"))
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	filler, _ := json.Marshal(assistantToolUse("f"))
	filler = append(filler, '\n')
	chunk := bytes.Repeat(filler, 10000)
	for written := int64(0); written < maxTail+(2<<20); written += int64(len(chunk)) {
		if _, err := f.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	f.Close()
	_ = size
	_, stdout, _ := pretool(t, p, "cap")
	if ok, _ := denied(t, stdout); ok {
		t.Fatalf("a turn beyond the read cap must allow, got %q", stdout)
	}
}

func TestPretoolReadsAWholeSmallFileAndAHalfLineAtTheCut(t *testing.T) {
	// Tail window starts mid-line in a big file: the partial line is dropped,
	// not mis-parsed as a turn.
	p, size := bigTranscript(t, 1500, assistantToolUse("a1"))
	f, _ := os.Open(p)
	defer f.Close()
	s, _, err := lastTurnAt(f, size)
	if err != nil || s.found {
		t.Fatalf("no user turn exists; got %+v, %v", s, err)
	}
}

func TestPretoolAllowsASubagentsCall(t *testing.T) {
	p := transcript(t, turnUUID("t1", "go"), assistantToolUse("a1"))
	raw, err := json.Marshal(stopPayload{SessionID: "sub", TranscriptPath: p, HookEventName: "PreToolUse", AgentID: "agent-7"})
	if err != nil {
		t.Fatal(err)
	}
	code, stdout, errOut := drive(t, string(raw), "--pretool")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if ok, _ := denied(t, stdout); ok {
		t.Fatalf("a subagent's call was judged on the parent's turn: %q", stdout)
	}
	if !strings.Contains(errOut, "subagent") {
		t.Errorf("it must say why: %q", errOut)
	}
}

// ⛔ HOOK MODE NEVER MAKES A NETWORK CALL; a normal run still warns.
func TestHookModeNeverCallsTheStalenessWarning(t *testing.T) {
	for _, args := range [][]string{{"--hook"}, {"--pretool"}, {"--pretool", "--bogus"}, {"--hook=yes"}} {
		calls := 0
		var out, errOut bytes.Buffer
		if !dispatch(args, &out, &errOut, func(io.Writer) { calls++ }) {
			t.Fatalf("%q: not recognised as hook mode", args)
		}
		if calls != 0 {
			t.Errorf("%q: the staleness warning ran (%d)", args, calls)
		}
	}
	calls := 0
	if dispatch([]string{"--transcript", "x"}, io.Discard, io.Discard, func(io.Writer) { calls++ }) || calls != 1 {
		t.Errorf("outside hook mode the warning must run once (calls=%d)", calls)
	}
}
