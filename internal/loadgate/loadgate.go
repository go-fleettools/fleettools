// Package loadgate reads the machine's load averages and refuses a measurement
// taken on a busy machine.
//
// ⛔ WHY A GATE AND NOT A NOTE. This fleet's standing rule is "never measure at a
// 1-minute load above ~8", and on 2026-11-16 it was broken by the session that
// had written a gate for it the day before. That gate (go-tex/engine's
// cmd/internal/loadguard) covers the two corpus tools and nothing else, so
// `go test -bench` — the measurement actually taken — was guarded by nothing.
//
// ⛔⛔ AND THE DAMAGE WAS A RATIO, WHICH IS THE CASE THAT LOOKS SAFE. The figure
// published was a SHARE: a serial suite on one side, a benchmark on the other,
// both taken in the same session, which feels like it should cancel. It does not.
// Retaken on a quiet machine:
//
//	                                 loaded     quiet    factor
//	suite, 2012 tests, serial        58.04 s    17.76 s  ×3.3
//	BenchmarkLoadLaTeX                3.658 ms   2.621 ms ×1.4
//	the share they make              "12.7%"    29.7%
//
// The wall clock stretches far more than a tight in-process loop, so the RATIO
// moves by more than either half. The wrong figure was published as a refutation
// of a colleague's finding, and it ruled out the candidate that was right.
//
// ⚠ DUPLICATION, SAID OUT LOUD. go-tex/engine's cmd/internal/loadguard is the
// same sixty lines with the same threshold. Two copies of one rule is what this
// fleet's own mandate forbids, and neither can import the other: both are
// `internal`. Raised as its own question rather than resolved here by making a
// third copy — see the pull request that added this package.
package loadgate

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Threshold is the load average above which this fleet does not measure.
const Threshold = 8.0

// Averages are the three load averages, as uptime reports them.
type Averages struct {
	One, Five, Fifteen float64
}

func (a Averages) String() string {
	return fmt.Sprintf("1-min %.2f / 5-min %.2f / 15-min %.2f", a.One, a.Five, a.Fifteen)
}

// Read returns the machine's load averages.
//
// Linux exposes them in /proc/loadavg; macOS does not, so sysctl is asked for
// vm.loadavg. ⛔ A platform where neither works returns an ERROR rather than a
// zero Averages: zero would read as an idle machine, and this fleet has paid for
// a scan that could not read reporting zero.
func Read() (Averages, error) {
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		return parse(string(b))
	}
	out, err := exec.Command("sysctl", "-n", "vm.loadavg").Output()
	if err != nil {
		return Averages{}, fmt.Errorf("no /proc/loadavg and sysctl vm.loadavg failed: %w", err)
	}
	// sysctl prints "{ 2.52 3.01 3.33 }".
	return parse(strings.Trim(strings.TrimSpace(string(out)), "{} "))
}

// parse takes the first three whitespace-separated floats of s.
func parse(s string) (Averages, error) {
	f := strings.Fields(strings.Trim(strings.TrimSpace(s), "{} "))
	if len(f) < 3 {
		return Averages{}, fmt.Errorf("load averages: want three numbers, got %q", s)
	}
	var v [3]float64
	for i := range v {
		x, err := strconv.ParseFloat(f[i], 64)
		if err != nil {
			return Averages{}, fmt.Errorf("load average %d of %q: %w", i+1, s, err)
		}
		v[i] = x
	}
	return Averages{One: v[0], Five: v[1], Fifteen: v[2]}, nil
}

// Check reports whether a measurement may proceed, returning the averages so the
// caller can print them WITH its result: the conditions belong in the report, not
// in the operator's memory.
//
// ⚠ BOTH the 1-minute AND the 5-minute average must be under the threshold. A
// 1-minute dip under a loaded 5-minute average is not a quiet machine, and
// reading only the first minute passed a run at 1-min 6.93 with a 5-min of 10.00.
//
// ⛔ An unreadable machine is refused, not waved through. override is a REASON,
// never a boolean, and the caller stamps it into the output.
func Check(override string) (Averages, error) {
	a, err := Read()
	if err != nil {
		if override != "" {
			return a, nil
		}
		return a, fmt.Errorf("refusing to measure: the load averages could not be read (%v). "+
			"An unreadable machine is not an idle one", err)
	}
	if override != "" {
		return a, nil
	}
	if a.One > Threshold || a.Five > Threshold {
		return a, fmt.Errorf("refusing to measure at %s: this fleet does not measure above "+
			"%.0f on the 1-minute or the 5-minute average", a, Threshold)
	}
	return a, nil
}

// Banner is the line a tool prints above its result so the conditions travel with
// the number. An override is NAMED in it, so a reader can see the measurement was
// taken deliberately on a busy machine.
func Banner(a Averages, override string) string {
	if override != "" {
		return fmt.Sprintf("load %s  ⚠ MEASURED UNDER LOAD ON PURPOSE: %s", a, override)
	}
	return fmt.Sprintf("load %s  (under the %.0f threshold on both the 1- and 5-minute averages)",
		a, Threshold)
}
