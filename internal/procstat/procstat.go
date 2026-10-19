// Package procstat reads and compares two readings of a Linux /proc/stat: how
// busy the CPUs were between them, and which are the idlest. It is the one home
// of that arithmetic, shared by cmd/hostload (which reports it) and cmd/abbench
// (which refuses to start a round on a loaded host).
package procstat

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Times is one line of /proc/stat in clock ticks: user, nice, system, idle,
// iowait, irq, softirq, steal (guest time is already inside user and nice,
// so it is not added again).
type Times struct {
	User, Nice, System, Idle, IOWait, IRQ, SoftIRQ, Steal uint64
}

func (t Times) total() uint64 {
	return t.User + t.Nice + t.System + t.Idle + t.IOWait + t.IRQ + t.SoftIRQ + t.Steal
}

// Snapshot is a whole /proc/stat: the aggregate "cpu" line and each "cpuN".
type Snapshot struct {
	All  Times
	CPUs map[int]Times
}

// Parse reads the cpu lines of a /proc/stat. Fields a kernel does not
// print (an old one has no steal) are zero.
func Parse(data []byte) (Snapshot, error) {
	s := Snapshot{CPUs: map[int]Times{}}
	sawAll := false
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 5 || !strings.HasPrefix(f[0], "cpu") {
			continue
		}
		var v [8]uint64
		for i := 0; i < 8 && i+1 < len(f); i++ {
			n, err := strconv.ParseUint(f[i+1], 10, 64)
			if err != nil {
				return s, fmt.Errorf("%s: field %d: %v", f[0], i+1, err)
			}
			v[i] = n
		}
		t := Times{v[0], v[1], v[2], v[3], v[4], v[5], v[6], v[7]}
		if f[0] == "cpu" {
			s.All, sawAll = t, true
			continue
		}
		id, err := strconv.Atoi(strings.TrimPrefix(f[0], "cpu"))
		if err != nil {
			return s, fmt.Errorf("%s: not a cpu line", f[0])
		}
		s.CPUs[id] = t
	}
	if err := sc.Err(); err != nil {
		return s, err
	}
	if !sawAll {
		return s, fmt.Errorf("no aggregate cpu line")
	}
	return s, nil
}

// ReadFile parses the /proc/stat-shaped file at path.
func ReadFile(path string) (Snapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Snapshot{}, err
	}
	return Parse(data)
}

// Usage is what happened between two snapshots of one line, as fractions of
// the elapsed ticks. Busy counts everything but idle and iowait: a CPU waiting
// on I/O is free to run a benchmark. Steal is reported apart because on a
// virtual machine it is time the host gave to someone else.
type Usage struct {
	Busy, User, Nice, System, IOWait, Steal float64
}

// Between is the usage from reading a to reading b.
func Between(a, b Times) Usage {
	d := b.total() - a.total()
	if d == 0 {
		return Usage{}
	}
	f := func(x, y uint64) float64 { return float64(y-x) / float64(d) }
	idle := f(a.Idle+a.IOWait, b.Idle+b.IOWait)
	return Usage{
		Busy:   1 - idle,
		User:   f(a.User, b.User),
		Nice:   f(a.Nice, b.Nice),
		System: f(a.System+a.IRQ+a.SoftIRQ, b.System+b.IRQ+b.SoftIRQ),
		IOWait: f(a.IOWait, b.IOWait),
		Steal:  f(a.Steal, b.Steal),
	}
}

// CPUUse is one CPU's busy fraction over the interval.
type CPUUse struct {
	ID   int
	Busy float64
}

// PerCPU returns every CPU present in both snapshots, idlest first, ties by
// number so the choice is stable.
func PerCPU(a, b Snapshot) []CPUUse {
	var out []CPUUse
	for id, tb := range b.CPUs {
		if ta, ok := a.CPUs[id]; ok {
			out = append(out, CPUUse{id, Between(ta, tb).Busy})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Busy != out[j].Busy {
			return out[i].Busy < out[j].Busy
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Pick returns the k idlest CPUs as a taskset list, "3" or "3,6,4".
func Pick(cs []CPUUse, k int) string {
	k = min(k, len(cs))
	ids := make([]string, k)
	for i := range k {
		ids[i] = strconv.Itoa(cs[i].ID)
	}
	return strings.Join(ids, ",")
}
