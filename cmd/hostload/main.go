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
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/go-fleettools/fleettools/internal/fleet"
)

func main() {
	fleet.WarnIfStale(os.Stderr)

	interval := flag.Duration("i", 2*time.Second, "time between the two readings")
	k := flag.Int("pick", 0, "print only the K idlest CPUs, comma-separated, for taskset -c")
	maxBusy := flag.Float64("max-busy", -1, "exit 1 if the host is busier than this many percent")
	flag.Usage = func() {
		fmt.Fprint(os.Stderr, `usage: hostload [-i 2s] [-pick K] [-max-busy PCT]

Reads /proc/stat twice and prints the busy share of all CPUs (busy = not idle
and not waiting on I/O), its parts (user, nice, system, iowait, steal), and
each CPU idlest first. With -pick K it prints only the K idlest CPUs:

    taskset -c "$(hostload -pick 1)" ./bench.test ...

With -max-busy it exits 1 when the host is busier than PCT percent, so a
measurement can refuse to start on a loaded machine. Exits 2 on an error.
`)
	}
	flag.Parse()

	a, err := read()
	if err != nil {
		fail(err)
	}
	time.Sleep(*interval)
	b, err := read()
	if err != nil {
		fail(err)
	}
	u := between(a.all, b.all)
	cs := perCPU(a, b)

	if *k > 0 {
		fmt.Println(pick(cs, *k))
	} else {
		fmt.Printf("busy %.1f%%  (user %.1f%%, nice %.1f%%, system %.1f%%, iowait %.1f%%, steal %.1f%%) over %v, %d CPUs\n",
			100*u.busy, 100*u.user, 100*u.nice, 100*u.system, 100*u.iowait, 100*u.steal, *interval, len(cs))
		for _, c := range cs {
			fmt.Printf("cpu%d %.0f%%\n", c.id, 100*c.busy)
		}
	}
	if *maxBusy >= 0 && 100*u.busy > *maxBusy {
		fmt.Fprintf(os.Stderr, "hostload: busy %.1f%% > %.1f%%\n", 100*u.busy, *maxBusy)
		os.Exit(1)
	}
}

func read() (snapshot, error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return snapshot{}, err
	}
	return parseStat(data)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "hostload:", err)
	os.Exit(2)
}
