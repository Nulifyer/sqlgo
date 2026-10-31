package tui

import (
	"context"
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
	if e.searchCancel != nil {
		e.searchCancel()
	}
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
	if e.searchCancel != nil {
		e.searchCancel()
	}
	e.searchLimited = false
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
	if e.searchRevision != e.buf.Revision() || e.searchLimited {
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
	before := e.navigation()
	e.collapseCursors()
	m := e.matches[e.currentMatch]
	e.buf.ClearSelection()
	e.buf.SetCursor(m.row, m.col)
	e.recordNavigation(before)
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
const maxEditorSearchMatches = 20000

func (e *editor) computeMatches(needle string) []matchRange {
	matches, limited := scanEditorMatches(context.Background(), e.buf.lines, needle, e.searchMatchCase, e.searchWholeWord)
	e.searchLimited = limited
	return matches
}

// KMP keeps searches linear even for repeated prefixes on very long lines.
// Fold runes as they are visited rather than allocating a second document.
func scanEditorMatches(ctx context.Context, lines [][]rune, needle string, matchCase, wholeWord bool) ([]matchRange, bool) {
	if needle == "" {
		return nil, false
	}
	fold := func(r rune) rune {
		if matchCase {
			return r
		}
		return toLowerRune(r)
	}
	pattern := []rune(needle)
	prefix := make([]int, len(pattern))
	for i := range pattern {
		pattern[i] = fold(pattern[i])
	}
	for i, j := 1, 0; i < len(pattern); i++ {
		if i%4096 == 0 && ctx.Err() != nil {
			return nil, false
		}
		for j > 0 && pattern[i] != pattern[j] {
			j = prefix[j-1]
		}
		if pattern[i] == pattern[j] {
			j++
		}
		prefix[i] = j
	}
	var out []matchRange
	for row, line := range lines {
		if ctx.Err() != nil {
			return nil, false
		}
		j := 0
		for i, r := range line {
			if i%4096 == 0 && ctx.Err() != nil {
				return nil, false
			}
			r = fold(r)
			for j > 0 && r != pattern[j] {
				j = prefix[j-1]
			}
			if r == pattern[j] {
				j++
			}
			if j != len(pattern) {
				continue
			}
			col := i + 1 - j
			if wholeWord && ((col > 0 && isWordChar(line[col-1])) || (i+1 < len(line) && isWordChar(line[i+1]))) {
				j = prefix[j-1]
				continue
			}
			if len(out) == maxEditorSearchMatches {
				return out, true
			}
			out = append(out, matchRange{row: row, col: col, length: j})
			j = 0 // accepted matches do not overlap
		}
	}
	return out, false
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
