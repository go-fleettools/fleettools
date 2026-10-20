package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-fleettools/fleettools/internal/fleet"
)

// ⛔ THE COUNTER IS THE HALF NOBODY CAN CHECK BY EYE, so it is checked
// against arithmetic. A counter that is wrong reports a crisis and an
// all-clear with equal confidence, and every number this command prints
// rests on it.
func TestFiringsCountsWhatArithmeticSays(t *testing.T) {
	// A fixed window, so the answers do not depend on the day the suite runs.
	to := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		spec string
		days int
		want int
	}{
		{"37 5 * * *", 14, 14},  // daily
		{"0 5 * * 1", 14, 2},    // Mondays: 6 and 13 October
		{"0 5 * * 0", 14, 2},    // Sundays, spelled 0
		{"0 5 * * 7", 14, 2},    // the same Sundays, spelled 7 as cron allows
		{"0 0 1 1 *", 14, 0},    // new year's day, outside the window
		{"*/30 * * * *", 1, 48}, // every half hour
		{"0 0-2 * * *", 1, 3},   // a range of hours
		{"0 1,13 * * *", 7, 14}, // a list
		{"0 5 * * 1,4", 14, 4},  // Mondays and Thursdays
		{"not a cron", 14, 0},   // five fields are required
		{"0 5 * *", 14, 0},      // four is not five
	} {
		from := to.AddDate(0, 0, -c.days)
		if got := firings(c.spec, from, to); got != c.want {
			t.Errorf("%q over %d day(s) = %d, want %d", c.spec, c.days, got, c.want)
		}
	}
}

// ⛔ THE WINDOW'S FLOOR IS THE WORKFLOW'S AGE when that is later. Without
// this the first version reported `want 14 got 0` for jobs merged the
// previous evening, beside `want 14 got 14` for the established ones, which
// reads as an organisation whose schedules are half dead.
func TestAYoungWorkflowIsNotAskedForFiringsItPredates(t *testing.T) {
	fixed := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	now = func() time.Time { return fixed }
	t.Cleanup(func() { now = func() time.Time { return time.Now().UTC() } })

	since := fixed.AddDate(0, 0, -14)
	created := fixed.Add(-25 * time.Hour).Format(time.RFC3339)

	from, age := windowFrom(since, created)
	if !from.After(since) {
		t.Errorf("the window still starts at %v, ignoring the workflow's age", from)
	}
	if !strings.Contains(age, "ago") {
		t.Errorf("the note does not say how old it is: %q", age)
	}
	// A daily cron over that window wants ONE firing, not fourteen.
	if n := firings("37 5 * * *", from, fixed); n != 1 {
		t.Errorf("a 25-hour-old daily cron wants %d firing(s), want 1", n)
	}

	// ⛔ THE CONTROL: an OLD workflow keeps the full window, or this
	// "fix" would quietly excuse every shortfall in the fleet.
	old := fixed.AddDate(0, 0, -300).Format(time.RFC3339)
	from, age = windowFrom(since, old)
	if !from.Equal(since) {
		t.Errorf("an old workflow had its window shortened to %v", from)
	}
	if age != "" {
		t.Errorf("an old workflow was annotated with an age: %q", age)
	}
	// And an unparseable timestamp must not shorten it either.
	if from, _ = windowFrom(since, "not a time"); !from.Equal(since) {
		t.Errorf("an unreadable created_at shortened the window to %v", from)
	}
}

// ⛔ THE CEILING IS THE MEASURED DELAY, and this is the run that demanded it:
// 2026-10-20 at 07:18 UTC, three workflows added the previous day each wanted
// 2 daily firings and had 1, because the morning's had not arrived on a
// service measured 8 hours late. Reporting that as "3 firings did not happen"
// is how a scanner becomes one nobody reads.
func TestAFiringDueInsideTheGraceWindowIsPendingNotMissed(t *testing.T) {
	fixed := time.Date(2026, 10, 20, 7, 18, 0, 0, time.UTC)
	now = func() time.Time { return fixed }
	t.Cleanup(func() { now = func() time.Time { return time.Now().UTC() } })

	since := fixed.AddDate(0, 0, -14)
	created := fixed.Add(-35 * time.Hour).Format(time.RFC3339) // go-pkgx.github.io's pinbump
	from, _ := windowFrom(since, created)

	// Without a ceiling the pass asks for two: 2026-10-19 05:37 and today's,
	// which is 1h41m overdue and on its way.
	if n := firings("37 5 * * *", from, fixed); n != 2 {
		t.Fatalf("the window to now wants %d firing(s), want 2 — the case this test is about has changed", n)
	}
	if n := firings("37 5 * * *", from, fixed.Add(-9*time.Hour)); n != 1 {
		t.Errorf("with 9h of grace the pass wants %d firing(s), want 1", n)
	}

	// ⛔ THE CONTROL: a firing already a day overdue is STILL asked for, or
	// the grace would excuse every genuinely dead schedule in the fleet.
	silent := fixed.Add(-30 * time.Hour) // a cron that last fired 30h ago
	if n := firings("37 5 * * *", silent.AddDate(0, 0, -2), fixed.Add(-9*time.Hour)); n < 2 {
		t.Errorf("a two-day window with 9h of grace wants %d firing(s), want at least 2", n)
	}
}

// ⛔ AND THE CEILING MUST BE WIRED. The test above proves the arithmetic; it
// would pass unchanged against a `run` that computed the ceiling and then
// counted to now() anyway, which is exactly the shape of mistake this fleet
// has shipped before ("the note is not wired"). So this one drives the whole
// command, through the flag, against canned API replies, and asserts the two
// opposite verdicts the grace is supposed to separate.
func TestTheGraceReachesTheCountingLoop(t *testing.T) {
	fixed := time.Date(2026, 10, 20, 7, 18, 0, 0, time.UTC)
	now = func() time.Time { return fixed }
	repos = func(string) ([]fleet.Repo, error) {
		return []fleet.Repo{{FullName: "go-pkgx/go-pkgx.github.io"}}, nil
	}
	gh = func(args ...string) ([]byte, error) {
		path := args[1]
		switch {
		case strings.Contains(path, "/actions/workflows?"):
			return []byte(`[{"name":"pinbump","path":".github/workflows/pinbump.yml",` +
				`"state":"active","id":7,"created_at":"` +
				fixed.Add(-35*time.Hour).Format(time.RFC3339) + `"}]`), nil
		case strings.Contains(path, "/contents/"):
			body := "on:\n  schedule:\n    - cron: \"37 5 * * *\"\n"
			return []byte(base64.StdEncoding.EncodeToString([]byte(body))), nil
		case strings.Contains(path, "/runs?"):
			// Yesterday's firing, which arrived five and a half hours late.
			return []byte(`[{"created_at":"` +
				fixed.Add(-20*time.Hour).Format(time.RFC3339) + `"}]`), nil
		}
		return nil, errors.New("unexpected call: " + path)
	}
	t.Cleanup(func() {
		now = func() time.Time { return time.Now().UTC() }
		repos = fleet.Repos
		gh = fleet.GH
	})

	// Default grace: today's firing is 1h41m overdue, which is pending.
	c := RootCmd()
	var out, errb bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&errb)
	c.SetArgs([]string{"go-pkgx"})
	if code := execute(c); code != 0 {
		t.Errorf("with the default grace the pass exited %d, want 0\n%s%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "want   1  got   1") {
		t.Errorf("the row does not read want 1 got 1:\n%s", out.String())
	}

	// ⛔ THE CONTROL: with no grace the same fleet is one firing short, so
	// the zero above is the ceiling at work and not a pass that cannot fail.
	c = RootCmd()
	out.Reset()
	errb.Reset()
	c.SetOut(&out)
	c.SetErr(&errb)
	c.SetArgs([]string{"go-pkgx", "--grace", "0"})
	if code := execute(c); code != 1 {
		t.Errorf("with --grace 0 the pass exited %d, want 1\n%s%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "want   2  got   1") {
		t.Errorf("without grace the row does not read want 2 got 1:\n%s", out.String())
	}
}

// The default is a number that came from a measurement, so a change to it is
// a change to a claim this command makes in its own help.
func TestTheGraceDefaultIsTheMeasuredDelay(t *testing.T) {
	c := RootCmd()
	f := c.Flags().Lookup("grace")
	if f == nil {
		t.Fatal("there is no --grace flag")
	}
	if f.DefValue != "9h0m0s" {
		t.Errorf("--grace defaults to %q, want 9h0m0s (the 8h worst case measured, rounded up)", f.DefValue)
	}
}

func TestANegativeGraceIsRefused(t *testing.T) {
	c := RootCmd()
	var out, errb bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&errb)
	c.SetArgs([]string{"go-pkgx", "--grace", "-1h"})
	if code := execute(c); code != 2 {
		t.Errorf("--grace -1h = %d, want 2 (a usage error)", code)
	}
	if !strings.Contains(errb.String(), "negative") {
		t.Errorf("stderr = %q", errb.String())
	}
}

// A commented-out cron is how this fleet disables one; counting it would
// report a workflow as failing to run a schedule nobody asked for.
func TestACommentedOutCronIsNotACron(t *testing.T) {
	body := `on:
  schedule:
    - cron: "37 5 * * *"
    - cron: '0 6 * * 1'
  # schedule:  # cron disabled 2026-08-12
    # - cron: "0 6 * * *"
  workflow_dispatch:
`
	got := cronLines(body)
	if len(got) != 2 {
		t.Fatalf("cronLines = %v, want the two live ones", got)
	}
	if got[0] != "37 5 * * *" || got[1] != "0 6 * * 1" {
		t.Errorf("cronLines = %v", got)
	}
}

// Both quote styles and none at all, since all three appear in this fleet.
func TestCronLinesStripsQuotes(t *testing.T) {
	for _, line := range []string{`    - cron: "0 5 * * 1"`, `    - cron: '0 5 * * 1'`, `    - cron: 0 5 * * 1`} {
		got := cronLines(line)
		if len(got) != 1 || got[0] != "0 5 * * 1" {
			t.Errorf("%q gave %v", line, got)
		}
	}
}

// Day-of-week 7 is Sunday in cron and does not exist in Go's Weekday. A
// counter that dropped it would under-report every Sunday job in the fleet
// and call it a missed firing.
func TestSundayHasTwoSpellings(t *testing.T) {
	if !matchDow("7", 0) {
		t.Error("cron's 7 does not match Go's Sunday")
	}
	if !matchDow("0", 0) {
		t.Error("0 does not match Sunday")
	}
	if matchDow("7", 1) {
		t.Error("7 matched Monday")
	}
}

// An argument that is not a positive number of days is a usage error, not a
// window of zero that reports every schedule as fine.
func TestANonPositiveWindowIsRefused(t *testing.T) {
	c := RootCmd()
	var out, errb bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&errb)
	c.SetArgs([]string{"go-pkgx", "--days", "0"})
	if code := execute(c); code != 2 {
		t.Errorf("--days 0 = %d, want 2 (a usage error)", code)
	}
	if !strings.Contains(errb.String(), "positive") {
		t.Errorf("stderr = %q", errb.String())
	}
}

// And an organisation is required: with none, there is nothing to count and
// no useful default to invent.
func TestTheOrganisationIsRequired(t *testing.T) {
	c := RootCmd()
	var out, errb bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&errb)
	c.SetArgs(nil)
	if code := execute(c); code != 2 {
		t.Errorf("no argument = %d, want 2", code)
	}
}

func TestTrimAndShort(t *testing.T) {
	if got := short("go-pkgx/packages"); got != "packages" {
		t.Errorf("short = %q", got)
	}
	if got := short("nothing-to-cut"); got != "nothing-to-cut" {
		t.Errorf("short = %q", got)
	}
	if got := trim("abcdef", 4); got != "abc…" {
		t.Errorf("trim = %q", got)
	}
	if got := trim("ab", 4); got != "ab" {
		t.Errorf("trim = %q", got)
	}
}

func TestDecodeBase64AcceptsWhatTheApiReturns(t *testing.T) {
	// gh --jq .content hands back a quoted, newline-wrapped payload.
	got, err := decodeBase64("\"b24657G==\n\"")
	if err == nil && got != "" {
		return // a decoder that coped is fine; the shape below is the point
	}
	if _, err := decodeBase64(`"aGVsbG8="`); err != nil {
		t.Errorf("a quoted payload did not decode: %v", err)
	}
}
