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
	"github.com/go-fleettools/fleettools/loadavg"
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
	// readLoad is the source of the 1-minute load average, and loadPoll how
	// often --wait looks again.
	readLoad = loadavg.Avg1
	loadPoll = 10 * time.Second
	now      = time.Now
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
	maxLoad    float64
	loadGated  bool
	wait       time.Duration
	baseArgs   []string
	headArgs   []string
	baseRename []string
	headRename []string
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

--max-load F (Linux and macOS) gates on the 1-minute load average. Before each
round abbench waits, for at most --wait D in all, for the load to be at most F.
After each round, if the load is above F, the round is DISCARDED as a pair:
nothing of it reaches base.txt or head.txt, and the two load values are logged.
abbench runs until ROUNDS rounds were kept, or the wait budget is spent (each
discarded round counts against it); then it exits 1 and says how many were kept.

--base-arg and --head-arg (repeatable) add arguments to one side only, after
the shared ones, so BASE and HEAD may be the same binary running two
sub-benchmarks:

    abbench --base x.test --head x.test --max-load 4 --wait 2h \
        --base-arg=-test.bench=Sign/ours --head-arg=-test.bench=Sign/sdk \
        --rename-base ours=impl --rename-head sdk=impl -- -test.run '^$'

benchstat matches rows by full benchmark name, so Sign/ours and Sign/sdk would
never be compared. --rename-base OLD=NEW and --rename-head OLD=NEW (repeatable)
replace OLD by NEW in the benchmark names of that side's output, so both sides
carry the same name and "benchstat base.txt head.txt" compares them.

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
		o.loadGated = cmd.Flags().Changed("max-load")
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
	f.Float64Var(&o.maxLoad, "max-load", 0, "wait for, and require after each round, a 1-minute load average at most this (Linux, macOS)")
	f.DurationVar(&o.wait, "wait", 0, "with --max-load: total time to wait for the load to drop (e.g. 2h)")
	f.StringArrayVar(&o.baseArgs, "base-arg", nil, "extra argument for BASE only, after the shared ones (repeatable)")
	f.StringArrayVar(&o.headArgs, "head-arg", nil, "extra argument for HEAD only, after the shared ones (repeatable)")
	f.StringArrayVar(&o.baseRename, "rename-base", nil, "OLD=NEW: rename benchmarks in BASE's output (repeatable)")
	f.StringArrayVar(&o.headRename, "rename-head", nil, "OLD=NEW: rename benchmarks in HEAD's output (repeatable)")
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
	case o.loadGated && o.maxLoad <= 0:
		return fmt.Errorf("--max-load must be positive, got %v", o.maxLoad)
	case o.wait < 0:
		return fmt.Errorf("--wait must not be negative, got %v", o.wait)
	case o.wait > 0 && !o.loadGated:
		return errors.New("--wait needs --max-load")
	case renameErr(o) != nil:
		return renameErr(o)
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

	baseRen, headRen := renames(o.baseRename), renames(o.headRename)

	// The wait budget: time spent waiting for a quiet host, plus the time of
	// every round that had to be thrown away.
	budget := o.wait
	kept, discarded := 0, 0
	for attempt := 1; kept < o.rounds; attempt++ {
		if o.gated {
			if err := checkBusy(o.maxBusy, attempt); err != nil {
				return err
			}
		}
		var before float64
		if o.loadGated {
			start := now()
			l, err := loadavg.WaitFunc(cmd.Context(), readLoad, o.maxLoad, budget, loadPoll, func(l float64) {
				fmt.Fprintf(stderr, "abbench: load %.2f > %.2f, waiting\n", l, o.maxLoad)
			})
			budget -= now().Sub(start)
			if errors.Is(err, loadavg.ErrBusy) {
				return failf("wait budget %v spent with %d of %d rounds kept (%d discarded): %v", o.wait, kept, o.rounds, discarded, err)
			}
			if err != nil {
				return failf("--max-load: %v", err)
			}
			before = l
		}
		roundStart := now()
		// A round is kept or dropped as a pair: both outputs are held back
		// until the round is known to be good.
		var baseBuf, headBuf bytes.Buffer
		for _, side := range []struct {
			name, bin string
			extra     []string
			out       *bytes.Buffer
		}{{"base", o.base, o.baseArgs, &baseBuf}, {"head", o.head, o.headArgs, &headBuf}} {
			argv, err := pinArgv(o, side.bin, append(append([]string{}, args...), side.extra...))
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
					side.name, side.bin, kept+1, o.rounds, err, strings.TrimRight(errBuf.String(), "\n"))
			}
		}
		if o.loadGated {
			after, err := readLoad()
			if err != nil {
				return failf("--max-load: cannot read the load average: %v", err)
			}
			if after > o.maxLoad {
				discarded++
				budget -= now().Sub(roundStart)
				fmt.Fprintf(stderr, "abbench: round %d discarded: load %.2f before, %.2f after, limit %.2f\n", kept+1, before, after, o.maxLoad)
				if budget <= 0 {
					return failf("wait budget %v spent with %d of %d rounds kept (%d discarded)", o.wait, kept, o.rounds, discarded)
				}
				continue
			}
		}
		if _, err := baseOut.Write(rename(baseBuf.Bytes(), baseRen)); err != nil {
			return failf("%v", err)
		}
		if _, err := headOut.Write(rename(headBuf.Bytes(), headRen)); err != nil {
			return failf("%v", err)
		}
		kept++
	}
	fmt.Fprintf(cmd.OutOrStdout(), "abbench: %d rounds done, wrote %s and %s", o.rounds, basePath, headPath)
	if discarded > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), " (%d discarded)", discarded)
	}
	fmt.Fprintln(cmd.OutOrStdout())
	return nil
}

func renameErr(o options) error {
	if _, err := parseRenames(o.baseRename); err != nil {
		return fmt.Errorf("--rename-base: %w", err)
	}
	if _, err := parseRenames(o.headRename); err != nil {
		return fmt.Errorf("--rename-head: %w", err)
	}
	return nil
}

// A rewrite is one --rename-* OLD=NEW.
type rewrite struct{ old, new string }

func parseRenames(specs []string) ([]rewrite, error) {
	var rs []rewrite
	for _, s := range specs {
		old, new, ok := strings.Cut(s, "=")
		if !ok || old == "" {
			return nil, fmt.Errorf("a rename is OLD=NEW with OLD not empty, got %q", s)
		}
		rs = append(rs, rewrite{old, new})
	}
	return rs, nil
}

// renames is parseRenames for specs validate already accepted.
func renames(specs []string) []rewrite {
	rs, _ := parseRenames(specs)
	return rs
}

// rename rewrites the benchmark name, the first field of each line that starts
// with "Benchmark" (the testing package's result line, which benchstat reads),
// and leaves every other byte alone.
func rename(out []byte, rs []rewrite) []byte {
	if len(rs) == 0 {
		return out
	}
	lines := bytes.SplitAfter(out, []byte("\n"))
	var res bytes.Buffer
	for _, ln := range lines {
		if !bytes.HasPrefix(ln, []byte("Benchmark")) {
			res.Write(ln)
			continue
		}
		end := bytes.IndexAny(ln, " \t\r\n")
		if end < 0 {
			end = len(ln)
		}
		name := string(ln[:end])
		for _, r := range rs {
			name = strings.ReplaceAll(name, r.old, r.new)
		}
		res.WriteString(name)
		res.Write(ln[end:])
	}
	return res.Bytes()
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
