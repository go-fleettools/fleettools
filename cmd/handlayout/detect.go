// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/tools/go/packages"
)

// The three detectors. The names are what a finding prints and what the tests
// pin; the advice says which toolkit piece to add instead.
const (
	detPainter = "painter-primitive"
	detArith   = "layout-arithmetic"
	detHit     = "hit-test"
)

var advice = map[string]string{
	detPainter: "a go-widgets application never draws: add the widget (or the paint option) to go-widgets/toolkit and use it from here",
	detArith:   "a go-widgets application never computes layout: add the container, weight, wrap or metric to go-widgets/toolkit (VBox/HBox/Grid/Splitter, a text-wrap helper) and use it from here",
	detHit:     "a go-widgets application never hit-tests widget bounds: handle the mouse event in a toolkit widget and call back into the application",
}

const (
	toolkitPath = "github.com/go-widgets/toolkit"
	painterPath = "github.com/go-widgets/painter"
)

// metricName is the name of a toolkit/painter geometry or spacing member: the
// fields of Rect, a theme's spacing, and the exported metrics (SplitterHandleW,
// StatusbarH, ...). It is only ever applied to an object that the type checker
// resolved INTO the toolkit or painter package, never to a bare identifier.
var metricName = regexp.MustCompile(`^(X|Y|W|H|Min|Max|Pad|Padding|Gap|Spacing|Margin|Width|Height|Left|Right|Top|Bottom)$|(W|H|Pad|Gap|Margin|Spacing|Height|Width|Inset|Radius)$`)

// metricFunc is a toolkit/painter function or method that answers a font,
// glyph or density metric.
var metricFunc = regexp.MustCompile(`^(TextWidth|TextHeight|Measure.*|LineHeight|GlyphW.*|GlyphH.*|FontHeight|TouchTarget|Wrap.*|Preferred.*|Pref.*)$`)

// primitiveName is a painter drawing primitive: rect fills and strokes,
// glyph and text draws, image and mask blits, pixels.
var primitiveName = regexp.MustCompile(`^(Fill|Stroke|Draw|Blit|Put|Text|Image|Mask|Glyph)`)

// Finding is one site a detector refuses.
type Finding struct {
	File     string // slash path relative to the audited directory
	Line     int
	EndLine  int
	Detector string
	Text     string // the source expression, shortened
}

func (f Finding) String() string {
	return fmt.Sprintf("%s:%d: %s: %s", f.File, f.Line, f.Detector, f.Text)
}

// Audit is what one pass over a repository found, and could not read.
type Audit struct {
	Consumer bool   // depends on the toolkit or the painter
	Exempt   string // why nothing was looked at, when it is the toolkit itself
	Findings []Finding
	Unread   []string // what the detector could not read: never counted as clean
}

func inWidgets(p *types.Package) bool {
	return p != nil && (p.Path() == toolkitPath || p.Path() == painterPath ||
		strings.HasPrefix(p.Path(), toolkitPath+"/") || strings.HasPrefix(p.Path(), painterPath+"/"))
}

// loadPackages is a variable so a test can make the driver itself fail.
var loadPackages = packages.Load

// moduleKind reads dir/go.mod: whether the module depends on the toolkit or the
// painter, and, when it IS one of them, the reason it is exempt.
func moduleKind(dir string) (consumer bool, exempt string, err error) {
	b, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return false, "", err
	}
	mf, err := modfile.Parse("go.mod", b, nil)
	if err != nil {
		return false, "", err
	}
	if mf.Module != nil {
		switch mf.Module.Mod.Path {
		case toolkitPath, painterPath:
			return false, "the module is " + mf.Module.Mod.Path + " itself", nil
		}
	}
	for _, r := range mf.Require {
		if r.Mod.Path == toolkitPath || r.Mod.Path == painterPath {
			consumer = true
		}
	}
	return consumer, "", nil
}

// auditRepo audits the module rooted at dir.
func auditRepo(dir string) Audit {
	var a Audit
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs // findings are named relative to it, and go/packages reports absolute files
	}
	consumer, exempt, err := moduleKind(dir)
	if err != nil {
		a.Unread = append(a.Unread, "go.mod: "+err.Error())
		return a
	}
	if exempt != "" {
		a.Exempt = exempt
		return a
	}
	if a.Consumer = consumer; !consumer {
		return a
	}
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles | packages.NeedImports |
			packages.NeedDeps | packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo,
		Dir:   dir,
		Tests: false,
		// A hook must never reach the network for a dependency it cannot read:
		// that is an unread package, reported as such.
		Env: append(os.Environ(), "GOPROXY=off", "GOFLAGS=-mod=readonly"),
	}
	pkgs, err := loadPackages(cfg, "./...")
	if err != nil {
		a.Unread = append(a.Unread, "go list: "+err.Error())
		return a
	}
	if len(pkgs) == 0 {
		a.Unread = append(a.Unread, "go list: no packages")
	}
	for _, p := range pkgs {
		for _, e := range p.Errors {
			a.Unread = append(a.Unread, fmt.Sprintf("%s: %s", p.PkgPath, e.Msg))
		}
		if p.TypesInfo == nil {
			continue
		}
		for _, f := range p.Syntax {
			name := p.Fset.File(f.Pos()).Name()
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			rel, err := filepath.Rel(dir, name)
			if err != nil {
				rel = name
			}
			an := &analyzer{fset: p.Fset, info: p.TypesInfo, file: filepath.ToSlash(rel), tainted: map[types.Object]bool{}}
			an.run(f)
			a.Findings = append(a.Findings, an.out...)
		}
	}
	sort.SliceStable(a.Findings, func(i, j int) bool {
		x, y := a.Findings[i], a.Findings[j]
		if x.File != y.File {
			return x.File < y.File
		}
		return x.Line < y.Line
	})
	a.Unread = dedup(a.Unread)
	return a
}

func dedup(s []string) []string {
	seen := map[string]bool{}
	out := s[:0]
	for _, x := range s {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// analyzer reads one file with the type information of its package.
type analyzer struct {
	fset    *token.FileSet
	info    *types.Info
	file    string
	tainted map[types.Object]bool // numeric locals computed from toolkit geometry
	out     []Finding
}

func (a *analyzer) run(f *ast.File) {
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		// A local gets its taint from earlier statements; three rounds reach a
		// fixpoint for the straight-line code layout is written in.
		for range 3 {
			a.propagate(fd.Body)
		}
		a.scan(fd.Body)
	}
	// package-level var initialisers
	for _, d := range f.Decls {
		if gd, ok := d.(*ast.GenDecl); ok && gd.Tok == token.VAR {
			a.scan(gd)
		}
	}
}

// metric reports whether obj is a toolkit/painter geometry or metric member.
func (a *analyzer) metric(obj types.Object) bool {
	if obj == nil || !inWidgets(obj.Pkg()) {
		return false
	}
	switch o := obj.(type) {
	case *types.Func:
		return metricFunc.MatchString(o.Name())
	case *types.Var:
		if o.IsField() {
			return metricName.MatchString(o.Name())
		}
		return o.Parent() == o.Pkg().Scope() && o.Exported() && metricName.MatchString(o.Name())
	case *types.Const:
		return o.Exported() && metricName.MatchString(o.Name())
	}
	return false
}

func (a *analyzer) use(id *ast.Ident) types.Object {
	if o := a.info.Uses[id]; o != nil {
		return o
	}
	return a.info.Defs[id]
}

// geo reports whether e is computed from toolkit geometry: it mentions a
// metric member, a function that answers one, or a local already computed from
// them. Composite-literal keys are field names, not uses, and a func literal is
// another scope.
func (a *analyzer) geo(e ast.Node) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.KeyValueExpr:
			if a.geo(n.Value) {
				found = true
			}
			return false
		case *ast.Ident:
			if o := a.use(n); o != nil && (a.metric(o) || a.tainted[o]) {
				found = true
			}
		}
		return !found
	})
	return found
}

func (a *analyzer) numeric(e ast.Expr) bool {
	tv, ok := a.info.Types[e]
	if !ok || tv.Type == nil {
		return false
	}
	b, ok := tv.Type.Underlying().(*types.Basic)
	return ok && b.Info()&(types.IsInteger|types.IsFloat) != 0
}

// propagate marks the numeric locals assigned from geometry.
func (a *analyzer) propagate(body ast.Node) {
	mark := func(lhs []ast.Expr, rhs []ast.Expr) {
		for i, l := range lhs {
			id, ok := l.(*ast.Ident)
			if !ok {
				continue
			}
			var r ast.Expr
			switch {
			case len(rhs) == len(lhs):
				r = rhs[i]
			case len(rhs) == 1:
				r = rhs[0]
			default:
				continue
			}
			if o := a.use(id); o != nil && a.geo(r) {
				if v, ok := o.(*types.Var); ok && !v.IsField() {
					if b, ok := v.Type().Underlying().(*types.Basic); ok && b.Info()&(types.IsInteger|types.IsFloat) != 0 {
						a.tainted[o] = true
					}
				}
			}
		}
	}
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			mark(n.Lhs, n.Rhs)
		case *ast.ValueSpec:
			ids := make([]ast.Expr, len(n.Names))
			for i, id := range n.Names {
				ids[i] = id
			}
			mark(ids, n.Values)
		}
		return true
	})
}

func (a *analyzer) add(n ast.Node, det string) {
	s := types.ExprString(n.(ast.Expr))
	if len(s) > 90 {
		s = s[:87] + "..."
	}
	a.out = append(a.out, Finding{
		File: a.file, Line: a.fset.Position(n.Pos()).Line, EndLine: a.fset.Position(n.End()).Line,
		Detector: det, Text: s,
	})
}

func isCompare(op token.Token) bool {
	return op == token.LSS || op == token.LEQ || op == token.GTR || op == token.GEQ
}

func isArith(op token.Token) bool {
	return op == token.ADD || op == token.SUB || op == token.MUL || op == token.QUO
}

// comparisons counts the geometry comparisons of a && / || chain.
func (a *analyzer) comparisons(e ast.Expr) int {
	e = ast.Unparen(e)
	b, ok := e.(*ast.BinaryExpr)
	if !ok {
		return 0
	}
	switch {
	case b.Op == token.LAND || b.Op == token.LOR:
		return a.comparisons(b.X) + a.comparisons(b.Y)
	case isCompare(b.Op) && (a.geo(b.X) || a.geo(b.Y)):
		return 1
	}
	return 0
}

// scan walks a body and records each site once: the outermost expression.
func (a *analyzer) scan(root ast.Node) {
	ast.Inspect(root, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			if sel, ok := n.Fun.(*ast.SelectorExpr); ok {
				if f, ok := a.info.Uses[sel.Sel].(*types.Func); ok && f.Pkg() != nil &&
					f.Pkg().Path() == painterPath && primitiveName.MatchString(f.Name()) {
					a.add(n, detPainter)
				}
			}
		case *ast.BinaryExpr:
			if n.Op == token.LAND || n.Op == token.LOR {
				if a.comparisons(n) >= 2 {
					a.add(n, detHit)
					return false
				}
			} else if isArith(n.Op) && a.numeric(n) && a.geo(n) {
				a.add(n, detArith)
				return false
			}
		}
		return true
	})
}
