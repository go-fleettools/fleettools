// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"fmt"
	"go/ast"
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
var metricName = regexp.MustCompile(`^(X|Y|W|H|Min|Max|Pad|Padding|Gap|Spacing|Margin|Width|Height|Left|Right|Top|Bottom|Size|Offset|Cols|Rows|Columns)$|(W|H|Pad|Gap|Margin|Spacing|Height|Width|Inset|Radius)$`)

// metricFunc is a toolkit/painter function or method that answers a font,
// glyph, density or geometry metric: the accessors Size() and Bounds() too.
var metricFunc = regexp.MustCompile(`^(TextWidth|TextHeight|Measure.*|LineHeight|GlyphW.*|GlyphH.*|FontHeight|TouchTarget|Wrap.*|Preferred.*|Pref.*|Size|Bounds|Offset|Inner.*|Content.*|Cols|Rows|Columns)$`)

// primitiveName is a painter drawing primitive: rect fills and strokes,
// glyph and text draws, image and mask blits, pixels, lines, arcs, clears.
var primitiveName = regexp.MustCompile(`^(Fill|Stroke|Draw|Blit|Put|Text|Image|Mask|Glyph|SetPixel|Clear|Arc|Circle|Ellipse|Polygon|Polyline)|^(Line|Lines|LineTo|Rect|Rects)$`)

// imageFunc and imageField are the members of image.Rectangle and image.Point
// that carry geometry.
var (
	imageFunc  = regexp.MustCompile(`^(Dx|Dy|Size)$`)
	imageField = regexp.MustCompile(`^(X|Y|Min|Max)$`)
)

// geomTypeName names the toolkit/painter geometry structs; for package image
// only Rectangle and Point count.
var geomTypeName = regexp.MustCompile(`^(Rect|Point|Size|Insets?|Padding|Margins?|Offset|Edges?|Rectangle)$`)

// geomParam is the name of a function parameter that takes a geometry value.
var geomParam = regexp.MustCompile(`(?i)^(x|y|w|h|width|height|min|max|pad|padding|gap|spacing|margin|left|right|top|bottom|size|offset)$`)

// hitMethod is a Rect/Point method that is a hit test.
var hitMethod = regexp.MustCompile(`^(Contains|In|Overlaps)$`)

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
		switch mp := mf.Module.Mod.Path; {
		case mp == toolkitPath || mp == painterPath:
			return false, "the module is " + mp + " itself", nil
		case isBackendPath(mp):
			return false, "the module " + mp + " is a painter backend (its primitives are the painter's own code)", nil
		}
	}
	for _, r := range mf.Require {
		if r.Mod.Path == toolkitPath || r.Mod.Path == painterPath {
			consumer = true
		}
	}
	return consumer, "", nil
}

// isBackendPath: a go-widgets module whose name says painter (gpupainter,
// softpainter...) is a painter backend, not an application.
func isBackendPath(mp string) bool {
	rest, ok := strings.CutPrefix(mp, "github.com/go-widgets/")
	return ok && rest != "painter" && !strings.Contains(rest, "/") && strings.HasSuffix(rest, "painter")
}

// backendType names a type of the audited packages that implements
// painter.Painter, or "": such a module is a painter backend whatever it is
// called.
func backendType(pkgs []*packages.Package) string {
	var iface *types.Interface
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		if p.PkgPath == painterPath && p.Types != nil {
			if o := p.Types.Scope().Lookup("Painter"); o != nil {
				iface, _ = o.Type().Underlying().(*types.Interface)
			}
		}
	})
	if iface == nil {
		return ""
	}
	for _, p := range pkgs {
		if p.Types == nil {
			continue
		}
		for _, name := range p.Types.Scope().Names() {
			tn, ok := p.Types.Scope().Lookup(name).(*types.TypeName)
			if !ok || types.IsInterface(tn.Type()) {
				continue
			}
			if types.Implements(tn.Type(), iface) || types.Implements(types.NewPointer(tn.Type()), iface) {
				return p.PkgPath + "." + name
			}
		}
	}
	return ""
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
	if bt := backendType(pkgs); bt != "" {
		a.Exempt = "the module implements painter.Painter (" + bt + "): a painter backend, its primitives are the painter's own code"
		return a
	}
	rel := func(name string) string {
		r, err := filepath.Rel(dir, name)
		if err != nil {
			r = name
		}
		return filepath.ToSlash(r)
	}
	for _, p := range pkgs {
		for _, e := range p.Errors {
			a.Unread = append(a.Unread, fmt.Sprintf("%s: %s", p.PkgPath, e.Msg))
		}
		// ⛔ A file a build constraint leaves out is never loaded: say so, so
		// that a clean report is not read as covering it.
		for _, name := range p.IgnoredFiles {
			if !strings.HasSuffix(name, "_test.go") {
				a.Unread = append(a.Unread, rel(name)+": excluded by build constraints, not read")
			}
		}
		if p.TypesInfo == nil {
			continue
		}
		an := &analyzer{fset: p.Fset, info: p.TypesInfo, tainted: map[types.Object]bool{}, seen: map[ast.Node]bool{}}
		var files []srcFile
		for _, f := range p.Syntax {
			name := p.Fset.File(f.Pos()).Name()
			if !strings.HasSuffix(name, "_test.go") {
				files = append(files, srcFile{rel(name), f})
			}
		}
		an.runPackage(files)
		a.Findings = append(a.Findings, an.out...)
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
