package shapes

import (
	"image"
	"math"

	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"
)

// Twin of each shape in pos.go: nothing here may fire.

// literals: plain values and constants
func LitOK(x, y int) toolkit.Rect { return toolkit.Rect{X: x, Y: y, W: 4, H: 4} }
func LitConst() toolkit.Rect      { return toolkit.Rect{X: 2 * 8} }

// field assignment without arithmetic
func PlaceOK(r *toolkit.Rect, a int) { r.X = a }

// arguments without arithmetic, and constant arithmetic
func PaneOK(w int) *toolkit.SplitPane { return toolkit.NewSplitPane("x", w) }
func PaneConst() *toolkit.SplitPane   { return toolkit.NewSplitPane("x", 3*40) }

// a parameter never fed geometry
func addApp(w, h int) int { return w/2 + h }
func CallApp() int        { return addApp(3, 4) }

// application fields never fed geometry
type counter struct{ n int }

func (c *counter) bump(k int) { c.n += k; c.n++ }
func (c *counter) twice() int { return c.n * 2 }

// a package variable never fed geometry
var base = 7

func useBase(x int) int { return x + base }

// Size() of an application type
type sizer struct{}

func (sizer) Size() (int, int) { return 1, 2 }
func AppSize(s sizer) int {
	w, h := s.Size()
	return w + h
}

// image members that carry no geometry
func Stride(img *image.RGBA) int { return img.Stride + 1 }

// painter: an interface with no painter value type, delegation, size reads
type ledger interface{ Fill(n int) }

func UseLedger(l ledger) { l.Fill(3) }

func Delegate(w toolkit.Widget, p painter.Painter) { w.Draw(p) }

func SizeOnly(p painter.Painter) (int, int) { return p.Size() }

func Metric() int { return toolkit.LineHeight() }

// compound assignment and ++ on application values
func Acc(xs []int) int {
	total := 0
	for _, x := range xs {
		total += x
	}
	total++
	return total
}

// other operators and clamps on application values
func OpsApp(n int, f float64) (int, float64) {
	return max(n, 3) % 4 >> 1, math.Max(f, 1) + float64(-n)
}

// one early return, an emptiness check, comparisons of application values
func One(x int, b toolkit.Rect) bool {
	if x < b.X {
		return false
	}
	return true
}

func Empty(r toolkit.Rect) bool { return r.W == 0 || r.H == 0 }

func AppRange(i, j int, xs []int) bool {
	if i < 0 {
		return false
	}
	if j < len(xs) {
		return true
	}
	return false
}

// Contains on an application type
type zone struct{}

func (zone) Contains(x, y int) bool { return false }
func InZone(z zone) bool            { return z.Contains(1, 2) }

// Sentinel checks on a value derived from geometry: a lookup's "not found" and
// a bound on a slice are not a hit test.
func Sentinel(r toolkit.Rect, xs []int) bool {
	i := r.X
	if i < 0 {
		return false
	}
	if i >= len(xs) {
		return false
	}
	return true
}

// An application function with a geometry-looking parameter is not the toolkit's.
func pad(w int) int { return w }

func CallPad(a, b int) int { return pad(a * b) }

// Equality against a constant, on either side, is an emptiness or sentinel check.
func Const80(r toolkit.Rect) bool { return r.W == 80 || r.H == 80 }

func Const80Left(r toolkit.Rect) bool { return 80 == r.W || 80 == r.H }

// Bit operations and unary plus are not layout arithmetic.
func Bits(r toolkit.Rect) toolkit.Rect {
	return toolkit.Rect{X: ^r.W, Y: r.W & 3, W: +r.W}
}

// a, r.X = f(): a call is not arithmetic.
func two() (int, int) { return 1, 2 }

func Pair(r *toolkit.Rect) {
	var a int
	a, r.X = two()
	_ = a
}

// Two zero checks, two len checks, two cap checks: index and bounds tests.
func Zeros(r toolkit.Rect) bool {
	i, j := r.X, r.Y
	if i < 0 {
		return false
	}
	if j < 0 {
		return false
	}
	return true
}

func Lens(r toolkit.Rect, xs []int) bool {
	i, j := r.X, r.Y
	if i >= len(xs) {
		return false
	}
	if j >= len(xs) {
		return false
	}
	return true
}

func Caps(r toolkit.Rect, xs []int) bool {
	i, j := r.X, r.Y
	if i >= cap(xs) {
		return false
	}
	if j >= cap(xs) {
		return false
	}
	return true
}
