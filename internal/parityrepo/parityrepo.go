// Package parityrepo decides whether a repository makes bit identity with a
// reference its CONTRACT, or states the contract the fleet decision of
// 2026-11-11 asks for: a stated error bound, asserted against an
// extended-precision oracle, with bit-exact arithmetic as an option.
//
// # The marker (explicit; nothing is inferred)
//
// A repository that states the contract has, in its README, its docs/ or the
// godoc of a package, a section whose heading is "Accuracy contract" (any
// heading level; in godoc, "# Accuracy contract"), with three keyed lines:
//
//	## Accuracy contract
//
//	Bound: relative RMS error <= c*eps*log2(n), c measured per function
//	Oracle: double-double (math/big for small n)
//	Exact: Options.Exact keeps the bit-exact arithmetic
//
// A repository whose results are integers or bytes has no rounding and no
// bound: bit identity is the only correct answer there. It says so with one
// line in the same section, `Domain: exact - <why>`, and passes. (Statements
// are also counted only in a repository whose prose and godoc use floating-point
// words — float32, float64, floating-point, ULP, FMA, NumPy, SciPy, FFTW, BLAS,
// IEEE-754 — at least FloatMin times, so most such repositories never need the
// line.)
//
// and a test whose name carries the convention, so the bound is asserted and
// not only written: a Go function `TestAccuracyBound...` in a _test.go file
// (Python: `def test_accuracy_bound...`).
//
// # The verdicts
//
//   - PASS: no bit-identity contract is stated, or the marker is complete (all
//     three keys and a bound test), which presents bit-exact as an option;
//   - FAIL: a bit-identity statement stands and the marker is not complete;
//   - UNREAD: a surface could not be read (and no complete marker was found).
//     UNREAD is not PASS.
//
// A bit-identity STATEMENT is a sentence of README*.md, docs/ (.md .mdx .rst
// .txt) or a non-test Go comment with a parity cue (bit-identical, bit-exact,
// bit-for-bit, bit parity, bit identity...) that is not negated or presented as
// an option (not, never, no longer, without, opt-in, option, optional, "exact
// mode", "was a step", "non-goal"), and not inside the marker section.
package parityrepo

import (
	"fmt"
	"go/scanner"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go-fleettools/fleettools/internal/paritytext"
)

// Verdict is the answer.
type Verdict string

const (
	Pass   Verdict = "PASS"
	Fail   Verdict = "FAIL"
	Unread Verdict = "UNREAD"
)

// MaxFile bounds one file read; a larger one is UNREAD, not truncated.
const MaxFile = 4 << 20

// FloatMin is how many floating-point words make a repository a floating-point
// one (see floatCue).
const FloatMin = 5

// Heading is the marker's section title.
const Heading = "Accuracy contract"

// Statement is one sentence that states bit identity.
type Statement struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Sentence string `json:"sentence"`
	// Cue names the paritytext rule that refused a parked sentence.
	Cue string `json:"cue,omitempty"`
}

// Marker is what was found of the contract section.
type Marker struct {
	File   string `json:"file,omitempty"`
	Bound  bool   `json:"bound"`
	Oracle bool   `json:"oracle"`
	Exact  bool   `json:"exact"`
	Test   string `json:"test,omitempty"` // file holding the bound test
	// ExactDomain is `Domain: exact`: the results are integers or bytes, there
	// is no rounding, and bit identity is the only correct answer.
	ExactDomain bool `json:"exact_domain,omitempty"`
}

func (m Marker) keys() int {
	n := 0
	for _, b := range []bool{m.Bound, m.Oracle, m.Exact, m.ExactDomain} {
		if b {
			n++
		}
	}
	return n
}

// Complete says whether the marker states the contract and tests it.
func (m Marker) Complete() bool { return m.Bound && m.Oracle && m.Exact && m.Test != "" }

// Report is the whole answer.
type Report struct {
	Verdict    Verdict     `json:"verdict"`
	Statements []Statement `json:"statements,omitempty"`
	// Parked are committed sentences the text-mode scanner refuses.
	Parked    []Statement `json:"parked,omitempty"`
	Marker    Marker      `json:"marker"`
	Unread    []string    `json:"unread,omitempty"`
	Files     int         `json:"files"`
	FloatCues int         `json:"float_cues"`
	Reason    string      `json:"reason"`
}

var (
	// negated: a statement that is not a contract claim.
	negated = regexp.MustCompile(`(?i)\b(not|never|no\s+longer|isn'?t|aren'?t|without|opt-in|opt\s+in|optional|option|options|exact\s+mode|non-goal|was\s+a\s+(porting\s+)?step|oracle\s+only|unless|neither|nor|were|measured|found)\b`)

	// floatCue says a repository is about floating-point results: where
	// results are integers or bytes there is no rounding, no error bound can
	// exist, and bit identity IS the only correct answer. A repository is
	// floating-point when its prose and godoc use these words FloatMin times;
	// one whose exact results the count misreads says so with `Domain: exact`
	// in the marker.
	floatCue = regexp.MustCompile(`(?i)\b(float(16|32|64)|floating[\s-]point|ulps?|fma|numpy|scipy|fftw|pocketfft|blas|lapack|openblas|ieee[\s-]?754)\b`)

	headingRe = regexp.MustCompile(`^\s{0,3}(#{1,6})\s+(.*?)\s*#*\s*$`)
	keyRe     = regexp.MustCompile(`(?i)^\s*(?:[-*+]\s+)?(Bound|Oracle|Exact|Domain)\s*:\s*(\S.*)$`)
	testRe    = regexp.MustCompile(`(?m)^\s*func\s+TestAccuracyBound\w*\s*\(|^\s*def\s+test_accuracy_bound\w*\s*\(`)
)

var skipDirs = map[string]bool{".git": true, "vendor": true, "node_modules": true, "testdata": true, "third_party": true}

// Check reads the repository at root.
func Check(root string) (*Report, error) {
	st, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", root)
	}
	r := &Report{}
	var surfaces []surface
	// The callback never returns an error but SkipDir, so WalkDir has none to give.
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		rel, _ := filepath.Rel(root, p)
		if err != nil {
			r.Unread = append(r.Unread, rel+": "+err.Error())
			// WalkDir reports an error on a DIRECTORY it could not list (or the
			// root it could not stat); the rest of that directory is skipped
			// and, being recorded, makes the verdict UNREAD.
			return fs.SkipDir
		}
		if d.IsDir() {
			if p != root && (skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil // symlinks and devices are not followed
		}
		kind := classify(rel)
		if kind == none {
			return nil
		}
		b, err := readBounded(p)
		if err != nil {
			r.Unread = append(r.Unread, rel+": "+err.Error())
			return nil
		}
		r.Files++
		switch kind {
		case prose:
			surfaces = append(surfaces, surface{rel, string(b)})
		case goSource:
			surfaces = append(surfaces, surface{rel, goComments(b)})
		case testFile:
			if testRe.Match(b) && r.Marker.Test == "" {
				r.Marker.Test = rel
			}
		}
		return nil
	})
	var all, parked []Statement
	for _, s := range surfaces {
		sec, rest := section(s.text)
		if sec != nil {
			if m := parseKeys(sec); m.keys() > r.Marker.keys() {
				m.File, m.Test = s.file, r.Marker.Test
				r.Marker = m
			}
		}
		// The text-mode scanner over the committed prose: a sentence that parks
		// a performance change on bit identity is refused here as in a pull
		// request body (a measured bound in its paragraph exempts it), and no
		// waiver exists in repo mode: a sentence that must stay is rewritten to
		// cite its measurement. The marker section itself is read like any.
		for _, f := range paritytext.Scan(s.text) {
			parked = append(parked, Statement{File: s.file, Line: f.Line, Cue: f.Rule, Sentence: strings.Join(strings.Fields(f.Sentence), " ")})
		}
		r.FloatCues += len(floatCue.FindAllString(s.text, -1))
		for _, u := range paritytext.Units(rest) {
			if isStatement(u.Text) {
				all = append(all, Statement{File: s.file, Line: u.Line, Sentence: strings.Join(strings.Fields(u.Text), " ")})
			}
		}
	}
	if r.FloatCues >= FloatMin {
		r.Statements = all
		if !r.Marker.ExactDomain {
			r.Parked = parked
		}
	}
	switch {
	case r.Marker.ExactDomain:
		r.Verdict, r.Reason = Pass, "the results are exact (Domain: exact): there is no rounding, so bit identity is the only correct answer"
	case len(r.Parked) > 0:
		// Certain evidence, so it outranks UNREAD and the marker: a repository
		// can carry the marker while its docs still park perf gaps on bit identity.
		r.Verdict = Fail
		r.Reason = fmt.Sprintf("%d committed sentence(s) defer or decline a performance change because of bit identity, which the accuracy contract does not allow (a measured bound in the paragraph, or a rewrite, is the way out; repo mode has no waiver)", len(r.Parked))
	case r.Marker.Complete():
		r.Verdict, r.Reason = Pass, "the accuracy contract is stated (bound, oracle, exact option) and a TestAccuracyBound test asserts it"
	case len(r.Unread) > 0:
		r.Verdict, r.Reason = Unread, fmt.Sprintf("%d surface(s) could not be read, so no verdict", len(r.Unread))
	case len(r.Statements) > 0:
		r.Verdict = Fail
		r.Reason = "bit identity is stated as the contract and the accuracy contract (Bound, Oracle, Exact, TestAccuracyBound) is not complete"
	default:
		r.Verdict, r.Reason = Pass, "no bit-identity contract is stated"
	}
	return r, nil
}

type surface struct{ file, text string }

type kindOf int

const (
	none kindOf = iota
	prose
	goSource
	testFile
)

func classify(rel string) kindOf {
	base := filepath.Base(rel)
	lower := strings.ToLower(base)
	ext := strings.ToLower(filepath.Ext(base))
	top := strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]
	switch {
	case strings.HasSuffix(base, "_test.go"), strings.HasPrefix(base, "test_") && ext == ".py", strings.HasSuffix(base, "_test.py"):
		return testFile
	case ext == ".go":
		return goSource
	case strings.HasPrefix(lower, "readme") && (ext == ".md" || ext == ".rst" || ext == ".txt" || ext == ""):
		return prose
	case top == "docs" && (ext == ".md" || ext == ".mdx" || ext == ".rst" || ext == ".txt"):
		return prose
	}
	return none
}

func readBounded(p string) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, MaxFile+1))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxFile {
		return nil, fmt.Errorf("over %d bytes", MaxFile)
	}
	return b, nil
}

// goComments returns the comments of a Go file, one per line they occupy, so
// that line numbers match the source. "//" and "/* */" markers are removed, so
// a godoc heading reads "# Accuracy contract".
func goComments(src []byte) string {
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))
	var s scanner.Scanner
	s.Init(file, src, func(token.Position, string) {}, scanner.ScanComments)
	var lines []string
	put := func(line int, text string) {
		for len(lines) < line {
			lines = append(lines, "")
		}
		lines[line-1] = text
	}
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		if tok != token.COMMENT {
			continue
		}
		line := fset.Position(pos).Line
		if strings.HasPrefix(lit, "//") {
			put(line, strings.TrimPrefix(strings.TrimPrefix(lit, "//"), " "))
			continue
		}
		body := strings.TrimSuffix(strings.TrimPrefix(lit, "/*"), "*/")
		for i, l := range strings.Split(body, "\n") {
			put(line+i, strings.TrimSpace(l))
		}
	}
	return strings.Join(lines, "\n")
}

// section finds the marker section. It returns the section's text (nil when
// absent) and the text with that section blanked (newlines kept).
func section(text string) (sec []string, rest string) {
	lines := strings.Split(text, "\n")
	level := 0
	inFence := false
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			inFence = !inFence
		}
		m := headingRe.FindStringSubmatch(l)
		if inFence || m == nil {
			if level > 0 {
				sec = append(sec, l)
				lines[i] = ""
			}
			continue
		}
		if level > 0 && len(m[1]) <= level {
			level = 0
		}
		if level == 0 && strings.EqualFold(strings.TrimSpace(m[2]), Heading) {
			level = len(m[1])
			sec = append(sec, l)
			lines[i] = ""
			continue
		}
		if level > 0 {
			sec = append(sec, l)
			lines[i] = ""
		}
	}
	return sec, strings.Join(lines, "\n")
}

func parseKeys(sec []string) Marker {
	var m Marker
	for _, l := range sec {
		k := keyRe.FindStringSubmatch(l)
		if k == nil {
			continue
		}
		switch strings.ToLower(k[1]) {
		case "bound":
			m.Bound = true
		case "oracle":
			m.Oracle = true
		case "exact":
			m.Exact = true
		case "domain":
			// `Domain: exact - <why>`: the reason is required, because "exact"
			// alone is a way to silence the check without saying anything.
			v := strings.TrimSpace(k[2])
			if strings.HasPrefix(strings.ToLower(v), "exact") {
				why := strings.Trim(strings.TrimSpace(v[len("exact"):]), "-–—:. ")
				m.ExactDomain = len(strings.Fields(why)) >= 3
			}
		}
	}
	return m
}

// isStatement: a parity cue that is not negated or an option.
func isStatement(s string) bool {
	return paritytext.HasBitIdentity(s) && !negated.MatchString(s)
}
