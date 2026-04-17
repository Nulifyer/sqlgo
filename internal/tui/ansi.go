package tui

import (
	"github.com/Nulifyer/sqlgo/internal/tui/term"
)

// ANSI constant shims. New code should import term/ directly; these
// aliases keep existing call sites in tui/ compiling unchanged.

const (
	esc = "\x1b"

	cursorHide   = term.CursorHide
	cursorShow   = term.CursorShow
	altScreenOn  = term.AltScreenOn
	altScreenOff = term.AltScreenOff
	resetStyle   = term.ResetStyle
	mouseOn      = term.MouseOn
	mouseOff     = term.MouseOff
	pasteOn      = term.PasteOn
	pasteOff     = term.PasteOff
)
