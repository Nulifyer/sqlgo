package tui

// editor is a text-input widget wrapping a buffer and a viewport. It draws
// inside a rect (minus a 1-cell border caller has already drawn) and handles
// insert-mode keys. Horizontal and vertical scrolling follow the cursor.
type editor struct {
	buf        *buffer
	scrollRow  int // index of first visible line
	scrollCol  int // index of first visible column
}

func newEditor() *editor {
	return &editor{buf: newBuffer()}
}

// handleInsert applies a keypress in INSERT mode. Returns true if the key
// was consumed. Esc/F5 and other mode/command keys should be filtered by
// the caller before reaching this method.
func (e *editor) handleInsert(k Key) bool {
	switch k.Kind {
	case KeyRune:
		if k.Ctrl {
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
		// Soft tabs: insert spaces up to the next 4-column stop so Tab
		// aligns visually regardless of the current cursor column.
		_, col := e.buf.Cursor()
		for n := 4 - (col % 4); n > 0; n-- {
			e.buf.Insert(' ')
		}
		return true
	}
	return false
}

// draw renders the editor into the interior of r (inside the border). If
// cursorVisible is true it also registers the terminal cursor position on
// the screen so it gets moved after flush.
func (e *editor) draw(s *cellbuf, r rect, cursorVisible bool) {
	innerRow := r.row + 1
	innerCol := r.col + 1
	innerW := r.w - 2
	innerH := r.h - 2
	if innerW <= 0 || innerH <= 0 {
		return
	}

	e.ensureCursorVisible(innerW, innerH)

	for i := 0; i < innerH; i++ {
		lineIdx := e.scrollRow + i
		if lineIdx >= e.buf.LineCount() {
			break
		}
		line := e.buf.Line(lineIdx)
		// horizontal scroll: take runes [scrollCol .. scrollCol+innerW)
		start := e.scrollCol
		if start > len(line) {
			start = len(line)
		}
		end := start + innerW
		if end > len(line) {
			end = len(line)
		}
		s.writeAt(innerRow+i, innerCol, string(line[start:end]))
	}

	if cursorVisible {
		row, col := e.buf.Cursor()
		s.placeCursor(innerRow+(row-e.scrollRow), innerCol+(col-e.scrollCol))
	}
}

// ensureCursorVisible scrolls the viewport so the cursor sits inside the
// visible region. Called at draw time so the viewport reacts to resizes.
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
