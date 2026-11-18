package main

import (
	"bytes"
	"errors"
	"io/fs"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/procstat"
)

var t0 = time.Date(2026, 11, 18, 21, 42, 0, 0, time.UTC)

// rig installs a clock whose sleeps advance it, and a load source that answers
// the given values in order, then repeats the last one. Nothing really sleeps.
func rig(t *testing.T, loads ...float64) (reads *int, slept *time.Duration) {
	t.Helper()
	clock := t0
	var n int
	var total time.Duration
	oldNow, oldSleep, oldLoad, oldStat := now, sleep, readLoad, readStat
	t.Cleanup(func() { now, sleep, readLoad, readStat = oldNow, oldSleep, oldLoad, oldStat })
	now = func() time.Time { return clock }
	sleep = func(d time.Duration) { clock = clock.Add(d); total += d }
	readLoad = func() (float64, error) {
		i := n
		if i >= len(loads) {
			i = len(loads) - 1
		}
		n++
		return loads[i], nil
	}
	readStat = func(string) (procstat.Snapshot, error) {
		t.Fatal("/proc/stat read without --max-busy")
		return procstat.Snapshot{}, nil
	}
	return &n, &total
}

func drive(args ...string) (int, string, string) {
	var o, e bytes.Buffer
	code := cli.Drive(RootCmd(), args, &o, &e)
	return code, o.String(), e.String()
}

func TestPassesAfterSettlingReadings(t *testing.T) {
	reads, slept := rig(t, 20, 12, 7, 6.2, 6.0)
	code, out, _ := drive("--max-load", "8", "--timeout", "1h", "--settle", "3")
	if code != 0 {
		t.Fatalf("code %d, out %q", code, out)
	}
	if *reads != 5 {
		t.Fatalf("%d readings, want 5", *reads)
	}
	if *slept != 40*time.Second {
		t.Fatalf("slept %s, want 40s", *slept)
	}
	if want := "waitload: load1 6.0 ≤ 8 for 3 readings after 40s\n"; out != want {
		t.Fatalf("out %q, want %q", out, want)
	}
}

func TestADipDoesNotPass(t *testing.T) {
	// 7, 7, then a spike, then three quiet ones: the spike resets the count.
	reads, _ := rig(t, 7, 7, 30, 7, 7, 7)
	code, _, _ := drive("--max-load", "8", "--timeout", "1h")
	if code != 0 || *reads != 6 {
		t.Fatalf("code %d after %d readings, want 0 after 6", code, *reads)
	}
}

func TestBoundaryIsAtMost(t *testing.T) {
	rig(t, 8)
	if code, _, _ := drive("--max-load", "8", "--timeout", "1h", "--settle", "1"); code != 0 {
		t.Fatalf("a load equal to the limit must pass, code %d", code)
	}
	rig(t, 8.01)
	if code, _, _ := drive("--max-load", "8", "--timeout", "1m", "--settle", "1"); code != 1 {
		t.Fatalf("a load above the limit must not pass, code %d", code)
	}
}

func TestTimeoutReportsLastAndLowest(t *testing.T) {
	_, _ = rig(t, 30, 12, 20)
	code, out, _ := drive("--max-load", "8", "--timeout", "30s")
	if code != 1 {
		t.Fatalf("code %d, out %q", code, out)
	}
	for _, want := range []string{"TIMEOUT", "last load1 20.00", "lowest load1 12.00", "4 reading(s)"} {
		if !strings.Contains(out, want) {
			t.Errorf("out %q lacks %q", out, want)
		}
	}
}

func TestTimeoutIsBoundedByElapsedTime(t *testing.T) {
	_, slept := rig(t, 50)
	drive("--max-load", "8", "--timeout", "95s")
	if *slept > 95*time.Second {
		t.Fatalf("slept %s, past a 95s timeout", *slept)
	}
}

func TestCountBoundHoldsWhenTheClockStands(t *testing.T) {
	// A clock that never advances defeats the deadline; the count must still end it.
	reads, _ := rig(t, 50)
	sleep = func(time.Duration) {}
	code, _, _ := drive("--max-load", "8", "--timeout", "1m", "--interval", "10s")
	if code != 1 {
		t.Fatalf("code %d", code)
	}
	if *reads != 7 {
		t.Fatalf("%d readings, want 7 (60s/10s + 1)", *reads)
	}
}

func TestMaxReadingsNeverOverflows(t *testing.T) {
	for _, tc := range []struct {
		timeout, interval time.Duration
		want              int
	}{
		{time.Minute, 10 * time.Second, 7},
		{time.Second, time.Hour, 1},
		{time.Duration(math.MaxInt64), time.Millisecond, 1<<32 + 1},
		{time.Duration(math.MaxInt64), time.Hour, int(math.MaxInt64/int64(time.Hour)) + 1},
	} {
		if got := maxReadings(tc.timeout, tc.interval); got != tc.want {
			t.Errorf("maxReadings(%s,%s) = %d, want %d", tc.timeout, tc.interval, got, tc.want)
		}
	}
}

func TestRefusals(t *testing.T) {
	for _, args := range [][]string{
		{"--timeout", "1m"},                      // no --max-load
		{"--max-load", "8"},                      // no --timeout
		{"--max-load", "0", "--timeout", "1m"},   // zero
		{"--max-load", "-1", "--timeout", "1m"},  // negative
		{"--max-load", "NaN", "--timeout", "1m"}, // NaN
		{"--max-load", "8", "--timeout", "0s"},   // not a wait
		{"--max-load", "8", "--timeout", "-1m"},  // not a wait
		{"--max-load", "8", "--timeout", "1m", "--settle", "0"},
		{"--max-load", "8", "--timeout", "1m", "--interval", "0s"},
		{"--max-load", "8", "--timeout", "1m", "--max-busy", "0"},
		{"--max-load", "8", "--timeout", "1m", "--max-busy", "101"},
		{"--max-load", "8", "--timeout", "1m", "--max-busy", "NaN"},
		{"--max-load", "8", "--timeout", "1m", "extra"},
	} {
		reads, _ := rig(t, 1)
		if code, _, _ := drive(args...); code != 2 {
			t.Errorf("%v: code %d, want 2", args, code)
		}
		if *reads != 0 {
			t.Errorf("%v: read the load before refusing", args)
		}
	}
}

func TestUnreadableOrNaNLoadRefuses(t *testing.T) {
	rig(t, 1)
	readLoad = func() (float64, error) { return 0, errors.New("sysctl failed") }
	if code, _, e := drive("--max-load", "8", "--timeout", "1m"); code != 2 || !strings.Contains(e, "sysctl failed") {
		t.Fatalf("code %d, err %q", code, e)
	}
	for _, v := range []float64{math.NaN(), math.Inf(1), -1} {
		rig(t, v)
		if code, out, _ := drive("--max-load", "8", "--timeout", "1m", "--settle", "1"); code != 2 {
			t.Errorf("load %v: code %d, out %q, want 2", v, code, out)
		}
	}
}

// stat builds a snapshot with the given busy and idle ticks.
func stat(busy, idle uint64) procstat.Snapshot {
	return procstat.Snapshot{All: procstat.Times{User: busy, Idle: idle}}
}

func TestMaxBusyMustAlsoHold(t *testing.T) {
	reads, _ := rig(t, 1)
	snaps := []procstat.Snapshot{
		stat(0, 0),     // baseline
		stat(90, 10),   // 90% busy: too busy
		stat(100, 100), // 10/100 = 10%
		stat(110, 190), // 10%
		stat(120, 280), // 10%
	}
	i := 0
	readStat = func(string) (procstat.Snapshot, error) { s := snaps[i]; i++; return s, nil }
	code, out, _ := drive("--max-load", "8", "--max-busy", "20", "--timeout", "1h")
	if code != 0 {
		t.Fatalf("code %d, out %q", code, out)
	}
	if *reads != 4 {
		t.Fatalf("%d readings, want 4: the first was too busy", *reads)
	}
	if !strings.Contains(out, "busy 10.0% ≤ 20%") {
		t.Fatalf("out %q does not report the busy share", out)
	}
}

func TestBusyAbsentIsARefusalNotAPass(t *testing.T) {
	rig(t, 1)
	readStat = func(string) (procstat.Snapshot, error) { return procstat.Snapshot{}, fs.ErrNotExist }
	code, out, e := drive("--max-load", "8", "--max-busy", "50", "--timeout", "1m")
	if code != 2 || out != "" || !strings.Contains(e, "does not have") {
		t.Fatalf("code %d out %q err %q", code, out, e)
	}
}

func TestFrozenProcStatIsNotIdle(t *testing.T) {
	rig(t, 1)
	readStat = func(string) (procstat.Snapshot, error) { return stat(5, 5), nil }
	if code, _, _ := drive("--max-load", "8", "--max-busy", "50", "--timeout", "1m"); code != 2 {
		t.Fatalf("a /proc/stat that did not advance must refuse, code %d", code)
	}
}
