package tui

import "sort"

type cursorPos struct{ row, col int }

const maxEditorSelections = 256

func (e *editor) hasMultiCursor() bool { return len(e.extraCursors) > 0 }
func (e *editor) collapseCursors()     { e.extraCursors = nil }
func (e *editor) addCursorRelative(delta int) {
	row, col := e.buf.Cursor()
	target := row + delta
	if target < 0 || target >= e.buf.LineCount() || len(e.extraCursors) >= maxEditorSelections-1 {
		return
	}
	tc := e.buf.lineLayout(target).runeColumn(e.buf.lineLayout(row).column(col))
	e.extraCursors = append(e.extraCursors, bufferCursor{row: target, col: tc})
	e.normalizeCursors()
}
func cursorRange(s bufferCursor) (cursorPos, cursorPos) {
	end := cursorPos{s.row, s.col}
	if !s.anchorSet {
		return end, end
	}
	start := cursorPos{s.anchorRow, s.anchorCol}
	if lessPosition(end, start) {
		return end, start
	}
	return start, end
}
func lessPosition(a, b cursorPos) bool { return a.row < b.row || a.row == b.row && a.col < b.col }
func transformPosition(p cursorPos, c bufferChange) cursorPos {
	if lessPosition(p, c.start) {
		return p
	}
	if lessPosition(p, c.oldEnd) {
		return c.newEnd
	}
	if p.row == c.oldEnd.row {
		p.col += c.newEnd.col - c.oldEnd.col
	}
	p.row += c.newEnd.row - c.oldEnd.row
	return p
}
func transformCursor(s bufferCursor, c bufferChange) bufferCursor {
	p := transformPosition(cursorPos{s.row, s.col}, c)
	s.row, s.col = p.row, p.col
	if s.anchorSet {
		p = transformPosition(cursorPos{s.anchorRow, s.anchorCol}, c)
		s.anchorRow, s.anchorCol = p.row, p.col
	}
	return s
}
func (e *editor) selections() []bufferCursor {
	return append([]bufferCursor{e.buf.cursorState()}, e.extraCursors...)
}
func (e *editor) setSelections(selections []bufferCursor) {
	if len(selections) == 0 {
		return
	}
	e.buf.restoreCursor(selections[0])
	e.extraCursors = append(e.extraCursors[:0], selections[1:]...)
	e.normalizeCursors()
}

// Apply independent selections from the end. Transform completed selections
// through every earlier edit so multiple ranges on one row stay valid.
func (e *editor) applyToAllCursors(fn func()) {
	if !e.hasMultiCursor() {
		fn()
		return
	}
	e.normalizeCursors()
	original := e.selections()
	order := make([]int, len(original))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool {
		a, _ := cursorRange(original[order[i]])
		b, _ := cursorRange(original[order[j]])
		return lessPosition(b, a)
	})
	e.buf.beginEdit(editNone)
	e.buf.pending.beforeExtras = append([]bufferCursor(nil), e.extraCursors...)
	results := make([]bufferCursor, len(original))
	done := []int{}
	for _, index := range order {
		e.buf.restoreCursor(original[index])
		before := len(e.buf.pending.changes)
		fn()
		for _, change := range e.buf.pending.changes[before:] {
			for _, other := range done {
				results[other] = transformCursor(results[other], change)
			}
		}
		results[index] = e.buf.cursorState()
		done = append(done, index)
	}
	e.setSelections(results)
	e.buf.pending.afterExtras = append([]bufferCursor(nil), e.extraCursors...)
	e.buf.endEdit()
}
func (e *editor) normalizeCursors() {
	primary := e.buf.cursorState()
	accepted := []bufferCursor{primary}
	valid := e.extraCursors[:0]
	for _, s := range e.extraCursors {
		if s.row < 0 || s.row >= e.buf.LineCount() {
			continue
		}
		s.col = max(0, min(s.col, len(e.buf.Line(s.row))))
		l := e.buf.lineLayout(s.row)
		s.col = l.runeColumn(l.column(s.col))
		if s.anchorSet {
			s.anchorRow = max(0, min(s.anchorRow, e.buf.LineCount()-1))
			s.anchorCol = max(0, min(s.anchorCol, len(e.buf.Line(s.anchorRow))))
			l = e.buf.lineLayout(s.anchorRow)
			s.anchorCol = l.runeColumn(l.column(s.anchorCol))
		}
		a, z := cursorRange(s)
		overlap := false
		for _, other := range accepted {
			b, y := cursorRange(other)
			if a == b || z == y || lessPosition(a, y) && lessPosition(b, z) {
				overlap = true
				break
			}
		}
		if overlap {
			continue
		}
		accepted = append(accepted, s)
		valid = append(valid, s)
	}
	e.extraCursors = valid
}
func (e *editor) selectedAt(row, col int) bool {
	if e.buf.HasSelection() {
		r1, c1, r2, c2 := e.buf.normalizedSelection()
		if inSelection(row, col, r1, c1, r2, c2) {
			return true
		}
	}
	for _, s := range e.extraCursors {
		a, z := cursorRange(s)
		if a != z && inSelection(row, col, a.row, a.col, z.row, z.col) {
			return true
		}
	}
	return false
}
