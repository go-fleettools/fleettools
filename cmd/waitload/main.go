// Command waitload blocks until the host is quiet enough to measure on, and
// exits 0 only then: the 1-minute load average at or below a limit, and with
// --max-busy the CPU busy share too, for K consecutive readings.
//
// ⛔ WHY THIS EXISTS. An agent must not measure on a busy host (1-minute load
// above about 8), and no fleet program could WAIT for a quiet one: hostload
// reads the load once and exits 1, and the shell loop or `sleep` an agent would
// reach for is refused by guard-bash. An agent stopped on exactly that at 21:42
// CET on 2026-11-18.
//
// ⛔ IT READS THE LOAD THROUGH loadavg.Avg1 AND THE BUSY SHARE THROUGH
// internal/procstat, the two readers hostload uses. A second reader would be a
// second place for the macOS sysctl decoding to be wrong.
//
// ⛔ THE LOOP IS BOUNDED BY A COUNT AND BY A DEADLINE, BOTH COMPUTED BEFORE THE
// FIRST READING (#327): the load is a value the host reports, and no predicate
// over a reported value is a bound this command controls. The count is the
// number of readings that fit in --timeout; the deadline is elapsed time.
package main

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"runtime"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/go-fleettools/fleettools/internal/procstat"
	"github.com/go-fleettools/fleettools/loadavg"
	"github.com/spf13/cobra"
)

const procPath = "/proc/stat"

// The seams, replaced by tests and by nothing in main.
var (
	now      = time.Now
	sleep    = time.Sleep
	readLoad = loadavg.Avg1
	readStat = procstat.ReadFile
)

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// RootCmd is the command, driven by the tests through cli.Drive.
func RootCmd() *cobra.Command {
	c := cli.New("waitload --max-load N --timeout D [--max-busy PCT] [--settle K] [--interval D]",
		"Block until the host is quiet enough to measure on",
		`Blocks until the 1-minute load average is at most --max-load, and, with
--max-busy, until the busy share of all CPUs (everything but idle and iowait,
from /proc/stat) is at most PCT percent. The condition must hold for --settle K
CONSECUTIVE readings (default 3), --interval apart (default 10s), so that a
brief dip does not pass; a reading that fails starts the count again.

--timeout is REQUIRED and has no default: a wait must never be unbounded. The
run is bounded in elapsed time and also by a count of readings (the number that
fit in --timeout), so a clock or a host that misbehaves cannot make it loop.

--max-load must be positive (0 would only be met by an idle host that does not
exist, as abbench also rejects it). --max-busy is a percentage in (0, 100].
--max-busy needs /proc/stat: asked for where there is none (macOS), the command
REFUSES with status 2 rather than silently waiting on the load alone, which is
the weaker question hostload explains.

exit 0 the condition held for K consecutive readings; one line says so
exit 1 the timeout passed first; the last reading and the lowest one seen are
       printed on stdout
exit 2 it refuses to decide: a missing or invalid flag, an unreadable or NaN
       reading, /proc/stat absent under --max-busy`)
	c.Args = cobra.NoArgs
	maxLoad := c.Flags().Float64("max-load", 0, "wait until the 1-minute load average is at most this (required, positive)")
	maxBusy := c.Flags().Float64("max-busy", 0, "also wait until the CPU busy share is at most this many percent (needs /proc/stat)")
	timeout := c.Flags().Duration("timeout", 0, "give up after this long (required, no default)")
	settle := c.Flags().Int("settle", 3, "consecutive readings that must pass")
	interval := c.Flags().Duration("interval", 10*time.Second, "time between readings")

	c.RunE = func(c *cobra.Command, _ []string) error {
		if cli.HelpRequested(c) {
			return nil
		}
		fl := c.Flags()
		o := options{settle: *settle, timeout: *timeout, interval: *interval}
		if !fl.Changed("max-load") {
			return cli.Unanswerable("--max-load is required: a wait is a condition")
		}
		if math.IsNaN(*maxLoad) || *maxLoad <= 0 {
			return cli.Unanswerable("--max-load must be positive, got %v", *maxLoad)
		}
		o.maxLoad = *maxLoad
		if fl.Changed("max-busy") {
			if math.IsNaN(*maxBusy) || *maxBusy <= 0 || *maxBusy > 100 {
				return cli.Unanswerable("--max-busy must be a percentage in (0, 100], got %v", *maxBusy)
			}
			o.busyOn, o.maxBusy = true, *maxBusy
		}
		if !fl.Changed("timeout") {
			return cli.Unanswerable("--timeout is required: a wait must be bounded")
		}
		if o.timeout <= 0 {
			return cli.Unanswerable("--timeout %s is not a wait", o.timeout)
		}
		if o.settle < 1 {
			return cli.Unanswerable("--settle must be at least 1, got %d", o.settle)
		}
		if o.interval < time.Millisecond {
			return cli.Unanswerable("--interval must be at least 1ms, got %s", o.interval)
		}
		return wait(c, o)
	}
	return c
}

type options struct {
	maxLoad, maxBusy float64
	busyOn           bool
	settle           int
	timeout          time.Duration
	interval         time.Duration
}

// maxReadings is how many readings fit in the timeout: one at the start and one
// per interval after it. Divided, never multiplied or rounded up with an added
// unit, so that the largest timeout cobra accepts cannot overflow.
func maxReadings(timeout, interval time.Duration) int {
	q := int64(timeout / interval)
	if q > 1<<32 {
		q = 1 << 32
	}
	return int(q) + 1
}

func wait(c *cobra.Command, o options) error {
	begin := now()
	deadline := begin.Add(o.timeout)
	limit := maxReadings(o.timeout, o.interval)

	var prev procstat.Snapshot
	if o.busyOn {
		var err error
		if prev, err = readStat(procPath); err != nil {
			return busyUnreadable(err)
		}
		// The first busy share is a difference, so it needs a span to span.
		sleep(o.interval)
	}

	streak := 0
	lowest := math.Inf(1)
	var lastLoad, lastBusy float64
	for n := 1; ; n++ {
		load, err := readLoad()
		if err != nil {
			return cli.Unanswerable("cannot read the load average: %v", err)
		}
		// ⛔ NaN loses every comparison, so a NaN load would read as "not above
		// the limit" in a careless test. It is a refusal, never a pass.
		if math.IsNaN(load) || math.IsInf(load, 0) || load < 0 {
			return cli.Unanswerable("the load average read as %v, which is not a reading", load)
		}
		ok := load <= o.maxLoad
		busy := math.NaN()
		if o.busyOn {
			cur, err := readStat(procPath)
			if err != nil {
				return busyUnreadable(err)
			}
			// ⛔ procstat.Between answers a zero-tick span as 0% busy, which
			// would read as an idle host. No elapsed ticks is no reading.
			if cur.All == prev.All {
				return cli.Unanswerable("/proc/stat did not advance between two readings, so no busy share can be computed")
			}
			busy = 100 * procstat.Between(prev.All, cur.All).Busy
			prev = cur
			if math.IsNaN(busy) || busy < 0 || busy > 100.0001 {
				return cli.Unanswerable("the busy share read as %v%%, which is not a reading", busy)
			}
			ok = ok && busy <= o.maxBusy
		}
		lastLoad, lastBusy = load, busy
		lowest = math.Min(lowest, load)

		if ok {
			streak++
		} else {
			streak = 0
		}
		if streak >= o.settle {
			fmt.Fprintf(cli.Out(c), "waitload: load1 %.1f ≤ %g%s for %d readings after %s\n",
				load, o.maxLoad, busyText(o, busy), o.settle, now().Sub(begin).Round(time.Second))
			return nil
		}

		// ⛔ THE CALLER'S BOUND IS JUDGED AFTER A PASS AND BEFORE A SLEEP, and
		// the count is the one that cannot be moved by what the host reports.
		if n >= limit || now().Add(o.interval).After(deadline) {
			fmt.Fprintf(cli.Out(c), "waitload: TIMEOUT after %s, %d reading(s): last load1 %.2f%s, lowest load1 %.2f, wanted ≤ %g%s for %d in a row\n",
				now().Sub(begin).Round(time.Second), n, lastLoad, busyLast(o, lastBusy), lowest, o.maxLoad, busyWant(o), o.settle)
			return cli.Found("the timeout passed with the host still busy")
		}
		sleep(o.interval)
	}
}

func busyText(o options, busy float64) string {
	if !o.busyOn {
		return ""
	}
	return fmt.Sprintf(", busy %.1f%% ≤ %g%%", busy, o.maxBusy)
}

func busyLast(o options, busy float64) string {
	if !o.busyOn {
		return ""
	}
	return fmt.Sprintf(", busy %.1f%%", busy)
}

func busyWant(o options) string {
	if !o.busyOn {
		return ""
	}
	return fmt.Sprintf(" and busy ≤ %g%%", o.maxBusy)
}

// busyUnreadable is the refusal for a /proc/stat that cannot be read, which on
// a platform without one is named as such rather than as a broken host.
func busyUnreadable(err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return cli.Unanswerable("--max-busy reads %s, which %s does not have; wait on --max-load alone, knowing it counts processes rather than CPU time", procPath, runtime.GOOS)
	}
	return cli.Unanswerable("cannot read %s: %v", procPath, err)
}
