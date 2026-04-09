package tui

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
// Undo/redo: every text mutation pushes a snapshot onto the undo stack.
// Snapshots are full-buffer strings plus cursor position -- small
// enough for typical editor usage and avoids the complexity of diff-
// based undo trees. Redo is cleared on any new edit.
//
// Not safe for concurrent use. The TUI only touches it from the main loop.
type buffer struct {
	lines [][]rune
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
	undo []bufferSnapshot
	redo []bufferSnapshot
}

type bufferSnapshot struct {
	text string
	row  int
	col  int
}

const maxUndoDepth = 256

func newBuffer() *buffer {
	return &buffer{lines: [][]rune{{}}}
}

// Text returns the buffer contents joined by '\n'.
func (b *buffer) Text() string {
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
	return string(out)
}

// LineCount returns the number of lines in the buffer (always >= 1).
func (b *buffer) LineCount() int { return len(b.lines) }

// Line returns the runes of line i. Callers must not mutate the slice.
func (b *buffer) Line(i int) []rune { return b.lines[i] }

// Cursor returns the current cursor position.
func (b *buffer) Cursor() (row, col int) { return b.row, b.col }

// Clear resets the buffer to a single empty line and wipes undo/redo.
func (b *buffer) Clear() {
	b.snapshot()
	b.lines = [][]rune{{}}
	b.row, b.col = 0, 0
	b.anchorSet = false
}

// SetText replaces the entire buffer with s, splitting on '\n'. The
// cursor lands at the end of the last line. Pushes an undo snapshot
// first so the replace can be undone.
func (b *buffer) SetText(s string) {
	b.snapshot()
	b.setTextRaw(s)
	b.anchorSet = false
}

// setTextRaw is SetText without taking an undo snapshot. Used by the
// undo / redo paths to apply a stored snapshot without recursing.
func (b *buffer) setTextRaw(s string) {
	b.lines = b.lines[:0]
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			b.lines = append(b.lines, []rune(s[start:i]))
			start = i + 1
		}
	}
	b.lines = append(b.lines, []rune(s[start:]))
	b.row = len(b.lines) - 1
	b.col = len(b.lines[b.row])
}

// Insert writes r at the cursor and advances one column. If a
// selection is active it's deleted first.
func (b *buffer) Insert(r rune) {
	b.snapshot()
	if b.anchorSet {
		b.deleteSelectionNoSnap()
	}
	line := b.lines[b.row]
	line = append(line, 0)
	copy(line[b.col+1:], line[b.col:])
	line[b.col] = r
	b.lines[b.row] = line
	b.col++
}

// InsertText writes a whole string (possibly multi-line) at the cursor.
// Used by Paste. A single undo snapshot covers the full paste.
func (b *buffer) InsertText(s string) {
	b.snapshot()
	if b.anchorSet {
		b.deleteSelectionNoSnap()
	}
	for _, r := range s {
		if r == '\n' {
			b.insertNewlineNoSnap()
			continue
		}
		if r == '\r' {
			continue
		}
		b.insertRuneNoSnap(r)
	}
}

// InsertNewline splits the current line at the cursor.
func (b *buffer) InsertNewline() {
	b.snapshot()
	if b.anchorSet {
		b.deleteSelectionNoSnap()
	}
	b.insertNewlineNoSnap()
}

func (b *buffer) insertNewlineNoSnap() {
	line := b.lines[b.row]
	head := append([]rune(nil), line[:b.col]...)
	tail := append([]rune(nil), line[b.col:]...)
	b.lines[b.row] = head
	b.lines = append(b.lines, nil)
	copy(b.lines[b.row+2:], b.lines[b.row+1:])
	b.lines[b.row+1] = tail
	b.row++
	b.col = 0
}

func (b *buffer) insertRuneNoSnap(r rune) {
	line := b.lines[b.row]
	line = append(line, 0)
	copy(line[b.col+1:], line[b.col:])
	line[b.col] = r
	b.lines[b.row] = line
	b.col++
}

// Backspace deletes the character before the cursor, the active
// selection, or joins with the previous line if at column 0.
func (b *buffer) Backspace() {
	b.snapshot()
	if b.anchorSet {
		b.deleteSelectionNoSnap()
		return
	}
	if b.col > 0 {
		line := b.lines[b.row]
		copy(line[b.col-1:], line[b.col:])
		b.lines[b.row] = line[:len(line)-1]
		b.col--
		return
	}
	if b.row == 0 {
		return
	}
	prev := b.lines[b.row-1]
	cur := b.lines[b.row]
	newCol := len(prev)
	b.lines[b.row-1] = append(prev, cur...)
	b.lines = append(b.lines[:b.row], b.lines[b.row+1:]...)
	b.row--
	b.col = newCol
}

// Delete removes the character under the cursor, the active selection,
// or joins the next line into the current one if at end of line.
func (b *buffer) Delete() {
	b.snapshot()
	if b.anchorSet {
		b.deleteSelectionNoSnap()
		return
	}
	line := b.lines[b.row]
	if b.col < len(line) {
		copy(line[b.col:], line[b.col+1:])
		b.lines[b.row] = line[:len(line)-1]
		return
	}
	if b.row == len(b.lines)-1 {
		return
	}
	next := b.lines[b.row+1]
	b.lines[b.row] = append(line, next...)
	b.lines = append(b.lines[:b.row+1], b.lines[b.row+2:]...)
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
	b.clearSelection()
	b.moveLeftNoSel()
}

func (b *buffer) moveLeftNoSel() {
	if b.col > 0 {
		b.col--
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
	b.clearSelection()
	b.moveRightNoSel()
}

func (b *buffer) moveRightNoSel() {
	if b.col < len(b.lines[b.row]) {
		b.col++
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
	if b.row == 0 {
		b.col = 0
		return
	}
	b.row--
	if b.col > len(b.lines[b.row]) {
		b.col = len(b.lines[b.row])
	}
}

// MoveDown moves the cursor down one line, clamping the column.
func (b *buffer) MoveDown() {
	b.clearSelection()
	b.moveDownNoSel()
}

func (b *buffer) moveDownNoSel() {
	if b.row == len(b.lines)-1 {
		b.col = len(b.lines[b.row])
		return
	}
	b.row++
	if b.col > len(b.lines[b.row]) {
		b.col = len(b.lines[b.row])
	}
}

// MoveHome jumps to column 0 on the current line.
func (b *buffer) MoveHome() {
	b.clearSelection()
	b.col = 0
}

// MoveEnd jumps to the end of the current line.
func (b *buffer) MoveEnd() {
	b.clearSelection()
	b.col = len(b.lines[b.row])
}

// MoveWordLeft jumps backward over one word boundary. Skips any run
// of non-word characters at the cursor, then skips the preceding run
// of word characters. Crosses line boundaries when at the start of a
// line, matching what most terminal editors do with Ctrl+Left.
func (b *buffer) MoveWordLeft() {
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
	b.ensureAnchor()
	b.moveWordLeftNoSel()
}

func (b *buffer) SelectWordRight() {
	b.ensureAnchor()
	b.moveWordRightNoSel()
}

// DeleteWordLeft deletes from the cursor backward to the previous word
// boundary. Uses the selection+delete machinery so it integrates with
// undo and matches what a selection-then-delete would do.
func (b *buffer) DeleteWordLeft() {
	b.snapshot()
	// Drop any existing selection; we're building a fresh range to
	// delete, anchored at the current cursor.
	b.anchorSet = false
	savedRow, savedCol := b.row, b.col
	// Temporarily treat the move as a selection extension so we can
	// reuse the multi-line delete path.
	b.ensureAnchor()
	b.moveWordLeftNoSel()
	// Normalize so anchor > cursor (we're deleting leftward).
	if b.row == savedRow && b.col == savedCol {
		b.anchorSet = false
		return
	}
	b.anchorRow, b.anchorCol = savedRow, savedCol
	b.deleteSelectionNoSnap()
}

// DeleteWordRight deletes from the cursor forward to the next word
// boundary.
func (b *buffer) DeleteWordRight() {
	b.snapshot()
	b.anchorSet = false
	savedRow, savedCol := b.row, b.col
	b.ensureAnchor()
	b.moveWordRightNoSel()
	if b.row == savedRow && b.col == savedCol {
		b.anchorSet = false
		return
	}
	// anchor at saved position, cursor at the forward boundary --
	// deleteSelectionNoSnap handles the ordering.
	b.anchorRow, b.anchorCol = savedRow, savedCol
	b.deleteSelectionNoSnap()
}

// isWordChar reports whether r belongs inside an identifier-like word
// run for the purposes of Ctrl+Left/Right navigation. Letters, digits,
// and underscore count; everything else is a boundary character.
func isWordChar(r rune) bool {
	if r == '_' {
		return true
	}
	if r >= '0' && r <= '9' {
		return true
	}
	if r >= 'a' && r <= 'z' {
		return true
	}
	if r >= 'A' && r <= 'Z' {
		return true
	}
	return false
}

// --- selection -------------------------------------------------------------

// SelectLeft extends the selection one rune to the left, setting the
// anchor on first call.
func (b *buffer) SelectLeft() {
	b.ensureAnchor()
	b.moveLeftNoSel()
}

// SelectRight extends the selection one rune to the right.
func (b *buffer) SelectRight() {
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
	b.ensureAnchor()
	b.col = 0
}

// SelectEnd extends the selection to the end of the line.
func (b *buffer) SelectEnd() {
	b.ensureAnchor()
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

// DeleteSelection removes the active selection, taking an undo snapshot
// before mutating.
func (b *buffer) DeleteSelection() {
	if !b.HasSelection() {
		return
	}
	b.snapshot()
	b.deleteSelectionNoSnap()
}

func (b *buffer) deleteSelectionNoSnap() {
	if !b.HasSelection() {
		b.anchorSet = false
		return
	}
	r1, c1, r2, c2 := b.normalizedSelection()
	if r1 == r2 {
		line := b.lines[r1]
		b.lines[r1] = append(line[:c1], line[c2:]...)
	} else {
		head := append([]rune(nil), b.lines[r1][:c1]...)
		tail := append([]rune(nil), b.lines[r2][c2:]...)
		head = append(head, tail...)
		b.lines[r1] = head
		b.lines = append(b.lines[:r1+1], b.lines[r2+1:]...)
	}
	b.row = r1
	b.col = c1
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

func (b *buffer) clearSelection() { b.anchorSet = false }

func (b *buffer) ensureAnchor() {
	if b.anchorSet {
		return
	}
	b.anchorRow = b.row
	b.anchorCol = b.col
	b.anchorSet = true
}

// --- undo / redo -----------------------------------------------------------

// snapshot pushes the current buffer state onto the undo stack and
// clears the redo stack (a new edit abandons any redo future).
func (b *buffer) snapshot() {
	snap := bufferSnapshot{text: b.Text(), row: b.row, col: b.col}
	b.undo = append(b.undo, snap)
	if len(b.undo) > maxUndoDepth {
		// Drop the oldest entries; bounded slice keeps the cost O(1)
		// per push amortized because the cap is small.
		n := len(b.undo) - maxUndoDepth
		b.undo = append(b.undo[:0], b.undo[n:]...)
	}
	b.redo = nil
}

// Undo reverts the most recent mutation. Pushes the current state
// onto the redo stack so Redo can round-trip.
func (b *buffer) Undo() bool {
	if len(b.undo) == 0 {
		return false
	}
	current := bufferSnapshot{text: b.Text(), row: b.row, col: b.col}
	last := b.undo[len(b.undo)-1]
	b.undo = b.undo[:len(b.undo)-1]
	b.redo = append(b.redo, current)
	b.setTextRaw(last.text)
	b.row = last.row
	b.col = last.col
	if b.row >= len(b.lines) {
		b.row = len(b.lines) - 1
	}
	if b.col > len(b.lines[b.row]) {
		b.col = len(b.lines[b.row])
	}
	b.anchorSet = false
	return true
}

// Redo replays the most recently undone mutation.
func (b *buffer) Redo() bool {
	if len(b.redo) == 0 {
		return false
	}
	current := bufferSnapshot{text: b.Text(), row: b.row, col: b.col}
	next := b.redo[len(b.redo)-1]
	b.redo = b.redo[:len(b.redo)-1]
	b.undo = append(b.undo, current)
	b.setTextRaw(next.text)
	b.row = next.row
	b.col = next.col
	if b.row >= len(b.lines) {
		b.row = len(b.lines) - 1
	}
	if b.col > len(b.lines[b.row]) {
		b.col = len(b.lines[b.row])
	}
	b.anchorSet = false
	return true
}
