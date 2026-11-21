// Package backend is a painter backend: it implements painter.Painter, so its
// primitives and scale arithmetic are the painter's own code, not an
// application's.
package backend

import "github.com/go-widgets/painter"

type Soft struct{ scale int }

func (s *Soft) FillRect(r painter.Rect, c painter.RGBA) {
	_ = painter.Rect{X: r.X * s.scale, W: r.W*s.scale + 1}
	s.PutPixel(r.X+1, r.Y, c)
}
func (s *Soft) StrokeRect(r painter.Rect, c painter.RGBA, lineW int) { s.FillRect(r, c) }
func (s *Soft) FillRoundRect(r painter.Rect, radius int, c painter.RGBA) {
	s.FillRect(r, c)
}
func (s *Soft) PutPixel(x, y int, c painter.RGBA)           {}
func (s *Soft) Text(x, y int, str string, ink painter.RGBA) {}
func (s *Soft) Line(x0, y0, x1, y1 int, c painter.RGBA)     {}
func (s *Soft) Clear(c painter.RGBA)                        {}
func (s *Soft) SetPixel(x, y int, c painter.RGBA)           {}
func (s *Soft) Size() (w, h int)                            { return 1, 1 }
