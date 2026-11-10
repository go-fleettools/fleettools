package copycheck

import "strconv"

// Numeric tables: the part of a codec that cannot be re-derived by thinking
// about it and is therefore the part most likely to be copied. Quantiser steps,
// window shapes, Huffman code lengths, scalefactor band offsets.

// NumRun is a maximal run of numeric literals separated only by what a table
// separates its entries with.
type NumRun struct {
	Vals  []string
	Lines []int
}

func isMacroName(s string) bool {
	if len(s) < 2 || s[0] < 'A' || s[0] > 'Z' {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'A' && c <= 'Z' || isDigit(c) || c == '_') {
			return false
		}
	}
	return true
}

// NumRuns extracts the numeric runs of at least minLen values.
//
// What may sit between two numbers of one run: `,` `{` `}` `(` `)`, a unary
// sign, and an ALL_CAPS name (FAAD2 writes FRAC_CONST(0.5), COEF_CONST(1.0):
// the macro is the table's spelling, the number is its content). Anything else
// (an identifier, `;`, `=`, an operator between two numbers) ends the run.
func NumRuns(toks []Token, minLen int) []NumRun {
	var runs []NumRun
	var cur NumRun
	flush := func() {
		if len(cur.Vals) >= minLen {
			runs = append(runs, cur)
		}
		cur = NumRun{}
	}
	neg := false
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		switch t.Kind {
		case KNum:
			v, ok := Canon(t.Text)
			if !ok {
				flush()
				neg = false
				continue
			}
			if neg && v != "0" {
				v = "-" + v
			}
			neg = false
			cur.Vals = append(cur.Vals, v)
			cur.Lines = append(cur.Lines, t.Line)
		case KPunct:
			switch t.Text {
			case ",", "{", "}", "(", ")":
				neg = false
			case "-", "+":
				// unary only when nothing numeric directly precedes it in the run
				// (after `,` `{` `(` or a macro name); a sign between two numbers
				// is arithmetic.
				if i > 0 && toks[i-1].Kind == KNum {
					flush()
					neg = false
					continue
				}
				neg = t.Text == "-"
			default:
				flush()
				neg = false
			}
		case KIdent:
			if isMacroName(t.Text) {
				continue
			}
			flush()
			neg = false
		default:
			flush()
			neg = false
		}
	}
	flush()
	return runs
}

func lowEntropy(gram []string) bool {
	seen := map[string]bool{}
	for _, g := range gram {
		seen[g] = true
		if len(seen) >= 3 {
			return false
		}
	}
	return true
}

// informative says whether a matched run carries enough to be evidence of
// copying. Distinct values alone is not enough: a band-edge table that grows by
// 2, 4, 8 and 12 is a handful of rules, and two of them agree for sixteen
// entries by coincidence (measured: Opus's own band edges against ATRAC9's).
// So the run also needs distinct first DIFFERENCES: five when it is shorter
// than 32 numbers, four from there on, where a long piecewise-arithmetic table
// (AAC's scalefactor band offsets) is still a table and still a finding.
func informative(v []string, minDistinct int) bool {
	if distinct(v) < minDistinct {
		return false
	}
	need := 5
	if len(v) >= 32 {
		need = 4
	}
	diffs := map[string]bool{}
	for i := 1; i < len(v); i++ {
		a, errA := strconv.ParseFloat(v[i-1], 64)
		b, errB := strconv.ParseFloat(v[i], 64)
		if errA != nil || errB != nil {
			diffs[v[i-1]+">"+v[i]] = true // not a plain number: every step is its own
			continue
		}
		diffs[strconv.FormatFloat(b-a, 'g', 6, 64)] = true
	}
	return len(diffs) >= need
}

func distinct(v []string) int {
	seen := map[string]bool{}
	for _, g := range v {
		seen[g] = true
	}
	return len(seen)
}

// TableFile is one file's runs with their fingerprints.
type TableFile struct {
	Path string
	Runs []NumRun
	fps  [][]Fingerprint // per run
}

// NewTableFile lexes src and fingerprints its numeric runs.
func NewTableFile(path string, src []byte, k, w int) *TableFile {
	f := &TableFile{Path: path, Runs: NumRuns(Lex(src), k)}
	for _, r := range f.Runs {
		f.fps = append(f.fps, Winnow(r.Vals, k, w, lowEntropy))
	}
	return f
}

type tloc struct{ file, run, pos int }

// TableIndex is the fingerprints of OUR files, looked up while a reference is
// streamed past it.
type TableIndex struct {
	k     int
	files []*TableFile
	by    map[uint64][]tloc
}

// NewTableIndex indexes our files.
func NewTableIndex(files []*TableFile, k int) *TableIndex {
	ix := &TableIndex{k: k, files: files, by: map[uint64][]tloc{}}
	for fi, f := range files {
		for ri, fps := range f.fps {
			for _, fp := range fps {
				ix.by[fp.Hash] = append(ix.by[fp.Hash], tloc{fi, ri, fp.Pos})
			}
		}
	}
	return ix
}

// TableMatch is a run of values common to one of our files and one reference
// file, verified value by value (a hash is a hint, never the evidence).
type TableMatch struct {
	OurFile          string
	OurStart, OurEnd int // lines
	RefFile          string
	RefStart, RefEnd int
	Len              int
	Distinct         int
}

// Match streams one reference file past the index and returns every maximal
// common run of at least minRun values with at least minDistinct different
// values (a run of one repeated value is a constant, not a table).
func (ix *TableIndex) Match(ref *TableFile, minRun, minDistinct int) []TableMatch {
	type key struct{ of, or, rr, diag int }
	covered := map[key]int{} // furthest end already reported on that diagonal
	var out []TableMatch
	for rr, fps := range ref.fps {
		rv := ref.Runs[rr]
		for _, fp := range fps {
			for _, l := range ix.by[fp.Hash] {
				ov := ix.files[l.file].Runs[l.run]
				i, j := l.pos, fp.Pos
				if !equalFrom(ov.Vals, i, rv.Vals, j, ix.k) {
					continue
				}
				kk := key{l.file, l.run, rr, i - j}
				if end, ok := covered[kk]; ok && i < end {
					continue
				}
				a, b := i, j
				for a > 0 && b > 0 && ov.Vals[a-1] == rv.Vals[b-1] {
					a--
					b--
				}
				c, d := i+ix.k, j+ix.k
				for c < len(ov.Vals) && d < len(rv.Vals) && ov.Vals[c] == rv.Vals[d] {
					c++
					d++
				}
				covered[kk] = c
				n := c - a
				if n < minRun || !informative(ov.Vals[a:c], minDistinct) {
					continue
				}
				out = append(out, TableMatch{
					OurFile: ix.files[l.file].Path, OurStart: ov.Lines[a], OurEnd: ov.Lines[c-1],
					RefFile: ref.Path, RefStart: rv.Lines[b], RefEnd: rv.Lines[d-1],
					Len: n, Distinct: distinct(ov.Vals[a:c]),
				})
			}
		}
	}
	return out
}

func equalFrom(a []string, i int, b []string, j, k int) bool {
	if i+k > len(a) || j+k > len(b) {
		return false
	}
	for x := 0; x < k; x++ {
		if a[i+x] != b[j+x] {
			return false
		}
	}
	return true
}
