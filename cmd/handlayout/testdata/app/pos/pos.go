// Package pos holds one positive for each detector; the test pins the line of
// every marked statement, so keep the markers (// WANT <detector>) in step.
package pos

import (
	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"
)

type Panel struct {
	toolkit.Base
	w, h int
}

// Draw paints by hand: an application widget drawing its own UI.
func (p *Panel) Draw(pt painter.Painter) {
	pt.FillRect(painter.Rect{X: 0, Y: 0, W: 4, H: 4}, painter.RGBA{}) // WANT painter-primitive
	pt.Text(1, 1, "hi", painter.RGBA{})                               // WANT painter-primitive
}

// Wrap computes a wrap width from a font metric.
func Wrap(w int) int {
	avg := toolkit.TextWidth("abcdefghijklmnopqrstuvwxyz0123456789 ") / 38 // WANT layout-arithmetic
	return w / avg                                                         // WANT layout-arithmetic
}

// Share sizes a pane from a toolkit constant.
func (p *Panel) Share(room int) int {
	return room - toolkit.SplitterHandleW*2 // WANT layout-arithmetic
}

// Inner places a child inside the bounds of a widget.
func (p *Panel) Inner() toolkit.Rect {
	b := p.Bounds()
	return toolkit.Rect{X: b.X + 4, Y: b.Y, W: b.W - 8, H: b.H} // WANT layout-arithmetic
}

// Enabled is a hand hit test with a flag in the chain.
func (p *Panel) Enabled(on bool, x int) bool {
	b := p.Bounds()
	return on && x >= b.X && x < b.X+b.W // WANT hit-test
}

// Hit is a hand hit test on widget bounds.
func (p *Panel) Hit(x, y int) bool {
	b := p.Bounds()
	return x >= b.X && x < b.X+b.W && y >= b.Y && y < b.Y+b.H // WANT hit-test
}
