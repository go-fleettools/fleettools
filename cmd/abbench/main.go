// Command abbench runs two Go test binaries alternately, base then head, for N
// rounds with the same arguments, and appends each run's stdout to
// <out>/base.txt and <out>/head.txt, so that
//
//	benchstat base.txt head.txt
//
// compares them. It exists because two benchmarks run one after the other
// measure the host's load drift as much as the code: a neighbour starting a job
// between the two blocks shows up as a regression. Alternating the runs spreads
// the drift over both sides.
//
//	abbench --base ./b.test --head ./h.test --rounds 8 --cpu 5 -- \
//	    -test.run '^$' -test.bench 'X' -test.count 1
//
// It is pure Go with no cgo, so it cross-compiles for every GOOS/GOARCH and is
// copied to the remote benchmark hosts like hostload.
//
// ⛔ A child that fails STOPS the run. A benchmark series with a hole in it is
// not a smaller series, it is a biased one, and benchstat would compare it
// without a word. The binary, the round and the child's stderr are printed and
// the status is 1.
//
// The binaries are executed directly from an argv, never through a shell.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/go-fleettools/fleettools/internal/procstat"
	"github.com/spf13/cobra"
)

// Seams the tests replace: the process exit, the platform, the way a binary is
// looked up on PATH, and the two readings of /proc/stat.
var (
	osExit   = os.Exit
	goos     = runtime.GOOS
	lookPath = exec.LookPath
	readStat = procStat
	sleep    = time.Sleep
)

func procStat() (procstat.Snapshot, error) { return procstat.ReadFile("/proc/stat") }

// failure is a run that could not complete (exit 1). Anything else that comes
// out of the command is a usage error (exit 2).
type failure struct{ err error }

func (f *failure) Error() string { return f.err.Error() }
func (f *failure) Unwrap() error { return f.err }

func failf(format string, a ...any) error { return &failure{fmt.Errorf(format, a...)} }

type options struct {
	base, head string
	rounds     int
	cpu        int
	pinned     bool
	gomaxprocs int
	out        string
	maxBusy    float64
	gated      bool
}

const longHelp = `Runs BASE and HEAD, two Go test binaries, alternately (base, head, base, head,
...) for ROUNDS rounds with the arguments given after "--". Each run's stdout is
appended to OUT/base.txt and OUT/head.txt, so that

    benchstat OUT/base.txt OUT/head.txt

compares them; alternating cancels the drift of the host's load. The children's
stderr goes to abbench's stderr.

--cpu N pins every run to CPU N (Linux, through taskset). --gomaxprocs sets
GOMAXPROCS in the children (0 leaves it as inherited). --max-busy PCT (Linux)
reads /proc/stat twice, one second apart, before each round and refuses to go on
when the host is busier than PCT percent.

A child that exits non-zero stops the run: abbench names the binary and the
round, prints the child's stderr, and exits 1. Exits 2 on a usage error.`

func RootCmd() *cobra.Command {
	var o options
	c := &cobra.Command{
		Use:          "abbench --base BASE.test --head HEAD.test [flags] -- TEST-ARGS...",
		Short:        "Run two test binaries alternately, for benchstat",
		Long:         longHelp,
		SilenceUsage: true,
		// Errors are printed by execute, which also decides the status.
		SilenceErrors: true,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 && cmd.ArgsLenAtDash() != 0 {
				return errors.New("arguments for the test binaries go after --")
			}
			return nil
		},
	}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		o.pinned = cmd.Flags().Changed("cpu")
		o.gated = cmd.Flags().Changed("max-busy")
		return run(cmd, o, args)
	}
	f := c.Flags()
	f.StringVarP(&o.base, "base", "b", "", "the baseline test binary (required)")
	f.StringVarP(&o.head, "head", "H", "", "the test binary under test (required)")
	f.IntVarP(&o.rounds, "rounds", "n", 8, "number of base+head rounds")
	f.IntVarP(&o.cpu, "cpu", "c", 0, "pin every run to this CPU (Linux, through taskset)")
	f.IntVarP(&o.gomaxprocs, "gomaxprocs", "g", 1, "GOMAXPROCS for the children; 0 leaves it as inherited")
	f.StringVarP(&o.out, "out", "o", ".", "directory receiving base.txt and head.txt")
	f.Float64VarP(&o.maxBusy, "max-busy", "m", 0, "Linux: refuse a round when the host is busier than this many percent")
	_ = c.MarkFlagRequired("base")
	_ = c.MarkFlagRequired("head")
	return c
}

// execute runs the command and returns the process status: 0 clean, 1 a run
// that failed, 2 a usage error.
func execute(c *cobra.Command) int {
	err := c.Execute()
	if err == nil {
		return 0
	}
	fmt.Fprintf(c.ErrOrStderr(), "abbench: %v\n", err)
	var f *failure
	if errors.As(err, &f) {
		return 1
	}
	return 2
}

func main() {
	fleet.WarnIfStale(os.Stderr)
	osExit(execute(RootCmd()))
}

func validate(o options) error {
	switch {
	case o.rounds < 1:
		return fmt.Errorf("--rounds must be at least 1, got %d", o.rounds)
	case o.gomaxprocs < 0:
		return fmt.Errorf("--gomaxprocs must not be negative, got %d", o.gomaxprocs)
	case o.pinned && o.cpu < 0:
		return fmt.Errorf("--cpu must not be negative, got %d", o.cpu)
	case o.gated && (o.maxBusy < 0 || o.maxBusy > 100):
		return fmt.Errorf("--max-busy is a percentage, got %v", o.maxBusy)
	case o.pinned && goos != "linux":
		return fmt.Errorf("--cpu pins through taskset and works on Linux only, not %s", goos)
	case o.gated && goos != "linux":
		return fmt.Errorf("--max-busy reads /proc/stat and works on Linux only, not %s", goos)
	}
	return nil
}

func run(cmd *cobra.Command, o options, args []string) error {
	if err := validate(o); err != nil {
		return err
	}
	if err := os.MkdirAll(o.out, 0o755); err != nil {
		return failf("%v", err)
	}
	basePath, headPath := filepath.Join(o.out, "base.txt"), filepath.Join(o.out, "head.txt")
	baseOut, err := os.OpenFile(basePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return failf("%v", err)
	}
	defer baseOut.Close()
	headOut, err := os.OpenFile(headPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return failf("%v", err)
	}
	defer headOut.Close()

	env := os.Environ()
	if o.gomaxprocs > 0 {
		env = append(env, "GOMAXPROCS="+strconv.Itoa(o.gomaxprocs))
	}
	stderr := cmd.ErrOrStderr()

	for round := 1; round <= o.rounds; round++ {
		if o.gated {
			if err := checkBusy(o.maxBusy, round); err != nil {
				return err
			}
		}
		for _, side := range []struct {
			name, bin string
			out       io.Writer
		}{{"base", o.base, baseOut}, {"head", o.head, headOut}} {
			argv, err := pinArgv(o, side.bin, args)
			if err != nil {
				return failf("%v", err)
			}
			var errBuf bytes.Buffer
			child := exec.Command(argv[0], argv[1:]...)
			child.Env = env
			child.Stdout = side.out
			child.Stderr = io.MultiWriter(stderr, &errBuf)
			if err := child.Run(); err != nil {
				return failf("%s binary %s failed in round %d of %d: %v\n%s",
					side.name, side.bin, round, o.rounds, err, strings.TrimRight(errBuf.String(), "\n"))
			}
		}
	}
	fmt.Fprintf(cmd.OutOrStdout(), "abbench: %d rounds done, wrote %s and %s\n", o.rounds, basePath, headPath)
	return nil
}

// pinArgv is the argv that runs bin, pinned to the CPU when one was asked for.
// Pinning goes through taskset(1), which every Linux benchmark host has, so
// abbench needs nothing beyond the standard library and stays buildable for
// every platform.
func pinArgv(o options, bin string, args []string) ([]string, error) {
	if !o.pinned {
		return append([]string{bin}, args...), nil
	}
	taskset, err := lookPath("taskset")
	if err != nil {
		return nil, fmt.Errorf("--cpu needs taskset on PATH: %v", err)
	}
	return append([]string{taskset, "-c", strconv.Itoa(o.cpu), bin}, args...), nil
}

// checkBusy reads /proc/stat twice, a second apart, and refuses when the host
// is busier than max percent.
func checkBusy(max float64, round int) error {
	a, err := readStat()
	if err != nil {
		return failf("--max-busy: %v", err)
	}
	sleep(time.Second)
	b, err := readStat()
	if err != nil {
		return failf("--max-busy: %v", err)
	}
	if busy := 100 * procstat.Between(a.All, b.All).Busy; busy > max {
		return failf("host busy %.1f%% > %.1f%% before round %d", busy, max, round)
	}
	return nil
}
