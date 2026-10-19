package procstat

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Two real readings of cfarm421 (a Zen 3 guest) 3 s apart, cut to two CPUs:
// the aggregate is a third niced work, which the load average did not show.
const before = `cpu  29443426 542705544 154657032 27106886870 13868182 0 3186060 14211720 0 0
cpu0 100 0 50 850 0 0 0 0 0 0
cpu1 900 0 50 50 0 0 0 0 0 0
intr 1 2 3
ctxt 99
`

const after = `cpu  29443426 542715176 154657033 27106902911 13868182 0 3186061 14211726 0 0
cpu0 110 0 55 935 0 0 0 0 0 0
cpu1 990 0 55 55 0 0 0 0 0 0
intr 1 2 3
`

func near(a, b float64) bool { return math.Abs(a-b) < 1e-3 }

func TestBusyIsNotIdleNotIOWait(t *testing.T) {
	a, err := Parse([]byte(before))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Parse([]byte(after))
	if err != nil {
		t.Fatal(err)
	}
	u := Between(a.All, b.All)
	// ticks: nice 9632, system 1, idle 16041, softirq 1, steal 6: 25681 total.
	if !near(u.Busy, 1-16041.0/25681) || !near(u.Nice, 9632.0/25681) || !near(u.Steal, 6.0/25681) {
		t.Fatalf("aggregate %+v", u)
	}
	cs := PerCPU(a, b)
	if len(cs) != 2 || cs[0].ID != 0 || cs[1].ID != 1 {
		t.Fatalf("order %+v: want the idle cpu0 first", cs)
	}
	if !near(cs[0].Busy, 15.0/100) || !near(cs[1].Busy, 95.0/100) {
		t.Fatalf("per cpu %+v", cs)
	}
	if got := Pick(cs, 1); got != "0" {
		t.Fatalf("pick 1 = %q", got)
	}
	if got := Pick(cs, 5); got != "0,1" {
		t.Fatalf("pick 5 of 2 = %q", got)
	}
}

// I/O wait is free CPU: a host whose only activity is waiting on a disk is
// idle for a benchmark. The Loongson whose load average read 146 was this.
func TestIOWaitCountsAsIdle(t *testing.T) {
	a, _ := Parse([]byte("cpu 0 0 0 100 0 0 0 0\ncpu0 0 0 0 100 0 0 0 0\n"))
	b, _ := Parse([]byte("cpu 0 0 0 150 50 0 0 0\ncpu0 0 0 0 150 50 0 0 0\n"))
	if u := Between(a.All, b.All); u.Busy != 0 || !near(u.IOWait, 0.5) {
		t.Fatalf("%+v", u)
	}
}

func TestTiesPickTheLowerNumber(t *testing.T) {
	a, _ := Parse([]byte("cpu 0 0 0 0\ncpu7 0 0 0 0\ncpu3 0 0 0 0\n"))
	b, _ := Parse([]byte("cpu 0 0 0 20\ncpu7 0 0 0 10\ncpu3 0 0 0 10\n"))
	if got := Pick(PerCPU(a, b), 2); got != "3,7" {
		t.Fatalf("pick = %q", got)
	}
}

func TestOldKernelWithoutSteal(t *testing.T) {
	s, err := Parse([]byte("cpu 1 2 3 4\n"))
	if err != nil || s.All.Idle != 4 || s.All.Steal != 0 {
		t.Fatalf("%+v %v", s.All, err)
	}
}

func TestNoTicksIsNoUsage(t *testing.T) {
	if u := Between(Times{}, Times{}); u != (Usage{}) {
		t.Fatalf("%+v", u)
	}
}

func TestMalformed(t *testing.T) {
	for _, in := range []string{
		"intr 1 2\n",                  // no aggregate line
		"cpu 1 x 3 4\n",               // a field that is not a number
		"cpu 1 2 3 4\ncpuX 1 2 3 4\n", // a cpu line that is not numbered
		strings.Repeat("x", 1<<20),    // a line the scanner cannot hold
	} {
		if _, err := Parse([]byte(in)); err == nil {
			t.Errorf("%.20q: accepted", in)
		}
	}
}

func TestReadFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "stat")
	if err := os.WriteFile(p, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := ReadFile(p)
	if err != nil || len(s.CPUs) != 2 {
		t.Fatalf("%+v %v", s, err)
	}
	if _, err := ReadFile(p + ".missing"); err == nil {
		t.Error("a missing file was read")
	}
}
