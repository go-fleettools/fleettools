package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
)

// inState runs fn with the state directory redirected to a fresh one.
func inState(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	withState(t, func() (string, error) { return dir, nil })
	saved := now
	now = func() time.Time { return clock }
	t.Cleanup(func() { now = saved })
	return dir
}

func runIn(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	c := RootCmd()
	c.SetIn(strings.NewReader(stdin))
	var out, errOut bytes.Buffer
	code := cli.Drive(c, args, &out, &errOut)
	return code, out.String(), errOut.String()
}

// denyOf runs --pretool over lines in the redirected state and returns the
// deny reason ("" when allowed).
func denyOf(t *testing.T, session string, lines ...line) string {
	t.Helper()
	p := transcript(t, lines...)
	_, stdout, _ := runIn(t, pretoolPayload(t, p, session, "PreToolUse"), "--pretool")
	ok, d := denied(t, stdout)
	if !ok {
		return ""
	}
	return d.HookSpecificOutput.PermissionDecisionReason
}

func records(t *testing.T, dir string) []denyRecord {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, denyLogName))
	if err != nil {
		t.Fatalf("no deny log: %v", err)
	}
	var out []denyRecord
	for _, l := range bytes.Split(bytes.TrimSpace(b), []byte("\n")) {
		var r denyRecord
		if err := json.Unmarshal(l, &r); err != nil {
			t.Fatalf("bad log line %q: %v", l, err)
		}
		out = append(out, r)
	}
	return out
}

// The 2026-11-22 incident, first shape: after a compaction the turn opens with
// a tool_use and no text. The record must say exactly that.
func TestDenyLogRecordsAToolUseFirstAfterACompaction(t *testing.T) {
	dir := inState(t)
	reason := denyOf(t, "s1",
		turnUUID("t0", "old"), assistantText("a0", "old reply"),
		compactionTurn("c1"), assistantThinking("a1"), assistantToolUse("a2"))
	if !strings.Contains(reason, filepath.Join(dir, denyLogName)) || !strings.Contains(reason, "replystamp why") {
		t.Fatalf("the deny does not name the log: %q", reason)
	}
	rs := records(t, dir)
	if len(rs) != 1 {
		t.Fatalf("%d records, want 1", len(rs))
	}
	r := rs[0]
	if r.Hook != "pretool" || r.Why != "no-text" || r.Session != "s1" || r.ToolUseID != "toolu_a2" {
		t.Errorf("header = %+v", r)
	}
	if r.Anchor != (traceAnchor{Line: 3, UUID: "c1", Kind: "compaction"}) {
		t.Errorf("anchor = %+v, want line 3 c1 compaction", r.Anchor)
	}
	want := []struct {
		line   int
		typ    string
		blocks string
		judged bool
	}{{3, "user", "text", false}, {4, "assistant", "thinking", false}, {5, "assistant", "tool_use", true}}
	if len(r.Entries) != len(want) {
		t.Fatalf("entries = %+v", r.Entries)
	}
	for i, w := range want {
		g := r.Entries[i]
		if g.Line != w.line || g.Type != w.typ || strings.Join(g.Blocks, ",") != w.blocks || g.Judged != w.judged {
			t.Errorf("entry %d = %+v, want %+v", i, g, w)
		}
	}
	if got := r.Entries[0].Texts; len(got) != 1 || got[0] != "This session is being continued from a p" {
		t.Errorf("anchor text prefix = %q, want the first 40 characters", got)
	}
}

// Second shape: a background task finishing opens the turn.
func TestDenyLogRecordsAToolUseFirstAfterATaskNotification(t *testing.T) {
	dir := inState(t)
	denyOf(t, "s2", turnUUID("t1", "go"), assistantText("a1", stamp(clock)+"\n\nold"),
		taskNotification("n1"), assistantToolUse("a2"))
	r := records(t, dir)[0]
	if r.Anchor.Kind != "notification" || r.Anchor.Line != 3 || r.Anchor.UUID != "n1" {
		t.Errorf("anchor = %+v", r.Anchor)
	}
	if r.Why != "no-text" || len(r.Entries) != 2 || !r.Entries[1].Judged {
		t.Errorf("record = %+v", r)
	}
}

// A plain user turn, and an unstamped text: the reason is the verdict's kind.
func TestDenyLogRecordsAnUnstampedTextAndAUserAnchor(t *testing.T) {
	dir := inState(t)
	denyOf(t, "s3", turnUUID("t1", "go"), assistantText("a1", "no stamp here"), assistantToolUse("a2"))
	r := records(t, dir)[0]
	if r.Why != "missing" || r.Anchor.Kind != "user" || r.Anchor.Line != 1 {
		t.Errorf("record = %+v", r)
	}
	if got := r.Entries[1].Texts; len(got) != 1 || got[0] != "no stamp here" {
		t.Errorf("texts = %q", got)
	}
}

func TestAPassingTurnLeavesNoRecord(t *testing.T) {
	dir := inState(t)
	if r := denyOf(t, "s4", turnUUID("t1", "go"), assistantText("a1", stamp(clock)+"\n\nok"), assistantToolUse("a2")); r != "" {
		t.Fatalf("denied: %q", r)
	}
	if _, err := os.Stat(filepath.Join(dir, denyLogName)); err == nil {
		t.Fatal("a log was written for a turn that passed")
	}
}

func TestTheStopHookLogsItsRefusalToo(t *testing.T) {
	dir := inState(t)
	p := transcript(t, userTurn("proceed"), assistantText("a1", "no stamp"))
	_, stdout, _ := runIn(t, `{"session_id":"stop1","transcript_path":"`+p+`","hook_event_name":"Stop"}`, "--hook")
	if !strings.Contains(stdout, "replystamp why") {
		t.Fatalf("the block does not name the log: %q", stdout)
	}
	r := records(t, dir)[0]
	if r.Hook != "stop" || r.Why != "missing" || r.ToolUseID != "" || len(r.Entries) != 2 {
		t.Errorf("record = %+v", r)
	}
}

// ⛔ No full text, no tool input, no tool result: a secret in any of them must
// not reach the log.
func TestDenyLogKeepsNoSecrets(t *testing.T) {
	dir := inState(t)
	long := "0123456789012345678901234567890123456789SECRETTAIL"
	denyOf(t, "s5",
		turnUUID("t1", long),
		line{"type": "assistant", "uuid": "a1", "message": line{"role": "assistant", "content": []line{
			{"type": "tool_use", "name": "Bash", "id": "toolu_x", "input": line{"command": "TOKEN=SECRETINPUT"}}}}},
		toolResult("SECRETRESULT"),
		assistantToolUse("a2"))
	b, _ := os.ReadFile(filepath.Join(dir, denyLogName))
	for _, leak := range []string{"SECRETTAIL", "SECRETINPUT", "SECRETRESULT"} {
		if bytes.Contains(b, []byte(leak)) {
			t.Errorf("the log holds %q", leak)
		}
	}
	if !bytes.Contains(b, []byte("0123456789012345678901234567890123456789")) {
		t.Error("the 40-character prefix is missing")
	}
	if fi, _ := os.Stat(filepath.Join(dir, denyLogName)); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}
}

// The line number is the file's, whichever window the anchor was found in.
func TestDenyLogLineNumbersSurviveTheTailWindow(t *testing.T) {
	savedStart, savedMax := tailStart, maxTail
	t.Cleanup(func() { tailStart, maxTail = savedStart, savedMax })
	dir := inState(t)
	lines := []line{}
	for i := 0; i < 60; i++ {
		lines = append(lines, assistantText("x"+string(rune('a'+i%26)), "filler filler filler filler"))
	}
	lines = append(lines, compactionTurn("c1"), assistantToolUse("a2"))
	tailStart, maxTail = 512, 1<<20
	denyOf(t, "s6", lines...)
	r := records(t, dir)[0]
	if r.Anchor.Line != 61 || r.Entries[0].Line != 61 || r.Entries[1].Line != 62 {
		t.Errorf("anchor %+v entries %+v, want lines 61 and 62", r.Anchor, r.Entries)
	}
}

func TestDenyLogCapsAHugeTurn(t *testing.T) {
	dir := inState(t)
	lines := []line{turnUUID("t1", "go")}
	for i := 0; i < 150; i++ {
		lines = append(lines, assistantToolUseID("a"+strings.Repeat("z", i%3)+string(rune('A'+i%26)), "toolu_"+string(rune(1000+i)), ""), toolResult("r"))
	}
	lines = append(lines, assistantToolUse("last"))
	denyOf(t, "s7", lines...)
	r := records(t, dir)[0]
	if len(r.Entries) != maxTraceEntries || r.Omitted != 302-maxTraceEntries {
		t.Errorf("%d entries, %d omitted; want %d and %d", len(r.Entries), r.Omitted, maxTraceEntries, 302-maxTraceEntries)
	}
	if !r.Entries[len(r.Entries)-1].Judged || r.Entries[0].Line != 1 {
		t.Errorf("the cap must keep the anchor and the judged call: first %+v last %+v", r.Entries[0], r.Entries[len(r.Entries)-1])
	}
}

func TestDenyLogRotatesAtItsBound(t *testing.T) {
	dir := inState(t)
	path := filepath.Join(dir, denyLogName)
	if err := os.WriteFile(path, bytes.Repeat([]byte("{}\n"), maxLogBytes/3+1), 0o600); err != nil {
		t.Fatal(err)
	}
	denyOf(t, "s8", turnUUID("t1", "go"), assistantToolUse("a1"))
	old, err := os.Stat(path + ".1")
	if err != nil || old.Size() < maxLogBytes {
		t.Fatalf("the full log was not rotated: %v", err)
	}
	if fi, _ := os.Stat(path); fi.Size() > 1<<16 {
		t.Errorf("the new log holds %d bytes: it did not start over", fi.Size())
	}
}

func TestLoggingNeverChangesTheVerdict(t *testing.T) {
	inState(t)
	withState(t, func() (string, error) { return "", errors.New("no state") })
	// No state directory: no counter either, so --pretool allows; recordDeny is
	// asked directly for the unwritable cases.
	cwd := t.TempDir()
	t.Chdir(cwd)
	savedOpen := openLog
	t.Cleanup(func() { openLog = savedOpen })
	opened := 0
	openLog = func(string) (io.WriteCloser, error) { opened++; return brokenWriter{}, nil }
	if recordDeny("pretool", "no-text", "s", "x", "") != "" || opened != 0 {
		t.Errorf("recorded without a state directory (log opened %d times)", opened)
	}
	if left, _ := os.ReadDir(cwd); len(left) != 0 {
		t.Errorf("with no state directory the log went to the working directory: %v", left)
	}
	if logged("reason", "") != "reason" {
		t.Error("a missing path must leave the reason alone")
	}

	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	withState(t, func() (string, error) { return filepath.Join(blocker, "sub"), nil })
	opened = 0
	if recordDeny("pretool", "no-text", "s", "x", "") != "" || opened != 0 {
		t.Errorf("recorded under a file (log opened %d times)", opened)
	}
	// An open that fails is a failure even when it hands back a writer.
	withState(t, func() (string, error) { return dir, nil })
	openLog = func(string) (io.WriteCloser, error) { return brokenWriter{}, errors.New("denied") }
	if recordDeny("pretool", "no-text", "s", "x", "") != "" {
		t.Error("recorded although the log could not be opened")
	}
	openLog = savedOpen

	withState(t, func() (string, error) { return dir, nil })
	if err := os.Mkdir(filepath.Join(dir, denyLogName), 0o755); err != nil {
		t.Fatal(err)
	}
	if recordDeny("pretool", "no-text", "s", "x", "") != "" {
		t.Error("recorded into a directory")
	}
}

func TestARecordThatCannotBeWrittenIsNotAnnounced(t *testing.T) {
	inState(t)
	saved := openLog
	t.Cleanup(func() { openLog = saved })
	openLog = func(string) (io.WriteCloser, error) { return brokenWriter{werr: errors.New("full")}, nil }
	reason := denyOf(t, "s9", turnUUID("t1", "go"), assistantToolUse("a1"))
	if reason == "" || strings.Contains(reason, "replystamp why") {
		t.Errorf("the deny must stand, without naming a log that holds nothing: %q", reason)
	}
}

// A transcript that cannot be traced still leaves a record that says so.
func TestDenyLogRecordsWhyItCouldNotTrace(t *testing.T) {
	dir := inState(t)
	if recordDeny("pretool", "no-text", "s", filepath.Join(dir, "nope.jsonl"), "") == "" {
		t.Fatal("the record was not written")
	}
	if recordDeny("pretool", "no-text", "s", transcript(t, assistantText("a", "x")), "") == "" {
		t.Fatal("the record was not written")
	}
	rs := records(t, dir)
	if len(rs) != 2 || rs[0].TraceError == "" || !strings.Contains(rs[1].TraceError, "no user turn") {
		t.Errorf("records = %+v", rs)
	}
}

type failingAt struct{ data []byte }

func (f failingAt) ReadAt(p []byte, off int64) (int, error) {
	if off > 0 {
		return 0, errors.New("disk gone")
	}
	return copy(p, f.data[:4]), nil
}

func TestLineAtReportsAReadError(t *testing.T) {
	if _, err := lineAt(failingAt{[]byte("a\nb\nc\nd\n")}, 100000); err == nil {
		t.Fatal("a failing read must fail")
	}
	if n, err := lineAt(bytes.NewReader([]byte("a\nb\nc\n")), 4); err != nil || n != 3 {
		t.Fatalf("lineAt = %d, %v; want 3", n, err)
	}
}

// tailOnly serves reads at a non-zero offset and fails the read from the start:
// the tail window is found, and counting the lines before it is what breaks.
type tailOnly struct{ data []byte }

func (f tailOnly) Stat() (os.FileInfo, error) { return fakeInfo{int64(len(f.data))}, nil }
func (f tailOnly) ReadAt(p []byte, off int64) (int, error) {
	if off == 0 {
		return 0, errors.New("disk gone")
	}
	return copy(p, f.data[off:]), nil
}

type brokenWriter struct{ werr, cerr error }

func (w brokenWriter) Write(p []byte) (int, error) { return len(p), w.werr }
func (w brokenWriter) Close() error                { return w.cerr }

func TestTraceOfAndAppendToReportTheirFailures(t *testing.T) {
	if _, _, _, err := traceOf(fakeFile{statErr: errors.New("no stat")}, ""); err == nil {
		t.Error("an unstattable file must fail")
	}
	saved := tailStart
	t.Cleanup(func() { tailStart = saved })
	tailStart = 90
	data := []byte(`{"type":"assistant","uuid":"a"}` + "\n" +
		`{"type":"user","uuid":"u","message":{"role":"user","content":"hello there"}}` + "\n")
	if _, _, _, err := traceOf(tailOnly{data}, ""); err == nil {
		t.Error("a failing line count must fail")
	}
	if appendTo(brokenWriter{}, nil) != true || appendTo(brokenWriter{werr: errors.New("x")}, nil) ||
		appendTo(brokenWriter{cerr: errors.New("x")}, nil) {
		t.Error("appendTo must hold only when the write and the close both do")
	}
}

func TestTraceTurnReportsAnUnreadableFile(t *testing.T) {
	// A directory opens and stats, but cannot be read as a transcript.
	_, _, _, err := traceTurn(t.TempDir(), "")
	if err == nil || strings.Contains(err.Error(), "no user turn") {
		t.Fatalf("a directory must fail on the READ, not be taken for an empty transcript: %v", err)
	}
	_, _, _, err = traceTurn(filepath.Join(t.TempDir(), "nope"), "")
	if err == nil || !strings.Contains(err.Error(), "no such file") {
		t.Fatalf("a missing file must fail on the OPEN: %v", err)
	}
}

func TestSummariseBoundsWhatItKeeps(t *testing.T) {
	if b, x := summarise(json.RawMessage(`12`)); b != nil || x != nil {
		t.Errorf("non-content = %v %v", b, x)
	}
	var parts []string
	for i := 0; i < maxBlocksPerItem+4; i++ {
		parts = append(parts, `{"type":"tool_use"}`)
	}
	parts[0] = `{"type":"a-block-type-that-is-far-too-long-to-keep"}`
	b, _ := summarise(json.RawMessage("[" + strings.Join(parts, ",") + "]"))
	if len(b) != maxBlocksPerItem+1 || b[maxBlocksPerItem] != "+4 more" || len(b[0]) != 24 {
		t.Errorf("blocks = %q", b)
	}
	if got := prefix(strings.Repeat("é", 100)); len([]rune(got)) != textPrefixRunes {
		t.Errorf("prefix cut to %d runes", len([]rune(got)))
	}
}

func TestWhyPrintsTheLatestRecords(t *testing.T) {
	inState(t)
	code, out, _ := runIn(t, "", "why")
	if code != 0 || !strings.Contains(out, "no denial recorded") {
		t.Fatalf("empty: %d %q", code, out)
	}
	denyOf(t, "w1", turnUUID("t0", "old"), assistantText("a0", "old reply"), compactionTurn("c1"), assistantToolUse("a2"))
	denyOf(t, "w2", turnUUID("t1", "go"), assistantText("a1", "no stamp here"), assistantToolUse("a2"))
	code, out, _ = runIn(t, "", "why")
	for _, want := range []string{"session w1", "session w2", "anchor: line 3  compaction  uuid c1",
		"<- the refused call", `text: "no stamp here"`, "missing"} {
		if code != 0 || !strings.Contains(out, want) {
			t.Errorf("why lacks %q:\n%s", want, out)
		}
	}
	_, out, _ = runIn(t, "", "why", "--last", "1")
	if strings.Contains(out, "session w1") || !strings.Contains(out, "session w2") {
		t.Errorf("--last 1 printed:\n%s", out)
	}
}

func TestWhyReadsBothHalvesAndSkipsGarbage(t *testing.T) {
	dir := inState(t)
	path := filepath.Join(dir, denyLogName)
	rec := func(s string) string {
		b, _ := json.Marshal(denyRecord{Session: s, Hook: "pretool", Why: "no-text"})
		return string(b) + "\n"
	}
	_ = os.WriteFile(path+".1", []byte(rec("old")), 0o600)
	_ = os.WriteFile(path, []byte("not json\n\n"+rec("new")), 0o600)
	code, out, errOut := runIn(t, "", "why")
	if code != 0 || !strings.Contains(out, "session old") || !strings.Contains(out, "session new") {
		t.Errorf("why = %d %q", code, out)
	}
	if !strings.Contains(errOut, "1 unreadable line") {
		t.Errorf("stderr = %q", errOut)
	}
}

func TestWhyRefusesWhatItCannotRead(t *testing.T) {
	inState(t)
	if code, _, _ := runIn(t, "", "why", "--last", "0"); code != 2 {
		t.Errorf("--last 0 exited %d, want 2", code)
	}
	withState(t, func() (string, error) { return "", errors.New("no state") })
	if code, _, _ := runIn(t, "", "why"); code != 2 {
		t.Errorf("no state dir exited %d, want 2", code)
	}
}

func TestWhyPrintsOmissionsAndTraceErrors(t *testing.T) {
	var b bytes.Buffer
	denyRecord{Time: "t", Hook: "stop", Why: "x", TraceError: "boom"}.print(&b)
	if !strings.Contains(b.String(), "could not trace the turn: boom") {
		t.Errorf("print = %q", b.String())
	}
	b.Reset()
	denyRecord{Entries: []traceItem{{Line: 1, Type: "user"}, {Line: 9, Type: "assistant"}}, Omitted: 7}.print(&b)
	if !strings.Contains(b.String(), "... 7 entries omitted ...") {
		t.Errorf("print = %q", b.String())
	}
}
