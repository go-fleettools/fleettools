package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
)

// fake is a scripted host and toolchain. Nothing runs, nothing sleeps.
type fake struct {
	machine   string
	goList    string
	buildFail map[string]bool // import path -> go test -c fails
	noBinary  map[string]bool // import path -> go test -c writes nothing
	runCode   map[string]int  // binary name (p0) -> exit status of the run
	runOut    map[string]string
	sshDown   int // the first N ssh/scp calls answer 255
	calls     []string
	slept     int
	statCalls int
}

func (f *fake) install(t *testing.T) {
	t.Helper()
	oldExe, oldSleep, oldNow := exe, sleep, now
	t.Cleanup(func() { exe, sleep, now = oldExe, oldSleep, oldNow })
	sleep = func(time.Duration) { f.slept++ }
	now = func() time.Time { return time.Date(2026, 11, 22, 15, 0, 0, 0, time.UTC) }
	exe = func(_ context.Context, env []string, dir, name string, args ...string) cmdResult {
		f.calls = append(f.calls, name+" "+strings.Join(args, " "))
		switch name {
		case "go":
			if args[0] == "list" {
				return cmdResult{out: []byte(f.goList)}
			}
			ip := args[len(args)-1]
			if f.buildFail[ip] {
				return cmdResult{out: []byte("undefined: Foo"), code: 1}
			}
			if !f.noBinary[ip] {
				for i, a := range args {
					if a == "-o" {
						os.WriteFile(args[i+1], []byte("elf"), 0o700)
					}
				}
			}
			return cmdResult{}
		case "scp":
			if f.sshDown > 0 {
				f.sshDown--
				return cmdResult{out: []byte("timed out"), code: 255}
			}
			return cmdResult{}
		case "ssh":
			if f.sshDown > 0 {
				f.sshDown--
				return cmdResult{out: []byte("Connection timed out during banner exchange"), code: 255}
			}
			remote := args[len(args)-1]
			switch {
			case remote == "uname -m":
				return cmdResult{out: []byte(f.machine + "\n")}
			case remote == "head -1 /proc/stat":
				f.statCalls++
				if f.statCalls == 1 {
					return cmdResult{out: []byte("cpu 100 0 100 700 0 0 0 100 0 0\n")}
				}
				return cmdResult{out: []byte("cpu 200 0 200 1400 0 0 0 200 0 0\n")}
			case remote == "cat /proc/loadavg":
				return cmdResult{out: []byte("0.10 0.20 0.30 1/200 99\n")}
			case strings.Contains(remote, "./p"):
				for n, code := range f.runCode {
					if strings.Contains(remote, "./"+n+".test") {
						return cmdResult{out: []byte(f.runOut[n]), code: code}
					}
				}
			}
			return cmdResult{}
		}
		t.Fatalf("unexpected command %s", name)
		return cmdResult{}
	}
}

func drive(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var o, e bytes.Buffer
	code := cli.Drive(RootCmd(), append(args, t.TempDir()+"/clone"), &o, &e)
	return code, o.String(), e.String()
}

func base(t *testing.T) []string {
	return []string{"--host", "linuxone", "--arch", "s390x", "--work", t.TempDir(), "--remote-dir", "scratch"}
}

func TestEveryOutcomeIsItsOwnStatus(t *testing.T) {
	f := &fake{
		machine: "s390x",
		goList:  "ex/a\t/x/a\nex/b\t/x/b\nex/c\t/x/c\nex/d\t/x/d\nex/e\t/x/e\n",
		buildFail: map[string]bool{
			"ex/b": true,
		},
		noBinary: map[string]bool{"ex/c": true},
		runCode:  map[string]int{"p0": 0, "p3": 124, "p4": 1},
		runOut:   map[string]string{"p4": "--- FAIL: TestX\nwrong output"},
	}
	f.install(t)
	code, out, _ := drive(t, base(t)...)
	if code != 1 {
		t.Fatalf("code %d, want 1 (a failure is a finding)\n%s", code, out)
	}
	for _, want := range []string{
		"PASS    ex/a", "BUILD   ex/b", "SKIP    ex/c", "TIMEOUT ex/d", "FAIL    ex/e",
		"1 PASS, 1 FAIL, 1 TIMEOUT, 1 BUILD, 1 SKIP, 0 ERROR",
		"wrong output", "undefined: Foo",
		"steal 10.0% over the run", "load 0.10 0.20 0.30",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q\n%s", want, out)
		}
	}
}

func TestAllPassIsZeroAndTheRemoteDirIsDeleted(t *testing.T) {
	f := &fake{machine: "s390x", goList: "ex/a\t/x/a\n", runCode: map[string]int{"p0": 0}}
	f.install(t)
	code, out, _ := drive(t, base(t)...)
	if code != 0 {
		t.Fatalf("code %d\n%s", code, out)
	}
	var removed bool
	for _, c := range f.calls {
		if strings.HasSuffix(c, "rm -rf scratch") {
			removed = true
		}
	}
	if !removed {
		t.Fatalf("scratch dir not removed: %v", f.calls)
	}
}

func TestKeepLeavesTheRemoteDir(t *testing.T) {
	f := &fake{machine: "s390x", goList: "ex/a\t/x/a\n"}
	f.install(t)
	drive(t, append(base(t), "--keep")...)
	for _, c := range f.calls {
		if strings.HasSuffix(c, "rm -rf scratch") {
			t.Fatalf("--keep removed the directory")
		}
	}
}

func TestTheWrongMachineIsRefusedBeforeAnythingRuns(t *testing.T) {
	f := &fake{machine: "x86_64", goList: "ex/a\t/x/a\n"}
	f.install(t)
	code, _, errOut := drive(t, base(t)...)
	if code != 2 || !strings.Contains(errOut, "wrong architecture") {
		t.Fatalf("code %d, err %q", code, errOut)
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c, "go ") {
			t.Fatalf("compiled before the host was checked: %s", c)
		}
	}
}

func TestAConnectionFailureIsRetriedThenCalledError(t *testing.T) {
	f := &fake{machine: "s390x", goList: "ex/a\t/x/a\n", sshDown: 2}
	f.install(t)
	code, _, _ := drive(t, base(t)...)
	if code != 0 || f.slept != 2 {
		t.Fatalf("a banner timeout twice then success: code %d, slept %d", code, f.slept)
	}
	g := &fake{machine: "s390x", goList: "ex/a\t/x/a\n", sshDown: 1000}
	g.install(t)
	code, _, errOut := drive(t, base(t)...)
	if code != 2 || !strings.Contains(errOut, "cannot reach") {
		t.Fatalf("unreachable host: code %d, err %q", code, errOut)
	}
}

func TestARemoteStatusOf255IsNotRetriedAsAFailureOfTheCode(t *testing.T) {
	// A test binary that itself exits 1 is FAIL once, never re-run.
	f := &fake{machine: "s390x", goList: "ex/a\t/x/a\n", runCode: map[string]int{"p0": 1}}
	f.install(t)
	drive(t, base(t)...)
	runs := 0
	for _, c := range f.calls {
		if strings.Contains(c, "./p0.test") {
			runs++
		}
	}
	if runs != 1 {
		t.Fatalf("binary ran %d times", runs)
	}
}

func TestBenchWritesRawOutputAndPassesBenchFlags(t *testing.T) {
	f := &fake{machine: "s390x", goList: "ex/a\t/x/a\n", runCode: map[string]int{"p0": 0}, runOut: map[string]string{"p0": "BenchmarkX 10 5 ns/op\n"}}
	f.install(t)
	dir := t.TempDir()
	rp := filepath.Join(dir, "r.json")
	code, _, _ := drive(t, append(base(t), "--bench", "Scalar|SIMD", "--report", rp)...)
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	b, err := os.ReadFile(filepath.Join(dir, "ex_a.bench.txt"))
	if err != nil || !strings.Contains(string(b), "BenchmarkX") {
		t.Fatalf("bench output: %q, %v", b, err)
	}
	var seen bool
	for _, c := range f.calls {
		if strings.Contains(c, "-test.bench='Scalar|SIMD'") && strings.Contains(c, "-test.run='^$'") {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("no bench invocation in %v", f.calls)
	}
	if _, err := os.Stat(rp); err != nil {
		t.Fatalf("no report: %v", err)
	}
}

func TestOnlyAndSkipSelectPackages(t *testing.T) {
	f := &fake{machine: "s390x", goList: "ex/a\t/x/a\nex/b\t/x/b\nex/c\t/x/c\n"}
	f.install(t)
	_, out, _ := drive(t, append(base(t), "--only", "ex/", "--skip", "ex/b")...)
	if strings.Contains(out, "ex/b") || !strings.Contains(out, "ex/a") || !strings.Contains(out, "ex/c") {
		t.Fatalf("selection wrong:\n%s", out)
	}
}

func TestNoPackagesIsARefusalNotAPass(t *testing.T) {
	f := &fake{machine: "s390x", goList: "\n"}
	f.install(t)
	code, _, _ := drive(t, base(t)...)
	if code != 2 {
		t.Fatalf("code %d, want 2: nothing ran is no answer", code)
	}
}

func TestUnsafeInputsAreRefused(t *testing.T) {
	for name, args := range map[string][]string{
		"host with option": {"--host", "-oProxyCommand=x", "--arch", "s390x"},
		"host with shell":  {"--host", "a;b", "--arch", "s390x"},
		"unknown arch":     {"--host", "h", "--arch", "vax"},
		"dir with shell":   {"--host", "h", "--arch", "s390x", "--remote-dir", "a;rm -rf ~"},
		"dir dotdot":       {"--host", "h", "--arch", "s390x", "--remote-dir", "a/../b"},
		"memlimit":         {"--host", "h", "--arch", "s390x", "--memlimit", "2GiB;x"},
		"bench shell":      {"--host", "h", "--arch", "s390x", "--bench", "x'; rm"},
		"benchtime shell":  {"--host", "h", "--arch", "s390x", "--benchtime", "1s;rm"},
		"retries":          {"--host", "h", "--arch", "s390x", "--retries", "0"},
		"timeout":          {"--host", "h", "--arch", "s390x", "--timeout", "10ms"},
		"bad regexp":       {"--host", "h", "--arch", "s390x", "--only", "("},
		"work in git":      {"--host", "h", "--arch", "s390x", "--work", "/x/.git/y"},
		"port":             {"--host", "h", "--arch", "s390x", "--port", "70000"},
	} {
		f := &fake{machine: "s390x", goList: "ex/a\t/x/a\n"}
		f.install(t)
		if code, _, _ := drive(t, args...); code != 2 {
			t.Errorf("%s: code %d, want 2", name, code)
		}
		if len(f.calls) != 0 {
			t.Errorf("%s: ran %v before refusing", name, f.calls)
		}
	}
}

func TestStealBetween(t *testing.T) {
	a := []uint64{100, 0, 100, 700, 0, 0, 0, 100}
	b := []uint64{200, 0, 200, 1400, 0, 0, 0, 200}
	if s, ok := stealBetween(a, b); !ok || s != 10 {
		t.Fatalf("got %v %v", s, ok)
	}
	if _, ok := stealBetween(a, a); ok {
		t.Fatal("no ticks passed must be no reading")
	}
	if _, ok := stealBetween(b, a); ok {
		t.Fatal("a counter that went back must be no reading")
	}
	if _, ok := stealBetween(a[:3], b); ok {
		t.Fatal("a short line must be no reading")
	}
	if parseCPU("cpu0 1 2") != nil || parseCPU("cpu 1 2 3 4 5 6 7 x") != nil || parseCPU("") != nil {
		t.Fatal("malformed lines must not parse")
	}
}

func TestTailKeepsTheEnd(t *testing.T) {
	long := strings.Repeat("a", tailCap) + "END"
	got := tail(long)
	if !strings.HasSuffix(got, "END") || !strings.HasPrefix(got, "…") {
		t.Fatalf("tail %q", got[:10])
	}
	if tail("  x \n") != "x" {
		t.Fatal("short output must be trimmed, not cut")
	}
}

func TestExecuteReportsStatusAndOutput(t *testing.T) {
	r := execute(context.Background(), []string{"X=1"}, "", "sh", "-c", "echo hi; exit 3")
	if r.code != 3 || !strings.Contains(string(r.out), "hi") {
		t.Fatalf("got %d %q", r.code, r.out)
	}
	r = execute(context.Background(), nil, "", "/no/such/program")
	if r.code != -1 || len(r.out) == 0 {
		t.Fatalf("a program that did not start: %d %q", r.code, r.out)
	}
	old := outCap
	outCap = 4
	defer func() { outCap = old }()
	r = execute(context.Background(), nil, "", "sh", "-c", "echo 123456789")
	if len(r.out) != 4 {
		t.Fatalf("output not capped: %q", r.out)
	}
}

func TestHelpIsNotARun(t *testing.T) {
	f := &fake{machine: "s390x"}
	f.install(t)
	var o, e bytes.Buffer
	if code := cli.Drive(RootCmd(), []string{"--help"}, &o, &e); code != 0 || len(f.calls) != 0 {
		t.Fatalf("code %d, calls %v", code, f.calls)
	}
}
