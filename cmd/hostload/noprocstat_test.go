package main

import (
	"bytes"
	"errors"
	"io/fs"
	"runtime"
	"strings"
	"testing"

	"github.com/go-fleettools/fleettools/internal/procstat"
)

// ⛔ EVERY TEST HERE DRIVES THE SEAMS, never the host. The point of the change
// is what hostload does where /proc/stat is ABSENT, and a test that needed a
// real absent file would pass vacuously on the Linux runner and never run on
// the Mac it was written for. The two readers are replaced instead, so each
// case runs on every platform.

// absentProcStat makes the /proc/stat reader answer the way every non-Linux
// host answers, and returns the number of times it was called -- which is a
// separate assertion, not decoration: the ordering claim below depends on it.
func absentProcStat(t *testing.T) *int {
	t.Helper()
	return failingProcStat(t, &fs.PathError{Op: "open", Path: procPath, Err: fs.ErrNotExist})
}

func failingProcStat(t *testing.T, err error) *int {
	t.Helper()
	calls := 0
	old := readSnapshot
	readSnapshot = func(string) (procstat.Snapshot, error) {
		calls++
		return procstat.Snapshot{}, err
	}
	t.Cleanup(func() { readSnapshot = old })
	return &calls
}

// fixedLoad replaces the load-average reader with one answer, or one error.
func fixedLoad(t *testing.T, v float64, err error) {
	t.Helper()
	old := readAvg1
	readAvg1 = func() (float64, error) { return v, err }
	t.Cleanup(func() { readAvg1 = old })
}

// ⛔ THE DEFECT THIS CHANGE IS FOR (#221). `hostload` on a Mac printed
// `open /proc/stat: no such file or directory`, which reads like a broken host
// -- a missing file somebody should go and look for -- rather than a platform
// that does not have the thing being read.
func TestWhereThereIsNoProcStatThePlatformIsNamed(t *testing.T) {
	absentProcStat(t)
	var out, errOut bytes.Buffer
	if code := run([]string{"-i", "0"}, &out, &errOut); code != 2 {
		t.Fatalf("code %d, want 2: nothing was measured\n%s", code, errOut.String())
	}
	e := errOut.String()
	for _, want := range []string{procPath, runtime.GOOS, "--max-load"} {
		if !strings.Contains(e, want) {
			t.Errorf("the refusal does not mention %q; it said %q", want, e)
		}
	}
	// ⛔ And the raw errno sentence is GONE, not merely accompanied: it is what
	// sent a reader looking for a missing file in the first place.
	if strings.Contains(e, "no such file or directory") {
		t.Errorf("the raw open error is still being printed: %q", e)
	}
}

// --pick and --max-busy are busy-share questions. Where the busy shares cannot
// be read they are refused, and the refusal names them -- an exit 2 that does
// not say which request it could not serve is a dead end.
//
// ⛔⛔ THE SECOND HALF OF THIS TABLE IS WHAT MAKES IT A TEST. With only the
// bare `--pick 1` rows, dropping `k > 0` from the guard SURVIVED the suite
// (measured by mutation, 2026-11-15): the request then fell through to the
// "nothing was measured" default, which also exits 2 and also names
// --max-load, so nothing could tell the two branches apart. The combination
// below is where the arm actually bears weight: with --max-load alongside it,
// the mutant exits 0 -- claiming success -- while stdout is EMPTY, and
// `taskset -c "$(hostload --pick 1)"` gets an empty argument.
func TestPickAndMaxBusyCannotBeAnsweredWithoutProcStat(t *testing.T) {
	for _, args := range [][]string{
		{"--pick", "1"},
		{"--max-busy", "50"},
		{"--pick", "1", "--max-load", "8"},
		{"--max-busy", "50", "--max-load", "8"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			absentProcStat(t)
			// A load well under the limit, so that nothing but the refusal
			// above can be what produces the status.
			fixedLoad(t, 0.5, nil)
			var out, errOut bytes.Buffer
			if code := run(append([]string{"-i", "0"}, args...), &out, &errOut); code != 2 {
				t.Fatalf("code %d, want 2: a busy-share question cannot be answered here\n%s", code, errOut.String())
			}
			e := errOut.String()
			if !strings.Contains(e, "--pick and --max-busy cannot be answered here") {
				t.Errorf("the refusal must name the request it could not serve; it said %q", e)
			}
			if !strings.Contains(e, "--max-load") {
				t.Errorf("the refusal must name what can be asked; it said %q", e)
			}
			if strings.Contains(out.String(), "cpu") {
				t.Errorf("no CPU list was measured, so none may be printed; stdout = %q", out.String())
			}
		})
	}
}

// The question the rule actually asks on this machine -- "is the 1-minute load
// below N" -- is answered where there is no /proc/stat, which is the whole
// point of the flag. And the answer SAYS which measure it came from, every
// time: a number whose provenance is silent cannot be compared across hosts.
func TestMaxLoadIsAnsweredWhereThereIsNoProcStat(t *testing.T) {
	absentProcStat(t)
	fixedLoad(t, 3.0, nil)
	var out, errOut bytes.Buffer
	if code := run([]string{"-i", "0", "--max-load", "8"}, &out, &errOut); code != 0 {
		t.Fatalf("code %d, want 0: the load was below the limit\n%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "load1 3.00") {
		t.Errorf("the number must be printed, not just acted on; stdout = %q", out.String())
	}
	if !strings.Contains(errOut.String(), "counts processes rather than CPU time") {
		t.Errorf("an answer from the weaker measure must say so; stderr = %q", errOut.String())
	}
}

// A loaded host is refused with both numbers, because a bare exit status cannot
// carry them.
func TestMaxLoadRefusesALoadedHost(t *testing.T) {
	absentProcStat(t)
	fixedLoad(t, 25.23, nil)
	var out, errOut bytes.Buffer
	if code := run([]string{"-i", "0", "--max-load", "8"}, &out, &errOut); code != 1 {
		t.Fatalf("code %d, want 1\n%s", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "25.23") || !strings.Contains(errOut.String(), "8.00") {
		t.Errorf("both numbers belong in the line; stderr = %q", errOut.String())
	}
	// ⛔ And the reading is on stdout EVEN ON A REFUSAL. A caller that gated on
	// a number wants to log the number it was refused for, not go and take a
	// second, later reading to find out.
	if !strings.Contains(out.String(), "load1 25.23") {
		t.Errorf("the reading must be printed on a refusal too; stdout = %q", out.String())
	}
}

// ⛔ THE BOUNDARY, which neither case above reaches: --max-load N is "at most
// N", as --max-busy is, so a load of exactly N passes. Without this, > and >=
// are indistinguishable and the limit a caller wrote means one of two things.
func TestALoadExactlyAtTheLimitPasses(t *testing.T) {
	absentProcStat(t)
	fixedLoad(t, 8, nil)
	var out, errOut bytes.Buffer
	if code := run([]string{"-i", "0", "--max-load", "8"}, &out, &errOut); code != 0 {
		t.Fatalf("code %d, want 0: a load equal to the limit is within it\n%s", code, errOut.String())
	}
}

// ⛔⛔ AN UNREAD LOAD IS NOT A LOW ONE. This is the failure this fleet keeps
// making in other shapes: a check that cannot see answers "go ahead". Exit 2 --
// refused to decide -- and no number printed, because there is none.
func TestALoadAverageThatCannotBeReadIsRefusedNeverPassed(t *testing.T) {
	absentProcStat(t)
	fixedLoad(t, 0, errors.New("load average not supported on plan9"))
	var out, errOut bytes.Buffer
	code := run([]string{"-i", "0", "--max-load", "8"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("an unreadable load exited 0, which reads as a pass; stderr = %q", errOut.String())
	}
	if code != 2 {
		t.Fatalf("code %d, want 2: refused to decide\n%s", code, errOut.String())
	}
	if strings.Contains(out.String(), "load1") {
		t.Errorf("a load was printed although none was read; stdout = %q", out.String())
	}
	if !strings.Contains(errOut.String(), "plan9") {
		t.Errorf("the underlying reason must survive; stderr = %q", errOut.String())
	}
}

// ⛔ THE ORDERING IS ASSERTED, not assumed. --max-load is answered before the
// first snapshot, so a host that already fails it is told so at once instead of
// being slept on for the interval -- and so that the flag works where the
// snapshot cannot be taken at all. The witness is the reader's CALL COUNT,
// which is the thing in question rather than a total containing it.
func TestTheLoadGateAnswersBeforeAnySnapshotIsTaken(t *testing.T) {
	calls := absentProcStat(t)
	fixedLoad(t, 25.23, nil)
	var out, errOut bytes.Buffer
	if code := run([]string{"-i", "0", "--max-load", "8"}, &out, &errOut); code != 1 {
		t.Fatalf("code %d, want 1", code)
	}
	if *calls != 0 {
		t.Errorf("/proc/stat was read %d time(s) although the load gate had already refused", *calls)
	}
}

// ⛔ TWO FAULTS THAT MUST NOT BE MERGED. A /proc/stat that EXISTS and cannot be
// read -- a permission, an unmounted /proc, a short read -- is not an
// unsupported platform, and reporting it as one would send the reader to the
// wrong place entirely.
func TestAProcStatThatExistsAndCannotBeReadIsNotAnUnsupportedPlatform(t *testing.T) {
	failingProcStat(t, &fs.PathError{Op: "open", Path: procPath, Err: fs.ErrPermission})
	var out, errOut bytes.Buffer
	if code := run([]string{"-i", "0"}, &out, &errOut); code != 2 {
		t.Fatalf("code %d, want 2", code)
	}
	e := errOut.String()
	if !strings.Contains(e, "permission denied") {
		t.Errorf("the real error must be reported; stderr = %q", e)
	}
	if strings.Contains(e, "does not have") {
		t.Errorf("a permission error was reported as an unsupported platform; stderr = %q", e)
	}
}

// And the SECOND reading failing is never a platform verdict either: the first
// succeeded a moment earlier, so the file is there.
func TestASecondReadingThatFailsKeepsItsOwnError(t *testing.T) {
	calls := 0
	old := readSnapshot
	readSnapshot = func(string) (procstat.Snapshot, error) {
		calls++
		if calls == 1 {
			return procstat.Snapshot{
				All:  procstat.Times{User: 10, Idle: 90},
				CPUs: map[int]procstat.Times{0: {User: 10, Idle: 90}},
			}, nil
		}
		return procstat.Snapshot{}, &fs.PathError{Op: "open", Path: procPath, Err: fs.ErrNotExist}
	}
	t.Cleanup(func() { readSnapshot = old })

	var out, errOut bytes.Buffer
	if code := run([]string{"-i", "0"}, &out, &errOut); code != 2 {
		t.Fatalf("code %d, want 2", code)
	}
	if strings.Contains(errOut.String(), "does not have") {
		t.Errorf("a second-read failure was reported as an unsupported platform; stderr = %q", errOut.String())
	}
}

// ⛔ THE LOAD GATE DOES NOT SHORTCUT THE BUSY GATE. Where /proc/stat exists,
// --max-load passing must not stand in for --max-busy: the busy share is the
// stronger measure and is still read and still gates. A load of 0.5 on a host
// whose CPUs are fully busy is exactly the case the package comment records.
func TestAQuietLoadDoesNotExcuseABusyHost(t *testing.T) {
	calls := 0
	old := readSnapshot
	readSnapshot = func(string) (procstat.Snapshot, error) {
		calls++
		// Every tick between the readings went to user time, and none to idle.
		return procstat.Snapshot{
			All:  procstat.Times{User: 100 * uint64(calls)},
			CPUs: map[int]procstat.Times{0: {User: 100 * uint64(calls)}},
		}, nil
	}
	t.Cleanup(func() { readSnapshot = old })
	fixedLoad(t, 0.5, nil)

	var out, errOut bytes.Buffer
	code := run([]string{"-i", "0", "--max-load", "8", "--max-busy", "10"}, &out, &errOut)
	if code != 1 {
		t.Fatalf("code %d, want 1: the host is 100%% busy whatever its load average says\n%s", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "busy 100.0%") {
		t.Errorf("the busy share must be what refuses; stderr = %q", errOut.String())
	}
	if calls != 2 {
		t.Errorf("/proc/stat was read %d time(s), want 2: the load gate must not replace the reading", calls)
	}
}

// A caller choosing between the two gates needs to know what the weaker one
// cannot see, so the reason lives in the help rather than only in the source.
func TestTheHelpSaysWhatTheLoadAverageCannotSee(t *testing.T) {
	var out, errOut bytes.Buffer
	run([]string{"--help"}, &out, &errOut)
	for _, want := range []string{"--max-load", "146", "niced", "WEAKER"} {
		if !strings.Contains(errOut.String(), want) {
			t.Errorf("the help does not carry %q", want)
		}
	}
}
