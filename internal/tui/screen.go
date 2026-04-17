package tui

import (
	"io"

	"github.com/Nulifyer/sqlgo/internal/tui/term"
)

// screen is an alias for term.Screen so existing tui/ code compiles unchanged.
type screen = term.Screen

func newScreen(out io.Writer, w, h int) *screen { return term.NewScreen(out, w, h) }
