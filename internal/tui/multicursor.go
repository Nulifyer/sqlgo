package tui

import "sort"

// cursorPos is one (row, col) pair used by the multi-cursor
// apply loop. Kept private to the tui package.
type cursorPos struct {
	row, col int
}

// hasMultiCursor reports whether there are any extra cursors
// beyond the primary. Short-circuits non-multi paths.
func (e *editor) hasMultiCursor() bool { return len(e.extraCursors) > 0 }

// addCursorRelative adds a new cursor delta rows from the
// primary at the primary's current column. Clamped to valid
// range; duplicates with existing cursors are dropped.
func (e *editor) addCursorRelative(delta int) {
	row, col := e.buf.Cursor()
	target := row + delta
	if target < 0 || target >= e.buf.LineCount() {
		return
	}
	// Column-add follows the visible caret column, including tabs and wide glyphs.
	tc := e.buf.lineLayout(target).runeColumn(e.buf.lineLayout(row).column(col))
	// Skip duplicates (primary or existing extra at same pos).
	if target == row && tc == col {
		return
	}
	for _, c := range e.extraCursors {
		if c.row == target {
			return // v1: at most one cursor per row
		}
	}
	e.extraCursors = append(e.extraCursors, cursorPos{row: target, col: tc})
}

// collapseCursors drops all extra cursors, leaving the primary.
func (e *editor) collapseCursors() {
	e.extraCursors = nil
}

// applyToAllCursors runs fn at each cursor position (primary +
// extras), walking highest-row-first so lower-row positions
// stay valid. fn operates on the primary cursor at its current
// position; this helper moves the primary between positions and
// captures the resulting location.
//
// Precondition: v1 requires all cursors on DIFFERENT rows. The
// constraint is enforced on addCursorRelative.
func (e *editor) applyToAllCursors(fn func()) {
	if !e.hasMultiCursor() {
		fn()
		return
	}
	e.buf.beginEdit(editNone)
	e.buf.pending.beforeExtras = append([]cursorPos(nil), e.extraCursors...)
	defer func() {
		e.buf.pending.afterExtras = append([]cursorPos(nil), e.extraCursors...)
		e.buf.endEdit()
	}()

	// Gather all positions.
	row, col := e.buf.Cursor()
	positions := make([]cursorPos, 0, len(e.extraCursors)+1)
	positions = append(positions, cursorPos{row: row, col: col})
	positions = append(positions, e.extraCursors...)

	// Sort highest-row-first so mutations below don't shift
	// rows that haven't run yet. Ties broken by col descending.
	sort.Slice(positions, func(i, j int) bool {
		if positions[i].row != positions[j].row {
			return positions[i].row > positions[j].row
		}
		return positions[i].col > positions[j].col
	})

	// Run fn at each; capture new positions.
	newPositions := make([]cursorPos, 0, len(positions))
	for _, p := range positions {
		e.buf.SetCursor(p.row, p.col)
		fn()
		nr, nc := e.buf.Cursor()
		newPositions = append(newPositions, cursorPos{row: nr, col: nc})
	}

	// Sort ascending again, take lowest as primary.
	sort.Slice(newPositions, func(i, j int) bool {
		if newPositions[i].row != newPositions[j].row {
			return newPositions[i].row < newPositions[j].row
		}
		return newPositions[i].col < newPositions[j].col
	})
	e.buf.SetCursor(newPositions[0].row, newPositions[0].col)
	e.extraCursors = e.extraCursors[:0]
	for _, p := range newPositions[1:] {
		e.extraCursors = append(e.extraCursors, p)
	}
	e.normalizeCursors()
}

// normalizeCursors enforces the current one-cursor-per-row contract after
// movement, which can cross line boundaries even without changing the text.
func (e *editor) normalizeCursors() {
	row, _ := e.buf.Cursor()
	seen := map[int]bool{row: true}
	valid := e.extraCursors[:0]
	for _, p := range e.extraCursors {
		if p.row < 0 || p.row >= e.buf.LineCount() || seen[p.row] {
			continue
		}
		p.col = max(0, min(p.col, len(e.buf.Line(p.row))))
		layout := e.buf.lineLayout(p.row)
		p.col = layout.runeColumn(layout.column(p.col))
		seen[p.row] = true
		valid = append(valid, p)
	}
	e.extraCursors = valid
}
