package tui

import "strings"

// SetSearch installs a new needle, recomputes matches, snaps
// currentMatch to the first hit at/after the cursor. Empty clears.
func (e *editor) SetSearch(needle string) {
	if needle == "" {
		e.ClearSearch()
		return
	}
	e.searchNeedle = needle
	e.matches = e.computeMatches(needle)
	e.currentMatch = e.firstMatchAtOrAfterCursor()
}

// RefreshMatches rebuilds matches against the current needle
// after a buffer edit. Clamps currentMatch.
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

func (e *editor) ClearSearch() {
	e.searchNeedle = ""
	e.matches = nil
	e.currentMatch = -1
}

func (e *editor) HasSearch() bool { return e.searchNeedle != "" }
func (e *editor) MatchCount() int { return len(e.matches) }

// CurrentMatchIndex returns 1-based index, or 0 when none.
func (e *editor) CurrentMatchIndex() int {
	if e.currentMatch < 0 || len(e.matches) == 0 {
		return 0
	}
	return e.currentMatch + 1
}

// NextMatch advances with wrap-around.
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

// PrevMatch steps back with wrap-around.
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

// ReplaceCurrent replaces the active match, rebuilds, advances.
func (e *editor) ReplaceCurrent(replacement string) bool {
	if e.currentMatch < 0 || e.currentMatch >= len(e.matches) {
		return false
	}
	m := e.matches[e.currentMatch]
	e.applyReplaceAt(m, replacement)
	e.RefreshMatches()
	if len(e.matches) > 0 {
		if e.currentMatch >= len(e.matches) {
			e.currentMatch = 0
		}
		e.jumpToCurrentMatch()
	}
	return true
}

// ReplaceAll walks matches in reverse so earlier positions stay
// valid under shortening replacements.
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

// applyReplaceAt positions cursor at match end, backspaces match
// runes, inserts replacement. All through public buf API so undo
// snapshots stay consistent.
func (e *editor) applyReplaceAt(m matchRange, replacement string) {
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
	for i := 0; i < m.col+m.length; i++ {
		e.buf.MoveRight()
	}
	for i := 0; i < m.length; i++ {
		e.buf.Backspace()
	}
	e.buf.InsertText(replacement)
	e.ClearErrorLocation()
}

// jumpToCurrentMatch moves the cursor to the match start; scroll
// follows the cursor on the next draw.
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

// matchStyleAt reports whether (row, col) falls in a match and
// whether that match is the current one. Linear scan; match list
// is bounded by visible hits.
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

// computeMatches returns non-overlapping case-insensitive matches
// per line. Matches don't cross newlines.
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
				col += len(needleRunes) - 1
			}
		}
	}
	return out
}

// firstMatchAtOrAfterCursor returns the index of the first match
// at/after the cursor so the first NextMatch doesn't jump back.
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

// toLowerRune does ASCII-only folding. SQL identifiers are ASCII
// in practice; full Unicode folding would double the cost.
func toLowerRune(r rune) rune {
	if r >= 'A' && r <= 'Z' {
		return r + ('a' - 'A')
	}
	return r
}
