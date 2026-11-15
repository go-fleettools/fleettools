package checkout_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEveryMaterialiserFetchesFirst requires that a function which
// materialises a remote ref also asks the remote about it.
//
// ⛔ THIS IS A GATE ON AN ORDER THAT A CONVENTION KEPT THREE TIMES AND LOST THE
// FOURTH. MaterialiseAt writes out the tree at `origin/<branch>` AS THE DISK
// HOLDS IT, and `Staleness` measures the distance against that same local copy;
// neither asks the remote anything. Re-deriving a finding against an unfetched
// ref is therefore the stale tree's verdict wearing the remote's name, and it
// comes out of a scanner MORE confident than the unverified finding was.
//
// Measured 2026-11-15: cmd/editurl, on its first run, reported
// go-desktop/docs as broken hours after the fix was merged AND published,
// re-derived the finding against the unfetched ref, and printed "each was
// RE-DERIVED against the remote default and still holds" over it. Three
// mistakes stacked: a stale distance, a stale oracle, and a sentence of
// confirmation.
//
// The three callers that existed at the time were all correct --
// cmd/judgescan, cmd/testscan and cmd/vscan each fetch first, and vscan also
// moves origin/HEAD because `git fetch` does not. The author who got it wrong
// was the one who had read the lesson. That is what makes this a test rather
// than another comment: the rule was written, in this package's own doc
// comments, and being written did not stop it.
//
// ⛔ IT ASKS ONLY ABOUT A REF THAT CAN MOVE, and that narrowing was forced by
// its own first run: the rule as first written fired on cmd/vscan/apidiff.go,
// which materialises a TAG. A tag cannot move -- the fleet never moves or
// reuses one -- so no fetch can change what it holds, and refusing that call
// would be a false positive. Four false positives against two findings is a
// guard switched off within a week. The subject is therefore a function that
// materialises a ref it got from DefaultRef, or an "origin/..." literal.
//
// ⚠ WHAT IT CANNOT SEE, stated rather than implied. It checks that the fetch
// is WRITTEN in the same function, not that it RAN: a fetch behind a flag the
// caller leaves off still satisfies this, and a scanner that re-derives
// offline still has to say so in its own report -- which `StalenessWarning`'s
// `confirmed` argument exists for. And a remote ref arriving as a PARAMETER,
// fetched by a caller, is not seen either. A static check covers the shape it
// is given; this one covers the shape in which all four occurrences happened.
func TestEveryMaterialiserFetchesFirst(t *testing.T) {
	root := repoRoot(t)
	fset := token.NewFileSet()
	var offenders []string
	checked := 0

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name != "." && (strings.HasPrefix(name, ".") || name == "testdata") {
				return fs.SkipDir
			}
			return nil
		}
		// ⛔ _test.go files are exempt BECAUSE THE FUNCTION'S OWN TESTS CALL
		// IT, deliberately and without a remote: TestMaterialiseAtGivesTheRefs
		// BytesNotTheTrees is the test for this very function. A test is not a
		// scanner publishing a verdict about a repository.
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return perr
		}
		rel, _ := filepath.Rel(root, path)
		ast.Inspect(f, func(n ast.Node) bool {
			var body *ast.BlockStmt
			switch fn := n.(type) {
			case *ast.FuncDecl:
				body = fn.Body
			case *ast.FuncLit:
				body = fn.Body
			default:
				return true
			}
			if body == nil || !callsNamed(body, "MaterialiseAt") {
				return true
			}
			// A ref that cannot move needs no fetch; see the tag case above.
			if !callsNamed(body, "DefaultRef") && !materialisesARemoteBranch(body) {
				return true
			}
			checked++
			if callsNamed(body, "Refresh") || fetches(body) {
				return true
			}
			offenders = append(offenders, rel+":"+itoa(fset.Position(body.Pos()).Line))
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// ⛔ AN EMPTY POPULATION IS NOT A PASS. If the walk reads nothing -- a
	// renamed package, a root this test could not find -- "no offender" is
	// what a check that ran over zero functions prints too.
	if checked == 0 {
		t.Fatal("no function materialises a ref that can move: this test read nothing, which is not the same as finding nothing")
	}
	if len(offenders) > 0 {
		t.Errorf("%d function(s) materialise a remote ref without asking the remote about it: %v\n"+
			"  MaterialiseAt writes the tree at origin/<branch> AS THE DISK HOLDS IT. Call\n"+
			"  checkout.Refresh (or git fetch) in the same function first, or a finding\n"+
			"  re-derived here is the stale tree's verdict under the remote's name.",
			len(offenders), offenders)
	}
}

// callsNamed says whether body calls a function or method of this name.
func callsNamed(body *ast.BlockStmt, name string) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fn := call.Fun.(type) {
		case *ast.SelectorExpr:
			if fn.Sel.Name == name {
				found = true
			}
		case *ast.Ident:
			if fn.Name == name {
				found = true
			}
		}
		return !found
	})
	return found
}

// materialisesARemoteBranch says whether body names an "origin/..." ref
// literally, for a caller that writes the branch out rather than asking
// DefaultRef for it.
func materialisesARemoteBranch(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if ok && lit.Kind == token.STRING && strings.Contains(lit.Value, "origin/") {
			found = true
		}
		return !found
	})
	return found
}

// fetches says whether body runs a git fetch directly, which is how cmd/vscan
// does it -- it needs `remote set-head` in the same breath, because a fetch
// does not move origin/HEAD.
func fetches(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		for _, arg := range call.Args {
			lit, ok := arg.(*ast.BasicLit)
			if ok && lit.Kind == token.STRING && strings.Contains(lit.Value, "fetch") {
				found = true
			}
		}
		return !found
	})
	return found
}

// repoRoot walks up from this package to the directory holding go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("no go.mod above the working directory: the walk would read nothing")
	return ""
}

// itoa avoids pulling strconv in for one number.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
