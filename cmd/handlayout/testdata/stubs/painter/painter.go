// Package painter is a minimal stand-in for github.com/go-widgets/painter, with
// the names and shapes handlayout resolves (painter.go in the real module).
package painter

type Rect struct{ X, Y, W, H int }

func (r Rect) Contains(px, py int) bool {
	return px >= r.X && px < r.X+r.W && py >= r.Y && py < r.Y+r.H
}

type RGBA struct{ R, G, B, A uint8 }

type Painter interface {
	FillRect(r Rect, c RGBA)
	StrokeRect(r Rect, c RGBA, lineW int)
	FillRoundRect(r Rect, radius int, c RGBA)
	PutPixel(x, y int, c RGBA)
	Text(x, y int, s string, ink RGBA)
	Size() (w, h int)
}
