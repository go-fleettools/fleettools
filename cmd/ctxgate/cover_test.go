// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
)

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

// main wires the staleness note and the command to the process; driven here
// through os.Args and the exit seam so its own two lines are held by a test.
func TestMainRunsTheCommandAndExitsWithItsStatus(t *testing.T) {
	oldArgs, oldExit := os.Args, cli.OsExit
	t.Cleanup(func() { os.Args, cli.OsExit = oldArgs, oldExit })
	exited := -1
	cli.OsExit = func(code int) { exited = code }
	os.Args = []string{"ctxgate"}
	main()
	if exited != 2 {
		t.Errorf("nothing to do exited %d, want 2", exited)
	}
}

// A hook whose stdin cannot be read allows and says WHY: it could not read, not
// that it could not decode.
func TestAnUnreadableStdinAllowsAndSaysSo(t *testing.T) {
	var out, errOut bytes.Buffer
	code := hookMode(&out, &errOut, failingReader{}, 200000)
	if code != 0 || out.Len() != 0 ||
		errOut.String() != "ctxgate: could not read the hook payload; allowing (a refusal is not a verdict)\n" {
		t.Errorf("status %d, stdout %q, stderr %q", code, out.String(), errOut.String())
	}
}

// A target that is not an id is never turned into a path: "a/../b" would
// clean to subagents/b.jsonl, a file outside the agent-<id>.jsonl naming.
func TestATargetWithPathSyntaxIsNotAnIDAndIsAllowed(t *testing.T) {
	tr, subs := session(t)
	writeLines(t, filepath.Join(subs, "b.jsonl"), asst("m", "x", 1, 900_000, 0, "2026-11-20T09:00:00Z"))
	out, errOut := hookRun(t, payload("SendMessage", "a/../b", tr, ""))
	if out != "" || errOut != "" {
		t.Errorf("a path-shaped target was resolved: stdout %q stderr %q", out, errOut)
	}
}

// An agent this session has no transcript for is allowed in silence: reading it
// would otherwise print a "could not read" note.
func TestAnUnknownAgentIsAllowedWithoutANote(t *testing.T) {
	tr, _ := session(t)
	out, errOut := hookRun(t, payload("SendMessage", "nosuch", tr, ""))
	if out != "" || errOut != "" {
		t.Errorf("stdout %q stderr %q, want both empty", out, errOut)
	}
}

// A payload that is not JSON is allowed and the reason is the decode error.
func TestAnUndecodablePayloadAllowsAndSaysSo(t *testing.T) {
	out, errOut := hookRun(t, "{{{")
	if out != "" || !strings.HasPrefix(errOut, "ctxgate: could not decode the hook payload (") ||
		!strings.HasSuffix(errOut, "); allowing (a refusal is not a verdict)\n") {
		t.Errorf("stdout %q stderr %q", out, errOut)
	}
}

// A root that is absent or is a file is an error, not an empty census.
func TestScanRefusesARootThatIsNotADirectory(t *testing.T) {
	d := t.TempDir()
	file := filepath.Join(d, "x.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, root := range map[string]string{"absent": filepath.Join(d, "gone"), "a file": file} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s: panicked: %v", name, r)
				}
			}()
			c, err := scan(root, time.Now())
			if err == nil || c != nil || err.Error() != root+" is not a readable directory" {
				t.Errorf("%s: scan = %v, %v", name, c, err)
			}
		}()
	}
}

// The command reports an unreadable projects directory as status 2 with the reason.
func TestReportOnAMissingProjectsDirIsStatus2(t *testing.T) {
	var out, errOut bytes.Buffer
	missing := filepath.Join(t.TempDir(), "gone")
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("panicked: %v", r)
		}
	}()
	code := run([]string{"report", "--projects", missing}, &out, &errOut)
	if code != 2 || !strings.Contains(errOut.String(), missing+" is not a readable directory") || out.Len() != 0 {
		t.Errorf("status %d, stdout %q, stderr %q", code, out.String(), errOut.String())
	}
}

// A user entry carrying usage and a line that is not JSON are rejected by their
// own guard (the panics a missing guard would cause must not be what says so).
func TestDecodeLineRejectionsAreNotPanics(t *testing.T) {
	for name, l := range map[string]string{
		"user with usage": `{"type":"user","message":{"usage":{"input_tokens":5}}}`,
		"not json":        `"usage" {{{`,
		"no usage object": `{"type":"assistant","message":{"usage":null}}`,
	} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s: panicked: %v", name, r)
				}
			}()
			if c, ok := decodeLine([]byte(l)); ok {
				t.Errorf("%s accepted: %+v", name, c)
			}
		}()
	}
}

// A read error that is not EOF is returned, not mistaken for "no usage": a
// directory opens and has a size, and reading it fails.
func TestLastContextReturnsAReadError(t *testing.T) {
	_, err := lastContext(t.TempDir(), 1<<20)
	if err == nil || errors.Is(err, errNoUsage) {
		t.Errorf("err = %v, want a read error", err)
	}
}

// human picks the unit by magnitude: each threshold is the first value of its unit.
func TestHumanPicksTheUnit(t *testing.T) {
	for _, c := range []struct {
		n    int64
		want string
	}{{999, "999"}, {1000, "1.0k"}, {999_999, "1000.0k"}, {1_000_000, "1.0M"}, {2_500_000, "2.5M"},
		{999_999_999, "1000.0M"}, {1_000_000_000, "1.00G"}, {3_250_000_000, "3.25G"}} {
		if got := human(c.n); got != c.want {
			t.Errorf("human(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

// A call with no model is grouped as "unknown", and the model groups are listed
// largest cache-read first, ties broken by name.
func TestRenderOrdersModelGroupsByCacheReadThenName(t *testing.T) {
	c := newCensus()
	for _, x := range []struct {
		name  string
		model string
		cr    int64
	}{{"main", "b-model", 1000}, {"main", "a-model", 1000}, {"main", "z-big", 5000}, {"main", "", 10}} {
		_ = x.name
		c.add("all", call{ctx: 10, cacheRead: x.cr})
		c.add("main", call{ctx: 10, cacheRead: x.cr})
		m := x.model
		if m == "" {
			m = "unknown"
		}
		c.add("main "+m, call{ctx: 10, cacheRead: x.cr})
	}
	var o bytes.Buffer
	c.render(&o, time.Hour)
	s := o.String()
	last := -1
	for _, g := range []string{"\nall ", "\nmain ", "\nmain z-big", "\nmain a-model", "\nmain b-model", "\nmain unknown"} {
		i := strings.Index(s, g)
		if i <= last {
			t.Fatalf("group %q out of order (at %d, previous %d):\n%s", g, i, last, s)
		}
		last = i
	}
}

// A transcript with no model field is counted under "<kind> unknown"; one older
// than the cutoff by modification time is never opened.
func TestScanGroupsAModellessCallAndSkipsStaleFiles(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	ts := now.Add(-time.Hour).Format(time.RFC3339)
	fresh := filepath.Join(root, "p", "fresh.jsonl")
	writeLines(t, fresh, asst("a", "", 1, 500, 0, ts))
	stale := filepath.Join(root, "p", "stale.jsonl")
	writeLines(t, stale, asst("b", "claude-opus-5-5", 1, 700, 0, ts))
	for p, mt := range map[string]time.Time{fresh: now, stale: now.Add(-30 * 24 * time.Hour)} {
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatal(err)
		}
	}
	c, err := scan(root, now.Add(-168*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if c.files != 1 {
		t.Errorf("files = %d, want 1 (the stale file must not be read)", c.files)
	}
	if g := c.groups["main unknown"]; g == nil || g.total().calls != 1 || g.total().cacheRead != 500 {
		t.Errorf("main unknown = %+v", g)
	}
	if c.groups["main claude-opus-5-5"] != nil {
		t.Error("the stale file was counted")
	}
}

// With no home directory the default root is empty, not a path under "".
func TestDefaultRootWithoutAHome(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("home", "")
	if _, err := os.UserHomeDir(); err == nil {
		if got := defaultRoot(); got == "" {
			t.Fatal("home resolved but root empty")
		}
		t.Skip("this platform resolves a home without environment")
	}
	if got := defaultRoot(); got != "" {
		t.Errorf("defaultRoot() = %q, want empty", got)
	}
}

// A window that starts mid-line and holds no newline has no whole line to
// offer: the answer is "no usage", not a decode of half a line.
func TestLastContextWindowInsideOneLongLine(t *testing.T) {
	p := filepath.Join(t.TempDir(), "long.jsonl")
	line := asst("m", "x", 1, 9, 0, "2026-11-20T09:00:00Z")
	if err := os.WriteFile(p, []byte(line), 0o644); err != nil { // no trailing newline
		t.Fatal(err)
	}
	if _, err := lastContext(p, 10); !errors.Is(err, errNoUsage) {
		t.Errorf("err = %v, want errNoUsage", err)
	}
	if n, err := lastContext(p, 1<<20); err != nil || n != 10 {
		t.Errorf("whole window: %d, %v; want 10", n, err)
	}
}
