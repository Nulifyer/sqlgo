package tui

import (
	"sort"
	"strings"
)

// completionKind tags a candidate for display marker + ranking.
type completionKind int

const (
	completeKeyword completionKind = iota
	completeSchema
	completeTable
	completeView
	completeColumn
	completeAlias
	completeFunction
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
	case completeFunction:
		return "f"
	}
	return " "
}

// completionItem is one candidate shown in the popup.
type completionItem struct {
	text     string
	kind     completionKind
	typeHint string // column type, shown dim after text
}

// completionState is the live popup.
type completionState struct {
	startCol int              // where the prefix begins in the buffer line
	prefix   string           // prefix used to open
	items    []completionItem // filtered matches
	selected int
}

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

func (c *completionState) current() (completionItem, bool) {
	if c == nil || len(c.items) == 0 {
		return completionItem{}, false
	}
	if c.selected < 0 || c.selected >= len(c.items) {
		return completionItem{}, false
	}
	return c.items[c.selected], true
}

// openCompletion analyzes the cursor context, fetches candidates,
// filters by prefix, and shows the popup. No-op if the cursor is
// in a string/comment or no candidates remain.
func (e *editor) openCompletion(a *app) {
	row, col := e.buf.Cursor()
	line := e.buf.Line(row)
	word, startCol := wordBeforeCursor(line, col)
	qualifier := qualifierBeforeCursor(line, startCol)

	text := e.buf.Text()
	cursorOffset := runeOffsetOf(e.buf, row, col)
	ctx := analyzeCursorContext(text, cursorOffset)
	ctx.qualifier = qualifier
	ctx.prefix = word
	ctx.startCol = startCol

	// Clear any existing popup up front so a live-refine that
	// narrows to zero items dismisses cleanly.
	e.complete = nil

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

// runeOffsetOf converts (row, col) to a rune offset into buf.Text().
func runeOffsetOf(b *buffer, row, col int) int {
	off := 0
	for i := 0; i < row && i < b.LineCount(); i++ {
		off += len(b.Line(i)) + 1
	}
	off += col
	return off
}

// qualifierBeforeCursor returns the identifier preceding a '.' at
// startCol-1, or empty when there's no dot.
func qualifierBeforeCursor(line []rune, startCol int) string {
	if startCol <= 0 || startCol > len(line) {
		return ""
	}
	if line[startCol-1] != '.' {
		return ""
	}
	end := startCol - 1
	start := end
	for start > 0 && isIdentRune(line[start-1]) {
		start--
	}
	return string(line[start:end])
}

// acceptCompletion replaces the prefix with the selected item and
// closes the popup. Goes through buf.Backspace+InsertText so the
// change lands as one undo bracket.
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
	for i := 0; i < toDelete; i++ {
		e.buf.Backspace()
	}
	e.buf.InsertText(item.text)
	e.complete = nil
}

// filterCompletions keeps case-insensitive prefix matches, sorted
// by kindRank then alphabetical. Empty prefix = no filter.
//
// Case preservation: if the prefix is all-lowercase, keywords and
// functions are emitted lowercase so "sel" → "select" matches the
// user's typing style instead of force-upper SELECT.
func filterCompletions(items []completionItem, prefix string) []completionItem {
	needle := strings.ToLower(prefix)
	var out []completionItem
	for _, it := range items {
		if needle == "" || strings.HasPrefix(strings.ToLower(it.text), needle) {
			out = append(out, it)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].kind != out[j].kind {
			return kindRank(out[i].kind) < kindRank(out[j].kind)
		}
		return strings.ToLower(out[i].text) < strings.ToLower(out[j].text)
	})
	if prefix != "" && strings.ToLower(prefix) == prefix {
		for i := range out {
			if out[i].kind == completeKeyword || out[i].kind == completeFunction {
				out[i].text = strings.ToLower(out[i].text)
			}
		}
	}
	return out
}

// kindRank: lower = higher in popup. Columns/aliases outrank
// everything in SELECT/WHERE contexts; functions above keywords.
func kindRank(k completionKind) int {
	switch k {
	case completeColumn:
		return 0
	case completeAlias:
		return 1
	case completeSchema:
		return 2
	case completeTable:
		return 3
	case completeView:
		return 4
	case completeFunction:
		return 5
	case completeKeyword:
		return 6
	}
	return 7
}

// wordBeforeCursor returns the ident chars ending at col. A
// leading '.' is not consumed ("dbo.use|" → "use", col 4).
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

// drawComplete paints the popup anchored at the prefix start.
// Flips above the cursor when there isn't room below, clips
// horizontally to innerW, scrolls to keep selected visible.
func (e *editor) drawComplete(c *cellbuf, innerRow, innerCol, innerW, innerH int) {
	cs := e.complete
	if cs == nil || len(cs.items) == 0 {
		return
	}

	const markerWidth = 4 // " X  "
	const maxVisible = 8
	const typeGap = 2 // spaces between text and type hint
	visible := len(cs.items)
	if visible > maxVisible {
		visible = maxVisible
	}
	widestText := 0
	widestType := 0
	for _, it := range cs.items {
		if w := displayWidth(it.text); w > widestText {
			widestText = w
		}
		if w := displayWidth(it.typeHint); w > widestType {
			widestType = w
		}
	}
	popupW := markerWidth + widestText + 1
	if widestType > 0 {
		popupW = markerWidth + widestText + typeGap + widestType + 1
	}
	if popupW > innerW {
		popupW = innerW
	}
	if popupW < 8 {
		popupW = 8
	}

	curRow, _ := e.buf.Cursor()
	anchorRow := innerRow + (curRow - e.scrollRow)
	anchorCol := innerCol + (cs.startCol - e.scrollCol)

	maxCol := innerCol + innerW - popupW
	if anchorCol > maxCol {
		anchorCol = maxCol
	}
	if anchorCol < innerCol {
		anchorCol = innerCol
	}

	popupH := visible
	popupRow := anchorRow + 1
	if popupRow+popupH > innerRow+innerH {
		popupRow = anchorRow - popupH
		if popupRow < innerRow {
			popupRow = innerRow
		}
		if popupRow+popupH > innerRow+innerH {
			popupH = innerRow + innerH - popupRow
		}
	}
	if popupH <= 0 {
		return
	}

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
	typeDim := Style{FG: ansiBrightBlack, BG: ansiBrightBlack}
	typeDimSel := Style{FG: ansiDefault, BG: ansiDefaultBG, Attrs: attrReverse}

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
		for x := 0; x < popupW; x++ {
			c.writeStyled(popupRow+i, anchorCol+x, " ", style)
		}
		c.writeStyled(popupRow+i, anchorCol+1, it.kind.marker(), style)
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
		// Type hint (dim) right-justified within the row.
		if it.typeHint != "" {
			tStyle := typeDim
			if idx == cs.selected {
				tStyle = typeDimSel
			}
			hint := truncate(it.typeHint, widestType)
			hintCol := anchorCol + popupW - 1 - displayWidth(hint)
			if hintCol >= col+typeGap {
				for _, r := range hint {
					w := runeDisplayWidth(r)
					if w == 0 {
						continue
					}
					c.writeStyled(popupRow+i, hintCol, string(r), tStyle)
					hintCol += w
				}
			}
		}
	}
}

// isIdentRune mirrors sqltok's ident-continuation rule (letters,
// digits, underscore). Kept local since sqltok's isIdentCont is
// unexported.
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
