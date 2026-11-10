package copycheck

import (
	"sort"
	"strings"
)

// Code shape: what survives a translation from C to Go. Names mostly do
// (sample_rate becomes sampleRate), control flow does, expressions do. What
// does not: punctuation, declarations, types, casts, loop keywords.

// ST is one normalised token.
type ST struct {
	Text string
	Line int
}

// noise is dropped after normalisation: type words and declaration keywords of
// both languages. They carry the language, not the algorithm.
var noise = map[string]bool{
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"int8t": true, "int16t": true, "int32t": true, "int64t": true,
	"uint8t": true, "uint16t": true, "uint32t": true, "uint64t": true,
	"float": true, "float32": true, "float64": true, "double": true,
	"unsigned": true, "signed": true, "long": true, "short": true, "char": true,
	"byte": true, "bool": true, "sizet": true, "uintptr": true, "uintptrt": true,
	"const": true, "static": true, "inline": true, "void": true, "struct": true,
	"register": true, "extern": true, "volatile": true, "restrict": true,
	"var": true, "func": true, "type": true, "enum": true, "typedef": true,
	"union": true,
}

var mapped = map[string]string{
	"for": "loop", "while": "loop", "do": "loop", "range": "loop",
	"nil": "null", "null": "null",
	"elif": "elseif",
}

// Shape normalises a token stream so that the same algorithm written in C and
// in Go gives the same stream:
//
//   - identifiers are lower-cased and lose their underscores;
//   - numbers, strings and chars become N, S and N (a C char is a number);
//   - for/while/do/range become one keyword; `->` becomes `.`; `:=` becomes `=`;
//   - braces, parentheses and semicolons are dropped (C needs them where Go
//     does not, and they carry no information the other tokens lack);
//   - type words and declaration keywords are dropped.
func Shape(toks []Token) []ST {
	out := make([]ST, 0, len(toks))
	for _, t := range toks {
		var s string
		switch t.Kind {
		case KIdent:
			s = strings.ReplaceAll(strings.ToLower(t.Text), "_", "")
			if s == "" {
				continue
			}
			if noise[s] {
				continue
			}
			if m, ok := mapped[s]; ok {
				s = m
			}
		case KNum, KChar:
			s = "N"
		case KStr:
			s = "S"
		case KPunct:
			switch t.Text {
			case "{", "}", "(", ")", ";":
				continue
			case "->":
				s = "."
			case ":=":
				s = "="
			default:
				s = t.Text
			}
		}
		out = append(out, ST{s, t.Line})
	}
	return collapseTables(out)
}

// collapseTables replaces every run of six or more tokens made only of N and
// `,` and `-` by one TABLE token. A table says nothing about control flow (the
// numbers have their own measurement), and without this every pair of tables
// matches every other as `N , N , N , ...`.
func collapseTables(in []ST) []ST {
	out := in[:0:0]
	for i := 0; i < len(in); {
		j := i
		for j < len(in) && (in[j].Text == "N" || in[j].Text == "," || in[j].Text == "-") {
			j++
		}
		if j-i >= 6 {
			out = append(out, ST{"TABLE", in[i].Line})
			i = j
			continue
		}
		out = append(out, in[i])
		i++
	}
	return out
}

// ShapeFile is one file's normalised stream and fingerprints.
type ShapeFile struct {
	Path string
	Toks []ST
	FPs  []Fingerprint
}

// NewShapeFile lexes, normalises and fingerprints src.
func NewShapeFile(path string, src []byte, k, w int) *ShapeFile {
	st := Shape(Lex(src))
	seq := make([]string, len(st))
	for i, t := range st {
		seq[i] = t.Text
	}
	return &ShapeFile{Path: path, Toks: st, FPs: Winnow(seq, k, w, nil)}
}

type sloc struct{ file, idx int } // idx into the file's FPs

// ShapeIndex is the fingerprints of our files.
type ShapeIndex struct {
	k     int
	files []*ShapeFile
	by    map[uint64][]sloc
}

// NewShapeIndex indexes our files.
func NewShapeIndex(files []*ShapeFile, k int) *ShapeIndex {
	ix := &ShapeIndex{k: k, files: files, by: map[uint64][]sloc{}}
	for fi, f := range files {
		for i, fp := range f.FPs {
			ix.by[fp.Hash] = append(ix.by[fp.Hash], sloc{fi, i})
		}
	}
	return ix
}

// ShapeMatch is a block of our file whose fingerprints are found, in the same
// order and at nearly the same distance from each other, in one reference file.
type ShapeMatch struct {
	OurFile          string
	OurStart, OurEnd int // lines
	RefFile          string
	RefStart, RefEnd int
	Tokens           int     // length of the block, in normalised tokens
	Similarity       float64 // matched fingerprints / our fingerprints inside the block
}

type hit struct{ our, ref int } // token positions

// Match streams one reference file past the index.
//
// A block is a chain of hits sorted by position in OUR file, cut where the gap
// exceeds maxGap tokens or where the offset between the two files drifts by
// more than maxDrift. The second cut is what keeps a few lines of boilerplate
// that happen to occur all over a large reference from adding up to a "block".
func (ix *ShapeIndex) Match(ref *ShapeFile, minTokens int, minSim float64, maxGap, maxDrift int) []ShapeMatch {
	per := map[int][]hit{}
	for _, fp := range ref.FPs {
		for _, l := range ix.by[fp.Hash] {
			per[l.file] = append(per[l.file], hit{ix.files[l.file].FPs[l.idx].Pos, fp.Pos})
		}
	}
	var out []ShapeMatch
	for fi, hits := range per {
		our := ix.files[fi]
		sort.Slice(hits, func(a, b int) bool {
			if hits[a].our != hits[b].our {
				return hits[a].our < hits[b].our
			}
			return hits[a].ref < hits[b].ref
		})
		start := 0
		flush := func(end int) {
			blk := hits[start:end]
			if len(blk) == 0 {
				return
			}
			first, last := blk[0].our, blk[len(blk)-1].our
			span := last - first + ix.k
			if span < minTokens {
				return
			}
			// our fingerprints inside [first, last]
			lo := sort.Search(len(our.FPs), func(i int) bool { return our.FPs[i].Pos >= first })
			hi := sort.Search(len(our.FPs), func(i int) bool { return our.FPs[i].Pos > last })
			if hi <= lo {
				return
			}
			seen := map[int]bool{}
			rmin, rmax := blk[0].ref, blk[0].ref
			for _, h := range blk {
				seen[h.our] = true
				if h.ref < rmin {
					rmin = h.ref
				}
				if h.ref > rmax {
					rmax = h.ref
				}
			}
			// The reference side must be as big as ours, within half: one dense
			// line of a reference (an inline-asm blob, a long table row) cannot
			// be the source of a 339-token function, whatever its hashes say
			// (measured against ffmpeg's arm/aac.h).
			if rmax-rmin+ix.k < span/2 {
				return
			}
			sim := float64(len(seen)) / float64(hi-lo)
			if sim > 1 {
				sim = 1
			}
			if sim < minSim {
				return
			}
			re := rmax + ix.k - 1
			if re >= len(ref.Toks) {
				re = len(ref.Toks) - 1
			}
			ol := first + ix.k - 1
			if ol >= len(our.Toks) {
				ol = len(our.Toks) - 1
			}
			out = append(out, ShapeMatch{
				OurFile: our.Path, OurStart: our.Toks[first].Line, OurEnd: our.Toks[ol].Line,
				RefFile: ref.Path, RefStart: ref.Toks[rmin].Line, RefEnd: ref.Toks[re].Line,
				Tokens: span, Similarity: sim,
			})
		}
		for i := 1; i < len(hits); i++ {
			gap := hits[i].our - hits[i-1].our
			drift := (hits[i].ref - hits[i].our) - (hits[i-1].ref - hits[i-1].our)
			if drift < 0 {
				drift = -drift
			}
			if gap > maxGap || drift > maxDrift {
				flush(i)
				start = i
			}
		}
		flush(len(hits))
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].OurFile != out[b].OurFile {
			return out[a].OurFile < out[b].OurFile
		}
		return out[a].OurStart < out[b].OurStart
	})
	return out
}
