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
	line := e.buf.Line(target)
	tc := col
	if tc > len(line) {
		tc = len(line)
	}
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
// Returns true if any mutation happened so the caller can skip
// scroll updates when it was a no-op.
//
// Precondition: v1 requires all cursors on DIFFERENT rows. The
// constraint is enforced on addCursorRelative.
func (e *editor) applyToAllCursors(fn func()) {
	if !e.hasMultiCursor() {
		fn()
		return
	}

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
}
