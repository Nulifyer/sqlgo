package tui

import (
	"github.com/Nulifyer/sqlgo/internal/tui/widget"
)

// input aliases widget.Input so existing tui/ code compiles unchanged.
// New code should reference widget.Input directly.
type input = widget.Input

func newInput(seed string) *input { return widget.NewInput(seed) }

func drawInput(c *cellbuf, in *input, row, col, maxW int) {
	widget.DrawInput(c, in, row, col, maxW)
}
