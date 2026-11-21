// Package ink is a painter subpackage: drawing helpers built on the painter.
package ink

import "github.com/go-widgets/painter"

func Stroke(r painter.Rect, c painter.RGBA) {}

// Height is a metric, not a primitive.
func LineHeight() int { return 14 }
