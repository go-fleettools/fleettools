// Command hostload says how busy a Linux host's CPUs are right now, from two
// readings of /proc/stat, and which CPUs are the idlest -- the check to run
// before a measurement, and the CPU to pin it to.
//
// It exists because the load average answered the wrong question twice. On a
// Loongson (cfarm401) it read 146 while every CPU was idle: 146 processes stuck
// in uninterruptible sleep count toward the load and use no CPU. On a Zen 3
// guest (cfarm421) a quiet load hid a third of the CPU time going to other
// users' niced jobs. Both were found only by diffing /proc/stat by hand, in
// throwaway scripts; this is that diff with a home and tests.
package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/go-fleettools/fleettools/internal/procstat"
	"github.com/spf13/cobra"
)

const longHelp = `Reads /proc/stat twice and prints the busy share of all CPUs (busy = not idle
and not waiting on I/O), its parts (user, nice, system, iowait, steal), and each
CPU idlest first.

With --pick K it prints only the K idlest CPUs, comma-separated:

    taskset -c "$(hostload --pick 1)" ./bench.test ...

With --max-busy it exits 1 when the host is busier than PCT percent, so a
measurement can refuse to start on a loaded machine.

Exit status: 0 the host was read, 1 it is busier than --max-busy, 2 /proc/stat
could not be read.`

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
	)
	c := cli.New("hostload [flags]",
		"How busy this host's CPUs are, and which are idlest", longHelp)
	c.Args = cobra.NoArgs
	c.RunE = func(cmd *cobra.Command, _ []string) error {
		return cli.FromStatus(readLoad(cli.Out(cmd), cli.Err(cmd), interval, k, maxBusy))
	}
	f := c.Flags()
	// -i keeps its one-letter spelling, which pflag accepts unchanged; --pick
	// and --max-busy now need two dashes, which internal/cli's shim rewrites
	// for an existing call site, with a warning.
	f.DurationVarP(&interval, "interval", "i", 2*time.Second, "time between the two readings")
	f.IntVar(&k, "pick", 0, "print only the K idlest CPUs, comma-separated, for taskset -c")
	f.Float64Var(&maxBusy, "max-busy", -1, "exit 1 if the host is busier than this many percent")
	return c
}

// run drives the command and returns its status, for a test.
func run(args []string, out, errOut io.Writer) int {
	return cli.Drive(RootCmd(), args, out, errOut)
}

// readLoad is the whole measurement, taking its writers so that what it prints
// can be read by something other than a terminal.
func readLoad(stdout, stderr io.Writer, interval time.Duration, k int, maxBusy float64) int {
	a, err := procstat.ReadFile("/proc/stat")
	if err != nil {
		fmt.Fprintln(stderr, "hostload:", err)
		return 2
	}
	time.Sleep(interval)
	b, err := procstat.ReadFile("/proc/stat")
	if err != nil {
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
