package tui

import (
	"github.com/Nulifyer/sqlgo/internal/sqltok"
)

// editor is a text-input widget: buffer + viewport + selection +
// clipboard + undo. Scrolls to follow cursor.
//
// Keys: Shift+arrows select, Ctrl+C/X/V clipboard, Ctrl+A all,
// Alt+Z/Y undo/redo (Ctrl+Z/Y eaten by shell job control / BSD
// VDSUSP), Ctrl+Space autocomplete, Ctrl+L clear (via main_layer).
type editor struct {
	buf       *buffer
	scrollRow int
	scrollCol int

	// complete is the live autocomplete popup or nil. One-shot:
	// typing after open dismisses (no prefix refinement).
	complete *completionState

	// Find/replace state. findLayer writes these; empty needle =
	// no search. Draw paints matches with a dim bg and the
	// current one with a stronger accent.
	searchNeedle string
	matches      []matchRange
	currentMatch int // -1 when empty
}

// matchRange is one find/replace hit. row/col are rune indices;
// length is in runes. v1 matches don't cross newlines.
type matchRange struct {
	row    int
	col    int
	length int
}

func newEditor() *editor {
	return &editor{buf: newBuffer()}
}

// handleInsert applies one keypress. Returns true if consumed.
// nil app is fine (tests); clipboard calls become no-ops.
func (e *editor) handleInsert(a *app, k Key) bool {
	// Ctrl+Space: open autocomplete. Handled before Ctrl+rune
	// clipboard block below.
	if k.Ctrl && k.Kind == KeyRune && k.Rune == ' ' {
		e.openCompletion(a)
		return true
	}

	// Popup-owned keys when open: Up/Down navigate, Enter/Tab
	// accept, Esc cancels. Any other key dismisses and falls
	// through so the keystroke still does its normal thing.
	if e.complete != nil {
		switch k.Kind {
		case KeyUp:
			e.complete.moveSelection(-1)
			return true
		case KeyDown:
			e.complete.moveSelection(1)
			return true
		case KeyEnter, KeyTab:
			e.acceptCompletion()
			return true
		case KeyEsc:
			e.complete = nil
			return true
		}
		e.complete = nil
	}

	// Shift+arrow: extend selection. Handled before the plain-arrow
	// branch so selection is the default when Shift is held. Ctrl +
	// Shift combos extend by a word at a time.
	if k.Shift {
		switch k.Kind {
		case KeyLeft:
			if k.Ctrl {
				e.buf.SelectWordLeft()
			} else {
				e.buf.SelectLeft()
			}
			return true
		case KeyRight:
			if k.Ctrl {
				e.buf.SelectWordRight()
			} else {
				e.buf.SelectRight()
			}
			return true
		case KeyUp:
			e.buf.SelectUp()
			return true
		case KeyDown:
			e.buf.SelectDown()
			return true
		case KeyHome:
			e.buf.SelectHome()
			return true
		case KeyEnd:
			e.buf.SelectEnd()
			return true
		}
	}

	// Ctrl + arrow / Ctrl + backspace / Ctrl + delete: word-granular
	// navigation and deletion. Must be checked before the generic
	// Ctrl+<rune> block below because the keys arrive as non-KeyRune.
	if k.Ctrl {
		switch k.Kind {
		case KeyLeft:
			e.buf.MoveWordLeft()
			return true
		case KeyRight:
			e.buf.MoveWordRight()
			return true
		case KeyBackspace:
			e.buf.DeleteWordLeft()
			return true
		case KeyDelete:
			e.buf.DeleteWordRight()
			return true
		}
	}

	// Ctrl+<letter> clipboard shortcuts. Undo/redo live on Alt instead
	// of Ctrl because Ctrl+Z is intercepted by the shell's job
	// control (SIGTSTP) in several common environments before the
	// raw terminal can deliver the byte, and Ctrl+Y is VDSUSP on
	// BSD/macOS which has the same failure mode.
	if k.Ctrl && k.Kind == KeyRune {
		switch k.Rune {
		case 'c':
			if sel := e.buf.Selection(); sel != "" && a != nil && a.clipboard != nil {
				_ = a.clipboard.Copy(sel)
			}
			return true
		case 'x':
			if sel := e.buf.Selection(); sel != "" && a != nil && a.clipboard != nil {
				_ = a.clipboard.Copy(sel)
				e.buf.DeleteSelection()
			}
			return true
		case 'v':
			if a != nil && a.clipboard != nil {
				if text, err := a.clipboard.Paste(); err == nil && text != "" {
					e.buf.InsertText(text)
				}
			}
			return true
		case 'a':
			e.buf.SelectAll()
			return true
		}
	}

	// Alt+<letter> shortcuts. Alt keys arrive as ESC-prefixed byte
	// sequences and aren't eligible for shell signal interception,
	// so they're safe homes for undo/redo. Any other Alt combo is
	// swallowed here so the raw letter doesn't end up inserted into
	// the buffer on a miss.
	if k.Alt && k.Kind == KeyRune {
		switch k.Rune {
		case 'z', 'Z':
			e.buf.Undo()
		case 'y', 'Y':
			e.buf.Redo()
		}
		return true
	}

	switch k.Kind {
	case KeyRune:
		if k.Ctrl {
			// Ctrl+<rune> combos not handled above fall through to
			// main_layer (e.g. Ctrl+L clear).
			return false
		}
		e.buf.Insert(k.Rune)
		return true
	case KeyEnter:
		e.buf.InsertNewline()
		return true
	case KeyBackspace:
		e.buf.Backspace()
		return true
	case KeyDelete:
		e.buf.Delete()
		return true
	case KeyLeft:
		e.buf.MoveLeft()
		return true
	case KeyRight:
		e.buf.MoveRight()
		return true
	case KeyUp:
		e.buf.MoveUp()
		return true
	case KeyDown:
		e.buf.MoveDown()
		return true
	case KeyHome:
		e.buf.MoveHome()
		return true
	case KeyEnd:
		e.buf.MoveEnd()
		return true
	case KeyTab:
		// Soft tabs: insert spaces up to the next 4-column stop so
		// Tab aligns visually regardless of the current cursor
		// column.
		_, col := e.buf.Cursor()
		for n := 4 - (col % 4); n > 0; n-- {
			e.buf.Insert(' ')
		}
		return true
	}
	return false
}

// draw renders the editor into the interior of r (inside the border).
// If cursorVisible is true it also registers the terminal cursor
// position on the screen so it gets moved after flush.
//
// Each visible line is tokenized with sqltok and each rune is painted
// with the style belonging to whichever token contains it. When a
// selection is active, selected cells are painted with a reversed
// style instead -- the selection always wins over syntax highlighting
// so the user can see their selection clearly. Trailing empty space
// on multi-line selections is also filled with the selection style so
// the highlight reads as a continuous block.
func (e *editor) draw(s *cellbuf, r rect, cursorVisible bool) {
	innerRow := r.row + 1
	innerCol := r.col + 1
	innerW := r.w - 2
	innerH := r.h - 2
	if innerW <= 0 || innerH <= 0 {
		return
	}

	e.ensureCursorVisible(innerW, innerH)

	selStyle := Style{FG: ansiDefault, BG: ansiDefaultBG, Attrs: attrReverse}
	hasSel := e.buf.HasSelection()
	var selR1, selC1, selR2, selC2 int
	if hasSel {
		selR1, selC1, selR2, selC2 = e.buf.normalizedSelection()
	}

	// Find highlights. Selection wins over matches.
	matchBg := Style{FG: ansiDefault, BG: ansiBrightBlack}
	currentMatchBg := Style{FG: ansiDefault, BG: ansiDefaultBG, Attrs: attrReverse | attrUnderline}
	hasSearch := e.searchNeedle != "" && len(e.matches) > 0

	for i := 0; i < innerH; i++ {
		lineIdx := e.scrollRow + i
		if lineIdx >= e.buf.LineCount() {
			break
		}
		line := e.buf.Line(lineIdx)

		tokens := sqltok.TokenizeLine(line)

		// Walk runes from scrollCol onward. colOut advances by
		// runewidth so wide glyphs take 2 columns.
		colOut := 0
		for vc := e.scrollCol; vc < len(line); vc++ {
			r := line[vc]
			rw := runeDisplayWidth(r)
			if rw == 0 {
				continue
			}
			if colOut >= innerW {
				break
			}
			st := styleForCol(tokens, vc)
			if hasSearch {
				if isCurrent, inMatch := e.matchStyleAt(lineIdx, vc); inMatch {
					if isCurrent {
						st = currentMatchBg
					} else {
						st = matchBg
					}
				}
			}
			if hasSel && inSelection(lineIdx, vc, selR1, selC1, selR2, selC2) {
				st = selStyle
			}
			// If the wide rune would spill past the right edge,
			// paint a space instead so no half-glyph leaks.
			if rw == 2 && colOut+2 > innerW {
				s.writeStyled(innerRow+i, innerCol+colOut, " ", st)
				colOut++
				break
			}
			s.writeStyled(innerRow+i, innerCol+colOut, string(r), st)
			colOut += rw
		}

		// Extend the selection highlight across trailing empty space
		// on wrapped or short lines so a multi-line block selection
		// looks continuous.
		if hasSel && lineIdx >= selR1 && lineIdx < selR2 {
			if colOut < 0 {
				colOut = 0
			}
			for colOut < innerW {
				s.writeStyled(innerRow+i, innerCol+colOut, " ", selStyle)
				colOut++
			}
		}
	}

	if cursorVisible {
		row, col := e.buf.Cursor()
		s.placeCursor(innerRow+(row-e.scrollRow), innerCol+(col-e.scrollCol))
	}

	if e.complete != nil {
		e.drawComplete(s, innerRow, innerCol, innerW, innerH)
	}
}

// styleForCol returns the syntax-highlight style for the column offset
// within a line. A linear scan is fine here because the token list per
// line is short and the editor's viewport is likewise bounded by
// innerW. Whitespace between tokens and anything past the last token
// fall back to the default style.
func styleForCol(tokens []sqltok.Token, col int) Style {
	for _, t := range tokens {
		if col < t.StartCol {
			return defaultStyle()
		}
		if col < t.EndCol {
			return styleForKind(t.Kind)
		}
	}
	return defaultStyle()
}

// styleForKind maps a tokenizer kind to the current theme's
// corresponding Style. Text / Ident / Whitespace fall through to the
// default so identifiers retain readable contrast against the user's
// terminal background.
func styleForKind(k sqltok.Kind) Style {
	switch k {
	case sqltok.Keyword:
		return currentTheme.SQLKeyword
	case sqltok.String:
		return currentTheme.SQLString
	case sqltok.Number:
		return currentTheme.SQLNumber
	case sqltok.Comment:
		return currentTheme.SQLComment
	case sqltok.Operator:
		return currentTheme.SQLOperator
	case sqltok.Punct:
		return currentTheme.SQLPunct
	}
	return defaultStyle()
}

// inSelection reports whether (row, col) falls inside a normalized
// selection range [r1,c1 .. r2,c2). Exclusive on the high end so the
// cell under the cursor (when the cursor is the selection end) isn't
// counted.
func inSelection(row, col, r1, c1, r2, c2 int) bool {
	if row < r1 || row > r2 {
		return false
	}
	if row == r1 && col < c1 {
		return false
	}
	if row == r2 && col >= c2 {
		return false
	}
	return true
}

// ensureCursorVisible scrolls the viewport so the cursor sits inside
// the visible region. Called at draw time so the viewport reacts to
// resizes.
func (e *editor) ensureCursorVisible(innerW, innerH int) {
	row, col := e.buf.Cursor()
	if row < e.scrollRow {
		e.scrollRow = row
	} else if row >= e.scrollRow+innerH {
		e.scrollRow = row - innerH + 1
	}
	if col < e.scrollCol {
		e.scrollCol = col
	} else if col >= e.scrollCol+innerW {
		e.scrollCol = col - innerW + 1
	}
	if e.scrollRow < 0 {
		e.scrollRow = 0
	}
	if e.scrollCol < 0 {
		e.scrollCol = 0
	}
}
