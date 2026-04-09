package tui

import (
	"github.com/Nulifyer/sqlgo/internal/sqltok"
)

// editor is a text-input widget wrapping a buffer and a viewport. It
// draws inside a rect (minus a 1-cell border the caller has already
// drawn) and handles insert-mode keys with selection + clipboard +
// undo support. Horizontal and vertical scrolling follow the cursor.
//
// Selection comes from Shift+Arrow / Shift+Home / Shift+End, which the
// key decoder now reports as Key.Shift. Ctrl+C copies, Ctrl+X cuts,
// Ctrl+V pastes, Ctrl+A selects all, Ctrl+Z undo, Ctrl+Y redo.
// Ctrl+L clears the buffer and is still handled by main_layer so it
// can also reset status text.
type editor struct {
	buf       *buffer
	scrollRow int // index of first visible line
	scrollCol int // index of first visible column
}

func newEditor() *editor {
	return &editor{buf: newBuffer()}
}

// handleInsert applies a keypress in INSERT mode. Returns true if the
// key was consumed. The app parameter gives access to the shared
// clipboard; a nil app still works (no copy/paste) so tests can feed
// keys without wiring a full app.
func (e *editor) handleInsert(a *app, k Key) bool {
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

	// Ctrl+<letter> clipboard + undo shortcuts.
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
		case 'z':
			e.buf.Undo()
			return true
		case 'y':
			e.buf.Redo()
			return true
		}
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

	for i := 0; i < innerH; i++ {
		lineIdx := e.scrollRow + i
		if lineIdx >= e.buf.LineCount() {
			break
		}
		line := e.buf.Line(lineIdx)
		start := e.scrollCol
		if start > len(line) {
			start = len(line)
		}
		end := start + innerW
		if end > len(line) {
			end = len(line)
		}

		// Tokenize the visible slice's underlying line so the token
		// col offsets align with the buffer. StartCol/EndCol in the
		// returned tokens index into `line`, so the viewport math
		// below just compares rune columns directly.
		tokens := sqltok.TokenizeLine(line)

		for vc := start; vc < end; vc++ {
			st := styleForCol(tokens, vc)
			if hasSel && inSelection(lineIdx, vc, selR1, selC1, selR2, selC2) {
				st = selStyle
			}
			s.writeStyled(innerRow+i, innerCol+(vc-start), string(line[vc]), st)
		}

		// Extend the selection highlight across trailing empty space
		// on wrapped or short lines so a multi-line block selection
		// looks continuous.
		if hasSel && lineIdx >= selR1 && lineIdx < selR2 {
			for vc := len(line); vc < start+innerW; vc++ {
				if vc < start {
					continue
				}
				s.writeStyled(innerRow+i, innerCol+(vc-start), " ", selStyle)
			}
		}
	}

	if cursorVisible {
		row, col := e.buf.Cursor()
		s.placeCursor(innerRow+(row-e.scrollRow), innerCol+(col-e.scrollCol))
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
