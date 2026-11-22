package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-fleettools/fleettools/internal/cli"
)

// A walk up from a directory with no .claude/agents anywhere stops at the root
// instead of looping, and still offers the user's directory.
func TestTheWalkUpStopsAtTheFilesystemRoot(t *testing.T) {
	old := homeDir
	t.Cleanup(func() { homeDir = old })
	home := t.TempDir()
	homeDir = func() (string, error) { return home, nil }
	if _, err := os.Stat("/.claude/agents"); err == nil {
		t.Skip("this machine has /.claude/agents")
	}
	got := agentDirs("/")
	want := []string{filepath.Join(home, ".claude", "agents")}
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("agentDirs(/) = %v, want %v", got, want)
	}
}

// A directory entry that is not a regular file is "unreadable", and a type named
// after it is reported as such rather than skipped silently.
func TestReadBoundedRefusesWhatIsNotARegularFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := readBounded(filepath.Join(dir, "absent.md")); err == nil || !strings.Contains(err.Error(), "symlinks are not followed") {
		t.Errorf("a missing file gave %v", err)
	}
	if _, err := readBounded(dir); err == nil {
		t.Error("a directory was read as an agent file")
	}
}

// A listed agent directory that cannot be read is skipped and the next one is
// tried; with none readable the type has no model, and the reason says so.
func TestAnUnreadableAgentDirectoryIsSkipped(t *testing.T) {
	old := homeDir
	t.Cleanup(func() { homeDir = old })
	home := t.TempDir()
	homeDir = func() (string, error) { return home, nil }
	// The user's agents directory does not exist: ReadDir fails on it.
	if m, reason := agentModel("", "ghost"); m != "" || reason == "" {
		t.Errorf("agentModel = %q, %q; want no model and a reason", m, reason)
	}
}

// main wires the staleness note and the command to the process; driven here
// through os.Args and the exit seam so its own two lines are held by a test.
func TestMainRunsTheCommandAndExitsWithItsStatus(t *testing.T) {
	oldArgs, oldExit := os.Args, cli.OsExit
	t.Cleanup(func() { os.Args, cli.OsExit = oldArgs, oldExit })
	exited := -1
	cli.OsExit = func(code int) { exited = code }
	os.Args = []string{"modelgate", "--explain", "zzzz-no-such-word"}
	main()
	if exited != 1 {
		t.Errorf("an unclassified description exited %d, want 1", exited)
	}
}

// A hook whose stdin cannot be read allows and says WHY it could not read, not
// that it could not decode: the weaker "allowing" check in
// TestUnreadableStdinAllows let the read error be mistaken for an empty payload.
func TestAnUnreadableStdinAllowsAndSaysSo(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := hookMode(&out, &errOut, failingReader{}); code != 0 || out.Len() != 0 ||
		!strings.Contains(errOut.String(), "could not read the hook payload; allowing") {
		t.Errorf("status %d, stdout %q, stderr %q", code, out.String(), errOut.String())
	}
}

// A file that exists but cannot be opened is an error, not an empty definition.
func TestReadBoundedReportsAFileItCannotOpen(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root opens everything")
	}
	path := filepath.Join(t.TempDir(), "locked.md")
	if err := os.WriteFile(path, []byte("---\nmodel: opus\n---\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	if b, err := readBounded(path); err == nil || b != nil {
		t.Errorf("readBounded = %q, %v; want the open error", b, err)
	}
}

// --explain on a description in no class says so and exits 1; the class line is
// for a match only.
func TestExplainOfAnUnclassifiedDescription(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{"--explain", "zzzz-no-such-word"}, &out, &errOut)
	if code != 1 || !strings.HasPrefix(out.String(), "unclassified: no word of") || strings.Contains(out.String(), "class ") {
		t.Errorf("status %d, stdout %q", code, out.String())
	}
}
