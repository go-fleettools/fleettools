package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/go-fleettools/fleettools/internal/procstat"
)

// The fake base, head and taskset are this very test binary, reached through
// symlinks of those names (the helper-process pattern): what a process does is
// decided by the name it was started under.
func TestMain(m *testing.M) {
	switch role := filepath.Base(os.Args[0]); role {
	case "base.test", "head.test":
		helper(strings.TrimSuffix(role, ".test"))
	case "taskset":
		fakeTaskset()
	}
	os.Exit(m.Run())
}

// helper appends its role to $ABBENCH_LOG, which is how the tests see the order
// the runs happened in, then writes one line to stdout. $ABBENCH_FAIL=head:2
// makes the second run of head fail.
func helper(role string) {
	prior := 0
	if b, err := os.ReadFile(os.Getenv("ABBENCH_LOG")); err == nil {
		prior = strings.Count(string(b), role+"\n")
	}
	f, err := os.OpenFile(os.Getenv("ABBENCH_LOG"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(99)
	}
	fmt.Fprintln(f, role)
	f.Close()
	fmt.Fprintf(os.Stderr, "hello from %s\n", role)
	if os.Getenv("ABBENCH_FAIL") == fmt.Sprintf("%s:%d", role, prior+1) {
		fmt.Fprintln(os.Stderr, "boom")
		os.Exit(3)
	}
	fmt.Printf("%s round=%d GOMAXPROCS=%q args=%s\n", role, prior+1, os.Getenv("GOMAXPROCS"), strings.Join(os.Args[1:], " "))
	os.Exit(0)
}

// fakeTaskset is `taskset -c N prog args...`: it says which CPU it was asked
// for, then runs the program.
func fakeTaskset() {
	fmt.Printf("pinned %s\n", os.Args[2])
	c := exec.Command(os.Args[3], os.Args[4:]...)
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	if err := c.Run(); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

type bins struct{ base, head, taskset, log string }

func setup(t *testing.T) bins {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	b := bins{
		base:    filepath.Join(dir, "base.test"),
		head:    filepath.Join(dir, "head.test"),
		taskset: filepath.Join(dir, "taskset"),
		log:     filepath.Join(dir, "order.log"),
	}
	for _, p := range []string{b.base, b.head, b.taskset} {
		if err := os.Symlink(self, p); err != nil {
			t.Skipf("no symlinks here: %v", err)
		}
	}
	t.Setenv("ABBENCH_LOG", b.log)
	t.Setenv("ABBENCH_FAIL", "")
	return b
}

func drive(args ...string) (code int, stdout, stderr string) {
	var out, errb bytes.Buffer
	c := RootCmd()
	c.SetArgs(args)
	c.SetOut(&out)
	c.SetErr(&errb)
	code = execute(c)
	return code, out.String(), errb.String()
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAlternatesBaseThenHeadEveryRound(t *testing.T) {
	b := setup(t)
	out := t.TempDir()
	code, stdout, stderr := drive("--base", b.base, "--head", b.head, "--rounds", "3", "--out", out,
		"--", "-test.run", "^$", "-test.bench", "X")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if got, want := read(t, b.log), strings.Repeat("base\nhead\n", 3); got != want {
		t.Fatalf("order %q, want %q", got, want)
	}
	baseTxt, headTxt := read(t, filepath.Join(out, "base.txt")), read(t, filepath.Join(out, "head.txt"))
	for i, line := range strings.Split(strings.TrimSpace(baseTxt), "\n") {
		want := fmt.Sprintf(`base round=%d GOMAXPROCS="1" args=-test.run ^$ -test.bench X`, i+1)
		if line != want {
			t.Errorf("base.txt line %d = %q, want %q", i+1, line, want)
		}
	}
	if strings.Contains(baseTxt, "head") || strings.Contains(headTxt, "base") || strings.Count(headTxt, "\n") != 3 {
		t.Errorf("outputs crossed: base.txt %q head.txt %q", baseTxt, headTxt)
	}
	if n := strings.Count(stderr, "hello from base"); n != 3 {
		t.Errorf("child stderr reached abbench's stderr %d times, want 3: %q", n, stderr)
	}
	want := fmt.Sprintf("abbench: 3 rounds done, wrote %s and %s\n", filepath.Join(out, "base.txt"), filepath.Join(out, "head.txt"))
	if stdout != want {
		t.Errorf("summary %q, want %q", stdout, want)
	}
}

func TestOutputsAreAppendedAndTheDirectoryCreated(t *testing.T) {
	b := setup(t)
	out := filepath.Join(t.TempDir(), "new", "dir")
	for range 2 {
		if code, _, stderr := drive("-b", b.base, "-H", b.head, "-n", "1", "-o", out); code != 0 {
			t.Fatalf("exit %d: %s", code, stderr)
		}
	}
	if n := strings.Count(read(t, filepath.Join(out, "base.txt")), "\n"); n != 2 {
		t.Fatalf("base.txt has %d lines after two runs, want 2 (appended)", n)
	}
}

func TestGOMAXPROCSReachesTheChild(t *testing.T) {
	b := setup(t)
	t.Setenv("GOMAXPROCS", "7")
	for _, tc := range []struct{ flag, want string }{
		{"4", `"4"`}, // set, over the inherited 7
		{"0", `"7"`}, // 0 leaves the environment alone
	} {
		out := t.TempDir()
		if code, _, stderr := drive("-b", b.base, "-H", b.head, "-n", "1", "-g", tc.flag, "-o", out); code != 0 {
			t.Fatalf("exit %d: %s", code, stderr)
		}
		if got := read(t, filepath.Join(out, "head.txt")); !strings.Contains(got, "GOMAXPROCS="+tc.want) {
			t.Errorf("--gomaxprocs %s: head saw %q, want GOMAXPROCS=%s", tc.flag, got, tc.want)
		}
	}
	// Default is 1.
	out := t.TempDir()
	drive("-b", b.base, "-H", b.head, "-n", "1", "-o", out)
	if got := read(t, filepath.Join(out, "base.txt")); !strings.Contains(got, `GOMAXPROCS="1"`) {
		t.Errorf("default: %q", got)
	}
}

func TestAFailingChildStopsTheRun(t *testing.T) {
	b := setup(t)
	t.Setenv("ABBENCH_FAIL", "head:2")
	out := t.TempDir()
	code, stdout, stderr := drive("-b", b.base, "-H", b.head, "-n", "5", "-o", out)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if stdout != "" {
		t.Errorf("a failed run printed a summary: %q", stdout)
	}
	for _, want := range []string{"head binary", b.head, "round 2 of 5", "exit status 3", "boom"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	if got, want := read(t, b.log), "base\nhead\nbase\nhead\n"; got != want {
		t.Errorf("ran %q after the failure, want it to stop at %q", got, want)
	}
	if n := strings.Count(read(t, filepath.Join(out, "base.txt")), "\n"); n != 2 {
		t.Errorf("base.txt has %d lines, want 2", n)
	}
}

func TestABinaryThatCannotStartIsAFailure(t *testing.T) {
	b := setup(t)
	code, _, stderr := drive("-b", b.base+".missing", "-H", b.head, "-o", t.TempDir())
	if code != 1 || !strings.Contains(stderr, "base binary") || !strings.Contains(stderr, "round 1 of 8") {
		t.Fatalf("exit %d: %s", code, stderr)
	}
}

func TestUsageErrorsExitTwo(t *testing.T) {
	b := setup(t)
	good := []string{"-b", b.base, "-H", b.head, "-o", t.TempDir()}
	with := func(extra ...string) []string { return append(append([]string{}, good...), extra...) }
	for name, args := range map[string][]string{
		"nothing":              {},
		"no head":              {"-b", b.base},
		"no base":              {"-H", b.head},
		"unknown flag":         with("--nope"),
		"zero rounds":          with("-n", "0"),
		"negative gomaxprocs":  with("-g", "-1"),
		"negative cpu":         with("-c", "-1"),
		"max-busy over 100":    with("-m", "101"),
		"max-busy negative":    with("-m", "-1"),
		"args before the dash": with("-test.run", "x"),
		"positional":           with("extra"),
	} {
		t.Run(name, func(t *testing.T) {
			if code, _, stderr := drive(args...); code != 2 || !strings.HasPrefix(stderr, "abbench: ") {
				t.Errorf("exit %d, stderr %q; want 2", code, stderr)
			}
		})
	}
}

func TestHelpIsAnAnswerNotAnError(t *testing.T) {
	code, stdout, _ := drive("--help")
	if code != 0 || !strings.Contains(stdout, "--max-busy") || !strings.Contains(stdout, "benchstat") {
		t.Fatalf("exit %d: %s", code, stdout)
	}
}

func TestLinuxOnlyFlagsAreRefusedElsewhere(t *testing.T) {
	b := setup(t)
	old := goos
	goos = "plan9"
	defer func() { goos = old }()
	for _, extra := range [][]string{{"-c", "1"}, {"-m", "10"}} {
		args := append([]string{"-b", b.base, "-H", b.head, "-o", t.TempDir()}, extra...)
		if code, _, stderr := drive(args...); code != 2 || !strings.Contains(stderr, "Linux only") {
			t.Errorf("%v: exit %d: %s", extra, code, stderr)
		}
	}
}

func TestOutputProblemsAreFailures(t *testing.T) {
	b := setup(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "afile")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	baseDir, headDir := filepath.Join(dir, "b"), filepath.Join(dir, "h")
	for _, d := range []string{filepath.Join(baseDir, "base.txt"), filepath.Join(headDir, "head.txt")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, out := range map[string]string{
		"out is a file":     file,
		"base.txt is a dir": baseDir,
		"head.txt is a dir": headDir,
		"out under a file":  filepath.Join(file, "x"),
	} {
		if code, _, stderr := drive("-b", b.base, "-H", b.head, "-o", out); code != 1 {
			t.Errorf("%s: exit %d: %s", name, code, stderr)
		}
	}
}

func TestPinningGoesThroughTaskset(t *testing.T) {
	b := setup(t)
	oldGoos, oldLook := goos, lookPath
	goos = "linux"
	lookPath = func(string) (string, error) { return b.taskset, nil }
	defer func() { goos, lookPath = oldGoos, oldLook }()

	out := t.TempDir()
	code, _, stderr := drive("-b", b.base, "-H", b.head, "-n", "1", "-c", "3", "-o", out, "--", "-x")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	for _, f := range []string{"base.txt", "head.txt"} {
		got := read(t, filepath.Join(out, f))
		if !strings.HasPrefix(got, "pinned 3\n") || !strings.Contains(got, "args=-x") {
			t.Errorf("%s = %q", f, got)
		}
	}

	lookPath = func(string) (string, error) { return "", errors.New("not found") }
	if code, _, stderr := drive("-b", b.base, "-H", b.head, "-c", "3", "-o", t.TempDir()); code != 1 || !strings.Contains(stderr, "taskset") {
		t.Errorf("no taskset: exit %d: %s", code, stderr)
	}
}

func TestMaxBusyRefusesALoadedHost(t *testing.T) {
	b := setup(t)
	oldGoos, oldRead, oldSleep := goos, readStat, sleep
	goos = "linux"
	var slept time.Duration
	sleep = func(d time.Duration) { slept += d }
	defer func() { goos, readStat, sleep = oldGoos, oldRead, oldSleep }()

	idle := procstat.Snapshot{All: procstat.Times{Idle: 100}}
	later := func(busy uint64) procstat.Snapshot {
		return procstat.Snapshot{All: procstat.Times{Idle: 100 + 100 - busy, User: busy}}
	}
	feed := func(snaps ...procstat.Snapshot) {
		i := 0
		readStat = func() (procstat.Snapshot, error) {
			s := snaps[i]
			i++
			return s, nil
		}
	}
	args := func() []string { return []string{"-b", b.base, "-H", b.head, "-n", "2", "-m", "25", "-o", t.TempDir()} }

	// Quiet before both rounds: runs, and looked twice, a second apart, per round.
	feed(idle, later(10), idle, later(10))
	if code, _, stderr := drive(args()...); code != 0 {
		t.Fatalf("quiet host: exit %d: %s", code, stderr)
	}
	if slept != 2*time.Second {
		t.Errorf("slept %v, want 1s per round", slept)
	}

	// Busy before round 1: refuses before anything runs.
	if err := os.WriteFile(b.log, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	feed(idle, later(90))
	code, _, stderr := drive(args()...)
	if code != 1 || !strings.Contains(stderr, "busy 90.0% > 25.0% before round 1") {
		t.Errorf("loaded host: exit %d: %s", code, stderr)
	}
	if got := read(t, b.log); got != "" {
		t.Errorf("ran %q on a loaded host", got)
	}

	// Quiet for round 1, loaded for round 2.
	feed(idle, later(10), idle, later(90))
	if code, _, stderr := drive(args()...); code != 1 || !strings.Contains(stderr, "before round 2") {
		t.Errorf("round 2: exit %d: %s", code, stderr)
	}

	// /proc/stat unreadable, at either reading.
	boom := errors.New("no /proc")
	readStat = func() (procstat.Snapshot, error) { return procstat.Snapshot{}, boom }
	if code, _, stderr := drive(args()...); code != 1 || !strings.Contains(stderr, "no /proc") {
		t.Errorf("first read: exit %d: %s", code, stderr)
	}
	n := 0
	readStat = func() (procstat.Snapshot, error) {
		n++
		if n == 2 {
			return procstat.Snapshot{}, boom
		}
		return idle, nil
	}
	if code, _, stderr := drive(args()...); code != 1 || !strings.Contains(stderr, "no /proc") {
		t.Errorf("second read: exit %d: %s", code, stderr)
	}
}

func TestTheRealReadingIsProcStat(t *testing.T) {
	s, err := procStat()
	if runtime.GOOS == "linux" && (err != nil || len(s.CPUs) == 0) {
		t.Fatalf("/proc/stat: %v %+v", err, s)
	}
}

func TestFailureUnwraps(t *testing.T) {
	inner := errors.New("inner")
	if err := failf("wrapped: %w", inner); !errors.Is(err, inner) {
		t.Fatal("failure does not unwrap")
	}
}

func TestMainExitsWithTheStatusOfTheRun(t *testing.T) {
	// WarnIfStale shells out to gh; with an empty PATH it says it could not tell.
	t.Setenv("PATH", t.TempDir())
	oldArgs, oldExit := os.Args, osExit
	defer func() { os.Args, osExit = oldArgs, oldExit }()
	got := -1
	osExit = func(c int) { got = c }
	os.Args = []string{"abbench", "--help"}
	main()
	if got != 0 {
		t.Fatalf("exit %d", got)
	}
}
