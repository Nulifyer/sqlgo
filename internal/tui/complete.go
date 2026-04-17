package tui

import (
	"sort"
	"strings"

	"github.com/Nulifyer/sqlgo/internal/search/fzfmatch"
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

// marker returns a single-cell glyph that labels a completion item's kind
// in the popup's left gutter. Glyphs chosen to be visually distinct at a
// glance: keyword/schema/table/view/column/alias/function all look
// different without relying on color.
func (k completionKind) marker() string {
	switch k {
	case completeKeyword:
		return "◆"
	case completeSchema:
		return "§"
	case completeTable:
		return "▦"
	case completeView:
		return "◇"
	case completeColumn:
		return "∷"
	case completeAlias:
		return "@"
	case completeFunction:
		return "ƒ"
	}
	return " "
}

// completionItem is one candidate shown in the popup.
type completionItem struct {
	text     string
	kind     completionKind
	typeHint string // column type, shown dim after text
	// matches holds rune indices into text that matched the current
	// fuzzy prefix. Populated by filterCompletions; used by
	// drawComplete to highlight the matching runes. nil means "no
	// prefix" or "not yet filtered" -- render without highlights.
	matches []int
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
	qualifier, catalog := qualifiersBeforeCursor(line, startCol)

	text := e.buf.Text()
	cursorOffset := runeOffsetOf(e.buf, row, col)
	ctx := analyzeCursorContext(text, cursorOffset)
	ctx.qualifier = qualifier
	ctx.catalog = catalog
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

// qualifiersBeforeCursor walks back from startCol over optional
// "[schema].[" or "schema." and "[catalog].[schema]." prefixes,
// returning (qualifier, catalog). Supports MSSQL/Sybase bracketed
// identifiers and three-part "cat.schema.name" qualifiers. Empty
// strings mean "not present".
func qualifiersBeforeCursor(line []rune, startCol int) (qualifier, catalog string) {
	if startCol <= 0 || startCol > len(line) {
		return "", ""
	}
	// Skip the opening '[' of the word currently being typed.
	end := startCol
	if line[end-1] == '[' {
		end--
	}
	if end <= 0 || line[end-1] != '.' {
		return "", ""
	}
	// Parse the segment preceding the '.'.
	seg, before, ok := parseSegmentBack(line, end-1)
	if !ok {
		return "", ""
	}
	qualifier = seg
	// Look for a second '.' for a three-part name.
	if before <= 0 || line[before-1] != '.' {
		return qualifier, ""
	}
	seg2, _, ok := parseSegmentBack(line, before-1)
	if !ok {
		return qualifier, ""
	}
	catalog = seg2
	return qualifier, catalog
}

// parseSegmentBack parses one identifier segment ending at `end`
// (exclusive). Handles bracketed `[name]` and bare `name` forms.
// Returns the segment text, the position before it, and whether a
// segment was found.
func parseSegmentBack(line []rune, end int) (string, int, bool) {
	if end <= 0 {
		return "", end, false
	}
	if line[end-1] == ']' {
		p := end - 2
		for p >= 0 && line[p] != '[' {
			p--
		}
		if p < 0 {
			return "", end, false
		}
		return string(line[p+1 : end-1]), p, true
	}
	p := end
	for p > 0 && isIdentRune(line[p-1]) {
		p--
	}
	if p == end {
		return "", end, false
	}
	return string(line[p:end]), p, true
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

// filterCompletions keeps fuzzy (subsequence) matches, scored
// fzf-style and combined with a context-derived kind bonus so
// typing-quality and clause context both steer ranking. Empty
// prefix = no filter; sort falls back to kind bonus + alpha.
//
// Case preservation: if the prefix is all-lowercase, keywords and
// functions are emitted lowercase so "sel" → "select" matches the
// user's typing style instead of force-upper SELECT.
func filterCompletions(items []completionItem, prefix string) []completionItem {
	type scored struct {
		it      completionItem
		score   int
		matches []int
	}
	var out []scored
	for _, it := range items {
		s, matches, ok := fuzzyScore(prefix, it.text)
		if !ok {
			continue
		}
		s += kindBonus(it.kind)
		out = append(out, scored{it: it, score: s, matches: matches})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return strings.ToLower(out[i].it.text) < strings.ToLower(out[j].it.text)
	})
	result := make([]completionItem, len(out))
	for i := range out {
		result[i] = out[i].it
		result[i].matches = out[i].matches
	}
	if prefix != "" && strings.ToLower(prefix) == prefix {
		for i := range result {
			if result[i].kind == completeKeyword || result[i].kind == completeFunction {
				result[i].text = strings.ToLower(result[i].text)
			}
		}
	}
	return result
}

// fuzzyScore forwards to the shared fuzzy matcher so completion keeps the
// same ranking behavior as the rest of the app's fuzzy search surfaces.
func fuzzyScore(needle, haystack string) (int, []int, bool) {
	result, ok := fzfmatch.Match(needle, haystack)
	return result.Score, result.Positions, ok
}

// kindBonus layers context on top of the fuzzy score so columns
// (in SELECT/WHERE) and tables (in FROM) bubble up over keywords
// at equal match quality. Gap is small -- a clearly-better typed
// match still wins over a weakly-matched higher-ranked kind.
func kindBonus(k completionKind) int {
	switch k {
	case completeColumn:
		return 24
	case completeAlias:
		return 20
	case completeSchema:
		return 16
	case completeTable:
		return 14
	case completeView:
		return 12
	case completeFunction:
		return 8
	case completeKeyword:
		return 4
	}
	return 0
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
			c.WriteStyled(popupRow+i, anchorCol+x, " ", style)
		}
		c.WriteStyled(popupRow+i, anchorCol+1, it.kind.marker(), style)
		textCol := anchorCol + markerWidth
		avail := popupW - markerWidth - 1
		if avail < 0 {
			avail = 0
		}
		text := truncate(it.text, avail)
		col := textCol
		runes := []rune(text)
		mi := 0
		for ri, r := range runes {
			w := runeDisplayWidth(r)
			if w == 0 {
				continue
			}
			if col+w > anchorCol+popupW {
				break
			}
			runeStyle := style
			for mi < len(it.matches) && it.matches[mi] < ri {
				mi++
			}
			if mi < len(it.matches) && it.matches[mi] == ri {
				runeStyle.Attrs |= attrBold | attrUnderline
			}
			c.WriteStyled(popupRow+i, col, string(r), runeStyle)
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
					c.WriteStyled(popupRow+i, hintCol, string(r), tStyle)
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
