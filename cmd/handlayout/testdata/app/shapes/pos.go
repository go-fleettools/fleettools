// Package shapes holds one positive for each hand-layout SHAPE of fleettools#387;
// the test pins the line of every marked statement (// WANT <detector>), and
// neg.go holds the twin of each shape, which must stay silent.
package shapes

import (
	"image"
	"math"

	"github.com/go-widgets/painter"
	"github.com/go-widgets/painter/ink"
	"github.com/go-widgets/toolkit"
)

const rowH = 18

// SINK: a geometry composite literal fed arithmetic over application values.
func RowRect(i int) toolkit.Rect {
	return toolkit.Rect{X: 10 + i*rowH, Y: 0, W: 4, H: 4} // WANT layout-arithmetic
}

// SINK: arithmetic stored into a geometry field.
func Place(r *toolkit.Rect, a, b int) {
	r.X = a + b // WANT layout-arithmetic
}

// SINK: arithmetic handed to a geometry-named parameter of a toolkit function.
func Pane(a, b int) *toolkit.SplitPane {
	return toolkit.NewSplitPane("x", a*b) // WANT layout-arithmetic
}

// CARRIER: a parameter fed geometry by a caller.
func lay(w, h int) int {
	return w/2 + h // WANT layout-arithmetic
}

func UseLay(r toolkit.Rect) int { return lay(r.W, r.H) }

// CARRIER: an application struct field set from geometry, then computed on.
type scene struct{ w, n int }

func (s *scene) set(r toolkit.Rect) { s.w = r.W }

func (s *scene) half() int {
	return s.w / 2 // WANT layout-arithmetic
}

// CARRIER: a field set in a struct literal.
func newScene(r toolkit.Rect) *scene { return &scene{w: r.H} }

// CARRIER: a package variable.
var gap = toolkit.SplitterHandleW

func useGap(x int) int {
	return x + gap // WANT layout-arithmetic
}

// ACCESSOR: Size() of a toolkit value.
func FromSize(win toolkit.Win) int {
	w, h := win.Size()
	return w/2 + h/2 // WANT layout-arithmetic
}

// FIELDS: Cols and Offset of toolkit structs.
func Cells(g *toolkit.Grid, w int) int {
	return w / g.Cols // WANT layout-arithmetic
}

func Scrolled(s *toolkit.Scroll) int {
	return s.Offset * 3 // WANT layout-arithmetic
}

// IMAGE: image.Rectangle and image.Point.
func Mid(r image.Rectangle) int {
	return r.Dx() / 2 // WANT layout-arithmetic
}

func Shift(p image.Point) int {
	return p.X + 4 // WANT layout-arithmetic
}

func Inside(p image.Point, r image.Rectangle) bool {
	return p.In(r) // WANT hit-test
}

// PAINTER BY INDIRECTION: an application interface, a method value, a painter
// subpackage, a toolkit drawing function.
type canvas interface {
	FillRect(r painter.Rect, c painter.RGBA)
}

func ViaIface(c canvas) {
	c.FillRect(painter.Rect{}, painter.RGBA{}) // WANT painter-primitive
}

func MethodValue(p painter.Painter) {
	f := p.FillRect // WANT painter-primitive
	_ = f
}

func Sub() {
	ink.Stroke(painter.Rect{}, painter.RGBA{}) // WANT painter-primitive
}

func Frame(p painter.Painter) {
	toolkit.DrawFrame(p, toolkit.Rect{}) // WANT painter-primitive
}

// PRIMITIVE NAMES beyond the old prefix list.
func Names(p painter.Painter) {
	p.Line(0, 0, 1, 1, painter.RGBA{}) // WANT painter-primitive
	p.Clear(painter.RGBA{})            // WANT painter-primitive
	p.SetPixel(1, 1, painter.RGBA{})   // WANT painter-primitive
}

// COMPOUND ASSIGNMENT and ++ / --.
func Grow(r *toolkit.Rect, pad int) {
	r.X += pad // WANT layout-arithmetic
}

func Shrink(r toolkit.Rect) int {
	w := r.W
	w -= 10 // WANT layout-arithmetic
	return w
}

func Count(r toolkit.Rect) int {
	x := r.X
	x++ // WANT layout-arithmetic
	return x
}

// OTHER OPERATORS and clamps.
func Ops(r toolkit.Rect) {
	a := r.W % 3                          // WANT layout-arithmetic
	b := r.W >> 1                         // WANT layout-arithmetic
	c := -r.W                             // WANT layout-arithmetic
	d := max(r.W, 100)                    // WANT layout-arithmetic
	e := int(math.Max(float64(r.W), 100)) // WANT layout-arithmetic
	_, _, _, _, _ = a, b, c, d, e
}

// SPLIT HIT-TESTS: early returns with one comparison each, ==, Contains.
func Edge(x, y int, b toolkit.Rect) bool {
	if x < b.X { // WANT hit-test
		return false
	}
	if y < b.Y { // WANT hit-test
		return false
	}
	return true
}

func Eq(x, y int, b toolkit.Rect) bool {
	return x == b.X && y == b.Y // WANT hit-test
}

func Has(r toolkit.Rect) bool {
	return r.Contains(1, 2) // WANT hit-test
}

// ONE finding for a sink whose element is itself arithmetic over geometry.
func Once(r toolkit.Rect) toolkit.Rect {
	return toolkit.Rect{X: r.W + 4} // WANT layout-arithmetic
}
