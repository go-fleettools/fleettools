// Package toolkit is a minimal stand-in for github.com/go-widgets/toolkit.
package toolkit

import "github.com/go-widgets/painter"

type Rect = painter.Rect

const (
	SplitterHandleW   = 10
	StatusbarH        = 22
	NotebookTabStripH = 28
	KeyEnter          = 13 // not a metric: an arithmetic on it must not fire
)

func TextWidth(text string) int { return 6 * len(text) }
func TouchTarget(n int) int     { return n }
func Describe(n int) string     { return "n" }
func Count(items []string) int  { return len(items) }

type Widget interface {
	Bounds() Rect
	SetBounds(Rect)
	Draw(p painter.Painter)
}

type Base struct{ rect Rect }

func (b *Base) Bounds() Rect     { return b.rect }
func (b *Base) SetBounds(r Rect) { b.rect = r }

type SplitPane struct {
	Widget  Widget
	Name    string
	Min     int
	Default int
}

type Splitter struct{ Panes []*SplitPane }

func NewSplitter(vertical bool, panes ...*SplitPane) *Splitter { return &Splitter{Panes: panes} }

type VBox struct {
	Base
	Spacing int
}

func NewVBox() *VBox { return &VBox{} }
