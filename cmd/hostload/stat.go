package main

import (
	"bufio"
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// times is one line of /proc/stat in clock ticks: user, nice, system, idle,
// iowait, irq, softirq, steal (guest time is already inside user and nice,
// so it is not added again).
type times struct {
	user, nice, system, idle, iowait, irq, softirq, steal uint64
}

func (t times) total() uint64 {
	return t.user + t.nice + t.system + t.idle + t.iowait + t.irq + t.softirq + t.steal
}

// snapshot is a whole /proc/stat: the aggregate "cpu" line and each "cpuN".
type snapshot struct {
	all  times
	cpus map[int]times
}

// parseStat reads the cpu lines of a /proc/stat. Fields a kernel does not
// print (an old one has no steal) are zero.
func parseStat(data []byte) (snapshot, error) {
	s := snapshot{cpus: map[int]times{}}
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
		t := times{v[0], v[1], v[2], v[3], v[4], v[5], v[6], v[7]}
		if f[0] == "cpu" {
			s.all, sawAll = t, true
			continue
		}
		id, err := strconv.Atoi(strings.TrimPrefix(f[0], "cpu"))
		if err != nil {
			return s, fmt.Errorf("%s: not a cpu line", f[0])
		}
		s.cpus[id] = t
	}
	if err := sc.Err(); err != nil {
		return s, err
	}
	if !sawAll {
		return s, fmt.Errorf("no aggregate cpu line")
	}
	return s, nil
}

// usage is what happened between two snapshots of one line, as fractions of
// the elapsed ticks. Busy counts everything but idle and iowait: a CPU waiting
// on I/O is free to run a benchmark. Steal is reported apart because on a
// virtual machine it is time the host gave to someone else.
type usage struct {
	busy, user, nice, system, iowait, steal float64
}

func between(a, b times) usage {
	d := b.total() - a.total()
	if d == 0 {
		return usage{}
	}
	f := func(x, y uint64) float64 { return float64(y-x) / float64(d) }
	idle := f(a.idle+a.iowait, b.idle+b.iowait)
	return usage{
		busy:   1 - idle,
		user:   f(a.user, b.user),
		nice:   f(a.nice, b.nice),
		system: f(a.system+a.irq+a.softirq, b.system+b.irq+b.softirq),
		iowait: f(a.iowait, b.iowait),
		steal:  f(a.steal, b.steal),
	}
}

// cpuUse is one CPU's busy fraction over the interval.
type cpuUse struct {
	id   int
	busy float64
}

// perCPU returns every CPU present in both snapshots, idlest first, ties by
// number so the choice is stable.
func perCPU(a, b snapshot) []cpuUse {
	var out []cpuUse
	for id, tb := range b.cpus {
		if ta, ok := a.cpus[id]; ok {
			out = append(out, cpuUse{id, between(ta, tb).busy})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].busy != out[j].busy {
			return out[i].busy < out[j].busy
		}
		return out[i].id < out[j].id
	})
	return out
}

// pick returns the k idlest CPUs as a taskset list, "3" or "3,6,4".
func pick(cs []cpuUse, k int) string {
	k = min(k, len(cs))
	ids := make([]string, k)
	for i := range k {
		ids[i] = strconv.Itoa(cs[i].id)
	}
	return strings.Join(ids, ",")
}
