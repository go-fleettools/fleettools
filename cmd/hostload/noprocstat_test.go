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
// the mutant exits 0, claiming success for a --pick that produced NO CPU LIST.
//
// ⚠⚠ AND THE REASON WRITTEN HERE FIRST WAS FALSE, which a review measured and
// which is worth more than the fix. It said the mutant exits 0 "while stdout is
// EMPTY, and `taskset -c \"$(hostload --pick 1)\"` gets an empty argument".
// Stdout is NOT empty on that path: it carries the `load1 …` line. The arm is
// genuinely held and the mutation is genuinely caught — for a different reason
// than the one published beside it. That is the failure `cmd/failset` exists to
// refuse: a gate that works, with an inverted explanation, is worse than a
// known gap, because a reader who trusts the sentence believes a case is
// covered that is not. The claim is narrowed to what was measured: no CPU list
// is printed.
func TestPickAndMaxBusyCannotBeAnsweredWithoutProcStat(t *testing.T) {
	for _, args := range [][]string{
		{"--pick", "1"},
		{"--max-busy", "50"},
		{"--pick", "1", "--max-load", "8"},
		{"--max-busy", "50", "--max-load", "8"},
		// ⛔ ZERO IS A LIMIT, NOT "OFF". `maxBusy >= 0` surviving its move to
		// `> 0` was measured by a review: with that, `--max-busy 0` here still
		// exits 2 but through the "nothing was measured" default, losing the
		// line that names the request — which the comment above calls a dead
		// end. Same status, worse answer, and a status-only assertion cannot
		// see it.
		{"--max-busy", "0"},
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

// ⛔ --pick OWNS STDOUT, and the load reading must not land on it. Measured by
// a review on a host WITH /proc/stat: `--pick 1 --max-load 8` printed
// "load1 0.42 over 1 min" above the CPU list, so the README's own
// `taskset -c "$(hostload --pick 1)"` received a two-line argument. The
// reading still goes somewhere — stderr — because a caller that gated on a
// number wants the number.
func TestPickOwnsStdoutEvenWithTheLoadGate(t *testing.T) {
	calls := 0
	old := readSnapshot
	readSnapshot = func(string) (procstat.Snapshot, error) {
		calls++
		return procstat.Snapshot{
			All:  procstat.Times{Idle: 100 * uint64(calls)},
			CPUs: map[int]procstat.Times{0: {Idle: 100 * uint64(calls)}},
		}, nil
	}
	t.Cleanup(func() { readSnapshot = old })
	fixedLoad(t, 0.42, nil)

	var out, errOut bytes.Buffer
	if code := run([]string{"-i", "0", "--pick", "1", "--max-load", "8"}, &out, &errOut); code != 0 {
		t.Fatalf("code %d, want 0\nout: %s\nerr: %s", code, out.String(), errOut.String())
	}
	if strings.Contains(out.String(), "load1") {
		t.Errorf("stdout is the CPU list and nothing else; it said %q", out.String())
	}
	if strings.TrimSpace(out.String()) != "0" {
		t.Errorf("stdout must be exactly the taskset list; it said %q", out.String())
	}
	if !strings.Contains(errOut.String(), "load1 0.42") {
		t.Errorf("the reading must still be reported, on stderr; it said %q", errOut.String())
	}
}

// ⛔ `--pick K` IS WHAT PRINTS THE LIST, and `k >= 0` left the suite green
// while plain `hostload` printed an empty line instead of the busy table and
// the per-CPU rows. Found by a review's mutation; assertable for the first time
// because the readings are seams now.
func TestWithoutPickTheBusyTableIsPrinted(t *testing.T) {
	calls := 0
	old := readSnapshot
	readSnapshot = func(string) (procstat.Snapshot, error) {
		calls++
		return procstat.Snapshot{
			All:  procstat.Times{User: 100 * uint64(calls)},
			CPUs: map[int]procstat.Times{0: {User: 100 * uint64(calls)}},
		}, nil
	}
	t.Cleanup(func() { readSnapshot = old })

	var out, errOut bytes.Buffer
	if code := run([]string{"-i", "0"}, &out, &errOut); code != 0 {
		t.Fatalf("code %d, want 0\nerr: %s", code, errOut.String())
	}
	for _, want := range []string{"busy 100.0%", "1 CPUs", "cpu0 "} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("stdout should carry %q; it said %q", want, out.String())
		}
	}
	out.Reset()
	errOut.Reset()
	if code := run([]string{"-i", "0", "--pick", "1"}, &out, &errOut); code != 0 {
		t.Fatalf("code %d, want 0", code)
	}
	if strings.Contains(out.String(), "busy ") {
		t.Errorf("--pick prints the list alone; it said %q", out.String())
	}
}

// ⛔ --max-busy's BOUNDARY, which nothing pinned while --max-load's was: both
// use `>`, so a host exactly at the limit passes. A review's boundary sweep
// found `100*u.Busy > maxBusy` surviving its move to `>=`.
func TestAHostExactlyAtTheBusyLimitPasses(t *testing.T) {
	calls := 0
	old := readSnapshot
	readSnapshot = func(string) (procstat.Snapshot, error) {
		calls++
		return procstat.Snapshot{
			All:  procstat.Times{User: 50 * uint64(calls), Idle: 50 * uint64(calls)},
			CPUs: map[int]procstat.Times{0: {User: 50 * uint64(calls), Idle: 50 * uint64(calls)}},
		}, nil
	}
	t.Cleanup(func() { readSnapshot = old })

	var out, errOut bytes.Buffer
	if code := run([]string{"-i", "0", "--max-busy", "50"}, &out, &errOut); code != 0 {
		t.Fatalf("code %d, want 0: 50%% is within a limit of 50\nout: %s\nerr: %s", code, out.String(), errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if code := run([]string{"-i", "0", "--max-busy", "49"}, &out, &errOut); code != 1 {
		t.Fatalf("code %d, want 1: 50%% is above a limit of 49", code)
	}
	// ⛔ AND ZERO IS ON, NOT OFF. A boundary sweep found `maxBusy >= 0`
	// surviving its move to `> 0`: with that, `--max-busy 0` silently becomes
	// no gate at all, which is the worst way for a limit to be misread — the
	// caller asked for the strictest one and got none. A negative value is
	// what means off, and the help says so.
	out.Reset()
	errOut.Reset()
	if code := run([]string{"-i", "0", "--max-busy", "0"}, &out, &errOut); code != 1 {
		t.Fatalf("code %d, want 1: a limit of 0 is ON, and 50%% is above it", code)
	}
}

// ⛔⛔ A LIMIT OF NaN MADE THE GATE VANISH. Every comparison with NaN is false,
// so `--max-busy NaN` on a fully busy host exited 0 with nothing on stderr —
// the one failure mode that makes a gate worse than no gate. Reproduced by a
// review.
func TestALimitOfNaNIsRefused(t *testing.T) {
	for _, flag := range []string{"--max-busy", "--max-load"} {
		t.Run(flag, func(t *testing.T) {
			calls := 0
			old := readSnapshot
			readSnapshot = func(string) (procstat.Snapshot, error) {
				calls++
				return procstat.Snapshot{
					All:  procstat.Times{User: 100 * uint64(calls)},
					CPUs: map[int]procstat.Times{0: {User: 100 * uint64(calls)}},
				}, nil
			}
			t.Cleanup(func() { readSnapshot = old })
			fixedLoad(t, 99, nil)

			var out, errOut bytes.Buffer
			code := run([]string{"-i", "0", flag, "NaN"}, &out, &errOut)
			if code == 0 {
				t.Fatalf("a NaN limit exited 0, so the gate vanished\nout: %s\nerr: %s", out.String(), errOut.String())
			}
			if code != 2 {
				t.Fatalf("code %d, want 2: refused to decide", code)
			}
			if !strings.Contains(errOut.String(), "NaN") {
				t.Errorf("the refusal must name the problem; it said %q", errOut.String())
			}
		})
	}
}

// ⛔⛔ A LIMIT OF ZERO IS ON FOR BOTH GATES, and the help PUBLISHES that:
// "--max-load 0 is therefore ON and means only a load of exactly zero passes".
// A review measured that nothing held it — `maxLoad >= 0` moved to `> 0`
// survived on both paths — so the sentence was a claim with no gate under it,
// which is the defect this whole session has been about. `--max-busy 0` got its
// case and `--max-load 0` did not.
func TestALimitOfZeroIsOnForTheLoadGateToo(t *testing.T) {
	t.Run("with /proc/stat", func(t *testing.T) {
		calls := 0
		old := readSnapshot
		readSnapshot = func(string) (procstat.Snapshot, error) {
			calls++
			return procstat.Snapshot{
				All:  procstat.Times{Idle: 100 * uint64(calls)},
				CPUs: map[int]procstat.Times{0: {Idle: 100 * uint64(calls)}},
			}, nil
		}
		t.Cleanup(func() { readSnapshot = old })
		fixedLoad(t, 0.5, nil)

		var out, errOut bytes.Buffer
		if code := run([]string{"-i", "0", "--max-load", "0"}, &out, &errOut); code != 1 {
			t.Fatalf("code %d, want 1: 0.5 is above a limit of 0\nout: %s\nerr: %s", code, out.String(), errOut.String())
		}
	})
	// ⛔ And on the path where there is no /proc/stat, which is a DIFFERENT
	// `maxLoad >= 0` one switch along. The load has to be exactly zero here, or
	// the gate above refuses first and this arm is never reached — the kind of
	// shadowing that makes a second case look redundant when it is not.
	t.Run("without /proc/stat", func(t *testing.T) {
		absentProcStat(t)
		fixedLoad(t, 0, nil)
		var out, errOut bytes.Buffer
		if code := run([]string{"-i", "0", "--max-load", "0"}, &out, &errOut); code != 0 {
			t.Fatalf("code %d, want 0: a load of exactly 0 is within a limit of 0\nout: %s\nerr: %s", code, out.String(), errOut.String())
		}
		if !strings.Contains(errOut.String(), "counts processes rather than CPU time") {
			t.Errorf("the answer still came from the weaker measure and must say so; it said %q", errOut.String())
		}
	})
}

// ⛔⛔ THE NaN REFUSAL'S POSITION IS WHAT MATTERS, and nothing held it: a review
// moved the whole block after the load gate and the suite stayed green. The
// comment on that block insists it must come first, and it is right —
// `--max-busy NaN --max-load 8` would otherwise exit 1 on "load1 17.40 >
// 8.00", publishing a verdict from the WEAKER measure while the busy gate had
// silently vanished. A refusal in the wrong place is a refusal that answers the
// wrong question.
func TestNaNIsRefusedBeforeAnythingIsRead(t *testing.T) {
	calls := 0
	old := readSnapshot
	readSnapshot = func(string) (procstat.Snapshot, error) {
		calls++
		return procstat.Snapshot{
			All:  procstat.Times{User: 100 * uint64(calls)},
			CPUs: map[int]procstat.Times{0: {User: 100 * uint64(calls)}},
		}, nil
	}
	t.Cleanup(func() { readSnapshot = old })
	// A load that would FAIL --max-load 8, so a misplaced refusal produces a
	// verdict instead of a refusal and the test can tell the two apart.
	loadReads := 0
	oldAvg := readAvg1
	readAvg1 = func() (float64, error) { loadReads++; return 17.40, nil }
	t.Cleanup(func() { readAvg1 = oldAvg })

	var out, errOut bytes.Buffer
	code := run([]string{"-i", "0", "--max-busy", "NaN", "--max-load", "8"}, &out, &errOut)
	if code != 2 {
		t.Fatalf("code %d, want 2: a NaN limit cannot be compared against\nout: %s\nerr: %s", code, out.String(), errOut.String())
	}
	// ⛔ The two witnesses that make this about POSITION and not just status:
	// nothing was read, and no number was published.
	if loadReads != 0 {
		t.Errorf("the load average was read %d time(s) before the refusal", loadReads)
	}
	if calls != 0 {
		t.Errorf("/proc/stat was read %d time(s) before the refusal", calls)
	}
	if strings.Contains(out.String(), "load1") || strings.Contains(errOut.String(), "load1") {
		t.Errorf("a reading was published although the request was refused; out=%q err=%q", out.String(), errOut.String())
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
