// Package toolkit stands for the toolkit itself: drawing and layout arithmetic
// are its job, so handlayout must stay silent here.
package toolkit

import "github.com/go-widgets/painter"

const StatusbarH = 22

func Draw(p painter.Painter, r painter.Rect) {
	p.FillRect(painter.Rect{X: r.X + 1, Y: r.Y + StatusbarH, W: r.W - 2, H: r.H}, painter.RGBA{})
}
