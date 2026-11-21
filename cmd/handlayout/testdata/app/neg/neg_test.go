package neg

import (
	"testing"

	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"
)

// A test file may draw and compute: handlayout never reads _test.go.
func TestExempt(t *testing.T) {
	var p painter.Painter
	p.FillRect(painter.Rect{}, painter.RGBA{})
	_ = toolkit.TextWidth("x") / 3
}
