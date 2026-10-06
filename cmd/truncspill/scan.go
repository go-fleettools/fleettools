// Command truncspill reports where Go 1.27's loong64 back end spills a
// float-to-integer conversion in a way that can reach the stack slots next to
// it -- the shape of golang.org/issue/81000.
//
// On loong64 the rule
//
//	(Cvt64Fto32 x) => (TRUNCDW x)
//
// leaves an INTEGER result in a floating-point register, because TRUNCDW is
// declared `reg: fp11`. Spilling such a value therefore emits an 8-byte MOVD.
// CL 820040 fixes it by wrapping the conversions in MOVWfpgp/MOVVfpgp, so a
// toolchain whose output contains no MOV[WV]fpgp does not carry the fix.
//
// An occurrence is NOT a defect. An 8-byte store into its own 8-aligned
// 8-byte slot writes exactly its own value. It corrupts only when a live
// neighbour sits inside those eight bytes, which shows as a positive stride
// below 8 between two spill offsets in one frame, or as an offset that is not
// 8-aligned. That distinction is the whole point of this command: measured on
// go-gfx/gfx `./color`, go1.26.4 -- which PASSES on loong64 -- also spills,
// twice, onto -88 and -96; go1.27.1 -- which FAILS -- spills onto -145..-140
// and -275,-276.
package main

import (
	"bufio"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	reText  = regexp.MustCompile(`^(\S+) STEXT`)
	reTrunc = regexp.MustCompile(`\bTRUNC[DF][WV]\b\s+(F\d+),\s*(F\d+)`)
	// A slot name is fully qualified: it carries `/`, `.`, `*`, `(`, `)` and
	// `~`. A narrower character class matches NOTHING and so reports zero on
	// a build known to be broken -- which is how the first version of this
	// command passed its own control while seeing none of the 1134 spills in
	// front of it.
	reSpill = regexp.MustCompile(`\bMOVD\b\s+(F\d+),\s*([^\s,]+?)([-+]\d+)\(SP\)`)
	reMove  = regexp.MustCompile(`\bMOV[VW]\b\s+(F\d+),\s*(R\d+)`)
	reFpgp  = regexp.MustCompile(`MOV[WV]fpgp`)
	reLine  = regexp.MustCompile(`\(([^()]*\.go):(\d+)\)`)
)

// Spill is one TRUNCD* result stored to the stack as eight bytes.
type Spill struct {
	Func    string // the enclosing STEXT symbol
	Slot    string // the stack slot's symbol, without its offset
	Off     int    // the offset, negative, relative to SP
	At      string // file:line of the store
	TruncAt string // file:line of the conversion that produced the value
}

// Frame is every spill in one function, with the verdict for that function.
type Frame struct {
	Func   string
	Spills []Spill
	Stride int  // smallest positive gap between two offsets; 0 if fewer than two distinct
	Hazard bool // a live neighbour can be inside one of the stores
}

// Report is the whole of one assembly dump.
type Report struct {
	Seen    int // STEXT symbols in the dump, prefix or not: zero means nothing was read
	Funcs   int // STEXT symbols matching the prefix
	Truncs  int // TRUNCD* instructions seen
	Drained int // results moved straight to a general register: cannot be spilled as a float
	Fpgp    int // MOV[WV]fpgp, i.e. CL 820040 is present
	Frames  []Frame
}

// Spilled counts every spill, hazardous or not.
func (r Report) Spilled() int {
	n := 0
	for _, f := range r.Frames {
		n += len(f.Spills)
	}
	return n
}

// Hazards counts the spills in frames where a neighbour can be reached.
func (r Report) Hazards() int {
	n := 0
	for _, f := range r.Frames {
		if f.Hazard {
			n += len(f.Spills)
		}
	}
	return n
}

// Scan reads `go build -gcflags=-S` output for GOARCH=loong64, considering
// only functions whose symbol starts with prefix. An empty prefix takes
// everything -- which includes the standard library when the dump was
// produced with a cold build cache, so a measurement about one module has to
// name that module.
func Scan(in io.Reader, prefix string) (Report, error) {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1<<20), 1<<20)

	var rep Report
	fn := "?"
	skip := false
	live := map[string]string{} // FP register -> where its TRUNCD* was
	seen := map[Spill]bool{}
	byFunc := map[string][]Spill{}
	var order []string

	for sc.Scan() {
		l := sc.Text()
		if m := reText.FindStringSubmatch(l); m != nil {
			fn = m[1]
			rep.Seen++
			skip = prefix != "" && !strings.HasPrefix(fn, prefix)
			if !skip {
				rep.Funcs++
			}
			live = map[string]string{}
			continue
		}
		if skip {
			continue
		}
		if reFpgp.MatchString(l) {
			rep.Fpgp++
		}
		site := "?"
		if m := reLine.FindStringSubmatch(l); m != nil {
			p := strings.Split(m[1], "/")
			site = p[len(p)-1] + ":" + m[2]
		}
		if m := reTrunc.FindStringSubmatch(l); m != nil {
			rep.Truncs++
			live[m[2]] = site
			continue
		}
		// Once the value is in a general register it is no longer a float and
		// cannot be spilled as one, so this occurrence is settled.
		if m := reMove.FindStringSubmatch(l); m != nil {
			if _, ok := live[m[1]]; ok {
				rep.Drained++
				delete(live, m[1])
			}
			continue
		}
		if m := reSpill.FindStringSubmatch(l); m != nil {
			truncAt, ok := live[m[1]]
			if !ok {
				continue
			}
			delete(live, m[1])
			off, err := strconv.Atoi(m[3])
			if err != nil {
				continue
			}
			s := Spill{Func: fn, Slot: m[2], Off: off, At: site, TruncAt: truncAt}
			// One dump compiles a package more than once -- for itself, and
			// again as a test binary's dependency -- so the SAME instruction
			// appears twice. Counting both doubles the total and invents a
			// stride of 0 between a spill and itself, which reported
			// `int(dpi)` in go-pdfkit/conformance as a hazard although its
			// slot is an 8-aligned int.
			if seen[s] {
				continue
			}
			seen[s] = true
			if _, ok := byFunc[s.Func]; !ok {
				order = append(order, s.Func)
			}
			byFunc[s.Func] = append(byFunc[s.Func], s)
		}
	}
	if err := sc.Err(); err != nil {
		return rep, err
	}

	sort.Strings(order)
	for _, f := range order {
		rep.Frames = append(rep.Frames, judge(f, byFunc[f]))
	}
	return rep, nil
}

func judge(name string, g []Spill) Frame {
	sort.Slice(g, func(i, j int) bool { return g[i].Off < g[j].Off })

	stride := 0
	for i := 1; i < len(g); i++ {
		d := g[i].Off - g[i-1].Off
		// A gap of zero is the same slot written twice, not an overlap.
		if d > 0 && (stride == 0 || d < stride) {
			stride = d
		}
	}

	f := Frame{Func: name, Spills: g, Stride: stride}
	if stride > 0 && stride < 8 {
		f.Hazard = true
	}
	for _, s := range g {
		// A negative offset that is not a multiple of 8 cannot be the base of
		// an 8-byte slot, so the store runs past it either way.
		if s.Off%8 != 0 {
			f.Hazard = true
		}
	}
	return f
}
