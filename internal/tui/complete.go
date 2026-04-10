package tui

import (
	"sort"
	"strings"
)

// completionKind tags a candidate for display (so the popup can show
// a one-letter hint next to each entry) and for ranking (columns
// and identifier kinds sort before keywords).
type completionKind int

const (
	completeKeyword completionKind = iota
	completeSchema
	completeTable
	completeView
	completeColumn
	completeAlias
)

func (k completionKind) marker() string {
	switch k {
	case completeKeyword:
		return "k"
	case completeSchema:
		return "s"
	case completeTable:
		return "t"
	case completeView:
		return "v"
	case completeColumn:
		return "c"
	case completeAlias:
		return "a"
	}
	return " "
}

// completionItem is one candidate shown in the autocomplete popup.
// Text is the literal string that gets pasted into the buffer when
// the item is accepted; it does not need to match the user's prefix
// byte-for-byte (e.g. keyword entries are uppercase, so accepting
// "se" + SELECT replaces "se" with "SELECT").
type completionItem struct {
	text string
	kind completionKind
}

// completionState is the live popup: the prefix it was opened with,
// the starting rune column in the current line where that prefix
// begins (so accept() knows the span to replace), the filtered
// candidate list, and the selected index.
type completionState struct {
	startCol int              // rune column in the buffer line where the prefix starts
	prefix   string           // original prefix used to open the popup (preserved for display)
	items    []completionItem // filtered + sorted matches
	selected int              // index into items
}

// moveSelection advances the highlighted row by delta, clamping to
// [0, len(items)-1]. Called by the editor's Up/Down key handlers
// while the popup is open.
func (c *completionState) moveSelection(delta int) {
	if len(c.items) == 0 {
		return
	}
	c.selected += delta
	if c.selected < 0 {
		c.selected = 0
	}
	if c.selected >= len(c.items) {
		c.selected = len(c.items) - 1
	}
}

// current returns the selected completion item, or an empty item
// with kind=-1 when the popup's candidate list is empty.
func (c *completionState) current() (completionItem, bool) {
	if c == nil || len(c.items) == 0 {
		return completionItem{}, false
	}
	if c.selected < 0 || c.selected >= len(c.items) {
		return completionItem{}, false
	}
	return c.items[c.selected], true
}

// openCompletion builds a new popup against the word under the
// cursor. The steps are:
//
//  1. Figure out the identifier prefix under the cursor (word
//     before cursor plus any leading schema/alias qualifier).
//  2. Analyze where in the SQL statement the cursor is (SELECT
//     list / FROM target / WHERE-ish / generic) and which tables
//     are in scope via the FROM/JOIN clause.
//  3. Gather candidates for that context from the app (keywords,
//     schema names, table/view names, and columns fetched from
//     the live connection + cached per table).
//  4. Filter by the prefix (with qualifier handling) and drop the
//     popup onto the editor.
//
// A cursor inside a string literal or comment suppresses the
// popup entirely so typing Ctrl+Space inside `'abc'` doesn't turn
// half the string into a keyword.
func (e *editor) openCompletion(a *app) {
	row, col := e.buf.Cursor()
	line := e.buf.Line(row)
	word, startCol := wordBeforeCursor(line, col)

	// Detect a leading "qualifier." so `u.name` is parsed as
	// qualifier="u" + word="name" with the replacement span
	// starting at the 'n'. startCol still points at the start of
	// the identifier half, which is what accept() wants.
	qualifier := qualifierBeforeCursor(line, startCol)

	text := e.buf.Text()
	cursorOffset := runeOffsetOf(e.buf, row, col)
	ctx := analyzeCursorContext(text, cursorOffset)
	ctx.qualifier = qualifier
	ctx.prefix = word
	ctx.startCol = startCol

	if ctx.suppress {
		return
	}

	var items []completionItem
	if a != nil {
		items = a.gatherCompletionsCtx(ctx)
	}
	items = filterCompletions(items, word)
	if len(items) == 0 {
		return
	}
	e.complete = &completionState{
		startCol: startCol,
		prefix:   word,
		items:    items,
	}
}

// runeOffsetOf converts a (row, col) position in the buffer into a
// rune offset from the start of the text. The editor stores lines
// as []rune, and buffer.Text() joins them with "\n" bytes, so the
// rune offset is sum(len(line[i])+1 for i<row) + col. Kept here
// next to its only caller.
func runeOffsetOf(b *buffer, row, col int) int {
	off := 0
	for i := 0; i < row && i < b.LineCount(); i++ {
		off += len(b.Line(i)) + 1 // +1 for the newline
	}
	off += col
	return off
}

// qualifierBeforeCursor returns the identifier immediately preceding
// a '.' that sits at startCol-1. When the character at startCol-1
// isn't a '.', returns the empty string. Used so `u.name|` yields
// qualifier "u" and word "name", which the column-completion path
// then uses to narrow to u's columns.
func qualifierBeforeCursor(line []rune, startCol int) string {
	if startCol <= 0 || startCol > len(line) {
		return ""
	}
	if line[startCol-1] != '.' {
		return ""
	}
	// Walk back over the identifier that owns the dot.
	end := startCol - 1
	start := end
	for start > 0 && isIdentRune(line[start-1]) {
		start--
	}
	return string(line[start:end])
}

// acceptCompletion replaces the prefix under the cursor with the
// selected item's text and closes the popup. Implemented in the
// buffer's normal mutation vocabulary (delete + insert) so the
// operation lands on the undo stack as one edit bracket.
func (e *editor) acceptCompletion() {
	if e.complete == nil {
		return
	}
	item, ok := e.complete.current()
	if !ok {
		e.complete = nil
		return
	}
	_, col := e.buf.Cursor()
	toDelete := col - e.complete.startCol
	// Walk back over the prefix runes with Backspace so the buffer's
	// undo snapshot + cursor tracking stay consistent. toDelete is
	// already measured in runes (startCol is a rune column).
	for i := 0; i < toDelete; i++ {
		e.buf.Backspace()
	}
	e.buf.InsertText(item.text)
	e.complete = nil
}

// filterCompletions keeps items whose text starts with prefix
// (case-insensitive) and returns them ranked: exact-prefix matches
// first, identifier kinds before keywords at the same depth, then
// alphabetical. An empty prefix returns every item unfiltered in the
// same ranked order so an opened-on-empty popup isn't useless.
func filterCompletions(items []completionItem, prefix string) []completionItem {
	needle := strings.ToLower(prefix)
	var out []completionItem
	for _, it := range items {
		if needle == "" || strings.HasPrefix(strings.ToLower(it.text), needle) {
			out = append(out, it)
		}
	}
	// Stable sort by (kind bucket, text) so tables/views float above
	// keywords when both match. Kind order: schema, table, view,
	// keyword -- identifiers are usually what the user wants, and
	// the keyword list is large enough that it would drown out the
	// targeted matches otherwise.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].kind != out[j].kind {
			return kindRank(out[i].kind) < kindRank(out[j].kind)
		}
		return strings.ToLower(out[i].text) < strings.ToLower(out[j].text)
	})
	return out
}

// kindRank maps a completion kind to its sort bucket. Lower ranks
// sort higher (appear first) in the popup.
func kindRank(k completionKind) int {
	switch k {
	case completeSchema:
		return 0
	case completeTable:
		return 1
	case completeView:
		return 2
	case completeKeyword:
		return 3
	}
	return 4
}

// wordBeforeCursor walks the current line to the left of the cursor
// and returns the run of identifier characters (letters, digits,
// underscore) ending at the cursor. startCol is the rune index in the
// line where the prefix begins; when no identifier characters are to
// the left, startCol == cursor column and prefix is empty. A leading
// '.' is NOT consumed: "dbo.use|" yields ("use", col 4), not
// ("dbo.use", col 0). This keeps v1 simple and makes schema-qualified
// completion a future tweak rather than a rewrite.
func wordBeforeCursor(line []rune, col int) (prefix string, startCol int) {
	if col > len(line) {
		col = len(line)
	}
	start := col
	for start > 0 {
		r := line[start-1]
		if !isIdentRune(r) {
			break
		}
		start--
	}
	return string(line[start:col]), start
}

// drawComplete paints the autocomplete popup inside the editor's
// inner rect (the caller already added 1 for the border). The popup
// is anchored at the cursor's visual position, flipping above the
// cursor when there isn't enough vertical room below, and clipped
// horizontally so it never spills past the editor's right edge.
//
// Layout per line: " marker text " where marker is a single-letter
// kind hint (k/s/t/v). The selected row uses the reverse-video
// "selected" style; non-selected rows use a muted background so the
// popup reads as a distinct floating element.
func (e *editor) drawComplete(c *cellbuf, innerRow, innerCol, innerW, innerH int) {
	cs := e.complete
	if cs == nil || len(cs.items) == 0 {
		return
	}

	// Measure the widest entry so the popup is as narrow as possible
	// while still fitting every visible row without truncation.
	const markerWidth = 4 // " X  " (leading space, marker, two trailing spaces)
	const maxVisible = 8
	visible := len(cs.items)
	if visible > maxVisible {
		visible = maxVisible
	}
	widest := 0
	for _, it := range cs.items {
		if w := displayWidth(it.text); w > widest {
			widest = w
		}
	}
	popupW := markerWidth + widest + 1 // +1 for trailing padding
	if popupW > innerW {
		popupW = innerW
	}
	if popupW < 8 {
		popupW = 8
	}

	// Cursor anchor in screen coordinates. The buffer cursor is in
	// buffer space; the editor's scroll offsets translate to the
	// visible inner rect. The popup's horizontal anchor is the
	// prefix's start column so the candidate list lines up with the
	// word being completed rather than with the cursor caret.
	curRow, _ := e.buf.Cursor()
	anchorRow := innerRow + (curRow - e.scrollRow)
	anchorCol := innerCol + (cs.startCol - e.scrollCol)

	// Horizontal clip: slide left if the popup would overflow the
	// right edge. Never slide past innerCol.
	maxCol := innerCol + innerW - popupW
	if anchorCol > maxCol {
		anchorCol = maxCol
	}
	if anchorCol < innerCol {
		anchorCol = innerCol
	}

	// Vertical placement: prefer below the cursor; flip above if the
	// popup wouldn't fit. Height = visible rows (no border -- a 1-row
	// popup would waste half its space on chrome).
	popupH := visible
	popupRow := anchorRow + 1
	if popupRow+popupH > innerRow+innerH {
		// Not enough room below. Flip above the cursor, clamped to
		// the top of the editor inner rect.
		popupRow = anchorRow - popupH
		if popupRow < innerRow {
			popupRow = innerRow
		}
		// If it still doesn't fit (a tiny editor), clip the height.
		if popupRow+popupH > innerRow+innerH {
			popupH = innerRow + innerH - popupRow
		}
	}
	if popupH <= 0 {
		return
	}

	// Adjust scroll so the selected item is always visible. The list
	// scroll window is [scroll, scroll+popupH).
	scroll := 0
	if cs.selected >= popupH {
		scroll = cs.selected - popupH + 1
	}
	if scroll+popupH > len(cs.items) {
		scroll = len(cs.items) - popupH
	}
	if scroll < 0 {
		scroll = 0
	}

	normal := Style{FG: ansiDefault, BG: ansiBrightBlack}
	selected := Style{FG: ansiDefault, BG: ansiDefaultBG, Attrs: attrReverse}

	for i := 0; i < popupH; i++ {
		idx := scroll + i
		if idx >= len(cs.items) {
			break
		}
		it := cs.items[idx]
		style := normal
		if idx == cs.selected {
			style = selected
		}
		// Paint the whole row with the row style first so a short
		// entry still has a solid background out to popupW.
		for x := 0; x < popupW; x++ {
			c.writeStyled(popupRow+i, anchorCol+x, " ", style)
		}
		// Marker column: " x  "
		c.writeStyled(popupRow+i, anchorCol+1, it.kind.marker(), style)
		// Text, truncated to whatever width fits after the marker.
		textCol := anchorCol + markerWidth
		avail := popupW - markerWidth - 1
		if avail < 0 {
			avail = 0
		}
		text := truncate(it.text, avail)
		col := textCol
		for _, r := range text {
			w := runeDisplayWidth(r)
			if w == 0 {
				continue
			}
			if col+w > anchorCol+popupW {
				break
			}
			c.writeStyled(popupRow+i, col, string(r), style)
			col += w
		}
	}
}

// isIdentRune matches sqltok's identifier continuation rule so the
// word-under-cursor logic stays consistent with how the tokenizer
// sees the same text. Kept private to the tui package -- sqltok's
// isIdentCont is not exported, and duplicating the two-line rule is
// cheaper than exposing it.
func isIdentRune(r rune) bool {
	if r == '_' {
		return true
	}
	if r >= 'a' && r <= 'z' {
		return true
	}
	if r >= 'A' && r <= 'Z' {
		return true
	}
	if r >= '0' && r <= '9' {
		return true
	}
	return false
}
