// Package neg holds code that looks like layout and is not: nothing here may
// fire.
package neg

import (
	"fmt"

	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"
)

// Next is plain arithmetic on plain values.
func Next(i int, xs []string) int { return i + 1 + len(xs)*2 - 1 }

// Seconds is arithmetic on app-owned values only.
func Seconds(n int) int { return n*60 + 5 }

// Key does arithmetic on a toolkit constant that is not a metric.
func Key() int { return toolkit.KeyEnter + 1 }

// Label concatenates strings, one of which came from a geometry field: not
// arithmetic on numbers.
func Label(r toolkit.Rect) string { return "w=" + fmt.Sprint(r.W) }

// Range is a bounds check on app values, not on widget bounds.
func Range(i int, xs []string) bool { return i >= 0 && i < len(xs) }

// Build only hands geometry to the toolkit without computing it.
func Build(w toolkit.Widget) *toolkit.Splitter {
	sp := toolkit.NewSplitter(true, &toolkit.SplitPane{Widget: w, Name: "a", Min: 120, Default: 550})
	return sp
}

// Size reads a painter's canvas size: not a drawing primitive.
func Size(p painter.Painter) (int, int) { return p.Size() }

// Local arithmetic on a local struct that merely has fields called X and W.
type box struct{ X, W int }

func Mine(b box) int { return b.X + b.W }
