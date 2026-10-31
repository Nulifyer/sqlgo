package tui

import (
	"context"
	"fmt"
	"strings"
)

func (e *editor) requestSearch(a *app, needle string) {
	if needle == "" {
		e.ClearSearch()
		return
	}
	if a == nil || a.asyncCh == nil || !e.largeAnalysis() {
		e.SetSearch(needle)
		return
	}
	e.searchNeedle, e.lastSearch = needle, needle
	e.searchGeneration++
	if e.searchCancel != nil {
		e.searchCancel()
	}
	e.matches = nil
	e.matchRows = nil
	e.currentMatch = -1
	if e.searchBusy {
		return
	}
	e.searchBusy = true
	ctx, cancel := context.WithCancel(context.Background())
	e.searchCancel = cancel
	rev, gen := e.buf.Revision(), e.searchGeneration
	text := e.buf.Text()
	matchCase, wholeWord := e.searchMatchCase, e.searchWholeWord
	a.runBackground(func() func(*app) {
		defer cancel()
		matches, limited := scanEditorMatches(ctx, splitBufferText(text), needle, matchCase, wholeWord)
		return func(a *app) {
			e.searchBusy = false
			e.searchCancel = nil
			if !e.HasSearch() {
				return
			}
			if e.searchGeneration != gen || e.buf.Revision() != rev {
				e.requestSearch(a, e.searchNeedle)
				return
			}
			e.matches = matches
			e.searchLimited = limited
			e.indexMatches()
			e.searchRevision = rev
			e.currentMatch = e.firstMatchAtOrAfterCursor()
		}
	})
}
func (e *editor) replaceAllAsync(a *app, replacement string, done func(int, error)) {
	if e.replaceBusy {
		done(0, fmt.Errorf("replacement already in progress; editing remains available"))
		return
	}
	e.replaceBusy = true
	text := e.buf.Text()
	rev := e.buf.Revision()
	needle := e.searchNeedle
	matchCase, wholeWord := e.searchMatchCase, e.searchWholeWord
	a.runBackground(func() func(*app) {
		lines := splitBufferText(text)
		matches, limited := scanEditorMatches(context.Background(), lines, needle, matchCase, wholeWord)
		if limited {
			return func(a *app) {
				e.replaceBusy = false
				done(0, fmt.Errorf("more than %d matches; narrow the search before replacing all", maxEditorSearchMatches))
			}
		}
		var out strings.Builder
		index := 0
		for row, line := range lines {
			if row > 0 {
				out.WriteByte('\n')
			}
			start := 0
			for index < len(matches) && matches[index].row == row {
				m := matches[index]
				out.WriteString(string(line[start:m.col]))
				out.WriteString(replacement)
				start = m.col + m.length
				index++
			}
			out.WriteString(string(line[start:]))
		}
		result := out.String()
		prepared := splitBufferText(result)
		return func(a *app) {
			e.replaceBusy = false
			if e.buf.Revision() != rev {
				done(0, fmt.Errorf("document changed; replace again"))
				return
			}
			e.collapseCursors()
			e.complete = nil
			e.completionGeneration++
			e.buf.SetPreparedText(result, prepared)
			e.ClearErrorLocation()
			e.requestSearch(a, needle)
			done(len(matches), nil)
		}
	})
}

func (e *editor) replaceCurrentInUI(a *app, replacement string) bool {
	if !e.largeAnalysis() || a.asyncCh == nil {
		return e.ReplaceCurrent(replacement)
	}
	if e.searchRevision != e.buf.Revision() || e.currentMatch < 0 || e.currentMatch >= len(e.matches) {
		return false
	}
	e.applyReplaceAt(e.matches[e.currentMatch], replacement)
	e.requestSearch(a, e.searchNeedle)
	return true
}
