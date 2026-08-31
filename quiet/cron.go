// Package quiet decides whether a scheduled runner has stopped running.
//
// The decision lives here, away from the network, because it is the part that
// can be wrong in a way nobody notices: a watcher that says "all quiet" when a
// runner died is indistinguishable from a working one until the day it matters.
package quiet

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Cron is one POSIX five-field schedule line, evaluated in UTC -- which is what
// GitHub Actions uses regardless of where the repository or its owner is.
type Cron struct {
	expr    string
	min     [60]bool
	hour    [24]bool
	dom     [32]bool
	mon     [13]bool
	dow     [7]bool
	domStar bool
	dowStar bool
}

// ParseCron reads one cron line: five space-separated fields, each a list of
// "*", "n", "a-b", or any of those with a "/step" suffix.
func ParseCron(expr string) (*Cron, error) {
	f := strings.Fields(expr)
	if len(f) != 5 {
		return nil, fmt.Errorf("cron %q: want 5 fields, got %d", expr, len(f))
	}
	c := &Cron{expr: strings.Join(f, " ")}
	type field struct {
		set    []bool
		lo, hi int
		name   string
		src    string
	}
	minS := c.min[:]
	hourS := c.hour[:]
	domS := c.dom[:]
	monS := c.mon[:]
	dowS := c.dow[:]
	for _, fl := range []field{
		{minS, 0, 59, "minute", f[0]},
		{hourS, 0, 23, "hour", f[1]},
		{domS, 1, 31, "day-of-month", f[2]},
		{monS, 1, 12, "month", f[3]},
		// Day-of-week accepts 7 as well as 0 for Sunday, so the range check
		// runs to 7 and fillField folds it down to 0.
		{dowS, 0, 7, "day-of-week", f[4]},
	} {
		if err := fillField(fl.set, fl.lo, fl.hi, fl.src, fl.name); err != nil {
			return nil, fmt.Errorf("cron %q: %w", expr, err)
		}
	}
	c.domStar = isStar(f[2])
	c.dowStar = isStar(f[4])
	return c, nil
}

func isStar(s string) bool {
	// "*/2" restricts the field, so only a bare "*" (or a full range) counts as
	// unrestricted. Getting this wrong flips day-of-month/day-of-week from OR to
	// AND and silently halves how often the schedule appears to fire.
	for _, part := range strings.Split(s, ",") {
		if part == "*" {
			return true
		}
	}
	return false
}

func fillField(set []bool, lo, hi int, src, name string) error {
	sunday := name == "day-of-week"
	for _, part := range strings.Split(src, ",") {
		step := 1
		if i := strings.Index(part, "/"); i >= 0 {
			n, err := strconv.Atoi(part[i+1:])
			if err != nil || n <= 0 {
				return fmt.Errorf("bad %s step %q", name, part)
			}
			step = n
			part = part[:i]
		}
		a, b := lo, hi
		switch {
		case part == "*":
			// The whole range, already in a and b.
		case strings.Contains(part, "-"):
			bits := strings.SplitN(part, "-", 2)
			x, err1 := strconv.Atoi(bits[0])
			y, err2 := strconv.Atoi(bits[1])
			if err1 != nil || err2 != nil {
				return fmt.Errorf("bad %s range %q", name, part)
			}
			a, b = x, y
		default:
			x, err := strconv.Atoi(part)
			if err != nil {
				return fmt.Errorf("bad %s value %q", name, part)
			}
			a, b = x, x
		}
		if a < lo || b > hi || a > b {
			return fmt.Errorf("%s %q out of range %d-%d", name, part, lo, hi)
		}
		for v := a; v <= b; v += step {
			// Sunday is both 0 and 7 in cron; "5-7" is Friday to Sunday, not a
			// range that quietly loses a day off its end. Fold into a separate
			// index -- folding the loop variable itself never terminates.
			i := v
			if sunday && i == 7 {
				i = 0
			}
			set[i] = true
		}
	}
	return nil
}

func (c *Cron) dayMatches(d time.Time) bool {
	if !c.mon[int(d.Month())] {
		return false
	}
	dom := c.dom[d.Day()]
	dow := c.dow[int(d.Weekday())]
	switch {
	case c.domStar && c.dowStar:
		return true
	case c.domStar:
		return dow
	case c.dowStar:
		return dom
	default:
		// POSIX: when both are restricted, either one matching is enough.
		return dom || dow
	}
}

// searchYears bounds the walk so an expression that can never fire -- "0 0 30 2
// *", the thirtieth of February -- returns "no occurrence" instead of looping.
const searchYears = 5

// Next returns the first occurrence strictly after t.
func (c *Cron) Next(t time.Time) (time.Time, bool) {
	from := t.UTC().Truncate(time.Minute).Add(time.Minute)
	limit := from.AddDate(searchYears, 0, 0)
	day := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.UTC)
	for ; day.Before(limit); day = day.AddDate(0, 0, 1) {
		if !c.dayMatches(day) {
			continue
		}
		for h := 0; h < 24; h++ {
			if !c.hour[h] {
				continue
			}
			for m := 0; m < 60; m++ {
				if !c.min[m] {
					continue
				}
				cand := day.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute)
				if !cand.Before(from) {
					return cand, true
				}
			}
		}
	}
	return time.Time{}, false
}

// Prev returns the last occurrence at or before t. It is what turns a run
// timestamp into the lag behind the schedule that asked for it.
func (c *Cron) Prev(t time.Time) (time.Time, bool) {
	to := t.UTC().Truncate(time.Minute)
	limit := to.AddDate(-searchYears, 0, 0)
	day := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, time.UTC)
	for ; day.After(limit); day = day.AddDate(0, 0, -1) {
		if !c.dayMatches(day) {
			continue
		}
		for h := 23; h >= 0; h-- {
			if !c.hour[h] {
				continue
			}
			for m := 59; m >= 0; m-- {
				if !c.min[m] {
					continue
				}
				cand := day.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute)
				if !cand.After(to) {
					return cand, true
				}
			}
		}
	}
	return time.Time{}, false
}

func (c *Cron) String() string { return c.expr }

// Schedule is every cron line on one workflow. A workflow may carry several,
// and the fleet's runners are staggered across the day, so the period is never
// a constant: it is whatever THIS runner's own lines say.
type Schedule []*Cron

// ParseSchedule reads the `cron:` lines out of a workflow file. It is a
// deliberate line scan rather than a YAML parse: the workflow's shape is fixed
// by the fleet's own template, and a scan cannot fail to build for want of a
// dependency in a module that has none.
func ParseSchedule(yaml string) (Schedule, []error) {
	var s Schedule
	var errs []error
	inSchedule := false
	scheduleIndent := -1
	for _, line := range strings.Split(yaml, "\n") {
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if trimmed == "schedule:" {
			inSchedule, scheduleIndent = true, indent
			continue
		}
		if inSchedule && indent <= scheduleIndent && !strings.HasPrefix(trimmed, "-") {
			// Left the schedule block; a `cron:` further down belongs to
			// something else and must not be mistaken for this workflow's.
			inSchedule = false
		}
		if !inSchedule {
			continue
		}
		key := strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
		if !strings.HasPrefix(key, "cron:") {
			continue
		}
		val := strings.TrimSpace(strings.TrimPrefix(key, "cron:"))
		val = strings.Trim(val, `"'`)
		c, err := ParseCron(val)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		s = append(s, c)
	}
	return s, errs
}

// Next is the earliest next occurrence across every line.
func (s Schedule) Next(t time.Time) (time.Time, bool) {
	var best time.Time
	ok := false
	for _, c := range s {
		if n, got := c.Next(t); got && (!ok || n.Before(best)) {
			best, ok = n, true
		}
	}
	return best, ok
}

// Prev is the latest occurrence at or before t across every line.
func (s Schedule) Prev(t time.Time) (time.Time, bool) {
	var best time.Time
	ok := false
	for _, c := range s {
		if p, got := c.Prev(t); got && (!ok || p.After(best)) {
			best, ok = p, true
		}
	}
	return best, ok
}

func (s Schedule) String() string {
	parts := make([]string, len(s))
	for i, c := range s {
		parts[i] = c.expr
	}
	return strings.Join(parts, " | ")
}
