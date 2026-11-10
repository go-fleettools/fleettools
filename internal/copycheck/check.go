package copycheck

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Thresholds are the numbers the two measurements decide by. They are tuned on
// a control and a planted positive (see the README and the tests), and every one
// can be overridden on the command line, in which case the report says so.
type Thresholds struct {
	TableK           int `json:"table_k"`            // window of numbers (>= 8)
	TableW           int `json:"table_w"`            // winnowing window
	TableMinRun      int `json:"table_min_run"`      // matching numbers that make a finding
	TableMinDistinct int `json:"table_min_distinct"` // different values a run must hold

	ShapeK        int     `json:"shape_k"`          // window of normalised tokens
	ShapeW        int     `json:"shape_w"`          // winnowing window
	ShapeMinToks  int     `json:"shape_min_tokens"` // block size that makes a finding
	ShapeMinSim   float64 `json:"shape_min_sim"`    // matched fingerprints / ours, in the block
	ShapeMaxGap   int     `json:"shape_max_gap"`    // tokens between hits of one block
	ShapeMaxDrift int     `json:"shape_max_drift"`  // offset change between hits of one block
}

// DefaultThresholds are the tuned values.
func DefaultThresholds() Thresholds {
	return Thresholds{
		TableK: 8, TableW: 4, TableMinRun: 16, TableMinDistinct: 4,
		ShapeK: 10, ShapeW: 5, ShapeMinToks: 100, ShapeMinSim: 0.35, ShapeMaxGap: 40, ShapeMaxDrift: 24,
	}
}

// Validate refuses thresholds that would make the gate a sampler: a window
// under 8 numbers matches every small table in the world, and a minimum run
// under k+w-1 is below winnowing's guarantee of detection.
func (t Thresholds) Validate() error {
	switch {
	case t.TableK < 8:
		return fmt.Errorf("table window k=%d is below 8", t.TableK)
	case t.TableW < 1 || t.ShapeW < 1 || t.ShapeK < 4:
		return errors.New("window sizes out of range")
	case t.TableMinRun < t.TableK+t.TableW-1:
		return fmt.Errorf("table minimum run %d is below k+w-1=%d, where winnowing stops guaranteeing a detection", t.TableMinRun, t.TableK+t.TableW-1)
	case t.ShapeMinToks < t.ShapeK+t.ShapeW-1:
		return fmt.Errorf("shape minimum block %d is below k+w-1=%d", t.ShapeMinToks, t.ShapeK+t.ShapeW-1)
	case t.ShapeMinSim <= 0 || t.ShapeMinSim > 1:
		return errors.New("shape similarity must be in (0,1]")
	}
	return nil
}

// Location is a line range in a file.
type Location struct {
	File  string `json:"file"`
	Start int    `json:"start_line"`
	End   int    `json:"end_line"`
}

// Finding kinds.
const (
	KindPolicy      = "policy"
	KindTable       = "table"
	KindShape       = "shape"
	KindAttribution = "attribution"
	// KindDeclaration: a values entry that points at nothing.
	KindDeclaration = "declaration"
	// KindUndeclared: a table with no provenance at all, in a path that requires it.
	KindUndeclared = "undeclared-table"
	// KindDeclared is not a finding: a table that matches a reference and was
	// ACCEPTED because the symbol is declared. It is listed in Report.Accepted
	// so that the exemption is auditable, not silent.
	KindDeclared = "declared-values"
)

// Finding is one thing the gate refuses, with both ends of it.
type Finding struct {
	Kind       string    `json:"kind"`
	Message    string    `json:"message"`
	Ours       *Location `json:"ours,omitempty"`
	Reference  string    `json:"reference"`
	License    string    `json:"license,omitempty"`
	Role       string    `json:"role,omitempty"`
	Theirs     *Location `json:"theirs,omitempty"`
	Numbers    int       `json:"matching_numbers,omitempty"`
	Tokens     int       `json:"tokens,omitempty"`
	Similarity float64   `json:"similarity,omitempty"` // 0..1
}

// RefStatus records that a reference was read and which bytes it was.
type RefStatus struct {
	Name  string `json:"name"`
	Pin   string `json:"pin"`
	Tree  string `json:"tree_sha256"`
	Files int    `json:"files"`
}

// Report is the whole answer.
type Report struct {
	Thresholds Thresholds  `json:"thresholds"`
	OurFiles   int         `json:"our_files"`
	References []RefStatus `json:"references"`
	Findings   []Finding   `json:"findings"`
	// Accepted lists the table matches that were let through because the symbol
	// is declared normative-values or derived. Never code.
	Accepted []Finding `json:"accepted,omitempty"`
}

// Options are what Check needs.
type Options struct {
	Root       string
	Manifest   *Manifest
	Fetcher    *Fetcher
	Thresholds Thresholds
}

type srcFile struct {
	rel string
	b   []byte
}

func collect(root string, exclude []string, lim Limits) ([]srcFile, error) {
	var out []srcFile
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "vendor" || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			if excluded(rel+"/", exclude) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || !isSource(rel) || excluded(rel, exclude) {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if int64(len(b)) > lim.FileBytes {
			return fmt.Errorf("%s is %d bytes, over the %d limit; exclude it in the manifest if it is generated", rel, len(b), lim.FileBytes)
		}
		out = append(out, srcFile{rel, b})
		return nil
	})
	return out, err
}

func excluded(rel string, ex []string) bool {
	for _, e := range ex {
		if rel == e || (strings.HasSuffix(e, "/") && strings.HasPrefix(rel, e)) {
			return true
		}
	}
	return false
}

type raw struct {
	kind  string
	our   Location
	their Location
	ref   *Reference
	nums  int
	toks  int
	sim   float64
	// restricted names the per-file notice the reference file carries, if any.
	restricted string
}

func overlap(a, b Location) int {
	lo, hi := a.Start, a.End
	if b.Start > lo {
		lo = b.Start
	}
	if b.End < hi {
		hi = b.End
	}
	if hi < lo {
		return 0
	}
	return hi - lo + 1
}

// Check runs the whole gate. A non-nil error means the question could not be
// answered (exit 2); findings are in the report (exit 1 when any).
func Check(opt Options) (*Report, error) {
	if err := opt.Thresholds.Validate(); err != nil {
		return nil, err
	}
	t := opt.Thresholds
	m := opt.Manifest
	rep := &Report{Thresholds: t, Findings: []Finding{}}
	for _, p := range CheckPolicy(m) {
		var r Reference
		for _, x := range m.References {
			if x.Name == p.Reference {
				r = x
			}
		}
		rep.Findings = append(rep.Findings, Finding{Kind: KindPolicy, Message: p.Message, Reference: r.Name, License: r.License, Role: r.Role})
	}

	ours, err := collect(opt.Root, m.Exclude, opt.Fetcher.Limits)
	if err != nil {
		return nil, fmt.Errorf("reading our sources: %w", err)
	}
	if len(ours) == 0 {
		return nil, errors.New("no source files under the root: an empty population is not a clean one")
	}
	rep.OurFiles = len(ours)
	oursBy := map[string][]byte{}
	for _, f := range ours {
		oursBy[f.rel] = f.b
	}
	decl, declFindings := declared(m, oursBy)
	rep.Findings = append(rep.Findings, declFindings...)
	var tf []*TableFile
	var sf []*ShapeFile
	fileCL := map[string]bool{}
	for _, f := range ours {
		tf = append(tf, NewTableFile(f.rel, f.b, t.TableK, t.TableW))
		sf = append(sf, NewShapeFile(f.rel, f.b, t.ShapeK, t.ShapeW))
		fileCL[f.rel] = statesFileCopyleft(f.b)
	}
	tix, six := NewTableIndex(tf, t.TableK), NewShapeIndex(sf, t.ShapeK)

	var raws []raw
	for i := range m.References {
		ref := &m.References[i]
		dir, tree, err := opt.Fetcher.Fetch(*ref)
		if err != nil {
			return nil, err
		}
		n := 0
		err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			n++
			rel, _ := filepath.Rel(dir, p)
			rel = filepath.ToSlash(rel)
			rs := ref.restrictedBy(b)
			for _, mt := range tix.Match(NewTableFile(rel, b, t.TableK, t.TableW), t.TableMinRun, t.TableMinDistinct) {
				raws = append(raws, raw{kind: KindTable, our: Location{mt.OurFile, mt.OurStart, mt.OurEnd}, their: Location{mt.RefFile, mt.RefStart, mt.RefEnd}, ref: ref, nums: mt.Len, restricted: rs})
			}
			for _, mt := range six.Match(NewShapeFile(rel, b, t.ShapeK, t.ShapeW), t.ShapeMinToks, t.ShapeMinSim, t.ShapeMaxGap, t.ShapeMaxDrift) {
				raws = append(raws, raw{kind: KindShape, our: Location{mt.OurFile, mt.OurStart, mt.OurEnd}, their: Location{mt.RefFile, mt.RefStart, mt.RefEnd}, ref: ref, toks: mt.Tokens, sim: mt.Similarity, restricted: rs})
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("reading reference %s: %w", ref.Name, err)
		}
		if n == 0 {
			return nil, fmt.Errorf("reference %s has no source files: an empty reference is not a clean one", ref.Name)
		}
		rep.References = append(rep.References, RefStatus{Name: ref.Name, Pin: ref.Pin, Tree: tree, Files: n})
	}

	fs, accepted := judge(raws, m, fileCL, opt.Root, decl)
	rep.Findings = append(rep.Findings, fs...)
	rep.Accepted = accepted
	rep.Findings = append(rep.Findings, undeclaredTables(m, tf, raws, fs, accepted, decl, fileCL)...)
	sort.SliceStable(rep.Findings, func(a, b int) bool {
		x, y := rep.Findings[a], rep.Findings[b]
		if x.Kind != y.Kind {
			return x.Kind < y.Kind
		}
		if x.Ours != nil && y.Ours != nil && x.Ours.File != y.Ours.File {
			return x.Ours.File < y.Ours.File
		}
		return x.Reference < y.Reference
	})
	return rep, nil
}

// allowed: the role promises this kind of copying, and the licence permits it
// for this file.
func allowed(r raw, fileCL map[string]bool) bool {
	if r.restricted != "" {
		return false // the file carries its own notice: read-only, whatever the role
	}
	c := ClassOf(r.ref.License)
	roleOK := tablesAllowedByRole(r.ref.Role)
	if r.kind == KindShape {
		roleOK = codeAllowedByRole(r.ref.Role)
	}
	return roleOK && (c == Permissive || (c == FileCopyleft && fileCL[r.our.File]))
}

// judge turns raw matches into findings.
//
// A match that the declarations allow is an ATTRIBUTION, and shields what it
// covers: a table both in a copyleft reference and in the permissive reference
// the author declared taking tables from was taken from the second. Shielding
// needs the allowed match to cover at least 80% of the lines of the other. Every
// allowed match then requires the reference to be named in the notice file.
func judge(raws []raw, m *Manifest, fileCL map[string]bool, root string, decl map[string][]span) ([]Finding, []Finding) {
	var accepted []Finding
	var shields, bad []raw
	for _, r := range raws {
		if allowed(r, fileCL) {
			shields = append(shields, r)
		} else {
			bad = append(bad, r)
		}
	}
	var out []Finding
	best := map[string]int{}
	for _, r := range bad {
		covered := false
		for _, s := range shields {
			if s.kind == r.kind && s.our.File == r.our.File && float64(overlap(s.our, r.our)) >= 0.8*float64(r.our.End-r.our.Start+1) {
				covered = true
				break
			}
		}
		if covered {
			continue
		}
		// ⛔ NUMBERS ONLY. A table that matches a reference is accepted when the
		// symbol holding it is declared normative-values or derived. A shape
		// match is never accepted, declared constants or not: translating a
		// function whose constants are declared is still translating it.
		if r.kind == KindTable {
			if s := coveredBy(decl, r.our.File, r.our.Start, r.our.End); s != nil {
				accepted = append(accepted, Finding{Kind: KindDeclared, Reference: r.ref.Name, License: r.ref.License, Role: r.ref.Role,
					Ours: &Location{r.our.File, r.our.Start, r.our.End}, Theirs: &Location{r.their.File, r.their.Start, r.their.End}, Numbers: r.nums,
					Message: fmt.Sprintf("%s:%d-%d matches %s %s but %s %s is declared %s (%s)", r.our.File, r.our.Start, r.our.End, r.ref.Name, r.their.File, s.val.File, s.val.Symbol, s.val.Kind, s.val.Standard+" "+s.val.Clause+s.val.Source)})
				continue
			}
		}
		f := Finding{Kind: r.kind, Reference: r.ref.Name, License: r.ref.License, Role: r.ref.Role,
			Ours: &Location{r.our.File, r.our.Start, r.our.End}, Theirs: &Location{r.their.File, r.their.Start, r.their.End},
			Numbers: r.nums, Tokens: r.toks, Similarity: r.sim}
		f.Message = explain(r)
		key := fmt.Sprintf("%s|%s|%d|%s", r.kind, r.our.File, r.our.Start, r.ref.Name)
		if i, ok := best[key]; ok {
			if score(f) > score(out[i]) {
				out[i] = f
			}
			continue
		}
		best[key] = len(out)
		out = append(out, f)
	}
	used := map[string]Location{}
	for _, s := range shields {
		if _, ok := used[s.ref.Name]; !ok {
			used[s.ref.Name] = s.our
		}
	}
	if len(used) > 0 {
		notice, _ := os.ReadFile(filepath.Join(root, m.Notice))
		low := strings.ToLower(string(notice))
		names := make([]string, 0, len(used))
		for n := range used {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			if strings.Contains(low, strings.ToLower(n)) {
				continue
			}
			var r *Reference
			for i := range m.References {
				if m.References[i].Name == n {
					r = &m.References[i]
				}
			}
			loc := used[n]
			out = append(out, Finding{Kind: KindAttribution, Reference: n, License: r.License, Role: r.Role, Ours: &loc,
				Message: fmt.Sprintf("%s:%d reproduces content of %s (%s, role %s), which is allowed, but %s does not name %q: the licence's attribution is owed", loc.File, loc.Start, n, r.License, r.Role, m.Notice, n)})
		}
	}
	return out, accepted
}

// undeclaredTables enforces the provenance rule: in the paths the manifest
// names, every numeric table of at least MinNumbers numbers must be declared
// (normative-values or derived), or be the very table that a permissive
// tables-from / ported-from reference was declared as the source of. A table
// that is already a finding is not reported twice.
func undeclaredTables(m *Manifest, tf []*TableFile, raws []raw, findings, accepted []Finding, decl map[string][]span, fileCL map[string]bool) []Finding {
	rd := m.RequireDeclared
	if rd == nil {
		return nil
	}
	var out []Finding
	for _, f := range tf {
		if !strings.HasSuffix(f.Path, ".go") || strings.HasSuffix(f.Path, "_test.go") || !underPrefix(f.Path, rd.Paths) {
			continue
		}
		for _, run := range f.Runs {
			if len(run.Vals) < rd.MinNumbers {
				continue
			}
			a, b := run.Lines[0], run.Lines[len(run.Lines)-1]
			if coveredBy(decl, f.Path, a, b) != nil {
				continue
			}
			here := Location{f.Path, a, b}
			seen := false
			for _, r := range raws {
				if r.kind == KindTable && r.our.File == f.Path && allowed(r, fileCL) && overlap(r.our, here) > 0 {
					seen = true // attributed to a declared permissive source
				}
			}
			for _, x := range append(append([]Finding{}, findings...), accepted...) {
				if x.Ours != nil && x.Ours.File == f.Path && overlap(*x.Ours, here) > 0 {
					seen = true
				}
			}
			if seen {
				continue
			}
			out = append(out, Finding{Kind: KindUndeclared, Reference: "-", Ours: &here, Numbers: len(run.Vals),
				Message: fmt.Sprintf("%s:%d-%d is a table of %d numbers with no provenance: declare it under `values:` (normative-values with the standard and table number, or derived with the paper and equation), or generate it", f.Path, a, b, len(run.Vals))})
		}
	}
	return out
}

func score(f Finding) int { return f.Numbers + f.Tokens }

func explain(r raw) string {
	c := ClassOf(r.ref.License)
	what := "numeric table"
	if r.kind == KindShape {
		what = "code shape"
	}
	switch {
	case r.restricted != "":
		return fmt.Sprintf("%s in %s matches %s (%s), file %s, which carries the notice %q: that file is read-only for us under every role, tables-from and ported-from included", what, r.our.File, r.ref.Name, r.ref.License, r.their.File, r.restricted)
	case c == Copyleft || c == Unknown:
		return fmt.Sprintf("%s in %s matches %s (%s, copyleft): it may be read, never copied or translated", what, r.our.File, r.ref.Name, r.ref.License)
	case c == FileCopyleft:
		return fmt.Sprintf("%s in %s matches %s (%s): reuse is allowed only in a file that itself states that licence, and this one does not (or the role does not declare copying)", what, r.our.File, r.ref.Name, r.ref.License)
	case r.kind == KindShape:
		return fmt.Sprintf("%s in %s matches %s, declared role %s: only ported-from allows code to follow a reference; declare it, or rewrite", what, r.our.File, r.ref.Name, r.ref.Role)
	}
	return fmt.Sprintf("%s in %s matches %s, declared role %s: only tables-from or ported-from allows content to be taken; declare it, or rewrite", what, r.our.File, r.ref.Name, r.ref.Role)
}
