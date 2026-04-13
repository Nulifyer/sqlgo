package tui

import (
	"strconv"
	"strings"

	"github.com/Nulifyer/sqlgo/internal/sqltok"
)

// gutterWidth is the column count reserved at the left edge of the
// editor for line numbers. Scales to fit the largest line number plus
// a one-column separator; floors at 3 (two-digit count + space).
func (e *editor) gutterWidth() int {
	n := e.buf.LineCount()
	if n < 1 {
		n = 1
	}
	digits := len(strconv.Itoa(n))
	if digits < 2 {
		digits = 2
	}
	return digits + 1
}

// editor is a text-input widget: buffer + viewport + selection +
// clipboard + undo. Scrolls to follow cursor.
//
// Keys: Shift+arrows select, Ctrl+C/X/V clipboard, Ctrl+A all,
// Ctrl+Z/Y undo/redo, Ctrl+Space autocomplete, Ctrl+L clear (via
// main_layer).
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

	// extraCursors holds secondary cursor positions for column-
	// add multi-cursor edits. Primary cursor lives on the
	// buffer. v1 invariant: at most one cursor per row.
	extraCursors []cursorPos
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
	// Ctrl+Alt+Up/Down: column-add multi-cursor. Checked first
	// so the Ctrl-arrow word-jump path below doesn't swallow it.
	if k.Ctrl && k.Alt {
		switch k.Kind {
		case KeyUp:
			e.addCursorRelative(-1)
			return true
		case KeyDown:
			e.addCursorRelative(1)
			return true
		}
	}

	// Esc collapses extras when popup is closed. Popup-open Esc
	// is handled in the popup block below and takes precedence.
	if e.complete == nil && k.Kind == KeyEsc && e.hasMultiCursor() {
		e.collapseCursors()
		return true
	}

	// Ctrl+Space: open autocomplete. Handled before Ctrl+rune
	// clipboard block below.
	if k.Ctrl && k.Kind == KeyRune && k.Rune == ' ' {
		// Collapse multi-cursor first; autocomplete accept
		// would need line-boundary aware expansion otherwise.
		e.collapseCursors()
		e.openCompletion(a)
		return true
	}

	// Popup-owned keys when open. Live refine: ident chars and
	// '.' insert + re-open so the filter narrows as you type.
	// Backspace deletes + re-opens. Non-ident keys dismiss.
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
		case KeyBackspace:
			e.buf.Backspace()
			e.openCompletion(a)
			return true
		case KeyRune:
			if !k.Ctrl && !k.Alt && (isIdentRune(k.Rune) || k.Rune == '.') {
				e.buf.Insert(k.Rune)
				e.openCompletion(a)
				return true
			}
		}
		e.complete = nil
	}

	// Shift+arrow: extend selection. Collapses multi-cursor
	// first -- selection doesn't fan out in v1.
	if k.Shift {
		switch k.Kind {
		case KeyLeft, KeyRight, KeyUp, KeyDown, KeyHome, KeyEnd:
			e.collapseCursors()
		}
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
			if k.Ctrl {
				e.buf.SelectBufferStart()
			} else {
				e.buf.SelectHome()
			}
			return true
		case KeyEnd:
			if k.Ctrl {
				e.buf.SelectBufferEnd()
			} else {
				e.buf.SelectEnd()
			}
			return true
		}
	}

	// Ctrl+arrow / Ctrl+backspace / Ctrl+delete: word-granular.
	if k.Ctrl {
		switch k.Kind {
		case KeyLeft:
			e.applyToAllCursors(func() { e.buf.MoveWordLeft() })
			return true
		case KeyRight:
			e.applyToAllCursors(func() { e.buf.MoveWordRight() })
			return true
		case KeyBackspace:
			e.collapseCursors() // word deletion may cross line boundary
			e.buf.DeleteWordLeft()
			return true
		case KeyDelete:
			e.collapseCursors()
			e.buf.DeleteWordRight()
			return true
		case KeyHome:
			e.collapseCursors()
			e.buf.MoveBufferStart()
			return true
		case KeyEnd:
			e.collapseCursors()
			e.buf.MoveBufferEnd()
			return true
		}
	}

	// Ctrl+<letter> clipboard shortcuts. Multi-cursor collapses
	// first; clipboard fan-out is out of scope for v1.
	if k.Ctrl && k.Kind == KeyRune {
		switch k.Rune {
		case 'c':
			e.collapseCursors()
			if sel := e.buf.Selection(); sel != "" && a != nil && a.clipboard != nil {
				_ = a.clipboard.Copy(sel)
			}
			return true
		case 'x':
			e.collapseCursors()
			if sel := e.buf.Selection(); sel != "" && a != nil && a.clipboard != nil {
				_ = a.clipboard.Copy(sel)
				e.buf.DeleteSelection()
			}
			return true
		case 'v':
			e.collapseCursors()
			if a != nil && a.clipboard != nil {
				if text, err := a.clipboard.Paste(); err == nil && text != "" {
					e.buf.InsertText(text)
				}
			}
			return true
		case 'a':
			e.collapseCursors()
			e.buf.SelectAll()
			return true
		case 'z':
			// term.MakeRaw disables ISIG on posix and ReadConsoleW
			// on Windows delivers ^Z as a raw byte, so Ctrl+Z is
			// safe to use for undo.
			e.collapseCursors()
			e.buf.Undo()
			return true
		case 'y':
			e.collapseCursors()
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
		r := k.Rune
		e.applyToAllCursors(func() { e.buf.Insert(r) })
		// Auto-trigger completion when typing an identifier or '.'
		// with a single cursor. Multi-cursor is skipped since accept
		// would only replace one cursor's prefix.
		if !e.hasMultiCursor() && (isIdentRune(r) || r == '.') {
			e.openCompletion(a)
		}
		return true
	case KeyEnter:
		// Newlines break the "at most one cursor per row"
		// invariant. Collapse first.
		e.collapseCursors()
		e.buf.InsertNewline()
		return true
	case KeyBackspace:
		if e.hasMultiCursor() {
			// Skip cursors at col 0 -- joining lines would
			// break the invariant.
			e.applyToAllCursors(func() {
				_, col := e.buf.Cursor()
				if col > 0 {
					e.buf.Backspace()
				}
			})
		} else {
			e.buf.Backspace()
		}
		return true
	case KeyDelete:
		if e.hasMultiCursor() {
			e.applyToAllCursors(func() {
				row, col := e.buf.Cursor()
				if col < len(e.buf.Line(row)) {
					e.buf.Delete()
				}
			})
		} else {
			e.buf.Delete()
		}
		return true
	case KeyLeft:
		e.applyToAllCursors(func() { e.buf.MoveLeft() })
		return true
	case KeyRight:
		e.applyToAllCursors(func() { e.buf.MoveRight() })
		return true
	case KeyUp:
		// Vertical moves break the row-uniqueness invariant.
		// Collapse first.
		e.collapseCursors()
		e.buf.MoveUp()
		return true
	case KeyDown:
		e.collapseCursors()
		e.buf.MoveDown()
		return true
	case KeyHome:
		e.applyToAllCursors(func() { e.buf.MoveHome() })
		return true
	case KeyEnd:
		e.applyToAllCursors(func() { e.buf.MoveEnd() })
		return true
	case KeyTab:
		// Multi-line selection: indent each selected line by one soft
		// tab. Otherwise insert spaces up to the next softTabWidth
		// column stop at the cursor.
		if e.buf.HasSelection() {
			r1, _, r2, c2 := e.buf.normalizedSelection()
			if r1 != r2 {
				// A selection that ends right at column 0 of r2 was
				// only extended onto that row by the trailing newline
				// -- the user didn't really pick any text on it, so
				// don't indent it.
				end := r2
				if c2 == 0 {
					end = r2 - 1
				}
				if end >= r1 {
					e.buf.IndentRange(r1, end, softTabWidth)
					return true
				}
			}
		}
		e.applyToAllCursors(func() {
			_, col := e.buf.Cursor()
			for n := softTabWidth - (col % softTabWidth); n > 0; n-- {
				e.buf.Insert(' ')
			}
		})
		return true
	case KeyBackTab:
		// Dedent: remove up to softTabWidth leading spaces from each
		// line in the selection, or from the current line if there's
		// no selection.
		if e.buf.HasSelection() {
			r1, _, r2, c2 := e.buf.normalizedSelection()
			end := r2
			if r1 != r2 && c2 == 0 {
				end = r2 - 1
			}
			if end >= r1 {
				e.buf.DedentRange(r1, end, softTabWidth)
			}
		} else {
			row, _ := e.buf.Cursor()
			e.buf.DedentRange(row, row, softTabWidth)
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
	gutter := e.gutterWidth()
	if gutter >= innerW {
		gutter = 0
	}
	bodyCol := innerCol + gutter
	bodyW := innerW - gutter

	e.ensureCursorVisible(bodyW, innerH)

	gutterStyle := Style{FG: ansiBrightBlack, BG: ansiDefaultBG}
	if gutter > 0 {
		lineCount := e.buf.LineCount()
		for i := 0; i < innerH; i++ {
			lineIdx := e.scrollRow + i
			var label string
			if lineIdx < lineCount {
				num := strconv.Itoa(lineIdx + 1)
				pad := gutter - 1 - len(num)
				if pad < 0 {
					pad = 0
					num = num[len(num)-(gutter-1):]
				}
				label = strings.Repeat(" ", pad) + num + " "
			} else {
				label = strings.Repeat(" ", gutter)
			}
			s.writeStyled(innerRow+i, innerCol, label, gutterStyle)
		}
	}

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
			if colOut >= bodyW {
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
			if rw == 2 && colOut+2 > bodyW {
				s.writeStyled(innerRow+i, bodyCol+colOut, " ", st)
				colOut++
				break
			}
			s.writeStyled(innerRow+i, bodyCol+colOut, string(r), st)
			colOut += rw
		}

		// Extend the selection highlight across trailing empty space
		// on wrapped or short lines so a multi-line block selection
		// looks continuous.
		if hasSel && lineIdx >= selR1 && lineIdx < selR2 {
			if colOut < 0 {
				colOut = 0
			}
			for colOut < bodyW {
				s.writeStyled(innerRow+i, bodyCol+colOut, " ", selStyle)
				colOut++
			}
		}
	}

	if cursorVisible {
		row, col := e.buf.Cursor()
		s.placeCursor(innerRow+(row-e.scrollRow), bodyCol+(col-e.scrollCol))
		// Paint a reverse-video block at each extra cursor so
		// the user can see where their column-add cursors sit.
		// The real terminal caret stays on the primary.
		caretStyle := Style{FG: ansiDefault, BG: ansiDefaultBG, Attrs: attrReverse}
		for _, cp := range e.extraCursors {
			if cp.row < e.scrollRow || cp.row >= e.scrollRow+innerH {
				continue
			}
			if cp.col < e.scrollCol || cp.col-e.scrollCol >= bodyW {
				continue
			}
			sc := bodyCol + (cp.col - e.scrollCol)
			sr := innerRow + (cp.row - e.scrollRow)
			// Paint a space with the caret style; if there's a
			// real character there, use it verbatim.
			ch := " "
			line := e.buf.Line(cp.row)
			if cp.col < len(line) && isPrintable(line[cp.col]) {
				ch = string(line[cp.col])
			}
			s.writeStyled(sr, sc, ch, caretStyle)
		}
	}

	if e.complete != nil {
		e.drawComplete(s, innerRow, bodyCol, bodyW, innerH)
	}
}

// isPrintable reports whether a rune should be drawn verbatim
// under an extra-cursor caret marker. Control chars render as
// a blank.
func isPrintable(r rune) bool {
	return r >= 0x20 && r != 0x7f
}

// styleForCol returns the syntax-highlight style for the column offset
// within a line. A linear scan is fine here because the token list per
// line is short and the editor's viewport is likewise bounded by
// innerW. Whitespace between tokens and anything past the last token
// fall back to the default style.
func styleForCol(tokens []sqltok.Token, col int) Style {
	for i, t := range tokens {
		if col < t.StartCol {
			return defaultStyle()
		}
		if col < t.EndCol {
			if isFunctionToken(tokens, i) {
				return currentTheme.SQLFunction
			}
			return styleForKind(t.Kind)
		}
	}
	return defaultStyle()
}

// isFunctionToken reports whether tokens[i] should paint with the
// function style: either an identifier/keyword that is immediately
// followed by '(', or the '(' or matching ')' of such a call.
func isFunctionToken(tokens []sqltok.Token, i int) bool {
	t := tokens[i]
	switch t.Kind {
	case sqltok.Ident, sqltok.Keyword:
		for j := i + 1; j < len(tokens); j++ {
			if tokens[j].Kind == sqltok.Whitespace || tokens[j].Kind == sqltok.Comment {
				continue
			}
			if tokens[j].Kind != sqltok.Punct || tokens[j].Text != "(" {
				return false
			}
			// The paren must itself qualify as a function call.
			if !sqltok.IsFunctionCall(tokens, j) {
				return false
			}
			// Only identifiers automatically qualify; keywords must
			// be in the function whitelist, which IsFunctionCall
			// already checks against the token preceding '('.
			return true
		}
		return false
	case sqltok.Punct:
		if t.Text == "(" {
			return sqltok.IsFunctionCall(tokens, i)
		}
		if t.Text == ")" {
			depth := 0
			for j := i - 1; j >= 0; j-- {
				if tokens[j].Kind != sqltok.Punct {
					continue
				}
				switch tokens[j].Text {
				case ")":
					depth++
				case "(":
					if depth == 0 {
						return sqltok.IsFunctionCall(tokens, j)
					}
					depth--
				}
			}
		}
	}
	return false
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

// caretFromScreen maps a screen cell (screenRow, screenCol) inside the
// editor rect r to a buffer (row, col) position. Inverts the walk that
// draw() uses, so a click lands on the rune the user saw under the
// pointer even with wide glyphs or horizontal scroll. Returns ok=false
// when the click is outside the drawable interior; callers then decide
// whether to ignore or clamp.
func (e *editor) caretFromScreen(r rect, screenRow, screenCol int) (int, int, bool) {
	innerRow := r.row + 1
	innerCol := r.col + 1
	innerW := r.w - 2
	innerH := r.h - 2
	if innerW <= 0 || innerH <= 0 {
		return 0, 0, false
	}
	gutter := e.gutterWidth()
	if gutter >= innerW {
		gutter = 0
	}
	bodyCol := innerCol + gutter
	bodyW := innerW - gutter
	dr := screenRow - innerRow
	if dr < 0 {
		dr = 0
	}
	if dr >= innerH {
		dr = innerH - 1
	}
	row := e.scrollRow + dr
	if row < 0 {
		row = 0
	}
	if row >= e.buf.LineCount() {
		row = e.buf.LineCount() - 1
	}
	line := e.buf.Line(row)

	target := screenCol - bodyCol
	if target < 0 {
		return row, e.scrollCol, true
	}
	if target >= bodyW {
		target = bodyW - 1
	}
	colOut := 0
	col := e.scrollCol
	if col > len(line) {
		col = len(line)
	}
	for col < len(line) {
		w := runeDisplayWidth(line[col])
		if w == 0 {
			col++
			continue
		}
		if colOut+w > target {
			break
		}
		colOut += w
		col++
	}
	return row, col, true
}

// clickAt positions the caret based on a mouse press inside rect r. The
// click count (1/2/3) drives the selection expansion: 1 collapses any
// existing selection to a point, 2 selects the word under the caret,
// 3 selects the whole line.
func (e *editor) clickAt(r rect, screenRow, screenCol, count int) {
	row, col, ok := e.caretFromScreen(r, screenRow, screenCol)
	if !ok {
		return
	}
	e.collapseCursors()
	e.complete = nil
	switch {
	case count >= 3:
		e.buf.SetCursor(row, 0)
		e.buf.SetAnchor(row, 0)
		line := e.buf.Line(row)
		e.buf.SetCursor(row, len(line))
	case count == 2:
		line := e.buf.Line(row)
		if len(line) == 0 {
			e.buf.ClearSelection()
			e.buf.SetCursor(row, 0)
			return
		}
		start, end := wordBoundsAt(line, col)
		if start == end {
			e.buf.ClearSelection()
			e.buf.SetCursor(row, col)
			return
		}
		e.buf.SetCursor(row, start)
		e.buf.SetAnchor(row, start)
		e.buf.SetCursor(row, end)
	default:
		e.buf.ClearSelection()
		e.buf.SetCursor(row, col)
		// Seed the anchor at the press point so a subsequent drag
		// extends from here without a separate press->anchor step.
		e.buf.SetAnchor(row, col)
	}
}

// dragTo extends the selection to the buffer cell under (screenRow,
// screenCol). Requires that clickAt (or another anchor-setting path)
// has already run, so the anchor marks the drag origin.
func (e *editor) dragTo(r rect, screenRow, screenCol int) {
	row, col, ok := e.caretFromScreen(r, screenRow, screenCol)
	if !ok {
		return
	}
	e.buf.SetCursor(row, col)
}

// wordBoundsAt returns the [start, end) rune range covering the word
// at col on the given line, using the same isWordChar classifier as
// Ctrl+Left/Right. If the caret sits on a non-word rune the result
// collapses to (col, col).
func wordBoundsAt(line []rune, col int) (int, int) {
	if col > len(line) {
		col = len(line)
	}
	// A caret sitting past the last rune of a word should still grab it.
	probe := col
	if probe == len(line) && probe > 0 && isWordChar(line[probe-1]) {
		probe--
	}
	if probe >= len(line) || !isWordChar(line[probe]) {
		return col, col
	}
	start := probe
	for start > 0 && isWordChar(line[start-1]) {
		start--
	}
	end := probe + 1
	for end < len(line) && isWordChar(line[end]) {
		end++
	}
	return start, end
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
