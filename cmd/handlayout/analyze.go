// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"bytes"
	"go/ast"
	"go/printer"
	"go/token"
	"go/types"
	"strings"
)

// srcFile is one non-test file of a package, named relative to the audit root.
type srcFile struct {
	name string
	f    *ast.File
}

// analyzer reads the files of ONE package with its type information. Taint
// (which numeric variables, struct fields and parameters carry toolkit
// geometry) is shared by the whole package: a field set in one file is read in
// another, a parameter is fed from a caller elsewhere.
type analyzer struct {
	fset    *token.FileSet
	info    *types.Info
	file    string
	tainted map[types.Object]bool // numeric vars, fields and params computed from geometry
	seen    map[ast.Node]bool     // nodes already inside a finding
	out     []Finding
}

func (a *analyzer) runPackage(files []srcFile) {
	// Taint travels through statements, fields and calls; three rounds reach a
	// fixpoint for the straight-line code layout is written in.
	for range 3 {
		for _, sf := range files {
			a.propagate(sf.f)
		}
	}
	for _, sf := range files {
		a.file = sf.name
		for _, d := range sf.f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Body != nil {
					a.scan(d.Body)
					a.splitHits(d.Body)
				}
			case *ast.GenDecl:
				if d.Tok == token.VAR { // package-level var initialisers
					a.scan(d)
				}
			}
		}
	}
}

func isPainterPkg(p *types.Package) bool {
	return p != nil && (p.Path() == painterPath || strings.HasPrefix(p.Path(), painterPath+"/"))
}

// metric reports whether obj is a toolkit/painter (or image) geometry or metric
// member.
func (a *analyzer) metric(obj types.Object) bool {
	if obj == nil || obj.Pkg() == nil {
		return false
	}
	if obj.Pkg().Path() == "image" {
		switch o := obj.(type) {
		case *types.Func:
			return imageFunc.MatchString(o.Name())
		case *types.Var:
			return o.IsField() && imageField.MatchString(o.Name())
		}
		return false
	}
	if !inWidgets(obj.Pkg()) {
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
// metric member, a function that answers one, or a variable, field or
// parameter already computed from them. Composite-literal keys are field
// names, not uses, and a func literal is another scope.
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

func isNumber(t types.Type) bool {
	b, ok := t.Underlying().(*types.Basic)
	return ok && b.Info()&(types.IsInteger|types.IsFloat) != 0
}

func (a *analyzer) numeric(e ast.Expr) bool {
	tv, ok := a.info.Types[e]
	return ok && isNumber(tv.Type)
}

func (a *analyzer) constant(e ast.Expr) bool {
	tv, ok := a.info.Types[e]
	return ok && (tv.Value != nil || tv.IsNil())
}

// arithExpr: a computed (non-constant) number, whatever its operands.
func (a *analyzer) arithExpr(e ast.Expr) bool {
	e = ast.Unparen(e)
	switch x := e.(type) {
	case *ast.BinaryExpr:
		if !isArith(x.Op) {
			return false
		}
	case *ast.UnaryExpr:
		if x.Op != token.SUB {
			return false
		}
	default:
		return false
	}
	return a.numeric(e) && !a.constant(e)
}

// geomType: a toolkit/painter geometry struct (Rect, Point, ...) or
// image.Rectangle / image.Point, by value or pointer.
func (a *analyzer) geomType(t types.Type) bool {
	t = types.Unalias(t)
	if p, ok := t.(*types.Pointer); ok {
		t = types.Unalias(p.Elem())
	}
	n, ok := t.(*types.Named)
	if !ok || n.Obj().Pkg() == nil {
		return false
	}
	if _, ok := n.Underlying().(*types.Struct); !ok {
		return false
	}
	if n.Obj().Pkg().Path() == "image" {
		return n.Obj().Name() == "Rectangle" || n.Obj().Name() == "Point"
	}
	return inWidgets(n.Obj().Pkg()) && geomTypeName.MatchString(n.Obj().Name())
}

// taint marks a numeric variable, struct field or parameter as carrying
// geometry. The toolkit's own fields are sources, not carriers.
func (a *analyzer) taint(o types.Object) {
	v, ok := o.(*types.Var)
	if !ok || v.Name() == "_" {
		return
	}
	if v.IsField() && v.Pkg() != nil && (inWidgets(v.Pkg()) || v.Pkg().Path() == "image") {
		return
	}
	if isNumber(v.Type()) {
		a.tainted[o] = true
	}
}

// target is the object a assignment writes: a variable, or the field of a
// struct (an application's own fields are carriers).
func (a *analyzer) target(l ast.Expr) types.Object {
	switch l := ast.Unparen(l).(type) {
	case *ast.Ident:
		return a.use(l)
	case *ast.SelectorExpr:
		return a.info.Uses[l.Sel] // nil when unresolved
	}
	return nil
}

func (a *analyzer) callee(c *ast.CallExpr) *types.Func {
	switch f := ast.Unparen(c.Fun).(type) {
	case *ast.Ident:
		fn, _ := a.info.Uses[f].(*types.Func)
		return fn
	case *ast.SelectorExpr:
		fn, _ := a.info.Uses[f.Sel].(*types.Func)
		return fn
	}
	return nil
}

// propagate marks what is computed from geometry: variables (local or package
// level), application struct fields (assigned or set in a literal) and the
// parameters a caller feeds a geometry value to.
func (a *analyzer) propagate(root ast.Node) {
	mark := func(lhs, rhs []ast.Expr) {
		for i, l := range lhs {
			var r ast.Expr
			switch {
			case len(rhs) == len(lhs):
				r = rhs[i]
			case len(rhs) == 1:
				r = rhs[0]
			default:
				continue
			}
			if o := a.target(l); o != nil && a.geo(r) {
				a.taint(o)
			}
		}
	}
	ast.Inspect(root, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			mark(n.Lhs, n.Rhs)
		case *ast.ValueSpec:
			ids := make([]ast.Expr, len(n.Names))
			for i, id := range n.Names {
				ids[i] = id
			}
			mark(ids, n.Values)
		case *ast.CompositeLit:
			for _, e := range n.Elts {
				if kv, ok := e.(*ast.KeyValueExpr); ok {
					if k, ok := kv.Key.(*ast.Ident); ok {
						if v, ok := a.info.Uses[k].(*types.Var); ok && v.IsField() && a.geo(kv.Value) {
							a.taint(v)
						}
					}
				}
			}
		case *ast.CallExpr:
			f := a.callee(n)
			if f == nil || (f.Pkg() != nil && inWidgets(f.Pkg())) {
				break
			}
			sig, _ := f.Type().(*types.Signature)
			if sig == nil || sig.Params().Len() == 0 {
				break
			}
			for i, arg := range n.Args {
				pi := i
				if pi >= sig.Params().Len() {
					if !sig.Variadic() {
						break
					}
					pi = sig.Params().Len() - 1
				}
				if a.geo(arg) {
					a.taint(sig.Params().At(pi))
				}
			}
		}
		return true
	})
}

// nodeText is the source of a node, on one line, shortened.
func (a *analyzer) nodeText(n ast.Node) string {
	var s string
	if e, ok := n.(ast.Expr); ok {
		s = types.ExprString(e)
	} else {
		var b bytes.Buffer
		if printer.Fprint(&b, a.fset, n) == nil {
			s = strings.Join(strings.Fields(b.String()), " ")
		}
	}
	if len(s) > 90 {
		s = s[:87] + "..."
	}
	return s
}

// add records a finding and keeps what is inside it from being reported again.
func (a *analyzer) add(n ast.Node, det string) {
	a.out = append(a.out, Finding{
		File: a.file, Line: a.fset.Position(n.Pos()).Line, EndLine: a.fset.Position(n.End()).Line,
		Detector: det, Text: a.nodeText(n),
	})
	a.seen[n] = true
}

// addTree is add for a site whose parts are not findings of their own.
func (a *analyzer) addTree(n ast.Node, det string) {
	a.add(n, det)
	ast.Inspect(n, func(c ast.Node) bool { a.seen[c] = true; return true })
}

func isArith(op token.Token) bool {
	switch op {
	case token.ADD, token.SUB, token.MUL, token.QUO, token.REM, token.SHL, token.SHR:
		return true
	}
	return false
}

// comparison reports whether b compares geometry: an ordered comparison, or an
// equality where neither side is a constant (r.W == 0 is an emptiness check).
func (a *analyzer) comparison(b *ast.BinaryExpr) bool {
	switch b.Op {
	case token.LSS, token.LEQ, token.GTR, token.GEQ:
	case token.EQL, token.NEQ:
		if a.constant(b.X) || a.constant(b.Y) {
			return false
		}
	default:
		return false
	}
	// `i >= 0` and `i < len(xs)` are index and sentinel checks (a lookup's
	// "not found"), not a position tested against widget bounds.
	if a.sentinel(b.X) || a.sentinel(b.Y) {
		return false
	}
	return a.geo(b.X) || a.geo(b.Y)
}

// sentinel: the constant zero, or len(...) / cap(...).
func (a *analyzer) sentinel(e ast.Expr) bool {
	e = ast.Unparen(e)
	if tv, ok := a.info.Types[e]; ok && tv.Value != nil && tv.Value.String() == "0" {
		return true
	}
	if c, ok := e.(*ast.CallExpr); ok {
		if id, ok := c.Fun.(*ast.Ident); ok && (id.Name == "len" || id.Name == "cap") {
			_, builtin := a.info.Uses[id].(*types.Builtin)
			return builtin
		}
	}
	return false
}

// comparisons counts the geometry comparisons of a && / || chain.
func (a *analyzer) comparisons(e ast.Expr) int {
	b, ok := ast.Unparen(e).(*ast.BinaryExpr)
	if !ok {
		return 0
	}
	switch {
	case b.Op == token.LAND || b.Op == token.LOR:
		return a.comparisons(b.X) + a.comparisons(b.Y)
	case a.comparison(b):
		return 1
	}
	return 0
}

// splitHits finds a hit test written as early returns, one comparison each:
// two or more `if <geometry comparison> { return }` in one function body.
func (a *analyzer) splitHits(body *ast.BlockStmt) {
	var conds []ast.Expr
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.IfStmt:
			if len(n.Body.List) > 0 && a.comparisons(n.Cond) == 1 {
				switch n.Body.List[len(n.Body.List)-1].(type) {
				case *ast.ReturnStmt, *ast.BranchStmt:
					conds = append(conds, n.Cond)
				}
			}
		}
		return true
	})
	if len(conds) >= 2 {
		for _, c := range conds {
			a.add(c, detHit)
		}
	}
}

// primitive: a call of a drawing primitive of the painter (or of one of its
// subpackages), a toolkit drawing function that takes a painter, or a method of
// the application's own interface or type that mirrors the painter's (a name of
// the primitive list and a painter value type among its parameters).
func (a *analyzer) primitive(f *types.Func) bool {
	name := f.Name()
	if !primitiveName.MatchString(name) || metricFunc.MatchString(name) {
		return false
	}
	sig := f.Signature()
	switch {
	case isPainterPkg(f.Pkg()):
		return true
	case inWidgets(f.Pkg()):
		return sig.Recv() == nil && a.takesPainterType(sig, true)
	}
	return sig.Recv() != nil && a.takesPainterType(sig, false)
}

// takesPainterType: a parameter of a type of the painter package; with
// anyKind false only a value struct (Rect, RGBA), so that an application
// widget's Draw(p painter.Painter) is not mistaken for a primitive.
func (a *analyzer) takesPainterType(sig *types.Signature, anyKind bool) bool {
	for i := range sig.Params().Len() {
		t := types.Unalias(sig.Params().At(i).Type())
		if p, ok := t.(*types.Pointer); ok {
			t = types.Unalias(p.Elem())
		}
		if s, ok := t.(*types.Slice); ok {
			t = types.Unalias(s.Elem())
		}
		n, ok := t.(*types.Named)
		if !ok || !isPainterPkg(n.Obj().Pkg()) {
			continue
		}
		if _, isStruct := n.Underlying().(*types.Struct); anyKind || isStruct {
			return true
		}
	}
	return false
}

// hit: Rect.Contains, Point.In and kin on a toolkit or image geometry value.
func (a *analyzer) hit(f *types.Func) bool {
	sig := f.Signature()
	return sig.Recv() != nil && hitMethod.MatchString(f.Name()) && a.geomType(sig.Recv().Type())
}

// clamp: max, min and math.Max/Min/... over geometry.
func (a *analyzer) clamp(c *ast.CallExpr) bool {
	named := false
	if id, ok := ast.Unparen(c.Fun).(*ast.Ident); ok {
		if b, ok := a.info.Uses[id].(*types.Builtin); ok && (b.Name() == "max" || b.Name() == "min") {
			named = true
		}
	}
	if f := a.callee(c); f != nil && f.Pkg() != nil && f.Pkg().Path() == "math" {
		switch f.Name() {
		case "Max", "Min", "Abs", "Floor", "Ceil", "Round", "Mod", "Trunc":
			named = true
		}
	}
	if !named || !a.numeric(c) {
		return false
	}
	for _, arg := range c.Args {
		if a.geo(arg) {
			return true
		}
	}
	return false
}

// geomField: the left side is a field of a toolkit/painter/image geometry
// struct (r.X, p.W, ...).
func (a *analyzer) geomField(l ast.Expr) bool {
	sel, ok := ast.Unparen(l).(*ast.SelectorExpr)
	if !ok {
		return false
	}
	v, ok := a.info.Uses[sel.Sel].(*types.Var)
	return ok && v.IsField() && a.metric(v)
}

// scan walks a body and records each site once: the outermost expression.
//
// Sources feed taint; the SINKS are checked here as well: a geometry composite
// literal, a geometry field, or a geometry-named parameter of a toolkit
// function that receives computed arithmetic is layout whatever its operands.
func (a *analyzer) scan(root ast.Node) {
	callFun := map[*ast.SelectorExpr]bool{}
	ast.Inspect(root, func(n ast.Node) bool {
		if a.seen[n] {
			return false
		}
		switch n := n.(type) {
		case *ast.FuncLit:
			a.splitHits(n.Body)
		case *ast.CompositeLit:
			if a.geomType(a.info.TypeOf(n)) {
				for _, e := range n.Elts {
					if kv, ok := e.(*ast.KeyValueExpr); ok {
						e = kv.Value
					}
					if a.arithExpr(e) {
						a.add(e, detArith)
					}
				}
			}
		case *ast.AssignStmt:
			a.assignSink(n)
		case *ast.IncDecStmt:
			if a.numeric(n.X) && a.geo(n.X) {
				a.addTree(n, detArith)
				return false
			}
		case *ast.CallExpr:
			if sel, ok := ast.Unparen(n.Fun).(*ast.SelectorExpr); ok {
				callFun[sel] = true
			}
			if a.clamp(n) && a.geoCall(n) {
				a.addTree(n, detArith)
				return false
			}
			if f := a.callee(n); f != nil {
				switch {
				case a.primitive(f):
					a.add(n, detPainter)
				case a.hit(f):
					a.add(n, detHit)
				default:
					a.argSink(n, f)
				}
			}
		case *ast.SelectorExpr:
			// a method value (f := p.FillRect) hands the primitive around
			if f, ok := a.info.Uses[n.Sel].(*types.Func); ok && !callFun[n] && a.primitive(f) {
				a.add(n, detPainter)
			}
		case *ast.UnaryExpr:
			if n.Op == token.SUB && a.numeric(n) && a.geo(n) {
				a.addTree(n, detArith)
				return false
			}
		case *ast.BinaryExpr:
			if n.Op == token.LAND || n.Op == token.LOR {
				if a.comparisons(n) >= 2 {
					a.addTree(n, detHit)
					return false
				}
			} else if isArith(n.Op) && a.numeric(n) && a.geo(n) {
				a.addTree(n, detArith)
				return false
			}
		}
		return true
	})
}

func (a *analyzer) geoCall(c *ast.CallExpr) bool {
	for _, arg := range c.Args {
		if a.geo(arg) {
			return true
		}
	}
	return false
}

// assignSink: a compound assignment over geometry (r.X += pad, w -= 10), or
// computed arithmetic stored into a geometry field (r.X = a + b).
func (a *analyzer) assignSink(n *ast.AssignStmt) {
	if n.Tok != token.ASSIGN && n.Tok != token.DEFINE {
		if len(n.Lhs) == 1 && len(n.Rhs) == 1 && a.numeric(n.Lhs[0]) && (a.geo(n.Lhs[0]) || a.geo(n.Rhs[0])) {
			a.addTree(n, detArith)
		}
		return
	}
	// a, r.X = f() pairs nothing: a call is not arithmetic, so the pairing of
	// the common prefix is harmless
	for i := range min(len(n.Lhs), len(n.Rhs)) {
		l := n.Lhs[i]
		if a.geomField(l) && a.arithExpr(n.Rhs[i]) {
			a.add(n.Rhs[i], detArith)
		}
	}
}

// argSink: computed arithmetic passed for a geometry-named numeric parameter of
// a toolkit function.
func (a *analyzer) argSink(c *ast.CallExpr, f *types.Func) {
	if !inWidgets(f.Pkg()) {
		return
	}
	sig := f.Signature()
	for i, arg := range c.Args {
		// the extra arguments of a variadic call all take the last parameter
		p := sig.Params().At(min(i, sig.Params().Len()-1))
		if isNumber(p.Type()) && geomParam.MatchString(p.Name()) && a.arithExpr(arg) {
			a.add(arg, detArith)
		}
	}
}
