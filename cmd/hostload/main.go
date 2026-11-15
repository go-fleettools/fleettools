// Command hostload says how busy a host's CPUs are right now, from two
// readings of /proc/stat, and which CPUs are the idlest -- the check to run
// before a measurement, and the CPU to pin it to.
//
// It exists because the load average answered the wrong question twice. On a
// Loongson (cfarm401) it read 146 while every CPU was idle: 146 processes stuck
// in uninterruptible sleep count toward the load and use no CPU. On a Zen 3
// guest (cfarm421) a quiet load hid a third of the CPU time going to other
// users' niced jobs. Both were found only by diffing /proc/stat by hand, in
// throwaway scripts; this is that diff with a home and tests.
//
// ⛔ /proc/stat IS LINUX, and this fleet's sessions run on a Mac. Asked for the
// busy shares where there is no /proc/stat, the command used to print
//
//	hostload: open /proc/stat: no such file or directory
//
// which reads like a broken host rather than an unsupported platform (#221).
// It now names the platform and what CAN be asked there: --max-load, the
// 1-minute load average, which the loadavg package reads on Linux and macOS
// alike. That measure is only ever used where the caller asks for it BY NAME,
// because it is the weaker one -- the two paragraphs above are what it got
// wrong -- and the refusal says so every time it answers from it.
package main

import (
	"errors"
	"fmt"
	"io"
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

const longHelp = `Reads /proc/stat twice and prints the busy share of all CPUs (busy = not idle
and not waiting on I/O), its parts (user, nice, system, iowait, steal), and each
CPU idlest first.

With --pick K it prints only the K idlest CPUs, comma-separated:

    taskset -c "$(hostload --pick 1)" ./bench.test ...

With --max-busy it exits 1 when the host is busier than PCT percent, so a
measurement can refuse to start on a loaded machine.

--max-load N exits 1 when the 1-MINUTE LOAD AVERAGE is above N. It is the only
question this command can answer where there is no /proc/stat, which is every
platform but Linux, and it is the WEAKER question: the load average counts
processes, not CPU time. It read 146 on a host whose every CPU was idle, and it
read quiet on a host giving a third of its CPU time to other users' niced jobs.
Prefer --max-busy wherever /proc/stat exists; reach for --max-load when it does
not, knowing what it cannot see.

A negative limit is OFF, for both gates. --max-load 0 is therefore ON and means
"only a load of exactly zero passes", which on a live host is a permanent
refusal; abbench rejects that value instead, and the difference is stated here
rather than discovered. With --pick, the reading goes to stderr: --pick's output
is piped into taskset and carries nothing else.

Exit status: 0 what was asked was answered, 1 the host is busier than
--max-busy or more loaded than --max-load, 2 REFUSED TO DECIDE.

A 2 means one of three things, and this sentence used to name only the first,
which a review measured as false for the other two: /proc/stat could not be
read and nothing answerable was asked; or a busy-share question (--pick,
--max-busy) was asked where there is no /proc/stat, which is refused even when
a --max-load alongside it did pass; or a limit was NaN, in which case nothing
was read at all. In every case 2 means the answer is not to be trusted, never
that the host is fine.`

// procPath is read through readSnapshot, below, and never opened directly.
const procPath = "/proc/stat"

// ⛔ THE TWO READINGS ARE SEAMS, and that is not a convenience. /proc/stat does
// not exist on every host this suite runs on, so a test that needed the real
// file would be skipped exactly where the behaviour matters -- on the Mac this
// change is for. Both are replaced by a test; neither is replaced in main.
var (
	readSnapshot = procstat.ReadFile
	readAvg1     = loadavg.Avg1
)

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// RootCmd builds the command. Tests drive it through run, below.
//
// ⛔ THIS COMMAND HAD NO run() AT ALL: it parsed os.Args in place through the
// package-level flag.Parse() and printed to os.Stdout from inside main, so
// nothing in it could be driven by a test. The conversion gives it a surface it
// did not have, which is most of the work and none of the spelling.
func RootCmd() *cobra.Command {
	var (
		interval time.Duration
		k        int
		maxBusy  float64
		maxLoad  float64
	)
	c := cli.New("hostload [flags]",
		"How busy this host's CPUs are, and which are idlest", longHelp)
	c.Args = cobra.NoArgs
	c.RunE = func(cmd *cobra.Command, _ []string) error {
		return cli.FromStatus(readLoad(cli.Out(cmd), cli.Err(cmd), interval, k, maxBusy, maxLoad))
	}
	f := c.Flags()
	// -i keeps its one-letter spelling, which pflag accepts unchanged; --pick
	// and --max-busy now need two dashes, which internal/cli's shim rewrites
	// for an existing call site, with a warning.
	f.DurationVarP(&interval, "interval", "i", 2*time.Second, "time between the two readings")
	f.IntVar(&k, "pick", 0, "print only the K idlest CPUs, comma-separated, for taskset -c")
	f.Float64Var(&maxBusy, "max-busy", -1, "exit 1 if the host is busier than this many percent")
	// A negative limit is OFF, as --max-busy already spells it, so that the two
	// gates read alike at a call site.
	f.Float64Var(&maxLoad, "max-load", -1, "exit 1 if the 1-minute load average is above this (Linux and macOS; the weaker question -- see --help)")
	return c
}

// run drives the command and returns its status, for a test.
func run(args []string, out, errOut io.Writer) int {
	return cli.Drive(RootCmd(), args, out, errOut)
}

// readLoad is the whole measurement, taking its writers so that what it prints
// can be read by something other than a terminal.
func readLoad(stdout, stderr io.Writer, interval time.Duration, k int, maxBusy, maxLoad float64) int {
	// ⛔⛔ NaN IS NOT A LIMIT, AND IT HAS TO BE REFUSED BEFORE ANYTHING IS READ.
	// Every comparison with NaN is false, so `--max-busy NaN` on a 100%-busy
	// host exited 0 with nothing on stderr — the gate silently vanished.
	// Measured by a review. A limit nobody can compare against is a refusal,
	// never a pass, and checking it after the readings would let the --max-load
	// gate pass on it first.
	if math.IsNaN(maxBusy) || math.IsNaN(maxLoad) {
		fmt.Fprintln(stderr, "hostload: a limit of NaN cannot be compared against; give a number")
		return 2
	}
	// ⛔ --max-load IS ANSWERED FIRST, for two reasons. It is the only question
	// answerable without /proc/stat, so it must not sit behind a read that
	// fails there; and a host that already fails it should not be slept on for
	// the interval before being told so.
	if maxLoad >= 0 {
		switch code := gateOnLoad(stdout, stderr, maxLoad, k > 0); code {
		case 1, 2:
			return code
		}
	}

	a, err := readSnapshot(procPath)
	if err != nil {
		return noProcStat(stderr, err, k, maxBusy, maxLoad)
	}
	time.Sleep(interval)
	b, err := readSnapshot(procPath)
	if err != nil {
		// ⛔ The SECOND read failing is never "unsupported platform": the first
		// one succeeded a moment ago, so whatever this is, it is a fault on a
		// host that has the file. It keeps its own error.
		fmt.Fprintln(stderr, "hostload:", err)
		return 2
	}
	u := procstat.Between(a.All, b.All)
	cs := procstat.PerCPU(a, b)

	if k > 0 {
		fmt.Fprintln(stdout, procstat.Pick(cs, k))
	} else {
		fmt.Fprintf(stdout, "busy %.1f%%  (user %.1f%%, nice %.1f%%, system %.1f%%, iowait %.1f%%, steal %.1f%%) over %v, %d CPUs\n",
			100*u.Busy, 100*u.User, 100*u.Nice, 100*u.System, 100*u.IOWait, 100*u.Steal, interval, len(cs))
		for _, c := range cs {
			fmt.Fprintf(stdout, "cpu%d %.0f%%\n", c.ID, 100*c.Busy)
		}
	}
	if maxBusy >= 0 && 100*u.Busy > maxBusy {
		// Status 1: the host WAS read, and it is too busy. The line says both
		// numbers, because a bare status cannot.
		fmt.Fprintf(stderr, "hostload: busy %.1f%% > %.1f%%\n", 100*u.Busy, maxBusy)
		return 1
	}
	return 0
}

// gateOnLoad answers --max-load from the 1-minute load average. It returns 0
// when the limit is met, 1 when it is not, and 2 when the load could not be
// read.
//
// ⛔⛔ AN UNREAD LOAD IS NOT A LOW ONE. Returning 0 here on a read error would
// make an unanswerable question read as a pass, which is the way this fleet has
// lost measurements before: the gate that cannot see says "go ahead".
func gateOnLoad(stdout, stderr io.Writer, maxLoad float64, picking bool) int {
	l, err := readAvg1()
	if err != nil {
		fmt.Fprintln(stderr, "hostload: --max-load: cannot read the load average:", err)
		return 2
	}
	// Printed whether it passes or not: a caller that gated on a number wants
	// the number, and a bare status cannot carry it.
	//
	// ⛔⛔ BUT NOT ON STDOUT WHEN --pick OWNS IT. Measured by a review:
	// `hostload --pick 1 --max-load 8` on a host WITH /proc/stat put
	// "load1 0.42 over 1 min" above the CPU list, so the README's own
	// `taskset -c "$(hostload --pick 1)"` got a two-line argument. --pick's
	// output is machine-readable and this command's own documentation pipes
	// it straight into another program; a diagnostic line on that stream is
	// not a cosmetic problem.
	where := stdout
	if picking {
		where = stderr
	}
	fmt.Fprintf(where, "load1 %.2f over 1 min\n", l)
	if l > maxLoad {
		fmt.Fprintf(stderr, "hostload: load1 %.2f > %.2f\n", l, maxLoad)
		return 1
	}
	return 0
}

// noProcStat is what the command says where its own measurement does not exist.
//
// ⛔ IT MUST NOT QUIETLY SUBSTITUTE THE LOAD AVERAGE. This command exists
// BECAUSE the load average answered the wrong question twice (the package
// comment has both), so the weaker measure is used only where the caller named
// it, and the line below says so even when the answer is 0. A tool that
// silently downgrades its measure is a tool whose output cannot be compared
// across hosts.
func noProcStat(stderr io.Writer, err error, k int, maxBusy, maxLoad float64) int {
	if !errors.Is(err, fs.ErrNotExist) {
		// A /proc/stat that EXISTS and cannot be read -- a permission, an
		// unmounted /proc, a short read -- is a different fault from an
		// unsupported platform, and must not be reported as one.
		fmt.Fprintln(stderr, "hostload:", err)
		return 2
	}
	fmt.Fprintf(stderr, "hostload: per-CPU busy shares are read from %s, which %s does not have\n", procPath, runtime.GOOS)
	switch {
	case k > 0 || maxBusy >= 0:
		fmt.Fprintln(stderr, "hostload: --pick and --max-busy cannot be answered here; --max-load N can, on the 1-minute load average")
		return 2
	case maxLoad >= 0:
		fmt.Fprintln(stderr, "hostload: --max-load was answered from the 1-minute load average alone, which counts processes rather than CPU time")
		return 0
	}
	fmt.Fprintln(stderr, "hostload: nothing was measured; ask the weaker question by name with --max-load N, or run this where /proc/stat exists")
	return 2
}
