package tui

import "strings"

const maxUndoBytes = 16 << 20

type bufferCursor struct {
	row, col, anchorRow, anchorCol int
	anchorSet                      bool
	goalVisual                     int
	goalValid                      bool
}
type bufferChange struct {
	start, oldEnd, newEnd cursorPos
	before, after         string
}
type bufferEdit struct {
	changes                   []bufferChange
	before, after             bufferCursor
	beforeExtras, afterExtras []bufferCursor
	bytes                     int
	beforeID, afterID         uint64
}

func (b *buffer) cursorState() bufferCursor {
	return bufferCursor{b.row, b.col, b.anchorRow, b.anchorCol, b.anchorSet, b.goalVisual, b.goalValid}
}
func (b *buffer) restoreCursor(s bufferCursor) {
	b.row = max(0, min(s.row, len(b.lines)-1))
	b.col = max(0, min(s.col, len(b.lines[b.row])))
	b.anchorRow = max(0, min(s.anchorRow, len(b.lines)-1))
	b.anchorCol = max(0, min(s.anchorCol, len(b.lines[b.anchorRow])))
	b.anchorSet = s.anchorSet
	b.goalVisual, b.goalValid = s.goalVisual, s.goalValid
}
func (b *buffer) endPosition() cursorPos {
	row := len(b.lines) - 1
	return cursorPos{row, len(b.lines[row])}
}
func (b *buffer) insertionRange() (cursorPos, cursorPos) {
	if b.HasSelection() {
		r1, c1, r2, c2 := b.normalizedSelection()
		return cursorPos{r1, c1}, cursorPos{r2, c2}
	}
	pos := cursorPos{b.row, b.col}
	return pos, pos
}
func (b *buffer) rangeText(start, end cursorPos) string {
	if start.row == end.row {
		return string(b.lines[start.row][start.col:end.col])
	}
	var out strings.Builder
	out.WriteString(string(b.lines[start.row][start.col:]))
	out.WriteByte('\n')
	for row := start.row + 1; row < end.row; row++ {
		out.WriteString(string(b.lines[row]))
		out.WriteByte('\n')
	}
	out.WriteString(string(b.lines[end.row][:end.col]))
	return out.String()
}
func advanceText(start cursorPos, text string) cursorPos {
	row, col := start.row, start.col
	for _, r := range text {
		if r == '\n' {
			row++
			col = 0
		} else {
			col++
		}
	}
	return cursorPos{row, col}
}

// Transaction groups related public buffer edits into one history entry.
func (b *buffer) Transaction(fn func()) { b.beginEdit(editNone); defer b.endEdit(); fn() }
func (b *buffer) beginEdit(kind editKind) {
	if b.editDepth == 0 {
		b.pending = &bufferEdit{before: b.cursorState(), beforeID: b.stateID}
		b.pendingKind = kind
		b.pendingCoalesce = kind != editNone && kind == b.lastEditKind && b.row == b.lastEditRow && b.col == b.lastEditCol && !b.HasSelection()
	}
	b.editDepth++
}
func (b *buffer) endEdit() {
	b.editDepth--
	if b.editDepth > 0 {
		return
	}
	edit := b.pending
	b.pending = nil
	if len(edit.changes) == 0 {
		return
	}
	edit.after = b.cursorState()
	edit.afterID = b.stateID
	b.redo = nil
	if b.pendingCoalesce && len(b.undo) > 0 {
		last := &b.undo[len(b.undo)-1]
		// Adjacent single-line typing can be represented by one insertion.
		if len(last.changes) == 1 && len(edit.changes) == 1 {
			a, c := &last.changes[0], edit.changes[0]
			if a.before == "" && c.before == "" && a.newEnd == c.start && a.start.row == c.start.row {
				a.after += c.after
				a.newEnd = c.newEnd
			} else {
				last.changes = append(last.changes, edit.changes...)
			}
		} else {
			last.changes = append(last.changes, edit.changes...)
		}
		last.after = edit.after
		last.afterID = edit.afterID
		last.afterExtras = edit.afterExtras
		last.bytes += edit.bytes
	} else {
		b.undo = append(b.undo, *edit)
	}
	b.historyBytes += edit.bytes
	for len(b.undo) > 1 && (len(b.undo) > maxUndoDepth || b.historyBytes > maxUndoBytes) {
		b.historyBytes -= b.undo[0].bytes
		b.undo[0] = bufferEdit{}
		b.undo = b.undo[1:]
	}
	b.lastEditKind = b.pendingKind
	b.lastEditRow, b.lastEditCol = b.row, b.col
}

// replaceRange splices normalized text once and records only the changed text.
// Positions refer to the current document; callers apply independent ranges in
// descending order inside Transaction.
func (b *buffer) replaceRange(start, end cursorPos, text string) {
	before := b.rangeText(start, end)
	newEnd := advanceText(start, text)
	if before == text {
		b.row, b.col = newEnd.row, newEnd.col
		return
	}
	pieces := strings.Split(text, "\n")
	if start.row == end.row && len(pieces) == 1 {
		line := b.lines[start.row]
		insert := []rune(text)
		oldLen := len(line)
		delta := len(insert) - (end.col - start.col)
		if delta > 0 {
			line = append(line, make([]rune, delta)...)
		}
		copy(line[start.col+len(insert):], line[end.col:oldLen])
		copy(line[start.col:], insert)
		b.lines[start.row] = line[:oldLen+delta]
	} else {
		replacements := make([][]rune, len(pieces))
		for i, piece := range pieces {
			line := []rune(piece)
			if i == 0 {
				line = append(append([]rune(nil), b.lines[start.row][:start.col]...), line...)
			}
			if i == len(pieces)-1 {
				line = append(line, b.lines[end.row][end.col:]...)
			}
			replacements[i] = line
		}
		lines := make([][]rune, 0, len(b.lines)-(end.row-start.row)+len(pieces)-1)
		lines = append(lines, b.lines[:start.row]...)
		lines = append(lines, replacements...)
		lines = append(lines, b.lines[end.row+1:]...)
		b.lines = lines
	}
	b.row, b.col = newEnd.row, newEnd.col
	b.layouts = nil
	b.goalValid = false
	b.rev++
	b.bumpState()
	b.textBytes += len(text) - len(before)
	b.dirtyTo = max(newEnd.row, b.dirtyTo+newEnd.row-end.row)
	if b.dirtyFrom < 0 || start.row < b.dirtyFrom {
		b.dirtyFrom = start.row
	}
	if !b.replaying {
		change := bufferChange{start, end, newEnd, before, text}
		b.pending.changes = append(b.pending.changes, change)
		b.pending.bytes += len(before) + len(text)
	}
}
func (b *buffer) ReplaceRange(r1, c1, r2, c2 int, text string) {
	b.beginEdit(editNone)
	defer b.endEdit()
	b.replaceRange(cursorPos{r1, c1}, cursorPos{r2, c2}, normalizeEditorLineEndings(text))
	b.anchorSet = false
}
func (b *buffer) Undo() bool {
	if len(b.undo) == 0 {
		return false
	}
	edit := b.undo[len(b.undo)-1]
	b.undo[len(b.undo)-1] = bufferEdit{}
	b.undo = b.undo[:len(b.undo)-1]
	b.historyBytes -= edit.bytes
	b.replaying = true
	for i := len(edit.changes) - 1; i >= 0; i-- {
		c := edit.changes[i]
		b.replaceRange(c.start, c.newEnd, c.before)
	}
	b.replaying = false
	b.restoreCursor(edit.before)
	b.stateID = edit.beforeID
	b.restoredExtras = append([]bufferCursor(nil), edit.beforeExtras...)
	b.lastEditKind = editNone
	b.redo = append(b.redo, edit)
	return true
}
func (b *buffer) Redo() bool {
	if len(b.redo) == 0 {
		return false
	}
	edit := b.redo[len(b.redo)-1]
	b.redo[len(b.redo)-1] = bufferEdit{}
	b.redo = b.redo[:len(b.redo)-1]
	b.replaying = true
	for _, c := range edit.changes {
		b.replaceRange(c.start, c.oldEnd, c.after)
	}
	b.replaying = false
	b.restoreCursor(edit.after)
	b.stateID = edit.afterID
	b.restoredExtras = append([]bufferCursor(nil), edit.afterExtras...)
	b.lastEditKind = editNone
	b.undo = append(b.undo, edit)
	b.historyBytes += edit.bytes
	return true
}

// SetPreparedText applies a worker-prepared whole-document edit with one undo
// record. Parsing and line allocation happen before returning to the UI.
func (b *buffer) SetPreparedText(text string, lines [][]rune) {
	b.beginEdit(editNone)
	defer b.endEdit()
	before := b.Text()
	if before == text {
		return
	}
	oldEnd := b.endPosition()
	newEnd := cursorPos{len(lines) - 1, len(lines[len(lines)-1])}
	b.pending.changes = append(b.pending.changes, bufferChange{cursorPos{}, oldEnd, newEnd, before, text})
	b.pending.bytes += len(before) + len(text)
	b.lines = lines
	b.row, b.col = newEnd.row, newEnd.col
	b.anchorSet = false
	b.rev++
	b.bumpState()
	b.textBytes = len(text)
	b.dirtyFrom, b.dirtyTo = 0, len(lines)-1
	b.layouts = nil
	b.goalValid = false
	b.textCache, b.textRevision, b.textCached = text, b.rev, true
}

// stateID follows undo/redo; rev always advances, including during replay.
func (b *buffer) bumpState() {
	b.nextStateID++
	b.stateID = b.nextStateID
	if b.onChange != nil {
		b.onChange()
	}
}
func (b *buffer) breakUndoGroup() { b.lastEditKind = editNone }
