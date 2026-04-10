package tui

import "strings"

// SetSearch installs a new needle on the editor, recomputes the
// match list, and snaps currentMatch to the first hit at or after
// the cursor. Called by findLayer whenever the user types in the
// Find field or edits the buffer under an open overlay. An empty
// needle clears all search state (same shape as ClearSearch).
func (e *editor) SetSearch(needle string) {
	if needle == "" {
		e.ClearSearch()
		return
	}
	e.searchNeedle = needle
	e.matches = e.computeMatches(needle)
	e.currentMatch = e.firstMatchAtOrAfterCursor()
}

// RefreshMatches recomputes the match list against the current
// needle and re-snaps currentMatch. Called after an edit under an
// open overlay (typing in the buffer via a replace, for example)
// so highlights stay consistent with the buffer text.
func (e *editor) RefreshMatches() {
	if e.searchNeedle == "" {
		return
	}
	e.matches = e.computeMatches(e.searchNeedle)
	if len(e.matches) == 0 {
		e.currentMatch = -1
		return
	}
	if e.currentMatch < 0 || e.currentMatch >= len(e.matches) {
		e.currentMatch = 0
	}
}

// ClearSearch drops all find/replace state and stops painting
// highlights on the next draw.
func (e *editor) ClearSearch() {
	e.searchNeedle = ""
	e.matches = nil
	e.currentMatch = -1
}

// HasSearch reports whether a search is currently live.
func (e *editor) HasSearch() bool { return e.searchNeedle != "" }

// MatchCount returns the number of hits for the active search.
func (e *editor) MatchCount() int { return len(e.matches) }

// CurrentMatchIndex returns the 1-based index of the current match,
// or 0 when there are no matches. Formatted this way so the find
// overlay's "3 of 17" status line doesn't have to branch.
func (e *editor) CurrentMatchIndex() int {
	if e.currentMatch < 0 || len(e.matches) == 0 {
		return 0
	}
	return e.currentMatch + 1
}

// NextMatch advances to the next match (wrapping around) and jumps
// the cursor to its start so the editor scrolls it into view. No-op
// when there are no matches.
func (e *editor) NextMatch() {
	if len(e.matches) == 0 {
		return
	}
	e.currentMatch++
	if e.currentMatch >= len(e.matches) {
		e.currentMatch = 0
	}
	e.jumpToCurrentMatch()
}

// PrevMatch moves to the previous match (wrapping around).
func (e *editor) PrevMatch() {
	if len(e.matches) == 0 {
		return
	}
	e.currentMatch--
	if e.currentMatch < 0 {
		e.currentMatch = len(e.matches) - 1
	}
	e.jumpToCurrentMatch()
}

// ReplaceCurrent replaces the currently highlighted match with the
// given text and advances to the next match. Returns true when a
// replacement was performed. Buffer mutation goes through the
// normal Insert/Backspace vocabulary so the change lands on the
// undo stack and the next Undo rolls back the whole replacement.
func (e *editor) ReplaceCurrent(replacement string) bool {
	if e.currentMatch < 0 || e.currentMatch >= len(e.matches) {
		return false
	}
	m := e.matches[e.currentMatch]
	e.applyReplaceAt(m, replacement)
	// Rebuild matches against the new buffer text. The next match
	// might be at the same index (if replacement shortened the
	// buffer) or later; RefreshMatches clamps for us.
	e.RefreshMatches()
	// Advance to the next hit so Enter repeatedly steps through.
	if len(e.matches) > 0 {
		if e.currentMatch >= len(e.matches) {
			e.currentMatch = 0
		}
		e.jumpToCurrentMatch()
	}
	return true
}

// ReplaceAll replaces every match with replacement and returns the
// number of substitutions made. Walks matches in reverse so earlier
// match positions don't shift as later ones are rewritten, which
// keeps the mutation loop simple and correct.
func (e *editor) ReplaceAll(replacement string) int {
	if len(e.matches) == 0 {
		return 0
	}
	n := len(e.matches)
	for i := n - 1; i >= 0; i-- {
		e.applyReplaceAt(e.matches[i], replacement)
	}
	e.RefreshMatches()
	if len(e.matches) > 0 {
		e.currentMatch = 0
		e.jumpToCurrentMatch()
	} else {
		e.currentMatch = -1
	}
	return n
}

// applyReplaceAt is the low-level mutation helper used by both
// ReplaceCurrent and ReplaceAll. It positions the cursor at the
// end of the match, backspaces the match runes, and inserts the
// replacement in their place. All three operations go through the
// buffer's public API so undo snapshots stay consistent.
func (e *editor) applyReplaceAt(m matchRange, replacement string) {
	// Move the cursor to the end of the match. The buffer's Cursor
	// setters are private, so we use public moves: first jump to
	// the row's start-of-line, then step right m.col+m.length times.
	e.buf.ClearSelection()
	// Step vertically to the target row from wherever we are.
	curRow, _ := e.buf.Cursor()
	for curRow < m.row {
		e.buf.MoveDown()
		curRow, _ = e.buf.Cursor()
	}
	for curRow > m.row {
		e.buf.MoveUp()
		curRow, _ = e.buf.Cursor()
	}
	// Snap to the start of the line, then step right to the end of
	// the match.
	e.buf.MoveHome()
	for i := 0; i < m.col+m.length; i++ {
		e.buf.MoveRight()
	}
	// Backspace over the match runes.
	for i := 0; i < m.length; i++ {
		e.buf.Backspace()
	}
	// Insert the replacement. InsertText correctly handles
	// embedded newlines even though v1 doesn't expose a way to
	// type them into the Replace field.
	e.buf.InsertText(replacement)
}

// jumpToCurrentMatch moves the editor's cursor to the start of the
// match at currentMatch. The cursor is the scroll anchor, so moving
// it is enough to bring the match into view on the next draw.
func (e *editor) jumpToCurrentMatch() {
	if e.currentMatch < 0 || e.currentMatch >= len(e.matches) {
		return
	}
	m := e.matches[e.currentMatch]
	e.buf.ClearSelection()
	curRow, _ := e.buf.Cursor()
	for curRow < m.row {
		e.buf.MoveDown()
		curRow, _ = e.buf.Cursor()
	}
	for curRow > m.row {
		e.buf.MoveUp()
		curRow, _ = e.buf.Cursor()
	}
	e.buf.MoveHome()
	for i := 0; i < m.col; i++ {
		e.buf.MoveRight()
	}
}

// matchStyleAt reports whether (row, col) falls inside any match
// range and, if so, whether it's the current (active) match. Called
// from the editor draw loop for every visible rune.
//
// Linear scan is fine here: the match list is bounded by "number of
// hits in a handful of visible lines" and the draw loop already
// does O(visible runes) work per frame. If profiles show this as a
// hot spot the matches slice can be sorted-by-row once on
// RefreshMatches and bisected here.
func (e *editor) matchStyleAt(row, col int) (isCurrent bool, inMatch bool) {
	for i, m := range e.matches {
		if m.row != row {
			continue
		}
		if col < m.col || col >= m.col+m.length {
			continue
		}
		return i == e.currentMatch, true
	}
	return false, false
}

// computeMatches returns every case-insensitive occurrence of
// needle in the buffer, walking each line independently so matches
// never cross a newline. Empty needle returns nil.
func (e *editor) computeMatches(needle string) []matchRange {
	if needle == "" {
		return nil
	}
	lowerNeedle := strings.ToLower(needle)
	needleRunes := []rune(lowerNeedle)
	var out []matchRange
	for row := 0; row < e.buf.LineCount(); row++ {
		line := e.buf.Line(row)
		lowered := make([]rune, len(line))
		for i, r := range line {
			lowered[i] = toLowerRune(r)
		}
		for col := 0; col+len(needleRunes) <= len(lowered); col++ {
			match := true
			for j, r := range needleRunes {
				if lowered[col+j] != r {
					match = false
					break
				}
			}
			if match {
				out = append(out, matchRange{
					row:    row,
					col:    col,
					length: len(needleRunes),
				})
				// Non-overlapping: advance past the match. The
				// for-loop's col++ brings us to col+len on the
				// next iteration.
				col += len(needleRunes) - 1
			}
		}
	}
	return out
}

// firstMatchAtOrAfterCursor returns the index of the first match
// whose position is at or after the current cursor. Used on
// SetSearch so the first next-match doesn't jump backwards from
// where the user was. Falls back to 0 (first match) when every hit
// is above the cursor.
func (e *editor) firstMatchAtOrAfterCursor() int {
	if len(e.matches) == 0 {
		return -1
	}
	row, col := e.buf.Cursor()
	for i, m := range e.matches {
		if m.row > row || (m.row == row && m.col >= col) {
			return i
		}
	}
	return 0
}

// toLowerRune does an ASCII-only lowercase conversion. SQL keywords
// and identifiers are ASCII in practice; dialing this up to full
// Unicode case folding (strings.ToLower per rune) would double the
// cost of computeMatches without buying anything for the target
// audience.
func toLowerRune(r rune) rune {
	if r >= 'A' && r <= 'Z' {
		return r + ('a' - 'A')
	}
	return r
}
