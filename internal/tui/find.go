package tui

import (
	"sort"
	"unicode"
)

type indexedMatch struct {
	matchRange
	index int
}

// SetSearch installs a new needle, recomputes matches, snaps
// currentMatch to the first hit at/after the cursor. Empty clears.
func (e *editor) SetSearch(needle string) {
	if needle == "" {
		e.ClearSearch()
		return
	}
	e.searchNeedle = needle
	e.lastSearch = needle
	e.searchGeneration++
	e.matches = e.computeMatches(needle)
	e.indexMatches()
	e.searchRevision = e.buf.Revision()
	e.currentMatch = e.firstMatchAtOrAfterCursor()
}

// RefreshMatches rebuilds matches against the current needle
// after a buffer edit. Clamps currentMatch.
func (e *editor) RefreshMatches() {
	if e.searchNeedle == "" {
		return
	}
	e.matches = e.computeMatches(e.searchNeedle)
	e.indexMatches()
	e.searchRevision = e.buf.Revision()
	if len(e.matches) == 0 {
		e.currentMatch = -1
		return
	}
	if e.currentMatch < 0 || e.currentMatch >= len(e.matches) {
		e.currentMatch = 0
	}
}

func (e *editor) ClearSearch() {
	e.searchGeneration++
	e.matchRows = nil
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
	if e.searchRevision != e.buf.Revision() {
		return false
	}
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
	if e.searchRevision != e.buf.Revision() {
		return 0
	}
	if len(e.matches) == 0 {
		return 0
	}
	n := len(e.matches)
	e.buf.Transaction(func() {
		for i := n - 1; i >= 0; i-- {
			e.applyReplaceAt(e.matches[i], replacement)
		}
	})
	e.RefreshMatches()
	if len(e.matches) > 0 {
		e.currentMatch = 0
		e.jumpToCurrentMatch()
	} else {
		e.currentMatch = -1
	}
	return n
}

// applyReplaceAt replaces one match as a single range edit.
func (e *editor) applyReplaceAt(m matchRange, replacement string) {
	e.buf.ReplaceRange(m.row, m.col, m.row, m.col+m.length, replacement)
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
	e.buf.SetCursor(m.row, m.col)
}

// indexMatches groups sorted matches by row for bounded rendering lookups.
func (e *editor) indexMatches() {
	e.matchRows = make(map[int][]indexedMatch)
	for index, m := range e.matches {
		e.matchRows[m.row] = append(e.matchRows[m.row], indexedMatch{m, index})
	}
}
func (e *editor) matchStyleAt(row, col int) (bool, bool) {
	matches := e.matchRows[row]
	i := sort.Search(len(matches), func(i int) bool { return matches[i].col+matches[i].length > col })
	if i < len(matches) && col >= matches[i].col {
		return matches[i].index == e.currentMatch, true
	}
	return false, false
}

// computeMatches returns non-overlapping case-insensitive matches
// per line. Matches don't cross newlines.
func (e *editor) computeMatches(needle string) []matchRange {
	return findEditorMatches(e.buf.lines, needle, e.searchMatchCase, e.searchWholeWord)
}
func findEditorMatches(lines [][]rune, needle string, matchCase, wholeWord bool) []matchRange {
	if needle == "" {
		return nil
	}
	needleRunes := []rune(needle)
	for i, r := range needleRunes {
		if !matchCase {
			needleRunes[i] = toLowerRune(r)
		}
	}
	var out []matchRange
	for row, line := range lines {
		lowered := make([]rune, len(line))
		for i, r := range line {
			lowered[i] = r
			if !matchCase {
				lowered[i] = toLowerRune(r)
			}
		}
		for col := 0; col+len(needleRunes) <= len(lowered); col++ {
			match := true
			for j, r := range needleRunes {
				if lowered[col+j] != r {
					match = false
					break
				}
			}
			if match && wholeWord {
				match = (col == 0 || !isWordChar(line[col-1])) && (col+len(needleRunes) == len(line) || !isWordChar(line[col+len(needleRunes)]))
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

// toLowerRune applies Unicode simple lowercase mapping on both search operands.
func toLowerRune(r rune) rune { return unicode.ToLower(r) }
