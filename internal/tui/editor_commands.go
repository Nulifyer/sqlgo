package tui

import (
	"context"
	"sort"
	"strings"
	"unicode/utf8"
)

type editorNavigation struct {
	selections           []bufferCursor
	scrollRow, scrollCol int
}

func (e *editor) navigation() editorNavigation {
	return editorNavigation{e.selections(), e.scrollRow, e.scrollCol}
}
func sameNavigation(a, b editorNavigation) bool {
	if a.scrollRow != b.scrollRow || a.scrollCol != b.scrollCol || len(a.selections) != len(b.selections) {
		return false
	}
	for i := range a.selections {
		if a.selections[i] != b.selections[i] {
			return false
		}
	}
	return true
}

// Bulk commands use a private editor on a worker. Only the prepared text and
// selection result cross back, under the same document/revision/navigation fence.
func (e *editor) runSelectionCommand(a *app, command func(*editor)) {
	if a == nil || a.asyncCh == nil || !e.largeAnalysis() {
		command(e)
		return
	}
	if e.commandBusy {
		a.mainLayerPtr().status = "editor command in progress; editing remains available"
		return
	}
	before, rev, text := e.navigation(), e.buf.Revision(), e.buf.Text()
	ctx, cancel := context.WithCancel(context.Background())
	e.commandBusy, e.commandCancel = true, cancel
	a.runBackground(func() func(*app) {
		defer cancel()
		worker := newEditor()
		worker.commandContext = ctx
		worker.buf.resetPreparedText(text, splitBufferText(text))
		worker.setSelections(before.selections)
		if ctx.Err() == nil {
			command(worker)
		}
		canceled := ctx.Err() != nil
		var result string
		var selections []bufferCursor
		var lines [][]rune
		if !canceled {
			result, selections, lines = worker.buf.Text(), worker.selections(), worker.buf.lines
		}
		return func(a *app) {
			e.commandBusy, e.commandCancel = false, nil
			if canceled || e.buf.Revision() != rev || !sameNavigation(before, e.navigation()) || a.mainLayerPtr().editor != e {
				a.mainLayerPtr().status = "editor command skipped because the document or selection changed"
				return
			}
			if result == text {
				return
			}
			e.buf.beginEdit(editNone)
			e.buf.pending.beforeExtras = append([]bufferCursor(nil), e.extraCursors...)
			e.buf.SetPreparedText(result, lines)
			e.setSelections(selections)
			e.buf.pending.afterExtras = append([]bufferCursor(nil), e.extraCursors...)
			e.buf.endEdit()
			e.ClearErrorLocation()
			a.mainLayerPtr().promoteActiveIfPreview()
		}
	})
}
func (e *editor) recordNavigation(before editorNavigation) {
	if sameNavigation(before, e.navigation()) {
		return
	}
	if len(e.cursorUndo) == 0 || !sameNavigation(e.cursorUndo[len(e.cursorUndo)-1], before) {
		e.cursorUndo = append(e.cursorUndo, before)
	}
	if len(e.cursorUndo) > 50 {
		e.cursorUndo = append([]editorNavigation(nil), e.cursorUndo[len(e.cursorUndo)-50:]...)
	}
	e.cursorRedo = nil
}
func (e *editor) undoNavigation(redo bool) {
	from, to := &e.cursorUndo, &e.cursorRedo
	if redo {
		from, to = to, from
	}
	if len(*from) == 0 {
		return
	}
	state := (*from)[len(*from)-1]
	(*from)[len(*from)-1] = editorNavigation{}
	*from = (*from)[:len(*from)-1]
	*to = append(*to, e.navigation())
	e.setSelections(state.selections)
	e.scrollRow, e.scrollCol = state.scrollRow, state.scrollCol
	e.complete = nil
	e.completionGeneration++
}
func (e *editor) addNextOccurrence(a *app) {
	if e.occurrenceBusy {
		e.occurrenceQueued = min(e.occurrenceQueued+1, maxEditorSelections)
		return
	}
	if !e.buf.HasSelection() {
		row, col := e.buf.Cursor()
		start, end := wordBoundsAt(e.buf.Line(row), col)
		if start == end {
			return
		}
		e.buf.SetAnchor(row, start)
		e.buf.SetCursor(row, end)
		return
	}
	if len(e.extraCursors) >= maxEditorSelections-1 || e.automaticAnalysisLimited() {
		if a != nil {
			a.mainLayerPtr().status = "next-occurrence selection limit reached"
		}
		return
	}
	before, rev := e.navigation(), e.buf.Revision()
	text, needle := e.buf.Text(), e.buf.Selection()
	if strings.ContainsRune(needle, '\n') {
		if a != nil {
			a.mainLayerPtr().status = "select a word or single-line text for Ctrl+D"
		}
		return
	}
	_, end := e.buf.insertionRange()
	ctx, cancel := context.WithCancel(context.Background())
	e.occurrenceCancel, e.occurrenceBusy = cancel, true
	work := func() func(*app) {
		defer cancel()
		offset := 0
		for row := 0; row < end.row; row++ {
			offset += strings.IndexByte(text[offset:], '\n') + 1
		}
		for col := 0; col < end.col; col++ {
			_, n := utf8.DecodeRuneInString(text[offset:])
			offset += n
		}
		var selected *bufferCursor
		// Scan after the primary and wrap once. Existing overlapping selections
		// are skipped, including matches in the same line.
		find := func(from, to int) bool {
			for from <= to-len(needle) {
				if ctx.Err() != nil {
					return false
				}
				found := strings.Index(text[from:to], needle)
				if found < 0 {
					return false
				}
				startByte := from + found
				endByte := startByte + len(needle)
				from = endByte
				row := strings.Count(text[:startByte], "\n")
				lineStart := strings.LastIndexByte(text[:startByte], '\n') + 1
				col := utf8.RuneCountInString(text[lineStart:startByte])
				s := bufferCursor{row: row, col: col + utf8.RuneCountInString(needle), anchorRow: row, anchorCol: col, anchorSet: true}
				x, z := cursorRange(s)
				overlap := false
				for _, old := range before.selections {
					y, w := cursorRange(old)
					if lessPosition(x, w) && lessPosition(y, z) {
						overlap = true
						break
					}
				}
				if !overlap {
					selected = &s
					return true
				}
			}
			return false
		}
		if !find(offset, len(text)) {
			find(0, offset)
		}
		return func(a *app) {
			e.occurrenceBusy, e.occurrenceCancel = false, nil
			if e.buf.Revision() != rev || !sameNavigation(before, e.navigation()) {
				e.occurrenceQueued = 0
				return
			}
			if a != nil && a.mainLayerPtr().editor != e {
				e.occurrenceQueued = 0
				return
			}
			if selected == nil {
				e.occurrenceQueued = 0
				if a != nil {
					a.mainLayerPtr().status = "all matching occurrences are selected"
				}
				return
			}
			selections := append([]bufferCursor{*selected}, before.selections...)
			e.setSelections(selections)
			e.complete = nil
			e.completionGeneration++
			e.recordNavigation(before)
			if e.occurrenceQueued > 0 {
				e.occurrenceQueued--
				e.addNextOccurrence(a)
			}
		}
	}
	if a != nil && a.asyncCh != nil && e.largeAnalysis() {
		a.runBackground(work)
	} else {
		work()(a)
	}
}
func (e *editor) surroundSelection(open, close rune) {
	e.buf.beginEdit(editNone)
	defer e.buf.endEdit()
	e.applyToAllCursors(func() {
		if !e.buf.HasSelection() {
			e.buf.Insert(open)
			return
		}
		start, end := e.buf.insertionRange()
		text := e.buf.Selection()
		forward := !lessPosition(cursorPos{e.buf.row, e.buf.col}, cursorPos{e.buf.anchorRow, e.buf.anchorCol})
		e.buf.ReplaceRange(start.row, start.col, end.row, end.col, string(open)+text+string(close))
		innerStart := advanceText(start, string(open))
		innerEnd := advanceText(innerStart, text)
		if !forward {
			innerStart, innerEnd = innerEnd, innerStart
		}
		e.buf.SetAnchor(innerStart.row, innerStart.col)
		e.buf.SetCursor(innerEnd.row, innerEnd.col)
	})
}
func leadingIndent(line []rune) int {
	n := 0
	for n < len(line) && (line[n] == ' ' || line[n] == '\t') {
		n++
	}
	return n
}
func (e *editor) selectedRows() []int {
	seen := map[int]bool{}
	for _, s := range e.selections() {
		start, end := cursorRange(s)
		last := end.row
		if end.row > start.row && end.col == 0 {
			last--
		}
		for row := start.row; row <= last; row++ {
			seen[row] = true
		}
	}
	rows := make([]int, 0, len(seen))
	for row := range seen {
		rows = append(rows, row)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(rows)))
	return rows
}

// One owner transforms every selection through a batch of line-prefix edits.
func (e *editor) editLinePrefixes(rows []int, edit func([]rune) (int, int, string)) {
	states := e.selections()
	e.buf.beginEdit(editNone)
	e.buf.pending.beforeExtras = append([]bufferCursor(nil), e.extraCursors...)
	for _, row := range rows {
		if e.commandContext != nil && e.commandContext.Err() != nil {
			break
		}
		start, remove, text := edit(e.buf.Line(row))
		count := len(e.buf.pending.changes)
		e.buf.ReplaceRange(row, start, row, start+remove, text)
		for _, change := range e.buf.pending.changes[count:] {
			for i := range states {
				states[i] = transformCursor(states[i], change)
			}
		}
	}
	e.setSelections(states)
	e.buf.pending.afterExtras = append([]bufferCursor(nil), e.extraCursors...)
	e.buf.endEdit()
	e.ClearErrorLocation()
}
func (e *editor) toggleLineComments() {
	rows := e.selectedRows()
	uncomment := true
	nonblank := false
	for _, row := range rows {
		line := e.buf.Line(row)
		indent := leadingIndent(line)
		if indent == len(line) {
			continue
		}
		nonblank = true
		if !strings.HasPrefix(string(line[indent:]), "--") {
			uncomment = false
			break
		}
	}
	uncomment = uncomment && nonblank
	e.editLinePrefixes(rows, func(line []rune) (int, int, string) {
		indent := leadingIndent(line)
		if uncomment {
			if indent+2 > len(line) || string(line[indent:indent+2]) != "--" {
				return 0, 0, ""
			}
			n := 2
			if indent+n < len(line) && line[indent+n] == ' ' {
				n++
			}
			return indent, n, ""
		}
		if indent == len(line) && len(rows) > 1 {
			return 0, 0, ""
		}
		return indent, 0, "-- "
	})
}
func (e *editor) indentSelectedLines(dedent bool) {
	e.editLinePrefixes(e.selectedRows(), func(line []rune) (int, int, string) {
		if !dedent {
			return 0, 0, strings.Repeat(" ", softTabWidth)
		}
		count, cells := 0, 0
		for count < len(line) && cells < softTabWidth {
			if line[count] == ' ' {
				cells++
			} else if line[count] == '\t' {
				cells += editorTabStop - cells%editorTabStop
			} else {
				break
			}
			count++
		}
		return 0, count, ""
	})
}
