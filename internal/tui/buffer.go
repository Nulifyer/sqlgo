package tui

import (
	"strings"
	"unicode"
)

// buffer is a simple multi-line text buffer indexed in runes (not bytes).
// It is the data model behind the query editor: a slice of rune slices,
// one per line, plus a cursor. Operations mutate in place and clamp the
// cursor to valid positions, so callers never have to bounds-check.
//
// Selection: a buffer can track an anchor position separately from the
// cursor. When selecting, anchor marks where a shift-click / shift-move
// started; the selected range is always [min(anchor,cursor),
// max(anchor,cursor)). An unset anchor means "no selection active".
//
// Undo/redo records changed ranges and selection state. Compound commands
// share a transaction; no-op commands preserve redo.
//
// Not safe for concurrent use. The TUI only touches it from the main loop.
type buffer struct {
	lines      [][]rune
	layouts    map[int]*textLayout
	goalVisual int
	goalValid  bool
	// row, col are 0-based. col is allowed to equal len(line) -- that
	// means the cursor is sitting after the last character on the line.
	row int
	col int

	// Selection anchor. When anchorSet is false, there is no active
	// selection; the cursor acts alone.
	anchorRow int
	anchorCol int
	anchorSet bool

	// Undo / redo stacks. Bounded so a runaway edit loop doesn't eat
	// memory; the cap is big enough that a normal editing session
	// never hits it.
	undo            []bufferEdit
	redo            []bufferEdit
	historyBytes    int
	editDepth       int
	pending         *bufferEdit
	pendingKind     editKind
	pendingCoalesce bool
	replaying       bool
	restoredExtras  []cursorPos
	textCache       string
	textRevision    uint64
	textCached      bool
	dirtyFrom       int

	// Coalescing: consecutive same-kind edits at adjacent cursor
	// positions share a single undo entry so Undo pops a word (or
	// run of backspaces) at a time instead of one rune at a time.
	lastEditKind editKind
	lastEditRow  int
	lastEditCol  int

	// rev increments on every text mutation. Lets editor code detect
	// buffer changes without rebuilding the entire buffer string.
	rev uint64
}

type editKind int

const (
	editNone editKind = iota
	editInsertWord
	editInsertPunct
	editBackspace
	editDelete
)

const maxUndoDepth = 256

func newBuffer() *buffer {
	return &buffer{lines: [][]rune{{}}, dirtyFrom: 0}
}

// Text returns the buffer contents joined by '\n'.
func (b *buffer) Text() string {
	if b.textCached && b.textRevision == b.rev {
		return b.textCache
	}
	n := 0
	for i, ln := range b.lines {
		if i > 0 {
			n++
		}
		n += len(ln) * 2 // rough: runes may be multi-byte
	}
	out := make([]byte, 0, n)
	for i, ln := range b.lines {
		if i > 0 {
			out = append(out, '\n')
		}
		out = append(out, string(ln)...)
	}
	b.textCache = string(out)
	b.textRevision, b.textCached = b.rev, true
	return b.textCache
}

// Revision returns a monotonically increasing counter that changes on
// every text mutation.
func (b *buffer) Revision() uint64 { return b.rev }

// LineCount returns the number of lines in the buffer (always >= 1).
func (b *buffer) LineCount() int { return len(b.lines) }

// Line returns the runes of line i. Callers must not mutate the slice.
func (b *buffer) Line(i int) []rune { return b.lines[i] }

// Cursor returns the current cursor position.
func (b *buffer) Cursor() (row, col int) { return b.row, b.col }

// SetCursor clamps (row, col) to valid range and moves the
// primary cursor there without touching selection. Used by the
// multi-cursor apply loop to walk positions.
func (b *buffer) SetCursor(row, col int) {
	b.goalValid = false
	if row < 0 {
		row = 0
	}
	if row >= len(b.lines) {
		row = len(b.lines) - 1
	}
	if col < 0 {
		col = 0
	}
	if col > len(b.lines[row]) {
		col = len(b.lines[row])
	}
	if col < len(b.lines[row]) {
		layout := b.lineLayout(row)
		col = layout.runeColumn(layout.column(col))
	}
	b.row = row
	b.col = col
	b.lastEditKind = editNone
}

// SetAnchor places the selection anchor at (row, col), clamped to the
// valid range, without touching the cursor. Used by the mouse drag /
// double-click paths to pin the start of a selection before extending
// it via SetCursor.
func (b *buffer) SetAnchor(row, col int) {
	b.goalValid = false
	if row < 0 {
		row = 0
	}
	if row >= len(b.lines) {
		row = len(b.lines) - 1
	}
	if col < 0 {
		col = 0
	}
	if col > len(b.lines[row]) {
		col = len(b.lines[row])
	}
	if col < len(b.lines[row]) {
		layout := b.lineLayout(row)
		col = layout.runeColumn(layout.column(col))
	}
	b.anchorRow = row
	b.anchorCol = col
	b.anchorSet = true
	b.lastEditKind = editNone
}

// Clear resets the buffer to a single empty line and wipes undo/redo.
func (b *buffer) Clear() {
	b.SetText("")
}

// SetText replaces the entire buffer with s, normalizing line endings to LF. The
// cursor lands at the end of the last line. Pushes an undo entry
// first so the replace can be undone.
func (b *buffer) SetText(s string) {
	b.beginEdit(editNone)
	defer b.endEdit()
	b.replaceRange(cursorPos{}, b.endPosition(), normalizeEditorLineEndings(s))
	b.anchorSet = false
}

// ResetText loads a different document, discarding the previous document's
// undo/redo and selection state. Use SetText for an undoable edit.
func (b *buffer) ResetText(s string) {
	b.setTextRaw(s)
	b.undo, b.redo = nil, nil
	b.historyBytes = 0
	b.anchorSet = false
	b.lastEditKind = editNone
}

func splitBufferText(s string) [][]rune {
	pieces := strings.Split(s, "\n")
	lines := make([][]rune, len(pieces))
	for i, piece := range pieces {
		lines[i] = []rune(piece)
	}
	return lines
}

// setTextRaw installs normalized text and clears cached layouts.
func (b *buffer) setTextRaw(s string) {
	s = normalizeEditorLineEndings(s)
	b.lines = splitBufferText(s)
	b.row = len(b.lines) - 1
	b.col = len(b.lines[b.row])
	b.rev++
	b.dirtyFrom = 0
	b.layouts = nil
	b.goalValid = false
	b.textCache, b.textRevision, b.textCached = s, b.rev, true
}

// resetPreparedText transfers a worker-prepared document to the UI without
// reparsing it. The caller must not mutate lines after ownership transfers.
func (b *buffer) resetPreparedText(s string, lines [][]rune) {
	if len(lines) == 0 {
		b.ResetText(s)
		return
	}
	b.lines = lines
	b.row, b.col = 0, 0
	b.anchorSet = false
	b.undo, b.redo = nil, nil
	b.historyBytes = 0
	b.lastEditKind = editNone
	b.rev++
	b.dirtyFrom = 0
	b.layouts = nil
	b.goalValid = false
	b.textCache, b.textRevision, b.textCached = s, b.rev, true
}

// Insert writes r at the cursor and advances one column. If a
// selection is active it's deleted first.
func (b *buffer) Insert(r rune) {
	kind := editInsertPunct
	if isWordChar(r) {
		kind = editInsertWord
	}
	if b.HasSelection() {
		kind = editNone
	}
	b.beginEdit(kind)
	defer b.endEdit()
	start, end := b.insertionRange()
	b.replaceRange(start, end, normalizeEditorLineEndings(string(r)))
	b.anchorSet = false
}

// normalizeEditorLineEndings gives every bulk text path the same LF model.
// CRLF is one line break; a bare CR is also a line break.
func normalizeEditorLineEndings(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

// InsertText writes a whole string at the cursor, normalizing line endings to LF.
// Used by Paste. A single undo entry covers the full paste.
func (b *buffer) InsertText(s string) {
	b.beginEdit(editNone)
	defer b.endEdit()
	start, end := b.insertionRange()
	b.replaceRange(start, end, normalizeEditorLineEndings(s))
	b.anchorSet = false
}

// InsertNewline splits the current line at the cursor.
func (b *buffer) InsertNewline() {
	b.InsertText("\n")
}

// Backspace deletes the character before the cursor, the active
// selection, or joins with the previous line if at column 0.
func (b *buffer) Backspace() {
	if b.HasSelection() {
		b.DeleteSelection()
		return
	}
	start := cursorPos{b.row, b.col}
	end := start
	kind := editBackspace
	if start.col > 0 {
		start.col = b.lineLayout(start.row).prev(start.col)
	} else if start.row > 0 {
		start.row--
		start.col = len(b.lines[start.row])
		kind = editNone
	} else {
		return
	}
	b.beginEdit(kind)
	defer b.endEdit()
	b.replaceRange(start, end, "")
}

// Delete removes the character under the cursor, the active selection,
// or joins the next line into the current one if at end of line.
func (b *buffer) Delete() {
	if b.HasSelection() {
		b.DeleteSelection()
		return
	}
	start := cursorPos{b.row, b.col}
	end := start
	kind := editDelete
	if end.col < len(b.lines[end.row]) {
		end.col = b.lineLayout(end.row).next(end.col)
	} else if end.row+1 < len(b.lines) {
		end.row++
		end.col = 0
		kind = editNone
	} else {
		return
	}
	b.beginEdit(kind)
	defer b.endEdit()
	b.replaceRange(start, end, "")
}

// --- cursor movement -------------------------------------------------------
//
// The plain move methods below do not touch the selection anchor -- so
// a move while Shift is held is handled by the editor itself (it sets
// the anchor first, then calls the plain move). The unshifted path
// clears any existing anchor so the selection collapses on movement.

// MoveLeft decrements the column, wrapping to the end of the previous
// line when at column 0.
func (b *buffer) MoveLeft() {
	if b.HasSelection() {
		r, c, _, _ := b.normalizedSelection()
		b.SetCursor(r, c)
		b.clearSelection()
		return
	}
	b.goalValid = false
	b.clearSelection()
	b.moveLeftNoSel()
}

func (b *buffer) moveLeftNoSel() {
	if b.col > 0 {
		b.col = b.lineLayout(b.row).prev(b.col)
		return
	}
	if b.row > 0 {
		b.row--
		b.col = len(b.lines[b.row])
	}
}

// MoveRight increments the column, wrapping to column 0 of the next
// line when past end-of-line.
func (b *buffer) MoveRight() {
	if b.HasSelection() {
		_, _, r, c := b.normalizedSelection()
		b.SetCursor(r, c)
		b.clearSelection()
		return
	}
	b.goalValid = false
	b.clearSelection()
	b.moveRightNoSel()
}

func (b *buffer) moveRightNoSel() {
	if b.col < len(b.lines[b.row]) {
		b.col = b.lineLayout(b.row).next(b.col)
		return
	}
	if b.row < len(b.lines)-1 {
		b.row++
		b.col = 0
	}
}

// MoveUp moves the cursor up one line, clamping the column.
func (b *buffer) MoveUp() {
	b.clearSelection()
	b.moveUpNoSel()
}

func (b *buffer) moveUpNoSel() {
	if !b.goalValid {
		b.goalVisual = b.lineLayout(b.row).column(b.col)
		b.goalValid = true
	}
	target := b.row + -1
	if target < 0 {
		b.col = 0
		return
	}
	if target >= len(b.lines) {
		b.col = len(b.lines[b.row])
		return
	}
	b.row = target
	b.col = b.lineLayout(target).runeColumn(b.goalVisual)
}

// MoveDown moves the cursor down one line, clamping the column.
func (b *buffer) MoveDown() {
	b.clearSelection()
	b.moveDownNoSel()
}

func (b *buffer) moveDownNoSel() {
	if !b.goalValid {
		b.goalVisual = b.lineLayout(b.row).column(b.col)
		b.goalValid = true
	}
	target := b.row + 1
	if target < 0 {
		b.col = 0
		return
	}
	if target >= len(b.lines) {
		b.col = len(b.lines[b.row])
		return
	}
	b.row = target
	b.col = b.lineLayout(target).runeColumn(b.goalVisual)
}

// MoveHome jumps to column 0 on the current line.
func (b *buffer) MoveHome() {
	b.goalValid = false
	b.clearSelection()
	b.col = 0
}

// MoveEnd jumps to the end of the current line.
func (b *buffer) MoveEnd() {
	b.goalValid = false
	b.clearSelection()
	b.col = len(b.lines[b.row])
}

// MoveBufferStart jumps to (0, 0).
func (b *buffer) MoveBufferStart() {
	b.goalValid = false
	b.clearSelection()
	b.row = 0
	b.col = 0
}

// MoveBufferEnd jumps to the end of the last line.
func (b *buffer) MoveBufferEnd() {
	b.goalValid = false
	b.clearSelection()
	b.row = len(b.lines) - 1
	b.col = len(b.lines[b.row])
}

// MoveWordLeft jumps backward over one word boundary. Skips any run
// of non-word characters at the cursor, then skips the preceding run
// of word characters. Crosses line boundaries when at the start of a
// line, matching what most terminal editors do with Ctrl+Left.
func (b *buffer) MoveWordLeft() {
	b.goalValid = false
	b.clearSelection()
	b.moveWordLeftNoSel()
}

func (b *buffer) moveWordLeftNoSel() {
	if b.col == 0 {
		if b.row == 0 {
			return
		}
		b.row--
		b.col = len(b.lines[b.row])
		return
	}
	line := b.lines[b.row]
	// Skip non-word chars immediately before the cursor.
	for b.col > 0 && !isWordChar(line[b.col-1]) {
		b.col--
	}
	// Skip the run of word chars.
	for b.col > 0 && isWordChar(line[b.col-1]) {
		b.col--
	}
}

// MoveWordRight jumps forward over one word boundary. Mirror of
// MoveWordLeft.
func (b *buffer) MoveWordRight() {
	b.goalValid = false
	b.clearSelection()
	b.moveWordRightNoSel()
}

func (b *buffer) moveWordRightNoSel() {
	line := b.lines[b.row]
	if b.col >= len(line) {
		if b.row == len(b.lines)-1 {
			return
		}
		b.row++
		b.col = 0
		return
	}
	// Skip the run of word chars the cursor is inside/at the start of.
	for b.col < len(line) && isWordChar(line[b.col]) {
		b.col++
	}
	// Skip the trailing run of non-word chars.
	for b.col < len(line) && !isWordChar(line[b.col]) {
		b.col++
	}
}

// SelectWordLeft / SelectWordRight are the shift-held counterparts.
// They set the anchor (if not already) and move without clearing it.
func (b *buffer) SelectWordLeft() {
	b.goalValid = false
	b.ensureAnchor()
	b.moveWordLeftNoSel()
}

func (b *buffer) SelectWordRight() {
	b.goalValid = false
	b.ensureAnchor()
	b.moveWordRightNoSel()
}

// DeleteWordLeft deletes from the cursor backward to the previous word
// boundary. Uses the selection+delete machinery so it integrates with
// undo and matches what a selection-then-delete would do.
func (b *buffer) DeleteWordLeft() {
	if b.HasSelection() {
		b.DeleteSelection()
		return
	}
	b.beginEdit(editNone)
	defer b.endEdit()
	end := cursorPos{b.row, b.col}
	b.moveWordLeftNoSel()
	start := cursorPos{b.row, b.col}
	b.replaceRange(start, end, "")
	b.anchorSet = false
}

// DeleteWordRight deletes from the cursor forward to the next word
// boundary.
func (b *buffer) DeleteWordRight() {
	if b.HasSelection() {
		b.DeleteSelection()
		return
	}
	b.beginEdit(editNone)
	defer b.endEdit()
	start := cursorPos{b.row, b.col}
	b.moveWordRightNoSel()
	end := cursorPos{b.row, b.col}
	b.replaceRange(start, end, "")
	b.anchorSet = false
}

// MoveLineUp swaps the current line with the one above, preserving the
// cursor column. No-op on the top line. Takes an undo entry.
func (b *buffer) MoveLineUp() {
	if b.row == 0 {
		return
	}
	row, col := b.row, b.col
	b.beginEdit(editNone)
	defer b.endEdit()
	text := string(b.lines[row]) + "\n" + string(b.lines[row-1])
	b.replaceRange(cursorPos{row - 1, 0}, cursorPos{row, len(b.lines[row])}, text)
	b.row, b.col = row-1, col
	b.anchorSet = false
}

// MoveLineDown swaps the current line with the one below. No-op on the
// last line. Takes an undo entry.
func (b *buffer) MoveLineDown() {
	if b.row+1 >= len(b.lines) {
		return
	}
	row, col := b.row, b.col
	b.beginEdit(editNone)
	defer b.endEdit()
	text := string(b.lines[row+1]) + "\n" + string(b.lines[row])
	b.replaceRange(cursorPos{row, 0}, cursorPos{row + 1, len(b.lines[row+1])}, text)
	b.row, b.col = row+1, col
	b.anchorSet = false
}

// DuplicateLineDown inserts a copy of the current line directly below
// it and moves the cursor onto the copy (matching VSCode's
// Shift+Alt+Down). Takes an undo entry.
func (b *buffer) DuplicateLineDown() {
	row, col := b.row, b.col
	b.beginEdit(editNone)
	defer b.endEdit()
	end := cursorPos{row, len(b.lines[row])}
	b.replaceRange(end, end, "\n"+string(b.lines[row]))
	b.row, b.col = row+1, col
	b.anchorSet = false
}

// DuplicateLineUp inserts a copy of the current line directly above it.
// The cursor stays on the upper copy. Takes an undo entry.
func (b *buffer) DuplicateLineUp() {
	row, col := b.row, b.col
	b.beginEdit(editNone)
	defer b.endEdit()
	start := cursorPos{row, 0}
	b.replaceRange(start, start, string(b.lines[row])+"\n")
	b.row, b.col = row, col
	b.anchorSet = false
}

// isWordChar reports whether r belongs inside an identifier-like word
// run for the purposes of Ctrl+Left/Right navigation. Letters, digits,
// and underscore count; everything else is a boundary character.
func isWordChar(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r)
}

// --- selection -------------------------------------------------------------

// SelectLeft extends the selection one rune to the left, setting the
// anchor on first call.
func (b *buffer) SelectLeft() {
	b.goalValid = false
	b.ensureAnchor()
	b.moveLeftNoSel()
}

// SelectRight extends the selection one rune to the right.
func (b *buffer) SelectRight() {
	b.goalValid = false
	b.ensureAnchor()
	b.moveRightNoSel()
}

// SelectUp extends the selection one line up.
func (b *buffer) SelectUp() {
	b.ensureAnchor()
	b.moveUpNoSel()
}

// SelectDown extends the selection one line down.
func (b *buffer) SelectDown() {
	b.ensureAnchor()
	b.moveDownNoSel()
}

// SelectHome extends the selection to column 0.
func (b *buffer) SelectHome() {
	b.goalValid = false
	b.ensureAnchor()
	b.col = 0
}

// SelectEnd extends the selection to the end of the line.
func (b *buffer) SelectEnd() {
	b.goalValid = false
	b.ensureAnchor()
	b.col = len(b.lines[b.row])
}

// SelectBufferStart extends the selection to (0, 0).
func (b *buffer) SelectBufferStart() {
	b.ensureAnchor()
	b.row = 0
	b.col = 0
}

// SelectBufferEnd extends the selection to the end of the last line.
func (b *buffer) SelectBufferEnd() {
	b.ensureAnchor()
	b.row = len(b.lines) - 1
	b.col = len(b.lines[b.row])
}

// SelectAll selects the entire buffer.
func (b *buffer) SelectAll() {
	b.anchorRow = 0
	b.anchorCol = 0
	b.anchorSet = true
	b.row = len(b.lines) - 1
	b.col = len(b.lines[b.row])
}

// HasSelection reports whether there is a non-empty active selection.
func (b *buffer) HasSelection() bool {
	if !b.anchorSet {
		return false
	}
	return b.anchorRow != b.row || b.anchorCol != b.col
}

// Selection returns the selected text, or "" if nothing is selected.
func (b *buffer) Selection() string {
	if !b.HasSelection() {
		return ""
	}
	r1, c1, r2, c2 := b.normalizedSelection()
	if r1 == r2 {
		line := b.lines[r1]
		return string(line[c1:c2])
	}
	var out []byte
	out = append(out, string(b.lines[r1][c1:])...)
	for r := r1 + 1; r < r2; r++ {
		out = append(out, '\n')
		out = append(out, string(b.lines[r])...)
	}
	out = append(out, '\n')
	out = append(out, string(b.lines[r2][:c2])...)
	return string(out)
}

// IndentRange prepends n spaces to each line in [row1, row2]. The
// cursor and selection anchor shift right by n when they sit on
// affected rows so the user's selection visually stays put.
func (b *buffer) IndentRange(row1, row2, n int) {
	row1 = max(row1, 0)
	row2 = min(row2, len(b.lines)-1)
	if n <= 0 || row1 > row2 {
		return
	}
	b.beginEdit(editNone)
	defer b.endEdit()
	state := b.cursorState()
	for row := row2; row >= row1; row-- {
		pos := cursorPos{row, 0}
		b.replaceRange(pos, pos, strings.Repeat(" ", n))
	}
	if state.row >= row1 && state.row <= row2 {
		state.col += n
	}
	if state.anchorSet && state.anchorRow >= row1 && state.anchorRow <= row2 {
		state.anchorCol += n
	}
	b.restoreCursor(state)
}

// DedentRange removes up to n leading spaces from each line in
// [row1, row2]. Cursor/anchor columns are clamped so they never
// fall off the start of a shortened line.
func (b *buffer) DedentRange(row1, row2, n int) {
	row1 = max(row1, 0)
	row2 = min(row2, len(b.lines)-1)
	if n <= 0 || row1 > row2 {
		return
	}
	b.beginEdit(editNone)
	defer b.endEdit()
	state := b.cursorState()
	for row := row2; row >= row1; row-- {
		k := 0
		cells := 0
		for k < len(b.lines[row]) && cells < n {
			r := b.lines[row][k]
			if r == ' ' {
				cells++
			} else if r == '\t' {
				cells += editorTabStop - cells%editorTabStop
			} else {
				break
			}
			k++
		}
		if k == 0 {
			continue
		}
		b.replaceRange(cursorPos{row, 0}, cursorPos{row, k}, "")
		if state.row == row {
			state.col = max(0, state.col-k)
		}
		if state.anchorSet && state.anchorRow == row {
			state.anchorCol = max(0, state.anchorCol-k)
		}
	}
	b.restoreCursor(state)
}

// DeleteSelection removes the active selection, taking an undo entry
// before mutating.
func (b *buffer) DeleteSelection() {
	if !b.HasSelection() {
		return
	}
	b.beginEdit(editNone)
	defer b.endEdit()
	start, end := b.insertionRange()
	b.replaceRange(start, end, "")
	b.anchorSet = false
}

// normalizedSelection returns (startRow, startCol, endRow, endCol)
// with start <= end. Caller must have checked HasSelection first.
func (b *buffer) normalizedSelection() (int, int, int, int) {
	if b.anchorRow < b.row || (b.anchorRow == b.row && b.anchorCol <= b.col) {
		return b.anchorRow, b.anchorCol, b.row, b.col
	}
	return b.row, b.col, b.anchorRow, b.anchorCol
}

// ClearSelection drops the active selection without moving the cursor.
func (b *buffer) ClearSelection() { b.clearSelection() }

func (b *buffer) clearSelection() {
	b.anchorSet = false
	// Any explicit cursor move (plain arrow / Home / End / word jump)
	// breaks undo coalescing so returning to the same position doesn't
	// silently extend the previous word/backspace run.
	b.lastEditKind = editNone
}

func (b *buffer) ensureAnchor() {
	// Shift-moves also count as cursor repositioning for coalesce
	// purposes -- break here so a shift-select then type doesn't fold
	// into the prior word.
	b.lastEditKind = editNone
	if b.anchorSet {
		return
	}
	b.anchorRow = b.row
	b.anchorCol = b.col
	b.anchorSet = true
}
