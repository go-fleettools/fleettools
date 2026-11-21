package main

import (
	"go/constant"
	"go/token"
	"go/types"
	"testing"

	"golang.org/x/tools/go/packages"
)

// crashes runs f and reports a panic as a failure naming what was asked: a guard
// that stops a nil dereference is only tested by driving the nil through it.
func crashes(t *testing.T, what string, f func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s: crashed instead of answering: %v", what, r)
		}
	}()
	f()
}

func namedType(pkgPath, name string, under types.Type) types.Type {
	var pkg *types.Package
	if pkgPath != "" {
		pkg = types.NewPackage(pkgPath, "p")
	}
	return types.NewNamed(types.NewTypeName(token.NoPos, pkg, name, nil), under, nil)
}

// geomType on both sides of each of its guards.
func TestGeomType(t *testing.T) {
	st := types.NewStruct(nil, nil)
	rect := namedType(toolkitPath, "Rect", st)
	for _, c := range []struct {
		name string
		t    types.Type
		want bool
	}{
		{"toolkit Rect", rect, true},
		{"pointer to toolkit Rect", types.NewPointer(rect), true},
		{"painter Point", namedType(painterPath, "Point", st), true},
		{"image.Rectangle", namedType("image", "Rectangle", st), true},
		{"image.Point", namedType("image", "Point", st), true},
		{"image.Config", namedType("image", "Config", st), false},
		{"a toolkit type that is not a struct", namedType(toolkitPath, "Size", types.Typ[types.Int]), false},
		{"a toolkit struct that is not geometry", namedType(toolkitPath, "Widget", st), false},
		{"an application Rect", namedType("example.com/app", "Rect", st), false},
		{"a basic type", types.Typ[types.Int], false},
	} {
		crashes(t, c.name, func() {
			if got := (&analyzer{}).geomType(c.t); got != c.want {
				t.Errorf("geomType(%s) = %v, want %v", c.name, got, c.want)
			}
		})
	}
	crashes(t, "a named struct without a package", func() {
		if (&analyzer{}).geomType(namedType("", "Rect", st)) {
			t.Error("a type without a package is geometry")
		}
	})
	crashes(t, "the universe error type", func() {
		if (&analyzer{}).geomType(types.Universe.Lookup("error").Type()) {
			t.Error("error is geometry")
		}
	})
}

// taint: which objects become carriers.
func TestTaint(t *testing.T) {
	tk := types.NewPackage(toolkitPath, "toolkit")
	img := types.NewPackage("image", "image")
	app := types.NewPackage("example.com/app", "app")
	in := types.Typ[types.Int]
	for _, c := range []struct {
		name string
		o    types.Object
		want bool
	}{
		{"a numeric variable", types.NewVar(token.NoPos, app, "x", in), true},
		{"a numeric parameter", types.NewParam(token.NoPos, app, "w", in), true},
		{"the blank identifier", types.NewVar(token.NoPos, app, "_", in), false},
		{"a string variable", types.NewVar(token.NoPos, app, "s", types.Typ[types.String]), false},
		{"a constant", types.NewConst(token.NoPos, app, "c", in, constant.MakeInt64(1)), false},
		{"an application field", types.NewField(token.NoPos, app, "w", in, false), true},
		{"a toolkit field (a source, not a carrier)", types.NewField(token.NoPos, tk, "W", in, false), false},
		{"an image field", types.NewField(token.NoPos, img, "X", in, false), false},
		{"a field without a package", types.NewField(token.NoPos, nil, "w", in, false), true},
	} {
		crashes(t, c.name, func() {
			a := &analyzer{tainted: map[types.Object]bool{}}
			a.taint(c.o)
			if got := a.tainted[c.o]; got != c.want {
				t.Errorf("taint(%s): tainted = %v, want %v", c.name, got, c.want)
			}
		})
	}
}

// metric must answer false, not crash, for what has no package.
func TestMetricWithoutAPackage(t *testing.T) {
	crashes(t, "metric(nil)", func() {
		if (&analyzer{}).metric(nil) {
			t.Error("metric(nil) is true")
		}
	})
	for _, name := range []string{"len", "nil", "true", "error"} {
		crashes(t, "metric("+name+")", func() {
			if (&analyzer{}).metric(types.Universe.Lookup(name)) {
				t.Errorf("metric(%s) is true", name)
			}
		})
	}
}

// backendType over packages that never import the painter: no interface to
// implement, so no backend, and no crash.
func TestBackendTypeWithoutThePainter(t *testing.T) {
	pkg := types.NewPackage("example.com/app", "app")
	pkg.Scope().Insert(types.NewTypeName(token.NoPos, pkg, "T", types.NewNamed(types.NewTypeName(token.NoPos, pkg, "T", nil), types.NewStruct(nil, nil), nil)))
	crashes(t, "backendType without the painter", func() {
		if got := backendType([]*packages.Package{{PkgPath: "example.com/app", Types: pkg}}); got != "" {
			t.Errorf("backendType = %q, want none", got)
		}
	})
}
