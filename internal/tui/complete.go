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

// fuzzyScore returns (score, matches, true) if needle is a
// case-insensitive subsequence of haystack, or (0, nil, false)
// otherwise. matches is the rune indices into haystack of the
// matched characters (len == len([]rune(needle))). Higher scores
// rank higher in the popup. Empty needle returns a neutral
// (0, nil, true) so empty-prefix filters don't drop anything.
//
// Scoring (fzf-inspired Smith-Waterman-ish DP):
//   - exact case-insensitive prefix match: flat 1000 so any full
//     prefix hit outranks any subsequence-only hit.
//   - first-char / post-separator ('_', '.', ' ', '-', '/'): +30 boundary.
//   - lower->upper camelCase boundary: +28.
//   - exact-case match: +2 (favors "Name" over "name" when typing "Name").
//   - consecutive haystack match (streak): +15.
//   - later positions: small -hi/4 penalty (earlier is better).
//   - longer haystacks: -(len(h)-len(n))/2 tiebreak so shorter wins.
//
// Complexity is O(n*m^2) with n = len(needle), m = len(haystack).
// Both are small in practice (identifiers, <~50 runes), so the
// naive DP is fine and keeps the code readable.
func fuzzyScore(needle, haystack string) (int, []int, bool) {
	if needle == "" {
		return 0, nil, true
	}
	nOrig := []rune(needle)
	hOrig := []rune(haystack)
	n := len(nOrig)
	m := len(hOrig)
	if n > m {
		return 0, nil, false
	}
	nLow := []rune(strings.ToLower(needle))
	hLow := []rune(strings.ToLower(haystack))

	// Prefix fast path. All-sequential matches, flat big score
	// minus a length tiebreak so shorter prefix hits still win.
	isPrefix := true
	for i := 0; i < n; i++ {
		if hLow[i] != nLow[i] {
			isPrefix = false
			break
		}
	}
	if isPrefix {
		matches := make([]int, n)
		for i := 0; i < n; i++ {
			matches[i] = i
		}
		return 1000, matches, true
	}

	const neg = -1 << 30
	best := make([][]int, n)
	prev := make([][]int, n)
	for j := 0; j < n; j++ {
		best[j] = make([]int, m)
		prev[j] = make([]int, m)
		for i := 0; i < m; i++ {
			best[j][i] = neg
			prev[j][i] = -1
		}
	}

	for i := 0; i < m; i++ {
		if hLow[i] != nLow[0] {
			continue
		}
		best[0][i] = charBonus(hOrig, hLow, nOrig, 0, i)
	}

	for j := 1; j < n; j++ {
		for i := j; i < m; i++ {
			if hLow[i] != nLow[j] {
				continue
			}
			bonus := charBonus(hOrig, hLow, nOrig, j, i)
			bestPrev := neg
			bestK := -1
			for k := j - 1; k < i; k++ {
				if best[j-1][k] == neg {
					continue
				}
				add := bonus
				if k == i-1 {
					add += 15 // streak
				}
				total := best[j-1][k] + add
				if total > bestPrev {
					bestPrev = total
					bestK = k
				}
			}
			if bestK >= 0 {
				best[j][i] = bestPrev
				prev[j][i] = bestK
			}
		}
	}

	bestEnd := -1
	bestScore := neg
	for i := n - 1; i < m; i++ {
		if best[n-1][i] > bestScore {
			bestScore = best[n-1][i]
			bestEnd = i
		}
	}
	if bestEnd < 0 {
		return 0, nil, false
	}

	matches := make([]int, n)
	idx := bestEnd
	for j := n - 1; j >= 0; j-- {
		matches[j] = idx
		idx = prev[j][idx]
	}
	bestScore -= (m - n) / 2
	return bestScore, matches, true
}

// charBonus scores a single haystack[i] ↔ needle[j] match independent
// of predecessor streaks (streak bonus is applied by the DP edge).
func charBonus(hOrig, hLow, nOrig []rune, j, i int) int {
	bonus := 10
	if i == 0 {
		bonus += 30
	} else {
		switch hLow[i-1] {
		case '_', '.', ' ', '-', '/':
			bonus += 30
		default:
			po := hOrig[i-1]
			co := hOrig[i]
			if po >= 'a' && po <= 'z' && co >= 'A' && co <= 'Z' {
				bonus += 28
			}
		}
	}
	if nOrig[j] == hOrig[i] {
		bonus += 2
	}
	bonus -= i / 4
	return bonus
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
			c.writeStyled(popupRow+i, col, string(r), runeStyle)
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
