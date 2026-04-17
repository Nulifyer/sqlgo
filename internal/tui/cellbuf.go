package tui

import (
	"github.com/Nulifyer/sqlgo/internal/tui/term"
)

// Type aliases so all existing tui/ code continues to compile unchanged.
type Style = term.Style
type cellAttrs = term.CellAttrs
type cell = term.Cell
type cellbuf = term.Cellbuf

const (
	attrBold      = term.AttrBold
	attrUnderline = term.AttrUnderline
	attrReverse   = term.AttrReverse
)

func newCellbuf(w, h int) *cellbuf    { return term.NewCellbuf(w, h) }
func defaultStyle() Style             { return term.DefaultStyle() }
func runeDisplayWidth(r rune) int     { return term.RuneDisplayWidth(r) }
func stringDisplayWidth(s string) int { return term.StringDisplayWidth(s) }
func isWideRune(r rune) bool          { return term.IsWideRune(r) }
