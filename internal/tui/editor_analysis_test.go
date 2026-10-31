package tui

import (
	"strings"
	"testing"
)

func TestSyntaxCarriesMultilineStringsAndCachesFrames(t *testing.T) {
	e := newEditor()
	e.buf.SetText("SELECT 'first\nSELECT second';")
	c, row, col := drawEditorForTest(t, e)
	if c.At(row+1, col).Style.FG != currentTheme.SQLString.FG {
		t.Fatal("multiline string continuation was not string-colored")
	}
	tokens := &e.syntax[0].tokens[0]
	drawEditorForTest(t, e)
	if tokens != &e.syntax[0].tokens[0] {
		t.Fatal("unchanged frame rebuilt syntax tokens")
	}
}
func TestSyntaxWorkerRejectsStaleRevision(t *testing.T) {
	m := newMainLayer()
	s := m.ensureActiveTab()
	e := s.editor
	e.buf.SetText(strings.Repeat("SELECT 1;\n", 1100))
	a := &app{layers: []Layer{m}, asyncCh: make(chan func(*app), 4)}
	e.scheduleSyntax(a)
	e.buf.InsertText("new")
	documentCallback(t, a)(a)
	if e.syntaxRevision == e.buf.Revision() {
		t.Fatal("stale syntax was accepted")
	}
	e.scheduleSyntax(a)
	documentCallback(t, a)(a)
	if e.syntaxRevision != e.buf.Revision() || e.syntaxBusy {
		t.Fatal("current syntax was not installed")
	}
}
func TestSearchWorkerUsesLatestNeedleAndRevision(t *testing.T) {
	m := newMainLayer()
	e := m.ensureActiveTab().editor
	e.buf.SetText(strings.Repeat("foo bar\n", 1100))
	a := &app{layers: []Layer{m}, asyncCh: make(chan func(*app), 4)}
	e.requestSearch(a, "foo")
	e.requestSearch(a, "bar")
	documentCallback(t, a)(a)
	documentCallback(t, a)(a)
	if e.searchNeedle != "bar" || e.MatchCount() != 1100 || e.searchBusy {
		t.Fatalf("needle=%q matches=%d busy=%v", e.searchNeedle, e.MatchCount(), e.searchBusy)
	}
}
func TestBackgroundReplaceAllPreservesEditsMadeWhileWorking(t *testing.T) {
	m := newMainLayer()
	e := m.ensureActiveTab().editor
	e.buf.SetText(strings.Repeat("foo\n", 1100))
	e.SetSearch("foo")
	a := &app{layers: []Layer{m}, asyncCh: make(chan func(*app), 4)}
	var result error
	e.replaceAllAsync(a, "bar", func(_ int, err error) { result = err })
	e.buf.InsertText("keep")
	documentCallback(t, a)(a)
	if result == nil || !strings.HasSuffix(e.buf.Text(), "keep") {
		t.Fatal("stale replacement overwrote newer edits")
	}
}
func TestPageNavigationAndAutoIndent(t *testing.T) {
	e := newEditor()
	e.buf.SetText(strings.Repeat("line\n", 30))
	e.buf.SetCursor(0, 0)
	e.handleInsert(nil, Key{Kind: KeyPgDn})
	if row, _ := e.buf.Cursor(); row == 0 {
		t.Fatal("PageDown did not move")
	}
	e.buf.SetText("\tSELECT 1;")
	e.handleInsert(nil, Key{Kind: KeyEnter})
	if e.buf.Text() != "\tSELECT 1;\n\t" {
		t.Fatalf("autoindent=%q", e.buf.Text())
	}
	e.buf.Undo()
	if e.buf.Text() != "\tSELECT 1;" {
		t.Fatal("autoindent did not undo as one command")
	}
}
func TestSearchCaseAndWholeWordOptions(t *testing.T) {
	e := newEditor()
	e.buf.SetText("foo Foo food")
	e.searchMatchCase = true
	e.searchWholeWord = true
	e.SetSearch("foo")
	if e.MatchCount() != 1 {
		t.Fatalf("case/word search found %d", e.MatchCount())
	}
	e.searchMatchCase = false
	e.SetSearch("foo")
	if e.MatchCount() != 2 {
		t.Fatalf("folded word search found %d", e.MatchCount())
	}
}

func TestBackgroundCompletionRejectsNewerText(t *testing.T) {
	m := newMainLayer()
	e := m.ensureActiveTab().editor
	m.focus = FocusQuery
	e.buf.ResetText(strings.Repeat("-- padding\n", 14000) + "sel")
	a := &app{layers: []Layer{m}, asyncCh: make(chan func(*app), 4)}
	e.openCompletion(a)
	if !e.completionBusy {
		t.Fatal("large completion ran on UI goroutine")
	}
	e.buf.InsertText("ect")
	documentCallback(t, a)(a)
	if e.complete != nil || e.completionBusy {
		t.Fatal("stale completion was installed")
	}
}

func TestBackgroundFormatPreservesNewerText(t *testing.T) {
	m := newMainLayer()
	e := m.ensureActiveTab().editor
	e.buf.ResetText("select 1;")
	a := &app{layers: []Layer{m}, asyncCh: make(chan func(*app), 4)}
	m.formatQuery(a)
	e.buf.InsertText(" -- keep")
	documentCallback(t, a)(a)
	if e.buf.Text() != "select 1; -- keep" {
		t.Fatal("format overwrote newer edits")
	}
}
