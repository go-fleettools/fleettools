// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-fleettools/fleettools/internal/cli"
)

// theRealLog is the output that caused the lesson: buildscan --vet, exit 0,
// the refusal on line 2 and the summary on the last line. A tail of six lines
// shows the summary and not the refusal, which is exactly what happened.
const theRealLog = `buildscan: reading .github/workflows from /Users/x/clone
buildscan: no record: the working tree has uncommitted changes, so this pass describes bytes HEAD does not hold; commit and vet again
buildscan: configuration 1/2 darwin/arm64
buildscan: configuration 2/2 windows/amd64
buildscan: 0 files no configuration compiles
buildscan: 2 configurations, 0 findings
buildscan: go vet passed under all 2
`

func TestTheRealLogIsNotClean(t *testing.T) {
	hits, n, err := scan("vet.log", strings.NewReader(theRealLog), declineWords)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if n != 7 {
		t.Fatalf("read %d lines, want 7", n)
	}
	if len(hits) != 1 {
		t.Fatalf("got %d declining lines, want 1: %+v", len(hits), hits)
	}
	if hits[0].Line != 2 {
		t.Fatalf("the refusal is on line %d, want 2", hits[0].Line)
	}
	if !strings.Contains(hits[0].Text, "no record") {
		t.Fatalf("line text %q does not hold the refusal", hits[0].Text)
	}
	// ⛔ The witness for the whole tool: the LAST line says it passed.
	last := strings.Split(strings.TrimRight(theRealLog, "\n"), "\n")
	if w := last[len(last)-1]; !strings.Contains(w, "passed under all") {
		t.Fatalf("fixture lost its reassuring tail: %q", w)
	}
}

func TestScanCountsLinesWithoutATrailingNewline(t *testing.T) {
	hits, n, err := scan("x", strings.NewReader("quiet\nskipped the step"), declineWords)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("read %d lines, want 2", n)
	}
	if len(hits) != 1 || hits[0].Line != 2 {
		t.Fatalf("hits %+v, want one on line 2", hits)
	}
}

func TestScanIsCaseInsensitive(t *testing.T) {
	for _, s := range []string{"NOT READ", "Not Read", "not read"} {
		hits, _, err := scan("x", strings.NewReader(s+"\n"), declineWords)
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) != 1 {
			t.Fatalf("%q: got %d hits, want 1", s, len(hits))
		}
	}
}

func TestScanEmptyReadsNothing(t *testing.T) {
	hits, n, err := scan("x", strings.NewReader(""), declineWords)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 || len(hits) != 0 {
		t.Fatalf("n=%d hits=%d, want 0 and 0", n, len(hits))
	}
}

// ⛔ A line past the bound is REFUSED, not truncated. Truncating would drop
// the half of the line the decline might be in and then return a nil error,
// which is this tool's own failure mode wearing its own clothes.
func TestScanRefusesAnEnormousLine(t *testing.T) {
	old := maxLineBytes
	maxLineBytes = 8
	defer func() { maxLineBytes = old }()
	long := strings.Repeat("a", 64) + "skipped\n"
	hits, n, err := scan("x", strings.NewReader(long), declineWords)
	if err == nil {
		t.Fatalf("a line past the bound was accepted: %d lines, %d hits", n, len(hits))
	}
	if !strings.Contains(err.Error(), "too long") {
		t.Fatalf("error %q does not say the line was too long", err)
	}
}

func TestMatchesReturnsTheWord(t *testing.T) {
	if w := matches("buildscan: no record: dirty", declineWords); w != "no record" {
		t.Fatalf("matches returned %q, want %q", w, "no record")
	}
	if w := matches("all good", declineWords); w != "" {
		t.Fatalf("matches returned %q on a clean line", w)
	}
}

// ⛔ The vocabulary must NOT fire on an ordinary green run, or the hook becomes
// noise and the next refusal is skipped past like this one was.
func TestOrdinaryOutputIsQuiet(t *testing.T) {
	quiet := []string{
		"ok  \tgithub.com/go-fleettools/fleettools/cmd/declines\t0.312s",
		"PASS",
		"go: downloading github.com/spf13/cobra v1.10.2",
		"To github.com/go-fleettools/fleettools",
		" * [new branch]      work -> work",
		"3 files changed, 42 insertions(+)",
		"gofmt: 0 files",
		"error: compile failed",
		"FAIL\tgithub.com/x/y\t0.1s",
		"warning: deprecated call",
	}
	for _, line := range quiet {
		if w := matches(line, declineWords); w != "" {
			t.Errorf("%q fired on %q", w, line)
		}
	}
}

func TestEveryDeclineWordIsLowerCaseAndNonEmpty(t *testing.T) {
	seen := map[string]bool{}
	for _, w := range declineWords {
		if w == "" {
			t.Fatal("an empty decline word matches every line")
		}
		if w != strings.ToLower(w) {
			t.Errorf("%q is not lower case, so matches() can never find it", w)
		}
		if seen[w] {
			t.Errorf("%q is listed twice", w)
		}
		seen[w] = true
	}
}

func TestScanFileRefusesADirectory(t *testing.T) {
	dir := t.TempDir()
	_, _, err := scanFile(dir, declineWords)
	if err == nil {
		t.Fatal("a directory was accepted as an output file")
	}
	if !strings.Contains(err.Error(), dir) {
		t.Fatalf("error %q does not name what it could not read", err)
	}
}

func TestScanFileRefusesAFileOverTheBound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.log")
	if err := os.WriteFile(path, []byte("skipped\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := maxFileBytes
	maxFileBytes = 3
	defer func() { maxFileBytes = old }()
	_, _, err := scanFile(path, declineWords)
	if err == nil {
		t.Fatal("a file over the bound was read")
	}
	if !strings.Contains(err.Error(), "more than the") {
		t.Fatalf("error %q does not name the bound", err)
	}
}

// errReader fails after handing over a prefix, so the non-EOF read path of
// scan() is exercised by an assertion rather than by nothing.
type errReader struct {
	prefix string
	done   bool
}

func (e *errReader) Read(p []byte) (int, error) {
	if !e.done {
		e.done = true
		n := copy(p, e.prefix)
		return n, nil
	}
	return 0, fmt.Errorf("the pipe broke")
}

func TestScanReportsAReadErrorAndWhatItHadRead(t *testing.T) {
	hits, n, err := scan("pipe", &errReader{prefix: "skipped one\nquiet\n"}, declineWords)
	if err == nil {
		t.Fatal("a broken read was reported as a complete file")
	}
	if !strings.Contains(err.Error(), "the pipe broke") || !strings.Contains(err.Error(), "pipe:") {
		t.Fatalf("error %q names neither the file nor the cause", err)
	}
	// ⛔ What it DID read is returned with the error: a partial read must not
	// come back as nothing, or the caller cannot tell it from a clean file.
	if n != 2 {
		t.Fatalf("read %d lines before the error, want 2", n)
	}
	if len(hits) != 1 {
		t.Fatalf("got %d declining lines before the error, want 1", len(hits))
	}
}

func TestScanFileMissing(t *testing.T) {
	_, _, err := scanFile(filepath.Join(t.TempDir(), "absent"), declineWords)
	if !os.IsNotExist(err) {
		t.Fatalf("err = %v, want a not-exist error so the hook can skip it", err)
	}
}

// --- redirect parsing ---

func TestRedirectTargets(t *testing.T) {
	cases := []struct {
		command string
		want    []string
	}{
		{"go vet ./... > vet.log 2>&1", []string{"vet.log"}},
		{"go vet ./... >vet.log", []string{"vet.log"}},
		{"cmd >> a.log", []string{"a.log"}},
		{"cmd 2> err.log", []string{"err.log"}},
		{"cmd 2>>err.log", []string{"err.log"}},
		{"cmd &> both.log", []string{"both.log"}},
		{"cmd >| clobber.log", []string{"clobber.log"}},
		{"cmd 1> out.log 2> err.log", []string{"out.log", "err.log"}},
		{"cmd > 'a file.log'", []string{"a file.log"}},
		{`cmd > "a file.log"`, []string{"a file.log"}},
		{"cmd > a.log; other > b.log", []string{"a.log", "b.log"}},
		{"cmd > a.log && other >> a.log", []string{"a.log"}},
		{"cmd < in.txt", nil},
		{"cmd 2>&1", nil},
		{"cmd >&2", nil},
		{"echo hello", nil},
		{"", nil},
		{"cmd >", nil},
		{"cmd > ; other", nil},
		{"cmd 'a > b'", nil},
		{`cmd "quoted > here"`, nil},
		{"cmd > a.log | tee b", []string{"a.log"}},
		// ⛔ Fuzz finding: `>>>>` once yielded the file name ">>". Now the
		// operator characters are syntax and the word after them is the file.
		{"cmd >>>> x", []string{"x"}},
		{"> > >", nil},
		{"cmd >>>x", []string{"x"}},
		{`cmd > ">x"`, []string{">x"}},
	}
	for _, c := range cases {
		got := redirectTargets(c.command)
		if fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("redirectTargets(%q) = %v, want %v", c.command, got, c.want)
		}
	}
}

func TestRedirectTargetsIsBounded(t *testing.T) {
	var b strings.Builder
	for i := 0; i < maxTargets*3; i++ {
		fmt.Fprintf(&b, "cmd > f%d.log; ", i)
	}
	got := redirectTargets(b.String())
	if len(got) > maxTargets {
		t.Fatalf("got %d targets, more than the bound %d", len(got), maxTargets)
	}
	if len(got) != maxTargets {
		t.Fatalf("got %d targets, want the bound %d", len(got), maxTargets)
	}
}

func TestRedirectTargetsTruncatesAnEnormousCommand(t *testing.T) {
	old := maxCommandBytes
	maxCommandBytes = 10
	defer func() { maxCommandBytes = old }()
	if got := redirectTargets(strings.Repeat("x", 64) + " > late.log"); len(got) != 0 {
		t.Fatalf("read past the command bound: %v", got)
	}
}

func TestUnresolved(t *testing.T) {
	for _, s := range []string{"$OUT/x.log", "~/x.log", "*.log", "a?.log", "`x`.log"} {
		if !unresolved(s) {
			t.Errorf("%q read as a plain file name", s)
		}
	}
	for _, s := range []string{"x.log", "dir/x.log", "/abs/x.log", "a-b_c.1.log"} {
		if unresolved(s) {
			t.Errorf("%q read as unexpanded shell syntax", s)
		}
	}
}

// --- hook mode ---

type hookResult struct {
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

// runHookIO feeds a payload to --hook and returns stdout, stderr and the
// status. ⛔ The status must be 0 whatever the payload is: a PostToolUse hook
// that exits non-zero after every Bash call is a session nobody can work in.
func runHookIO(t *testing.T, in string) (string, string, int) {
	t.Helper()
	old := stdin
	stdin = strings.NewReader(in)
	defer func() { stdin = old }()
	var out, errOut bytes.Buffer
	code := cli.Drive(RootCmd(), []string{"--hook"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("--hook exited %d\nstdout: %s\nstderr: %s", code, out.String(), errOut.String())
	}
	return out.String(), errOut.String(), code
}

// runHook feeds a payload to --hook and returns stdout.
func runHook(t *testing.T, in string) string {
	t.Helper()
	out, _, _ := runHookIO(t, in)
	return out
}

// mkPayload builds a PostToolUse payload for a Bash command.
func mkPayload(t *testing.T, cwd, command string, resp any) string {
	t.Helper()
	p := map[string]any{
		"hook_event_name": "PostToolUse",
		"tool_name":       "Bash",
		"cwd":             cwd,
		"tool_input":      map[string]any{"command": command},
	}
	if resp != nil {
		p["tool_response"] = resp
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestHookSurfacesTheRefusalInARedirectedLog(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "vet.log"), []byte(theRealLog), 0o600); err != nil {
		t.Fatal(err)
	}
	out := runHook(t, mkPayload(t, dir, "buildscan --dir . --vet > vet.log 2>&1", map[string]any{"stdout": ""}))
	var r hookResult
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("output is not a hook envelope: %v\n%s", err, out)
	}
	if r.HookSpecificOutput.HookEventName != "PostToolUse" {
		t.Fatalf("hookEventName = %q", r.HookSpecificOutput.HookEventName)
	}
	ctx := r.HookSpecificOutput.AdditionalContext
	if !strings.Contains(ctx, "no record") {
		t.Fatalf("the refusal is not in the context:\n%s", ctx)
	}
	if !strings.Contains(ctx, "vet.log:2:") {
		t.Fatalf("the context does not name file and line:\n%s", ctx)
	}
}

func TestHookIsSilentOnACleanLog(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ok.log"), []byte("PASS\nok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out := runHook(t, mkPayload(t, dir, "go test ./... > ok.log", nil)); out != "" {
		t.Fatalf("not silent on a clean log:\n%s", out)
	}
}

func TestHookSaysAnEmptyFileIsNotCleanRun(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "empty.log"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	out := runHook(t, mkPayload(t, dir, "tool > empty.log", nil))
	if !strings.Contains(out, "EMPTY") {
		t.Fatalf("an empty log passed as a clean run:\n%s", out)
	}
}

func TestHookSaysItCouldNotReadAnUnreadableTarget(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "adir")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	out := runHook(t, mkPayload(t, dir, "tool > adir", nil))
	if !strings.Contains(out, "NOT read") || !strings.Contains(out, "not counted as clean") {
		t.Fatalf("an unreadable target read as clean:\n%s", out)
	}
}

func TestHookSaysItDidNotExpandAShellName(t *testing.T) {
	out := runHook(t, mkPayload(t, t.TempDir(), "tool > $OUT/x.log", nil))
	if !strings.Contains(out, "NOT read") {
		t.Fatalf("an unexpanded name read as clean:\n%s", out)
	}
}

func TestHookSkipsATargetThatDoesNotExist(t *testing.T) {
	if out := runHook(t, mkPayload(t, t.TempDir(), "tool > absent.log", nil)); out != "" {
		t.Fatalf("reported on a file the command never created:\n%s", out)
	}
}

func TestHookIsSilentWhenTheCommandFailed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "vet.log"), []byte(theRealLog), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, resp := range []map[string]any{
		{"is_error": true},
		{"interrupted": true},
		{"error": "exit status 1"},
	} {
		out := runHook(t, mkPayload(t, dir, "tool > vet.log", resp))
		if out != "" {
			t.Fatalf("reported on a failed command (%v):\n%s", resp, out)
		}
	}
}

// ⛔ The other direction of the same doctrine: a tool_response this cannot
// parse must NOT buy silence.
func TestHookReportsWhenItCannotReadTheResponse(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "vet.log"), []byte(theRealLog), 0o600); err != nil {
		t.Fatal(err)
	}
	body := `{"hook_event_name":"PostToolUse","tool_name":"Bash","cwd":` +
		fmt.Sprintf("%q", dir) +
		`,"tool_input":{"command":"tool > vet.log"},"tool_response":"a string, not an object"}`
	if out := runHook(t, body); !strings.Contains(out, "no record") {
		t.Fatalf("an unparsable response bought silence:\n%s", out)
	}
}

// ⛔ The log must EXIST and hold a decline, or the test passes for the wrong
// reason — a missing file is silent whatever the tool name is.
func TestHookIgnoresOtherTools(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "y.log"), []byte("no record\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	body := `{"hook_event_name":"PostToolUse","tool_name":"Write","cwd":` + fmt.Sprintf("%q", dir) +
		`,"tool_input":{"command":"x > y.log"}}`
	if out := runHook(t, body); out != "" {
		t.Fatalf("reported on a non-Bash tool:\n%s", out)
	}
}

// A payload that names no tool is read anyway: the hook is wired to Bash, and
// refusing to work because a field is absent is the silence this tool refuses.
func TestHookReadsAPayloadWithNoToolName(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "y.log"), []byte("no record\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	body := `{"hook_event_name":"PostToolUse","cwd":` + fmt.Sprintf("%q", dir) +
		`,"tool_input":{"command":"x > y.log"}}`
	if out := runHook(t, body); !strings.Contains(out, "no record") {
		t.Fatalf("a payload with no tool_name was passed over:\n%s", out)
	}
}

// ⛔ Truncated JSON keeps the fields it had already decoded. A half-read
// tool_response saying is_error must NOT buy silence.
func TestHookReportsWhenTheResponseIsTruncated(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "vet.log"), []byte(theRealLog), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := failed(json.RawMessage(`{"is_error":true`)); got != false {
		t.Fatal("a truncated tool_response was read as a definite failure")
	}
	body := `{"hook_event_name":"PostToolUse","tool_name":"Bash","cwd":` + fmt.Sprintf("%q", dir) +
		`,"tool_input":{"command":"tool > vet.log"},"tool_response":{"is_error":true` + "\n"
	// The whole payload is truncated too, so the hook must say it could not
	// read it rather than fall quiet.
	_, errOut, _ := runHookIO(t, body)
	if !strings.Contains(errOut, "not counted as clean") {
		t.Fatalf("a truncated payload fell silent; stderr:\n%s", errOut)
	}
}

func TestHookSaysSoWhenItCannotReadStdin(t *testing.T) {
	old := stdin
	stdin = &errReader{prefix: ""}
	defer func() { stdin = old }()
	var out, errOut bytes.Buffer
	if code := cli.Drive(RootCmd(), []string{"--hook"}, &out, &errOut); code != 0 {
		t.Fatalf("--hook exited %d", code)
	}
	// ⛔ The CAUSE, not just the heading: asserting only "could not read the
	// payload" passed with the read error ignored, because the empty buffer
	// then failed to unmarshal and produced the same heading with the wrong
	// reason. A message that is right for the wrong reason is not a test.
	if !strings.Contains(errOut.String(), "the pipe broke") {
		t.Fatalf("stderr does not name the read error; stderr:\n%s", errOut.String())
	}
	if !strings.Contains(errOut.String(), "could not read the payload") {
		t.Fatalf("an unreadable stdin produced no word; stderr:\n%s", errOut.String())
	}
	if out.String() != "" {
		t.Fatalf("stdout should stay empty:\n%s", out.String())
	}
}

func TestHookIgnoresACommandWithNoRedirect(t *testing.T) {
	if out := runHook(t, mkPayload(t, t.TempDir(), "go test ./...", nil)); out != "" {
		t.Fatalf("reported with no redirect target:\n%s", out)
	}
}

func TestHookSurvivesGarbage(t *testing.T) {
	for _, body := range []string{"", "not json", "{", "[]", "null"} {
		if out := runHook(t, body); out != "" {
			t.Fatalf("payload %q produced output:\n%s", body, out)
		}
	}
}

func TestHookRefusesAnEnormousPayloadAndNamesTheBound(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "vet.log"), []byte(theRealLog), 0o600); err != nil {
		t.Fatal(err)
	}
	old := maxPayloadBytes
	maxPayloadBytes = 16
	defer func() { maxPayloadBytes = old }()
	out, errOut, _ := runHookIO(t, mkPayload(t, dir, "tool > vet.log", nil))
	if out != "" {
		t.Fatalf("read past the payload bound:\n%s", out)
	}
	// ⛔ And it SAYS it did not read it: a bound that silently drops the
	// payload is the very shape of defect this tool reports on.
	if !strings.Contains(errOut, "longer than the 16 bytes") {
		t.Fatalf("the bound was enforced in silence; stderr:\n%s", errOut)
	}
}

func TestHookBoundsWhatItAddsToTheContext(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	for i := 0; i < maxContextLines*2; i++ {
		fmt.Fprintf(&b, "step %d skipped\n", i)
	}
	if err := os.WriteFile(filepath.Join(dir, "many.log"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	out := runHook(t, mkPayload(t, dir, "tool > many.log", nil))
	var r hookResult
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatal(err)
	}
	ctx := r.HookSpecificOutput.AdditionalContext
	if !strings.Contains(ctx, "more declining line(s)") {
		t.Fatalf("the context does not say it was cut short:\n%s", ctx)
	}
	if n := strings.Count(ctx, "skipped"); n != maxContextLines {
		t.Fatalf("context holds %d declining lines, want the bound %d", n, maxContextLines)
	}
}

func TestHookReadsAnAbsoluteTargetWithoutCwd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "abs.log")
	if err := os.WriteFile(path, []byte("no record here\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	body := `{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"tool > ` + path + `"}}`
	if out := runHook(t, body); !strings.Contains(out, "no record") {
		t.Fatalf("an absolute target was not read:\n%s", out)
	}
}

func TestFailed(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{``, false},
		{`{}`, false},
		{`{"stdout":"x"}`, false},
		{`{"is_error":true}`, true},
		{`{"interrupted":true}`, true},
		{`{"error":"boom"}`, true},
		{`{"error":"   "}`, false},
		{`"a string"`, false},
		{`{"is_error":false,"error":""}`, false},
		// ⛔ A TYPE error does not stop the decode: IsError IS set here, and
		// the answer must still be "I could not read this", not "it failed".
		{`{"interrupted":"not a bool","is_error":true}`, false},
		{`{"is_error":true`, false},
	}
	for _, c := range cases {
		if got := failed(json.RawMessage(c.raw)); got != c.want {
			t.Errorf("failed(%s) = %v, want %v", c.raw, got, c.want)
		}
	}
}

// --- file mode ---

// runFiles drives the command and returns its status, through the same
// cli.Drive the fleet's other commands are tested with.
func runFiles(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := cli.Drive(RootCmd(), args, &out, &errOut)
	return out.String(), errOut.String(), code
}

func TestFileModeFindsDeclines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vet.log")
	if err := os.WriteFile(path, []byte(theRealLog), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, code := runFiles(t, path)
	if code != exitDeclines {
		t.Fatalf("exit %d, want %d", code, exitDeclines)
	}
	if !strings.Contains(out, "no record") {
		t.Fatalf("output lacks the refusal:\n%s", out)
	}
}

func TestFileModeCleanIsZero(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ok.log")
	if err := os.WriteFile(path, []byte("PASS\nok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, code := runFiles(t, path)
	if code != exitNone {
		t.Fatalf("exit %d, want %d", code, exitNone)
	}
	if !strings.Contains(out, "no declining line in 2 line(s)") {
		t.Fatalf("output does not say how much it read:\n%s", out)
	}
}

// ⛔ An empty file is not a clean run. Exit 2 exists for exactly this, because
// 0 would say "nothing declined" about a file nothing was read from.
func TestFileModeEmptyFileIsExitTwo(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.log")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, code := runFiles(t, path)
	if code != exitNothing {
		t.Fatalf("exit %d, want %d for a file with nothing in it", code, exitNothing)
	}
	if !strings.Contains(out, "NOTHING READ") {
		t.Fatalf("output does not say nothing was read:\n%s", out)
	}
}

func TestFileModeUnreadableFileIsExitTwoAndSaysSo(t *testing.T) {
	out, errOut, code := runFiles(t, filepath.Join(t.TempDir(), "absent.log"))
	if code != exitNothing {
		t.Fatalf("exit %d, want %d", code, exitNothing)
	}
	if !strings.Contains(errOut, "NOT read, so not counted as clean") {
		t.Fatalf("stderr does not refuse to count it clean:\n%s", errOut)
	}
	if !strings.Contains(out, "NOTHING READ") {
		t.Fatalf("stdout does not say nothing was read:\n%s", out)
	}
}

// ⛔ Naming no file is "nothing was read", not "nothing declined".
func TestFileModeNoArgumentsIsNothingRead(t *testing.T) {
	_, errOut, code := runFiles(t)
	if code != exitNothing {
		t.Fatalf("exit %d, want %d", code, exitNothing)
	}
	if !strings.Contains(errOut, "not a clean run") {
		t.Fatalf("stderr does not say why: %q", errOut)
	}
}

func TestFileModeReadsSeveralFilesAndOneUnreadable(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "a.log")
	if err := os.WriteFile(good, []byte("quiet\nskipped it\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, errOut, code := runFiles(t, good, filepath.Join(dir, "absent.log"))
	if code != exitDeclines {
		t.Fatalf("exit %d, want %d", code, exitDeclines)
	}
	if !strings.Contains(out, "skipped it") {
		t.Fatalf("the decline is missing:\n%s", out)
	}
	if !strings.Contains(errOut, "NOT read") {
		t.Fatalf("the unreadable file was passed over in silence:\n%s", errOut)
	}
}

func TestRootCmdHasTheHookFlagAndSilencesUsage(t *testing.T) {
	c := RootCmd()
	if c.Flags().Lookup("hook") == nil {
		t.Fatal("--hook is missing")
	}
	if !c.SilenceUsage {
		t.Fatal("SilenceUsage is false, so an error prints the whole usage")
	}
	// ⛔ A phrase that does not span the help text's line wrap: the first
	// spelling of this assertion looked for "exited without an error", which
	// is broken across two lines, so it failed on text that says exactly that.
	if !strings.Contains(c.Long, "without an error while its own output says it declined") {
		t.Fatal("the help text does not state the narrowing rule")
	}
}

// ⛔ The staleness note is a `gh api` call, and this hook runs after EVERY Bash
// call in about twenty-five sessions. Under --hook it must never be made: the
// cost is paid thousands of times an hour, out of a REST budget the whole
// machine shares.
func TestHookModeNeverSpendsAnApiCallOnTheStalenessNote(t *testing.T) {
	old := stdin
	stdin = strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"true"}}`)
	defer func() { stdin = old }()
	warned := 0
	var out, errOut bytes.Buffer
	if !dispatch([]string{"--hook"}, &out, &errOut, func(io.Writer) { warned++ }) {
		t.Fatal("dispatch did not take --hook")
	}
	if warned != 0 {
		t.Fatalf("the staleness note was asked for %d time(s) under --hook", warned)
	}
	// And outside hook mode it IS asked for, or every binary stops saying it
	// is behind.
	if dispatch([]string{"some.log"}, &out, &errOut, func(io.Writer) { warned++ }) {
		t.Fatal("dispatch claimed a plain run as a hook")
	}
	if warned != 1 {
		t.Fatalf("the staleness note was asked for %d time(s) outside --hook, want 1", warned)
	}
}

// Every spelling pflag accepts for a boolean, because a hand-written list of
// three covered four cases out of seven in the tool this shape came from.
func TestHookModeReadsEverySpellingPflagDoes(t *testing.T) {
	for _, a := range []string{"--hook", "-hook", "--hook=true", "--hook=1", "--hook=t", "--hook=TRUE", "--hook=yes"} {
		if !hookMode([]string{a}) {
			t.Errorf("%q was not read as hook mode", a)
		}
	}
	for _, a := range []string{"--hook=false", "--hook=0", "--hook=f", "--hook=FALSE"} {
		if hookMode([]string{a}) {
			t.Errorf("%q was read as hook mode", a)
		}
	}
	for _, a := range []string{"file.log", "--hooky", "", "-h"} {
		if hookMode([]string{a}) {
			t.Errorf("%q was read as hook mode", a)
		}
	}
}
