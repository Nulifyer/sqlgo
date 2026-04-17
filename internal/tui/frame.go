package tui

import (
	"github.com/Nulifyer/sqlgo/internal/tui/term"
)

// borderSet and borderSingle are kept as local aliases so callers that
// reference them directly (none currently, but kept for safety) compile.
type borderSet = term.BorderSet

var borderSingle = term.BorderSingle

func drawFrame(s *cellbuf, r rect, title string, focused bool) {
	term.DrawFrame(s, r, title, focused)
}

func drawFrameInfo(s *cellbuf, r rect, title, rightInfo string, focused bool) {
	term.DrawFrameInfo(s, r, title, rightInfo, focused)
}
