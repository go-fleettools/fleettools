package fleet

import (
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
	"time"
)

// The default Backoff grows with the attempt; a test that replaces it never
// sees the real schedule, so a typo in the multiplier would have no witness.
func TestTheDefaultBackoffGrowsWithTheAttempt(t *testing.T) {
	for attempt, want := range []time.Duration{20 * time.Second, 40 * time.Second, 60 * time.Second} {
		if got := defaultBackoff(attempt); got != want {
			t.Errorf("defaultBackoff(%d) = %v, want %v", attempt, got, want)
		}
	}
}

// A binary with no build info at all reports an empty vintage, which warnStale
// then words as "cannot tell" rather than guessing.
func TestBuildVintageWithoutBuildInfoIsEmpty(t *testing.T) {
	old := readBuildInfo
	t.Cleanup(func() { readBuildInfo = old })
	readBuildInfo = func() (*debug.BuildInfo, bool) { return nil, false }
	if v := BuildVintage(); v != (Vintage{}) {
		t.Errorf("BuildVintage() = %+v, want empty", v)
	}
}

// fakeGH puts a `gh` first on PATH that prints its arguments on stdout and a
// line on stderr, and exits with the status given. It is the only way to reach
// the default exec seam without a network.
func fakeGH(t *testing.T, status string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\necho \"args: $*\"\necho \"diagnostic\" >&2\nexit " + status + "\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// The default seam returns stdout and stderr apart, and the error when gh fails.
func TestTheDefaultExecSeamKeepsStdoutAndStderrApart(t *testing.T) {
	fakeGH(t, "0")
	out, msg, err := run([]string{"api", "x"})
	if err != nil || strings.TrimSpace(string(out)) != "args: api x" || strings.TrimSpace(msg) != "diagnostic" {
		t.Fatalf("run = %q, %q, %v", out, msg, err)
	}
	fakeGH(t, "3")
	if _, msg, err := run([]string{"api", "x"}); err == nil || strings.TrimSpace(msg) != "diagnostic" {
		t.Fatalf("a failing gh gave msg %q, err %v", msg, err)
	}
}

// Orgs hands back the failure of the listing rather than an empty, clean list.
func TestOrgsReportsAFailedListing(t *testing.T) {
	fake(t, []string{"gh: not logged in", "gh: not logged in", "gh: not logged in", "gh: not logged in", "gh: not logged in", "gh: not logged in"}, "")
	got, err := Orgs()
	if err == nil || got != nil {
		t.Fatalf("Orgs() = %v, %v; want the error", got, err)
	}
}

// A go test binary carries build info, but never a VCS stamp or a module
// version, so BuildVintage can only answer "no revision" here.
func TestBuildVintageReadsTheRunningBinary(t *testing.T) {
	v := BuildVintage()
	if v.Revision != "" || v.Module != "" {
		t.Errorf("a test binary reported %+v", v)
	}
}

// WarnIfStale goes through the real stamps and the real compare; with no gh
// able to answer, it says it could not tell rather than nothing.
func TestWarnIfStaleSaysWhenItCannotTell(t *testing.T) {
	var b strings.Builder
	WarnIfStale(&b)
	if !strings.HasPrefix(b.String(), "note: this binary carries no VCS stamp") {
		t.Errorf("WarnIfStale wrote %q", b.String())
	}
}

// compareWithHead: the three outcomes of the real function around the seam.
func TestCompareWithHeadOutcomes(t *testing.T) {
	old := run
	t.Cleanup(func() { run = old })

	run = func([]string) ([]byte, string, error) { return nil, "boom\nsecond", os.ErrNotExist }
	if _, err := compareWithHead("abc"); err == nil || err.Error() != "boom" {
		t.Errorf("a failed call gave %v, want its first line", err)
	}
	run = func([]string) ([]byte, string, error) { return []byte("{not json"), "", nil }
	if _, err := compareWithHead("abc"); err == nil {
		t.Error("an unparseable answer was accepted")
	}
	run = func([]string) ([]byte, string, error) {
		return []byte(`{"status":"ahead","ahead_by":2,"behind_by":0,"files":[{"filename":"a.go"}]}`), "", nil
	}
	c, err := compareWithHead("abc")
	if err != nil || c.Status != "ahead" || c.AheadBy != 2 {
		t.Errorf("a good answer gave %+v, %v", c, err)
	}
}

// firstLine of an empty message says so instead of printing an empty note.
func TestFirstLine(t *testing.T) {
	for in, want := range map[string]string{"": "no error message", "one": "one", "a\nb": "a", "\nb": ""} {
		if got := firstLine(in); got != want {
			t.Errorf("firstLine(%q) = %q, want %q", in, got, want)
		}
	}
}
