package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"sync/atomic"
)

// ⛔ A REGEX CANNOT TELL A CALL FROM A STRING THAT LOOKS LIKE ONE, and this
// tool reported ITSELF because of it: cmd/judgescan/main_test.go builds its
// fixtures out of Go source held in backquoted strings —
//
//	writeRepo(t, dir, `package p
//	func f() { exec.LookPath("pdftoppm") }`, nil)
//
// — so a whole-file search found pdftoppm, swtpm, btrfs and six more, and
// named go-fleettools/fleettools as a repository whose CI never installs the
// tools its tests need. Nine phantom tools on one line of a fourteen-line
// report, in the tool's own row.
//
// The parser knows the difference. A LookPath inside a string literal is not a
// CallExpr, so it simply is not there.

// lookPaths reads one Go file and reports the tools it really looks up.
//
// gated is the set whose absence stops something: a LookPath whose error is
// discarded, or whose failure leads to a fallback, is NOT gating and is
// returned in tolerated instead. dynamic says a LookPath took an argument this
// cannot name.
//
// ok is false when the file does not parse, and the caller must then fall back
// rather than treat it as a file naming no tools — silence from an unread file
// is the one answer that must never look like a clean one.
func lookPaths(src []byte) (gated, tolerated map[string]bool, dynamic, ok bool) {
	f, err := parser.ParseFile(token.NewFileSet(), "", src, parser.SkipObjectResolution)
	if err != nil {
		return nil, nil, false, false
	}
	gated, tolerated = map[string]bool{}, map[string]bool{}

	// A call is tolerated when the statement AROUND it forgives a failure, so
	// the statements are walked first and their calls claimed.
	claimed := map[*ast.CallExpr]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.AssignStmt:
			// p, _ := exec.LookPath("x") — nothing depends on it being there.
			if name, call, is := lookPathAssign(s); is && discardsError(s) {
				tolerated[name] = true
				claimed[call] = true
			}
		case *ast.IfStmt:
			// if p, err := exec.LookPath("x"); err == nil { … } else { … }
			as, is := s.Init.(*ast.AssignStmt)
			if !is {
				return true
			}
			if name, call, is := lookPathAssign(as); is && comparesErrToNil(s.Cond) {
				tolerated[name] = true
				claimed[call] = true
			}
		}
		return true
	})

	ast.Inspect(f, func(n ast.Node) bool {
		call, is := n.(*ast.CallExpr)
		if !is || !isLookPath(call) {
			return true
		}
		if claimed[call] {
			return true
		}
		name, literal := literalArg(call)
		if !literal {
			dynamic = true
			return true
		}
		if !tolerated[name] {
			gated[name] = true
		}
		return true
	})
	return gated, tolerated, dynamic, true
}

func isLookPath(call *ast.CallExpr) bool {
	sel, is := call.Fun.(*ast.SelectorExpr)
	return is && sel.Sel != nil && sel.Sel.Name == "LookPath"
}

// literalArg is the tool's name when the single argument is a string literal.
func literalArg(call *ast.CallExpr) (string, bool) {
	if len(call.Args) != 1 {
		return "", false
	}
	lit, is := call.Args[0].(*ast.BasicLit)
	if !is || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil || s == "" {
		return "", false
	}
	return s, true
}

// lookPathAssign reports the LookPath call on the right of an assignment.
func lookPathAssign(as *ast.AssignStmt) (string, *ast.CallExpr, bool) {
	if len(as.Rhs) != 1 {
		return "", nil, false
	}
	call, is := as.Rhs[0].(*ast.CallExpr)
	if !is || !isLookPath(call) {
		return "", nil, false
	}
	name, literal := literalArg(call)
	if !literal {
		return "", nil, false
	}
	return name, call, true
}

// discardsError reports `_` in the error position of a two-value assignment.
func discardsError(as *ast.AssignStmt) bool {
	if len(as.Lhs) != 2 {
		return false
	}
	id, is := as.Lhs[1].(*ast.Ident)
	return is && id.Name == "_"
}

// comparesErrToNil reports `err == nil`, whatever the variable is called.
func comparesErrToNil(cond ast.Expr) bool {
	bin, is := cond.(*ast.BinaryExpr)
	if !is || bin.Op != token.EQL {
		return false
	}
	id, is := bin.Y.(*ast.Ident)
	return is && id.Name == "nil"
}

// unparsedFiles counts the _test.go files the parser could not read, so the
// report can say the pass was not uniform. A fallback that nobody is told
// about is a second instrument reporting as if it were the first.
var unparsedFiles atomic.Int64
