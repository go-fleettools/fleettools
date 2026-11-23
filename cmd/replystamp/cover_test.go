package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
)

var errBoom = errors.New("boom")

// withState points the block counter at dir for one test.
func withState(t *testing.T, fn func() (string, error)) {
	t.Helper()
	saved := stateDir
	stateDir = fn
	t.Cleanup(func() { stateDir = saved })
}

func withClock(t *testing.T, at time.Time) {
	t.Helper()
	saved := now
	now = func() time.Time { return at }
	t.Cleanup(func() { now = saved })
}

// driveIn runs the command with an arbitrary stdin reader (drive takes a string).
func driveIn(t *testing.T, in io.Reader, args ...string) (int, string, string) {
	t.Helper()
	c := RootCmd()
	c.SetIn(in)
	var out, errOut bytes.Buffer
	code := cli.Drive(c, args, &out, &errOut)
	return code, out.String(), errOut.String()
}

// main wires dispatch and the command to the process: --clock answers and
// returns without exiting; anything else runs the warning (silenced here by an
// empty PATH, so gh cannot be found and nothing touches the network) and then
// the command, whose status goes through the exit seam.
func TestMainDispatchesClockAndRunsTheCommand(t *testing.T) {
	oldArgs, oldExit, oldIn := os.Args, cli.OsExit, stdin
	t.Cleanup(func() { os.Args, cli.OsExit, stdin = oldArgs, oldExit, oldIn })
	t.Setenv("PATH", t.TempDir())
	exited := -1
	cli.OsExit = func(code int) { exited = code }

	stdin = strings.NewReader("")
	os.Args = []string{"replystamp", "--clock"}
	main()
	if exited != -1 {
		t.Errorf("--clock exited %d, want no exit at all", exited)
	}

	os.Args = []string{"replystamp", "--transcript", filepath.Join(t.TempDir(), "absent.jsonl")}
	main()
	if exited != 2 {
		t.Errorf("an unreadable transcript exited %d, want 2", exited)
	}
}

// A run that was a request for help does no work: run returns before it looks
// at any mode (here --hook, which would otherwise read stdin and answer).
func TestARunThatWasAHelpRequestDoesNoWork(t *testing.T) {
	c := RootCmd()
	c.InitDefaultHelpFlag()
	if err := c.ParseFlags([]string{"--help"}); err != nil {
		t.Fatal(err)
	}
	c.SetIn(iotest.ErrReader(errBoom))
	var out, errOut bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&errOut)
	if err := run(c, &options{hook: true}); err != nil {
		t.Errorf("run = %v, want nil", err)
	}
	if out.Len() != 0 || errOut.Len() != 0 {
		t.Errorf("a help request wrote %q / %q", out.String(), errOut.String())
	}
}

func TestClockSaysSoWhenThePayloadCannotBeRead(t *testing.T) {
	fixedClock(t, "")
	var out, errOut bytes.Buffer
	runClock(iotest.ErrReader(errBoom), &out, &errOut)
	if !strings.Contains(errOut.String(), "unreadable payload (boom); assuming UserPromptSubmit") {
		t.Errorf("stderr = %q", errOut.String())
	}
	if !strings.Contains(out.String(), `"hookEventName":"UserPromptSubmit"`) || !strings.Contains(out.String(), wantContext) {
		t.Errorf("a clock that cannot read its payload must still print the time: %q", out.String())
	}
}

func TestDefaultStateDirIsUnderTheUserConfigDirAndFailsWithoutOne(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	got, err := defaultStateDir()
	if want := filepath.Join(base, "fleettools", "replystamp"); err != nil || got != want {
		t.Errorf("defaultStateDir = %q, %v; want %q", got, err, want)
	}
	t.Setenv("HOME", "")
	if got, err := defaultStateDir(); err == nil || got != "" {
		t.Errorf("with no home defaultStateDir = %q, %v; want an error", got, err)
	}
}

func TestReadReplyReturnsAScanError(t *testing.T) {
	if _, err := readReply(iotest.ErrReader(errBoom)); !errors.Is(err, errBoom) {
		t.Errorf("readReply error = %v, want boom", err)
	}
}

func TestIsUserTurnRefusesWhatIsNotAHumanMessage(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{"no content", "", false},
		{"a number", `42`, false},
		{"an image only", `[{"type":"image"}]`, false},
		{"a blank string", `"  "`, false},
		{"a string", `"hi"`, true},
		{"a text block", `[{"type":"text","text":"hi"}]`, true},
		{"a blank text block", `[{"type":"text","text":" "}]`, false},
		{"text beside a tool_result", `[{"type":"text","text":"hi"},{"type":"tool_result"}]`, false},
	}
	for _, c := range cases {
		m := message{Role: "user", Content: json.RawMessage(c.content)}
		if got := isUserTurn(entry{}, m); got != c.want {
			t.Errorf("%s: isUserTurn = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestFirstTextReadsAStringAndRefusesTheRest(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
		ok      bool
	}{
		{"no content", "", "", false},
		{"a blank string", `"  "`, "", false},
		{"a string", `"hello"`, "hello", true},
		{"a number", `42`, "", false},
		{"a blocks array", `[{"type":"thinking"},{"type":"text","text":"a"}]`, "a", true},
		{"no text block", `[{"type":"thinking"}]`, "", false},
	}
	for _, c := range cases {
		got, ok := firstText(json.RawMessage(c.content))
		if got != c.want || ok != c.ok {
			t.Errorf("%s: firstText = %q, %v; want %q, %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

// An assistant message whose content is a plain string is a reply too.
func TestAStringAssistantContentIsAReply(t *testing.T) {
	p := transcript(t, userTurn("go"), line{"type": "assistant", "uuid": "a1",
		"message": line{"role": "assistant", "content": "recur is green."}})
	f, err := findReply(p)
	if err != nil || f.text != "recur is green." || f.uuid != "a1" {
		t.Errorf("findReply = %+v, %v", f, err)
	}
}

func TestOpeningShortensOnARuneBoundaryAndNamesAnEmptyLine(t *testing.T) {
	long := strings.Repeat("a", 100)
	if got, want := opening(long), strings.Repeat("a", 60)+"…"; got != want {
		t.Errorf("opening(100 a) = %q, want %q", got, want)
	}
	// 31 two-byte runes: over 60 BYTES, under 60 runes, so it is not cut.
	e := strings.Repeat("é", 31)
	if got, want := opening(e), e+"…"; got != want {
		t.Errorf("opening(31 é) = %q, want %q", got, want)
	}
	// 70 two-byte runes are cut at 60 runes, never mid-rune.
	if got, want := opening(strings.Repeat("é", 70)), strings.Repeat("é", 60)+"…"; got != want {
		t.Errorf("opening(70 é) = %q, want %q", got, want)
	}
	// Exactly 60 bytes is quoted whole.
	if got := opening(strings.Repeat("b", 60)); got != strings.Repeat("b", 60) {
		t.Errorf("opening(60 b) = %q", got)
	}
	if got := opening("\nsecond"); got != "(an empty line)" {
		t.Errorf("opening of an empty first line = %q", got)
	}
	if got := opening("first\nsecond"); got != "first" {
		t.Errorf("opening(two lines) = %q", got)
	}
}

// A zone whose abbreviation would not match the stamp pattern is not offered as
// a stamp, and the reason then carries no clock line at all.
func TestNoStampIsOfferedWhenTheZoneCannotBeOne(t *testing.T) {
	withClock(t, time.Date(2026, 11, 11, 21, 30, 0, 0, time.FixedZone("", 3600)))
	if got := currentStamp(); got != "" {
		t.Errorf("currentStamp = %q, want none", got)
	}
	if got := clockLine(); got != "" {
		t.Errorf("clockLine = %q, want none", got)
	}
	if got := pretoolReason(); strings.Contains(got, "The current local time is exactly") {
		t.Errorf("pretoolReason offered a stamp: %q", got)
	}
	withClock(t, clock)
	if got := currentStamp(); got != stamp(clock) {
		t.Errorf("currentStamp = %q, want %q", got, stamp(clock))
	}
	if got := clockLine(); !strings.Contains(got, "exactly "+stamp(clock)+" -- copy it") {
		t.Errorf("clockLine = %q", got)
	}
}

func TestAStampedVerdictHasNoReasonAndUnknownKindsReportNothing(t *testing.T) {
	if got := (verdict{kind: stamped}).reason(); got != "" {
		t.Errorf("a stamped verdict's reason = %q", got)
	}
	if got := (verdict{kind: stamped}).baseReason(); got != "" {
		t.Errorf("a stamped verdict's baseReason = %q", got)
	}
	if got := (verdict{kind: kind(99)}).report(aReply{}); got != "" {
		t.Errorf("an unknown kind reported %q", got)
	}
}

func TestEachKindReportsItself(t *testing.T) {
	at := time.Date(2026, 11, 11, 19, 30, 0, 0, time.Local)
	cases := []struct {
		v    verdict
		want string
	}{
		{verdict{kind: stamped, at: at, off: 2 * time.Hour}, "STAMPED  2026-11-11 19:30  (2.0 hours from the clock)"},
		{verdict{kind: missing, got: "hello"}, `MISSING  the reply opens on "hello", not on **[2026-11-01 21:05 CET]**`},
		{verdict{kind: twelveHour, got: "9:21 pm"}, `AM/PM    the reply opens on a 12-hour clock: "9:21 pm"`},
		{verdict{kind: impossibleHour, got: "25:30"}, `HOUR     the reply opens on an hour of 24 or more: "25:30"`},
		{verdict{kind: stale, at: at, off: 72 * time.Hour}, "STALE    stamped 2026-11-11 19:30, 3 days from the clock"},
	}
	for _, c := range cases {
		if got := c.v.report(aReply{}); got != c.want {
			t.Errorf("kind %v: report = %q, want %q", c.v.kind, got, c.want)
		}
	}
}

func TestRoundlyPicksMinutesHoursOrDays(t *testing.T) {
	cases := map[time.Duration]string{
		90 * time.Second:  "1 minutes",
		59 * time.Minute:  "59 minutes",
		time.Hour:         "1.0 hours",
		47 * time.Hour:    "47.0 hours",
		48 * time.Hour:    "2 days",
		100 * time.Hour:   "4 days",
		150 * time.Minute: "2.5 hours",
	}
	for d, want := range cases {
		if got := roundly(d); got != want {
			t.Errorf("roundly(%v) = %q, want %q", d, got, want)
		}
	}
}

// Both hooks read the payload from stdin; one that cannot be read is allowed,
// with the reason on stderr and `{}` on stdout, never a refusal.
func TestAnUnreadablePayloadIsAllowedByBothHooks(t *testing.T) {
	for _, mode := range []string{"--hook", "--pretool"} {
		code, out, errOut := driveIn(t, iotest.ErrReader(errBoom), mode)
		if code != 0 || strings.TrimSpace(out) != "{}" {
			t.Errorf("%s: code %d, stdout %q; want an allow", mode, code, out)
		}
		if !strings.Contains(errOut, "replystamp: allowing: cannot read the hook payload: boom") {
			t.Errorf("%s: stderr = %q", mode, errOut)
		}
	}
}

type fakeFile struct {
	statErr error
	readErr error
	size    int64
}

func (f fakeFile) Stat() (os.FileInfo, error) {
	if f.statErr != nil {
		return nil, f.statErr
	}
	return fakeInfo{f.size}, nil
}
func (f fakeFile) ReadAt([]byte, int64) (int, error) { return 0, f.readErr }

type fakeInfo struct{ size int64 }

func (fakeInfo) Name() string       { return "f" }
func (i fakeInfo) Size() int64      { return i.size }
func (fakeInfo) Mode() os.FileMode  { return 0 }
func (fakeInfo) ModTime() time.Time { return time.Time{} }
func (fakeInfo) IsDir() bool        { return false }
func (fakeInfo) Sys() any           { return nil }

func TestLastTurnOfReturnsStatAndReadErrors(t *testing.T) {
	if _, err := lastTurnOf(fakeFile{statErr: errBoom}); !errors.Is(err, errBoom) {
		t.Errorf("a failing Stat gave %v", err)
	}
	s, total, err := lastTurnAt(fakeFile{readErr: errBoom}, 10)
	if !errors.Is(err, errBoom) || s.found || total != 0 {
		t.Errorf("a failing ReadAt gave %+v, %d, %v", s, total, err)
	}
	// io.EOF from ReadAt is not a failure: the short read is judged as it is.
	if _, _, err := lastTurnAt(fakeFile{readErr: io.EOF}, 10); err != nil {
		t.Errorf("EOF gave %v", err)
	}
}

func TestLastTurnOnADirectoryAllows(t *testing.T) {
	if _, err := lastTurn(t.TempDir()); err == nil {
		t.Error("a directory was read as a transcript")
	}
	if _, err := lastTurn(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Error("a missing transcript was read")
	}
}

func shrinkWindows(t *testing.T, start, max int64) {
	t.Helper()
	oldS, oldM := tailStart, maxTail
	tailStart, maxTail = start, max
	t.Cleanup(func() { tailStart, maxTail = oldS, oldM })
}

// The windows double until they reach the cap, and the last one is clamped to
// it: 64 + 128 + 200 bytes, not 64 + 128 + 256.
func TestTheLastWindowIsClampedToTheCap(t *testing.T) {
	shrinkWindows(t, 64, 200)
	data := bytes.Repeat([]byte("xxxxxxxxx\n"), 100)
	s, total, err := lastTurnAt(bytes.NewReader(data), int64(len(data)))
	if err != nil || s.found {
		t.Fatalf("got %+v, %v", s, err)
	}
	if total != 64+128+200 {
		t.Errorf("read %d bytes, want %d", total, 64+128+200)
	}
}

// A window holding no newline has no whole line to read: it is skipped and the
// next, wider one is tried; the whole file is the last resort.
func TestAWindowWithoutANewlineIsSkipped(t *testing.T) {
	shrinkWindows(t, 100, 1000)
	data := bytes.Repeat([]byte("x"), 300)
	s, total, err := lastTurnAt(bytes.NewReader(data), 300)
	if err != nil || s.found {
		t.Fatalf("got %+v, %v", s, err)
	}
	if total != 100+200+300 {
		t.Errorf("read %d bytes, want %d", total, 600)
	}
}

func jsonLines(t *testing.T, lines ...line) []byte {
	t.Helper()
	var b bytes.Buffer
	for _, l := range lines {
		raw, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	return b.Bytes()
}

// A text's timestamp is kept with it, so --pretool judges a stamp against the
// instant the text was written and not against the clock.
func TestNewestTurnKeepsEachTextsTimestamp(t *testing.T) {
	at := time.Date(2026, 11, 11, 19, 0, 0, 0, time.UTC)
	later := at.Add(time.Hour)
	st, ok := newestTurn(jsonLines(t, userTurn("go"),
		stampedAt("a1", "first", at), assistantText("a2", "untimed"), stampedAt("a3", "third", later)))
	if !ok || len(st.texts) != 3 {
		t.Fatalf("newestTurn = %+v, %v", st, ok)
	}
	if !st.texts[0].haveAt || !st.texts[0].at.Equal(at) || st.texts[0].text != "first" {
		t.Errorf("first text = %+v", st.texts[0])
	}
	if st.texts[1].haveAt || st.texts[1].text != "untimed" {
		t.Errorf("untimed text = %+v", st.texts[1])
	}
	if !st.texts[2].haveAt || !st.texts[2].at.Equal(later) {
		t.Errorf("third text = %+v", st.texts[2])
	}
	if !st.first.haveAt || !st.first.at.Equal(at) {
		t.Errorf("the turn's first text lost its time: %+v", st.first)
	}
}

// A stamp is judged against when its text was written: a text stamped three
// hours and a half before the CLOCK is fine if it was written then.
func TestPretoolJudgesAStampAgainstItsOwnTimestamp(t *testing.T) {
	old := clock.Add(-10 * time.Hour)
	p := transcript(t, turnUUID("t1", "go"), stampedAt("a1", stamp(old)+" ok", old), assistantToolUse("a2"))
	code, stdout, _ := pretool(t, p, "s-ownts")
	if ok, _ := denied(t, stdout); code != 0 || ok {
		t.Errorf("a stamp matching its own timestamp was denied: %d %q", code, stdout)
	}
}

func TestPretoolAllowsWhenTheRefusalCannotBeRecorded(t *testing.T) {
	file := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// The state directory sits under a regular file, so it cannot be created.
	withState(t, func() (string, error) { return filepath.Join(file, "sub"), nil })
	withClock(t, clock)
	p := transcript(t, turnUUID("t1", "go"), assistantToolUse("a1"))
	code, out, errOut := driveIn(t, strings.NewReader(pretoolPayload(t, p, "sess", "PreToolUse")), "--pretool")
	if code != 0 || strings.TrimSpace(out) != "{}" {
		t.Errorf("code %d, stdout %q; want an allow", code, out)
	}
	if !strings.Contains(errOut, "cannot record the refusal, so cannot bound it") {
		t.Errorf("stderr = %q", errOut)
	}
}

func TestACounterThatCannotBeKeptAllowsAndWritesNothing(t *testing.T) {
	withState(t, func() (string, error) { return "", errBoom })
	if p, ok := countPath("sess"); ok || p != "" {
		t.Errorf("countPath with no state dir = %q, %v", p, ok)
	}
	if p, ok := pretoolPath("sess"); ok || p != "" {
		t.Errorf("pretoolPath with no state dir = %q, %v", p, ok)
	}
}

func TestTheCounterFilesAreReadBackOrReadAsNothing(t *testing.T) {
	dir := t.TempDir()
	withState(t, func() (string, error) { return dir, nil })

	if n, u := blocksSoFar(""); n != 0 || u != "" {
		t.Errorf("blocksSoFar with no session = %d, %q", n, u)
	}
	if n, u := blocksSoFar("absent"); n != 0 || u != "" {
		t.Errorf("blocksSoFar of no file = %d, %q", n, u)
	}
	if err := os.WriteFile(filepath.Join(dir, "garbled.blocks"), []byte("many u1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if n, u := blocksSoFar("garbled"); n != 0 || u != "" {
		t.Errorf("blocksSoFar of a garbled file = %d, %q", n, u)
	}
	if err := os.WriteFile(filepath.Join(dir, "ok.blocks"), []byte("3 u9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if n, u := blocksSoFar("ok"); n != 3 || u != "u9" {
		t.Errorf("blocksSoFar = %d, %q; want 3, u9", n, u)
	}

	pf := filepath.Join(dir, "p.pretool")
	if n, turn := pretoolCount(pf); n != 0 || turn != "" {
		t.Errorf("pretoolCount of no file = %d, %q", n, turn)
	}
	if err := os.WriteFile(pf, []byte("lots t1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if n, turn := pretoolCount(pf); n != 0 || turn != "" {
		t.Errorf("pretoolCount of a garbled file = %d, %q", n, turn)
	}
	if err := os.WriteFile(pf, []byte("4 t7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if n, turn := pretoolCount(pf); n != 4 || turn != "t7" {
		t.Errorf("pretoolCount = %d, %q; want 4, t7", n, turn)
	}
}

func TestRecordBlockWritesNothingItCannotName(t *testing.T) {
	dir := t.TempDir()
	withState(t, func() (string, error) { return dir, nil })
	recordBlock("", 1, "u1")
	recordBlock("a/b", 1, "u1")
	if ents, err := os.ReadDir(dir); err != nil || len(ents) != 0 {
		t.Errorf("recordBlock wrote %v (%v) for a session it cannot name", ents, err)
	}
	recordBlock("good", 2, "u2")
	if b, err := os.ReadFile(filepath.Join(dir, "good.blocks")); err != nil || string(b) != "2 u2\n" {
		t.Errorf("good.blocks = %q, %v", b, err)
	}

	// With the directory impossible to create, it gives up silently.
	file := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	withState(t, func() (string, error) { return filepath.Join(file, "sub"), nil })
	recordBlock("good", 2, "u2")
	if _, err := os.Stat(filepath.Join(file, "sub")); err == nil {
		t.Error("recordBlock created a directory under a regular file")
	}
}

// ---- survivors of the first mutation sweep: each assertion below names the
// message or value that told the guard from its absence.

// within fails the test, instead of hanging it, when fn does not return: a
// mutation that removes a loop's exit must be caught by an assertion, not by a
// timeout. The stray goroutine ends with the test binary.
func within(t *testing.T, d time.Duration, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { fn(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("did not return within %v", d)
	}
}

func TestNoTranscriptNamesTheFlagToPass(t *testing.T) {
	code, _, errOut := drive(t, "")
	if code != 2 || !strings.Contains(errOut, "no transcript: pass --transcript <file>, or --hook") {
		t.Errorf("code %d, stderr %q", code, errOut)
	}
}

func TestAMissingTranscriptIsReportedAsMissing(t *testing.T) {
	_, err := findReply(filepath.Join(t.TempDir(), "absent.jsonl"))
	if err == nil || !strings.Contains(err.Error(), "no such file") {
		t.Errorf("findReply of a missing file = %v", err)
	}
	_, err = lastTurn(filepath.Join(t.TempDir(), "absent.jsonl"))
	if err == nil || !strings.Contains(err.Error(), "no such file") {
		t.Errorf("lastTurn of a missing file = %v", err)
	}
}

// An entry with a field of the wrong type is undecodable and skipped whole,
// not read with the fields that did decode.
func TestAnEntryWithAWronglyTypedFieldIsSkipped(t *testing.T) {
	good := string(jsonLines(t, userTurn("go")))
	bad := `{"type":"assistant","uuid":"a1","isApiErrorMessage":"yes","message":{"role":"assistant","content":[{"type":"text","text":"hello"}]}}` + "\n"
	_, err := readReply(strings.NewReader(good + bad))
	if err == nil || !strings.Contains(err.Error(), "no reply text") {
		t.Errorf("readReply = %v, want no reply text", err)
	}
	if _, _, ok := decodeEntry([]byte(strings.TrimSpace(bad))); ok {
		t.Error("decodeEntry accepted a wrongly typed field")
	}
	if _, _, ok := decodeEntry(nil); ok {
		t.Error("decodeEntry accepted an empty line")
	}
}

func TestBareHookAndClockFlagsAreModes(t *testing.T) {
	for _, a := range []string{"--hook", "-hook", "--pretool", "-pretool"} {
		if !hookMode([]string{a}) {
			t.Errorf("hookMode(%s) = false", a)
		}
	}
	for _, a := range []string{"--clock", "-clock"} {
		if !clockMode([]string{a}) {
			t.Errorf("clockMode(%s) = false", a)
		}
	}
	if hookMode([]string{"--hook=false", "--pretool=0"}) {
		t.Error("an explicit false is a hook")
	}
}

func TestHooksSayWhyTheyAllowABadPayload(t *testing.T) {
	cases := []struct {
		mode, stdin, want string
	}{
		{"--hook", "not json", "the hook payload is not JSON"},
		{"--hook", `{"hook_event_name":"Stop"}`, "the hook payload names no transcript"},
		{"--pretool", "not json", "the hook payload is not JSON"},
		{"--pretool", `{"hook_event_name":"PreToolUse"}`, "the hook payload names no transcript"},
	}
	for _, c := range cases {
		code, out, errOut := drive(t, c.stdin, c.mode)
		if code != 0 || strings.TrimSpace(out) != "{}" || !strings.Contains(errOut, c.want) {
			t.Errorf("%s %q: code %d stdout %q stderr %q; want an allow saying %q", c.mode, c.stdin, code, out, errOut, c.want)
		}
	}
}

func TestPretoolSaysWhyItAllowsWhatItCannotJudge(t *testing.T) {
	// A transcript that cannot be opened.
	code, out, errOut := pretool(t, filepath.Join(t.TempDir(), "absent.jsonl"), "s1")
	if code != 0 || strings.TrimSpace(out) != "{}" || !strings.Contains(errOut, "no such file") {
		t.Errorf("missing transcript: %d %q %q", code, out, errOut)
	}
	// A transcript with no user turn in it.
	_, out, errOut = pretool(t, transcript(t, assistantToolUse("a1")), "s2")
	if strings.TrimSpace(out) != "{}" || !strings.Contains(errOut, "no user turn located in the transcript") {
		t.Errorf("no turn: %q %q", out, errOut)
	}
	// A session it cannot name cannot be counted, so it is not refused.
	p := transcript(t, turnUUID("t1", "go"), assistantToolUse("a1"))
	_, out, errOut = pretool(t, p, "")
	if strings.TrimSpace(out) != "{}" || !strings.Contains(errOut, "cannot keep a refusal count for this turn") {
		t.Errorf("no session: %q %q", out, errOut)
	}
	// Nor a turn without a uuid.
	noUUID := userTurn("go")
	delete(noUUID, "uuid")
	_, out, errOut = pretool(t, transcript(t, noUUID, assistantToolUse("a1")), "s3")
	if strings.TrimSpace(out) != "{}" || !strings.Contains(errOut, "cannot keep a refusal count for this turn") {
		t.Errorf("no uuid: %q %q", out, errOut)
	}
}

func withSettle(t *testing.T, wait time.Duration, sleep func(time.Duration)) {
	t.Helper()
	oldWait, oldSleep := toolWait, settleSleep
	toolWait, settleSleep = wait, sleep
	t.Cleanup(func() { toolWait, settleSleep = oldWait, oldSleep })
}

func TestSettledTurnReturnsWhatItCouldNotRead(t *testing.T) {
	withSettle(t, 3*settleStep, func(time.Duration) {})
	_, why, err := settledTurn(filepath.Join(t.TempDir(), "absent.jsonl"), "toolu_x")
	if err == nil || why != "" {
		t.Errorf("a missing transcript gave %q, %v", why, err)
	}
	// No turn at all: nothing to wait for, whatever the id.
	p := transcript(t, assistantToolUse("a1"))
	for _, id := range []string{"", "toolu_x"} {
		s, why, err := settledTurn(p, id)
		if s.found || why != "" || err != nil {
			t.Errorf("id %q: found %v, why %q, err %v; want none", id, s.found, why, err)
		}
	}
	// A turn but no id: cannot be known complete.
	p = transcript(t, turnUUID("t1", "go"), assistantToolUse("a1"))
	if _, why, _ := settledTurn(p, ""); !strings.Contains(why, "names no tool_use_id") {
		t.Errorf("no id gave %q", why)
	}
	if _, why, _ := settledTurn(p, "toolu_a1"); why != "" {
		t.Errorf("an id present gave %q", why)
	}
}

// The wait for a tool_use that never lands ends at the cap and says so.
func TestSettledTurnStopsWaitingAtTheCap(t *testing.T) {
	sleeps := 0
	withSettle(t, 3*settleStep, func(time.Duration) { sleeps++ })
	p := transcript(t, turnUUID("t1", "go"), assistantToolUse("a1"))
	var why string
	within(t, 10*time.Second, func() { _, why, _ = settledTurn(p, "toolu_never") })
	if !strings.Contains(why, `tool_use "toolu_never" is not in the transcript after 150ms`) {
		t.Errorf("why = %q", why)
	}
	if sleeps != 3 {
		t.Errorf("slept %d times, want 3", sleeps)
	}
}

// The transcript changing under the wait: gone is an error, and one with no
// turn left is "no turn", each returned at once and not waited out.
func TestSettledTurnReturnsWhenTheTranscriptChangesDuringTheWait(t *testing.T) {
	p := transcript(t, turnUUID("t1", "go"), assistantToolUse("a1"))
	withSettle(t, time.Hour, func(time.Duration) { os.Remove(p) })
	var (
		why string
		err error
	)
	within(t, 10*time.Second, func() { _, why, err = settledTurn(p, "toolu_never") })
	if err == nil || why != "" {
		t.Errorf("a vanished transcript gave %q, %v", why, err)
	}

	p = transcript(t, turnUUID("t1", "go"), assistantToolUse("a1"))
	withSettle(t, time.Hour, func(time.Duration) {
		if err := os.WriteFile(p, jsonLines(t, assistantToolUse("a9")), 0o644); err != nil {
			t.Error(err)
		}
	})
	var s turnState
	within(t, 10*time.Second, func() { s, why, err = settledTurn(p, "toolu_never") })
	if err != nil || why != "" || s.found {
		t.Errorf("a transcript left with no turn gave found %v, %q, %v", s.found, why, err)
	}
}

// Windows that never find a turn end at the whole file, or at the cap, and
// never loop.
func TestTheSearchForATurnEndsAtTheWholeFileOrTheCap(t *testing.T) {
	data := bytes.Repeat([]byte("xxxx\n"), 10)
	var s turnState
	var err error
	within(t, 10*time.Second, func() { s, _, err = lastTurnAt(bytes.NewReader(data), int64(len(data))) })
	if s.found || err != nil {
		t.Errorf("whole file: %+v, %v", s, err)
	}
	shrinkWindows(t, 16, 32)
	data = bytes.Repeat([]byte("xxxx\n"), 40)
	var total int64
	within(t, 10*time.Second, func() { s, total, err = lastTurnAt(bytes.NewReader(data), int64(len(data))) })
	if s.found || err != nil || total != 16+32 {
		t.Errorf("capped: %+v, read %d, %v; want 48 bytes and no turn", s, total, err)
	}
}

// A loop that never exits would hang the suite until go test's own ten-minute
// timeout, and a mutation sweep would record that as HUNG, which holds nothing.
// The watchdog turns a hang into a plain, early FAIL (the suite takes seconds).
func TestMain(m *testing.M) {
	watchdog := time.AfterFunc(90*time.Second, func() {
		fmt.Fprintln(os.Stderr, "--- FAIL: the suite did not finish in 90s: a loop lost its exit")
		os.Exit(1)
	})
	code := m.Run()
	watchdog.Stop()
	os.Exit(code)
}
