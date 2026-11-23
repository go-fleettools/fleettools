package main

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/go-fleettools/fleettools/internal/cli"
	"golang.org/x/tools/go/packages"
)

const tkHead = "package x\n\nimport (\n\t\"github.com/go-widgets/painter\"\n\t\"github.com/go-widgets/toolkit\"\n)\n\nvar _ painter.Rect\nvar _ = toolkit.StatusbarH\n\n"

// wantAt runs --repo on a one-file consumer and compares the reported lines
// (and detectors) with the `// WANT <detector>` markers of the source, exactly.
func wantAt(t *testing.T, src string) {
	t.Helper()
	app := consumer(t, "", map[string]string{"x.go": src}, false)
	out, _, code := drive(t, "", "--repo", app)
	want := map[int]string{}
	for i, ln := range strings.Split(src, "\n") {
		if _, d, ok := strings.Cut(ln, "// WANT "); ok {
			want[i+1] = strings.TrimSpace(d)
		}
	}
	got := map[int]string{}
	for n, ds := range lines(out)["x.go"] {
		got[n] = strings.Join(ds, ",")
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("findings %v, want %v (exit %d)\n%s", got, want, code, out)
	}
}

// A func literal is another scope for taint, and a composite-literal key is a
// field name, not a use: only the VALUE of an element carries geometry.
func TestGeoSkipsFuncLitsAndLiteralKeys(t *testing.T) {
	wantAt(t, tkHead+`type T struct{ W int }

func f(v any) int { return 0 }

func A() int {
	n := f(T{W: toolkit.StatusbarH})
	m := f(toolkit.Rect{W: 5})
	k := f(func() int { return toolkit.StatusbarH })
	_ = m + 1
	_ = k + 1
	return n + 1 // WANT layout-arithmetic
}
`)
}

// A hit test split over early returns is found inside a func literal, and the
// comparisons of the literal are not counted for the function around it.
func TestSplitHitsInsideAFuncLit(t *testing.T) {
	wantAt(t, tkHead+`func F(r toolkit.Rect, x int) bool {
	if x < r.X {
		return false
	}
	g := func(y int) bool {
		if y < r.Y { // WANT hit-test
			return false
		}
		if y > r.H { // WANT hit-test
			return false
		}
		return true
	}
	return g(x)
}
`)
}

// A method that mirrors a painter primitive is one when a parameter is a painter
// struct behind a pointer or inside a slice.
func TestPrimitiveWithPointerAndSliceParameters(t *testing.T) {
	wantAt(t, tkHead+`type R struct{}

func (R) FillRects(rs []painter.Rect) {}
func (R) FillPtr(r *painter.Rect)     {}
func (R) FillName(name string)        {}

func B(r R, rs []painter.Rect, p *painter.Rect) {
	r.FillRects(rs) // WANT painter-primitive
	r.FillPtr(p)    // WANT painter-primitive
	r.FillName("a")
}
`)
}

// Calls and assignment targets that are not a name: indexing resolves to no
// object, so nothing is tainted and nothing crashes; with no finding the audit
// says it is clean.
func TestIndexedCallsAndTargetsAreNotCarriers(t *testing.T) {
	app := consumer(t, "", map[string]string{"x.go": tkHead + `func E(fns []func(int) int, xs []int) int {
	xs[0] = toolkit.StatusbarH
	_ = fns[0](xs[0])
	return xs[0] + 1
}
`}, false)
	out, _, code := drive(t, "", "--repo", app)
	if code != 0 || !strings.Contains(out, "handlayout: clean: 0 findings") {
		t.Errorf("exit %d, out %q; want clean", code, out)
	}
}

// Extra arguments beyond the parameters (a type error) and a variadic call with
// several arguments must not index past the parameter list.
func TestExtraArgumentsDoNotIndexPastTheParameters(t *testing.T) {
	app := consumer(t, "", map[string]string{"x.go": tkHead + `func g(a int) {}
func h(xs ...int) {}

func C() {
	g(1, toolkit.StatusbarH)
	h(1, toolkit.StatusbarH)
}
`}, false)
	crashes(t, "a call with too many arguments", func() {
		out, _, code := drive(t, "", "--repo", app)
		if code != 2 || !strings.Contains(out, "too many arguments") {
			t.Errorf("exit %d, out %q; want an UNREAD naming the type error", code, out)
		}
	})
}

// metric answers per kind of object; the cases the fixtures never reach.
func TestMetricPerKindOfObject(t *testing.T) {
	in := types.Typ[types.Int]
	img := types.NewPackage("image", "image")
	tk := types.NewPackage(toolkitPath, "toolkit")
	pkgVar := types.NewVar(token.NoPos, tk, "SplitterHandleW", in)
	tk.Scope().Insert(pkgVar)
	hidden := types.NewVar(token.NoPos, tk, "splitterW", in)
	tk.Scope().Insert(hidden)
	local := types.NewVar(token.NoPos, tk, "SplitterHandleH", in) // never inserted: not package-level
	for _, c := range []struct {
		name string
		o    types.Object
		want bool
	}{
		{"an image type", types.NewTypeName(token.NoPos, img, "Point", nil), false},
		{"an image constant", types.NewConst(token.NoPos, img, "X", in, constant.MakeInt64(1)), false},
		{"an image metric field", types.NewField(token.NoPos, img, "X", in, false), true},
		{"an image field that is not a metric", types.NewField(token.NoPos, img, "Pix", in, false), false},
		{"an image metric function", types.NewFunc(token.NoPos, img, "Dx", types.NewSignatureType(nil, nil, nil, nil, nil, false)), true},
		{"an image local variable named like a field", types.NewVar(token.NoPos, img, "X", in), false},
		{"an exported package-level metric variable", pkgVar, true},
		{"an unexported package-level variable", hidden, false},
		{"a variable that is not package-level", local, false},
		{"a toolkit metric constant", types.NewConst(token.NoPos, tk, "StatusbarH", in, constant.MakeInt64(22)), true},
		{"a toolkit constant that is not a metric", types.NewConst(token.NoPos, tk, "KeyEnter", in, constant.MakeInt64(13)), false},
		{"a toolkit type", types.NewTypeName(token.NoPos, tk, "Rect", nil), false},
	} {
		crashes(t, c.name, func() {
			if got := (&analyzer{}).metric(c.o); got != c.want {
				t.Errorf("metric(%s) = %v, want %v", c.name, got, c.want)
			}
		})
	}
}

// comparison on an operator that is not a comparison is not a hit test, and
// geoCall on a call with no geometry argument says so.
func TestComparisonAndGeoCallOnWhatTheyDoNotFit(t *testing.T) {
	a := &analyzer{info: &types.Info{
		Types: map[ast.Expr]types.TypeAndValue{}, Uses: map[*ast.Ident]types.Object{}, Defs: map[*ast.Ident]types.Object{},
	}}
	sum := &ast.BinaryExpr{X: ast.NewIdent("a"), Op: token.ADD, Y: ast.NewIdent("b")}
	if a.comparison(sum) {
		t.Error("a + b is a comparison")
	}
	if a.geoCall(&ast.CallExpr{Fun: ast.NewIdent("f"), Args: []ast.Expr{ast.NewIdent("a")}}) {
		t.Error("a call over no geometry is geometry")
	}
}

// The guards that a deletion only turns into a crash get an assertion of their
// own: what is not a comparison counts zero, what is not a field selector is not
// a geometry field, and a go.mod that does not parse is an error and not a module.
func TestGuardsAnswerTheirValue(t *testing.T) {
	a := &analyzer{info: &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Uses: map[*ast.Ident]types.Object{}}}
	crashes(t, "comparisons of a bare name", func() {
		if n := a.comparisons(ast.NewIdent("ok")); n != 0 {
			t.Errorf("comparisons(ident) = %d, want 0", n)
		}
	})
	crashes(t, "geomField of a bare name", func() {
		if a.geomField(ast.NewIdent("x")) {
			t.Error("a bare name is a geometry field")
		}
	})
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("this is not a go.mod\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	crashes(t, "moduleKind on a go.mod that does not parse", func() {
		c, ex, err := moduleKind(dir)
		if err == nil || !strings.Contains(err.Error(), "go.mod") || c || ex != "" {
			t.Errorf("moduleKind = %v, %q, %v; want an error naming go.mod", c, ex, err)
		}
	})
}

// backendType skips a package that was not type-checked and still finds the
// implementation in the next one.
func TestBackendTypeSkipsAnUntypedPackage(t *testing.T) {
	pp := types.NewPackage(painterPath, "painter")
	pp.Scope().Insert(types.NewTypeName(token.NoPos, pp, "Painter", types.NewNamed(types.NewTypeName(token.NoPos, pp, "Painter", nil), types.NewInterfaceType(nil, nil).Complete(), nil)))
	impl := types.NewPackage("example.com/b", "b")
	tn := types.NewTypeName(token.NoPos, impl, "Soft", nil)
	types.NewNamed(tn, types.NewStruct(nil, nil), nil)
	impl.Scope().Insert(tn)
	pkgs := []*packages.Package{
		{PkgPath: "example.com/broken"},
		{PkgPath: painterPath, Types: pp},
		{PkgPath: "example.com/b", Types: impl},
	}
	crashes(t, "backendType with an untyped package", func() {
		if got := backendType(pkgs); got != "example.com/b.Soft" {
			t.Errorf("backendType = %q, want example.com/b.Soft", got)
		}
	})
}

// A driver answering no package at all is unread, and a package that carries no
// type information is reported (errors, files a constraint left out) and skipped.
func TestLoadResultsThatCarryNothingToRead(t *testing.T) {
	app := consumer(t, "", map[string]string{"x.go": "package x\n"}, false)
	old := loadPackages
	t.Cleanup(func() { loadPackages = old })

	loadPackages = func(*packages.Config, ...string) ([]*packages.Package, error) { return nil, nil }
	if out, _, code := drive(t, "", "--repo", app); code != 2 || !strings.Contains(out, "UNREAD: go list: no packages\n") {
		t.Errorf("no packages: exit %d, out %q", code, out)
	}

	loadPackages = func(*packages.Config, ...string) ([]*packages.Package, error) {
		return []*packages.Package{{
			PkgPath:      "example.com/f",
			Errors:       []packages.Error{{Msg: "boom"}},
			IgnoredFiles: []string{"rel/skip.go", "rel/skip_test.go"},
		}}, nil
	}
	out, _, code := drive(t, "", "--repo", app)
	want := "UNREAD: example.com/f: boom\nUNREAD: rel/skip.go: excluded by build constraints, not read\n"
	if code != 2 || !strings.Contains(out, want) || strings.Contains(out, "skip_test.go") {
		t.Errorf("untyped package: exit %d, out %q; want exit 2 containing %q and no test file", code, out, want)
	}
}

// A hunk header that does not parse adds nothing and does not hide the next one.
func TestParseAddedSkipsAMalformedHunkHeader(t *testing.T) {
	got := parseAdded("+++ b/x.go\n@@ nonsense @@\n@@ -1 +2,3 @@\n")
	if len(got) != 1 || len(got["x.go"]) != 1 || got["x.go"][0] != (span{2, 4}) {
		t.Errorf("parseAdded = %v, want x.go:[{2 4}]", got)
	}
}

// git -C with a relative path is relative to the directory the command runs in,
// and a second -C is relative to the first.
func TestPushDirRelativeDashC(t *testing.T) {
	for _, tc := range []struct{ cmd, dir string }{
		{"git -C sub push", "/w/sub"},
		{"git -C /r -C sub push origin x", "/r/sub"},
		{"cd /a && git -C \"b\" push", "/a/b"},
	} {
		if d, ok := pushDir(tc.cmd, "/w"); !ok || d != tc.dir {
			t.Errorf("%q: got (%q, %v), want (%q, true)", tc.cmd, d, ok, tc.dir)
		}
	}
}

// --diff on the toolkit itself is exempt before any base is read.
func TestDiffOnTheToolkitIsExempt(t *testing.T) {
	out, _, code := drive(t, "", "--diff", "no-such-ref", "--dir", "testdata/toolkitself")
	if code != 0 || !strings.Contains(out, "handlayout: exempt: the module is github.com/go-widgets/toolkit itself") {
		t.Errorf("exit %d, out %q", code, out)
	}
}

// --diff without --dir audits the current directory.
func TestDiffDefaultsToTheCurrentDirectory(t *testing.T) {
	app := repo(t)
	base := sh(t, app, "rev-parse", "HEAD")
	t.Chdir(app)
	out, _, code := drive(t, "", "--diff", base)
	if code != 0 || !strings.Contains(out, "handlayout: clean: no finding added by "+base+"..HEAD") {
		t.Errorf("exit %d, out %q", code, out)
	}
	if got, _ := os.Getwd(); filepath.Base(got) != "app" {
		t.Fatalf("not in the repository: %s", got)
	}
}

// main wires the staleness note and the command to the process; driven through
// os.Args and the exit seam so its own two lines are held by a test.
func TestMainRunsTheCommandAndExitsWithItsStatus(t *testing.T) {
	oldArgs, oldExit := os.Args, cli.OsExit
	t.Cleanup(func() { os.Args, cli.OsExit = oldArgs, oldExit })
	exited := -1
	cli.OsExit = func(code int) { exited = code }
	os.Args = []string{"handlayout"}
	main()
	if exited != 2 {
		t.Errorf("no mode exited %d, want 2", exited)
	}
	exited = -1
	os.Args = []string{"handlayout", "--repo", "testdata/plain"}
	main()
	if exited != -1 {
		t.Errorf("a non-consumer exited %d, want no exit call (status 0)", exited)
	}
}
