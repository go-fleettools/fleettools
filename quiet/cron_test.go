package quiet

import (
	"strings"
	"testing"
	"time"
)

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t.UTC()
}

func mustCron(t *testing.T, e string) *Cron {
	t.Helper()
	c, err := ParseCron(e)
	if err != nil {
		t.Fatalf("ParseCron(%q): %v", e, err)
	}
	return c
}

func TestParseCronErrors(t *testing.T) {
	for _, e := range []string{
		"", "0 4 * *", "0 4 * * * *",
		"x 4 * * *", "0 4 * * x",
		"60 4 * * *", "0 24 * * *", "0 4 32 * *", "0 4 * 13 *", "0 4 * * 8",
		"0-x 4 * * *", "0 4-x * * *", "5-1 4 * * *",
		"*/0 4 * * *", "*/x 4 * * *",
		"  0 4 * *  ",
	} {
		if _, err := ParseCron(e); err == nil {
			t.Errorf("ParseCron(%q): want error, got none", e)
		}
	}
	if _, err := ParseCron("0 4 * * ,"); err == nil {
		t.Error("empty list element: want error")
	}
}

func TestParseCronForms(t *testing.T) {
	for _, e := range []string{
		"0 4 * * *", "44 16 * * *", "0 4,16 * * *", "*/15 * * * *",
		"0 0-6/2 * * *", "0 4 1-7 * *", "0 4 * * 0", "0 4 * * 7", "0 4 * * 1-5",
	} {
		c := mustCron(t, e)
		if c.String() == "" {
			t.Errorf("%q: empty String()", e)
		}
	}
}

func TestNextDaily(t *testing.T) {
	c := mustCron(t, "0 4 * * *")
	got, ok := c.Next(at("2026-08-31T03:00:00Z"))
	if !ok || !got.Equal(at("2026-08-31T04:00:00Z")) {
		t.Fatalf("Next = %v %v", got, ok)
	}
	// Strictly after: standing exactly on an occurrence yields the next one.
	got, _ = c.Next(at("2026-08-31T04:00:00Z"))
	if !got.Equal(at("2026-09-01T04:00:00Z")) {
		t.Fatalf("Next on the minute = %v", got)
	}
	// Sub-minute precision is truncated, not rounded up past the occurrence.
	got, _ = c.Next(at("2026-08-31T03:59:30Z"))
	if !got.Equal(at("2026-08-31T04:00:00Z")) {
		t.Fatalf("Next mid-minute = %v", got)
	}
}

func TestNextStaggered(t *testing.T) {
	// The fleet stages its runners across the day; each must be measured
	// against its OWN period, never a shared constant.
	for _, tc := range []struct{ expr, from, want string }{
		{"44 16 * * *", "2026-08-31T10:00:00Z", "2026-08-31T16:44:00Z"},
		{"11 15 * * *", "2026-08-31T16:00:00Z", "2026-09-01T15:11:00Z"},
		{"0 12 * * *", "2026-08-31T12:30:00Z", "2026-09-01T12:00:00Z"},
	} {
		c := mustCron(t, tc.expr)
		got, _ := c.Next(at(tc.from))
		if !got.Equal(at(tc.want)) {
			t.Errorf("%s from %s = %v, want %s", tc.expr, tc.from, got, tc.want)
		}
	}
}

func TestNextDomDowUnion(t *testing.T) {
	// POSIX: both fields restricted means EITHER may match. 2026-09-01 is a
	// Tuesday, so a "1st or Sunday" schedule fires on the 1st.
	c := mustCron(t, "0 4 1 * 0")
	got, _ := c.Next(at("2026-08-31T12:00:00Z"))
	if !got.Equal(at("2026-09-01T04:00:00Z")) {
		t.Fatalf("union = %v", got)
	}
	// Only day-of-week restricted: next Sunday.
	c = mustCron(t, "0 4 * * 0")
	got, _ = c.Next(at("2026-08-31T12:00:00Z"))
	if got.Weekday() != time.Sunday {
		t.Fatalf("dow-only = %v (%v)", got, got.Weekday())
	}
	// Only day-of-month restricted.
	c = mustCron(t, "0 4 15 * *")
	got, _ = c.Next(at("2026-08-31T12:00:00Z"))
	if !got.Equal(at("2026-09-15T04:00:00Z")) {
		t.Fatalf("dom-only = %v", got)
	}
	// "*/2" in day-of-month RESTRICTS the field: it must not be read as "*".
	c = mustCron(t, "0 4 */2 * 0")
	if c.domStar {
		t.Error(`"*/2" must not count as unrestricted`)
	}
	// A comma list containing a bare "*" is still unrestricted.
	c = mustCron(t, "0 4 *,15 * *")
	if !c.domStar {
		t.Error(`"*,15" should count as unrestricted`)
	}
}

func TestNextMonthAndUnreachable(t *testing.T) {
	c := mustCron(t, "0 4 1 1 *")
	got, _ := c.Next(at("2026-08-31T12:00:00Z"))
	if !got.Equal(at("2027-01-01T04:00:00Z")) {
		t.Fatalf("yearly = %v", got)
	}
	// The thirtieth of February never arrives; the search must end, not loop.
	c = mustCron(t, "0 0 30 2 *")
	if _, ok := c.Next(at("2026-08-31T12:00:00Z")); ok {
		t.Error("30 February: want no occurrence")
	}
	if _, ok := c.Prev(at("2026-08-31T12:00:00Z")); ok {
		t.Error("30 February: want no previous occurrence")
	}
}

func TestPrev(t *testing.T) {
	c := mustCron(t, "0 4 * * *")
	got, ok := c.Prev(at("2026-08-31T10:32:00Z"))
	if !ok || !got.Equal(at("2026-08-31T04:00:00Z")) {
		t.Fatalf("Prev = %v %v", got, ok)
	}
	// At or before, so standing on an occurrence returns it.
	got, _ = c.Prev(at("2026-08-31T04:00:00Z"))
	if !got.Equal(at("2026-08-31T04:00:00Z")) {
		t.Fatalf("Prev on the minute = %v", got)
	}
	// Before the day's occurrence, it is yesterday's.
	got, _ = c.Prev(at("2026-08-31T01:00:00Z"))
	if !got.Equal(at("2026-08-30T04:00:00Z")) {
		t.Fatalf("Prev before = %v", got)
	}
	c = mustCron(t, "0 4 15 * *")
	got, _ = c.Prev(at("2026-08-31T01:00:00Z"))
	if !got.Equal(at("2026-08-15T04:00:00Z")) {
		t.Fatalf("Prev dom = %v", got)
	}
}

const realWorkflow = `name: Renovate

on:
  schedule:
    - cron: '44 16 * * *'
  workflow_dispatch:
    inputs:
      logLevel:
        description: Renovate log level
        default: info

concurrency:
  group: renovate

jobs:
  renovate:
    runs-on: ubuntu-latest
`

func TestParseScheduleReal(t *testing.T) {
	s, errs := ParseSchedule(realWorkflow)
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(s) != 1 || s.String() != "44 16 * * *" {
		t.Fatalf("schedule = %q", s.String())
	}
	n, _ := s.Next(at("2026-08-31T10:00:00Z"))
	if !n.Equal(at("2026-08-31T16:44:00Z")) {
		t.Fatalf("Next = %v", n)
	}
	p, _ := s.Prev(at("2026-08-31T18:00:00Z"))
	if !p.Equal(at("2026-08-31T16:44:00Z")) {
		t.Fatalf("Prev = %v", p)
	}
}

func TestParseScheduleMultipleAndScoping(t *testing.T) {
	src := `on:
  schedule:
    - cron: "0 4 * * *"
    - cron: 0 16 * * *   # a bare, unquoted, commented line
jobs:
  x:
    steps:
      - run: echo cron: 0 0 * * *
`
	s, errs := ParseSchedule(src)
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	// The third `cron:` is in a job step, outside the schedule block, and must
	// not be mistaken for this workflow's schedule.
	if len(s) != 2 {
		t.Fatalf("schedule = %q (want 2 lines)", s.String())
	}
	n, _ := s.Next(at("2026-08-31T10:00:00Z"))
	if !n.Equal(at("2026-08-31T16:00:00Z")) {
		t.Fatalf("earliest Next = %v", n)
	}
	p, _ := s.Prev(at("2026-08-31T10:00:00Z"))
	if !p.Equal(at("2026-08-31T04:00:00Z")) {
		t.Fatalf("latest Prev = %v", p)
	}
}

func TestParseScheduleNoneAndBad(t *testing.T) {
	s, errs := ParseSchedule("on:\n  workflow_dispatch:\n")
	if len(s) != 0 || len(errs) != 0 {
		t.Fatalf("dispatch-only: %q %v", s.String(), errs)
	}
	if _, ok := s.Next(at("2026-08-31T10:00:00Z")); ok {
		t.Error("empty schedule: want no Next")
	}
	if _, ok := s.Prev(at("2026-08-31T10:00:00Z")); ok {
		t.Error("empty schedule: want no Prev")
	}
	s, errs = ParseSchedule("on:\n  schedule:\n    - cron: 'not a cron'\n")
	if len(s) != 0 || len(errs) != 1 {
		t.Fatalf("bad cron: %q %v", s.String(), errs)
	}
	if !strings.Contains(errs[0].Error(), "not a cron") {
		t.Errorf("error should name the line: %v", errs[0])
	}
}

func TestSundayIsBothZeroAndSeven(t *testing.T) {
	// "5-7" is Friday to Sunday. A fold that clipped the range to 5-6 would
	// quietly drop a day, and the schedule would look like it fires less often
	// than it does.
	c := mustCron(t, "0 4 * * 5-7")
	for _, d := range []time.Weekday{time.Friday, time.Saturday, time.Sunday} {
		if !c.dow[int(d)] {
			t.Errorf("5-7 should include %v", d)
		}
	}
	for _, d := range []time.Weekday{time.Monday, time.Thursday} {
		if c.dow[int(d)] {
			t.Errorf("5-7 should not include %v", d)
		}
	}
	// A bare 7 is Sunday too.
	if !mustCron(t, "0 4 * * 7").dow[int(time.Sunday)] {
		t.Error("7 should be Sunday")
	}
	// And 7-7.
	if !mustCron(t, "0 4 * * 7-7").dow[int(time.Sunday)] {
		t.Error("7-7 should be Sunday")
	}
}

func TestScheduleBlockIgnoresNonCronKeys(t *testing.T) {
	src := `on:
  schedule:
    - cron: '0 4 * * *'
      # a key that is not a cron, inside the schedule block
    - timezone: 'Etc/UTC'
`
	s, errs := ParseSchedule(src)
	if len(errs) != 0 || len(s) != 1 || s.String() != "0 4 * * *" {
		t.Fatalf("schedule = %q errs = %v", s.String(), errs)
	}
}
