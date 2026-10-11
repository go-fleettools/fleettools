package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"
)

// now is fixed, so an age assertion is about the fixture rather than about when
// the suite runs.
var now = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

// stream builds a govulncheck stream out of its parts, so each test can leave
// exactly one of them out.
func stream(parts ...string) string { return strings.Join(parts, "\n") + "\n" }

func config(stamp, goVersion string) string {
	return fmt.Sprintf(`{"config":{"go_version":%q,"scanner_version":"v1.9.0","db_last_modified":%q}}`, goVersion, stamp)
}

const (
	sbom  = `{"SBOM":{"go_version":"go1.27.2"}}`
	osv1  = `{"osv":{"id":"GO-2026-0001"}}`
	fresh = "2026-10-10T22:31:09Z" // a day before now
)

func called(id, fixed string) string {
	return fmt.Sprintf(`{"finding":{"osv":%q,"fixed_version":%q,"trace":[{"module":"golang.org/x/net","function":"ReadFrame"}]}}`, id, fixed)
}

func requiredOnly(id, fixed string) string {
	return fmt.Sprintf(`{"finding":{"osv":%q,"fixed_version":%q,"trace":[{"module":"golang.org/x/net"}]}}`, id, fixed)
}

func judgeString(t *testing.T, s string, args ...any) (int, string) {
	t.Helper()
	maxAge := 21 * 24 * time.Hour
	wantGo := ""
	for _, a := range args {
		switch v := a.(type) {
		case time.Duration:
			maxAge = v
		case string:
			wantGo = v
		}
	}
	var out bytes.Buffer
	code := judge(strings.NewReader(s), &out, maxAge, wantGo, now)
	return code, out.String()
}

// The gate's whole reason for existing: govulncheck exits 0 over an advisory it
// has decided this module does not call, so the status is not the answer.
func TestAReleasedFixIsRefusedEvenThoughNothingCallsIt(t *testing.T) {
	code, out := judgeString(t, stream(config(fresh, "go1.27.2"), sbom, osv1, requiredOnly("GO-2026-0001", "v0.60.0")))
	if code != 1 {
		t.Errorf("exit %d, want 1 — a released fix is an upgrade, not a note", code)
	}
	if !strings.Contains(out, "RELEASED fix exists (v0.60.0)") {
		t.Errorf("the output does not name the fix: %s", out)
	}
}

// And the other way round, so the gate is not simply refusing everything: an
// advisory with no released fix is a notice and the lane stays green.
func TestAnUnfixedAdvisoryIsANoticeAndNotAFailure(t *testing.T) {
	code, out := judgeString(t, stream(config(fresh, "go1.27.2"), sbom, osv1, requiredOnly("GO-2026-0002", "")))
	if code != 0 {
		t.Errorf("exit %d, want 0 — there is nothing to upgrade to", code)
	}
	if !strings.Contains(out, "::notice::") || !strings.Contains(out, "none published") {
		t.Errorf("the output does not say what is missing: %s", out)
	}
}

func TestSomethingReachableIsRefusedAndNamed(t *testing.T) {
	code, out := judgeString(t, stream(config(fresh, "go1.27.2"), sbom, osv1, called("GO-2026-0003", "v0.60.0")))
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if !strings.Contains(out, "reachable from this module: golang.org/x/net.ReadFrame") {
		t.Errorf("the output does not name where it is reached: %s", out)
	}
}

// UNREAD IS NOT CLEAN. A scan that could not load the packages prints its
// config and no finding, which reads exactly like a clean repository.
//
// The SBOM is the evidence, because it is written only once the packages have
// loaded. The control comes first: the same stream WITH an SBOM passes, so the
// refusal is about the SBOM and not about the rest of the fixture.
func TestAScanThatNeverLoadedThePackagesIsNotClean(t *testing.T) {
	if code, out := judgeString(t, stream(config(fresh, "go1.27.2"), sbom, osv1)); code != 0 {
		t.Fatalf("the control exited %d, want 0: %s", code, out)
	}

	code, out := judgeString(t, stream(config(fresh, "go1.27.2"), osv1))
	if code != 1 {
		t.Errorf("exit %d, want 1 — a scan that did not run is not a clean one", code)
	}
	if !strings.Contains(out, "no SBOM") {
		t.Errorf("the output does not say what was missing: %s", out)
	}
}

// An empty advisory list is not a failed scan.
//
// An earlier version of this gate refused a stream with no `osv` message, as a
// proxy for "it did not run". That is a false failure for any module whose
// dependencies and toolchain genuinely have nothing open against them, and the
// SBOM already says what the proxy was trying to say.
func TestAScanWithNoAdvisoriesAtAllIsCleanRatherThanBroken(t *testing.T) {
	code, out := judgeString(t, stream(config(fresh, "go1.27.2"), sbom))
	if code != 0 {
		t.Errorf("exit %d, want 0 — nothing open against this module is an answer: %s", code, out)
	}
	if !strings.Contains(out, "advisories in it: 0") {
		t.Errorf("the count is not reported: %s", out)
	}
}

// A scan is only as fresh as its database, and nothing in the stream makes that
// visible unless somebody prints it.
func TestAStaleDatabaseIsRefusedAndAFreshOneIsReported(t *testing.T) {
	// The control: a database a day old passes and its age is on screen.
	code, out := judgeString(t, stream(config(fresh, "go1.27.2"), sbom))
	if code != 0 {
		t.Fatalf("the control exited %d: %s", code, out)
	}
	if !strings.Contains(out, "database 2026-10-10T22:31:09Z") || !strings.Contains(out, "old)") {
		t.Errorf("a fresh run does not report the database and its age: %s", out)
	}

	old := "2026-01-02T00:00:00Z"
	code, out = judgeString(t, stream(config(old, "go1.27.2"), sbom))
	if code != 1 {
		t.Errorf("exit %d, want 1 — nine months is a frozen mirror, not a quiet week", code)
	}
	if !strings.Contains(out, old) || !strings.Contains(out, "stale mirror") {
		t.Errorf("the refusal does not name the date: %s", out)
	}
}

// A stream that does not say when its database was modified gets an error and
// not an age of zero, which is the freshest answer there is.
func TestADatabaseWithNoTimestampIsRefusedRatherThanReadAsNew(t *testing.T) {
	for _, stamp := range []string{"", "yesterday", "2026-13-45"} {
		code, out := judgeString(t, stream(config(stamp, "go1.27.2"), sbom))
		if code != 1 {
			t.Errorf("a db_last_modified of %q exited %d, want 1", stamp, code)
		}
		if !strings.Contains(out, "frozen database") {
			t.Errorf("a db_last_modified of %q gave %q", stamp, out)
		}
	}
	// A timestamp in the future is not staleness, and not this gate's argument.
	if code, _ := judgeString(t, stream(config("2027-01-01T00:00:00Z", "go1.27.2"), sbom)); code != 0 {
		t.Errorf("a database stamped in the future exited %d, want 0", code)
	}
}

// The standard library is judged by one version: the toolchain the scan ran
// under. A lane that silently resolved another answered a different question.
func TestAScanUnderAnotherToolchainIsRefusedWhenOneWasNamed(t *testing.T) {
	s := stream(config(fresh, "go1.27.1"), sbom)
	if code, out := judgeString(t, s); code != 0 {
		t.Fatalf("the control exited %d without -go: %s", code, out)
	}
	code, out := judgeString(t, s, "go1.27.2")
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if !strings.Contains(out, "go1.27.1") || !strings.Contains(out, "go1.27.2") {
		t.Errorf("the refusal does not name both versions: %s", out)
	}
}

// One finding is reported once however many traces carry it.
func TestAFindingRepeatedAcrossTracesIsSaidOnce(t *testing.T) {
	_, out := judgeString(t, stream(config(fresh, "go1.27.2"), sbom, osv1,
		called("GO-2026-0004", "v0.60.0"), called("GO-2026-0004", "v0.60.0")))
	if n := strings.Count(out, "GO-2026-0004"); n != 1 {
		t.Errorf("said it %d times, want 1: %s", n, out)
	}
	if !strings.Contains(out, "findings judged: 1") {
		t.Errorf("the count is wrong: %s", out)
	}
}

// Rubbish on the input is a refusal, not a clean sheet.
func TestOutputThatDoesNotDecodeIsRefused(t *testing.T) {
	code, out := judgeString(t, "this is not json\n")
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if !strings.Contains(out, "cannot parse") {
		t.Errorf("the refusal does not say so: %s", out)
	}
}

// The age is printed on every run, so it is rendered for somebody reading a CI
// log rather than by Go's default, which gives "59h0m0s" for two and a half
// days -- a number the reader has to divide before it means anything.
func TestAnAgeIsRenderedForAReader(t *testing.T) {
	for _, c := range []struct {
		in   time.Duration
		want string
	}{
		{90 * time.Minute, "2h0m0s"},
		{47 * time.Hour, "47h0m0s"},
		{48 * time.Hour, "2d"},
		{59 * time.Hour, "2d11h"},
		{21 * 24 * time.Hour, "21d"},
		{9 * 30 * 24 * time.Hour, "270d"},
	} {
		if got := round(c.in); got != c.want {
			t.Errorf("round(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}
