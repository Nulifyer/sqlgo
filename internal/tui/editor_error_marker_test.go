package tui

import (
	"context"
	"fmt"
	"testing"
)

func drawEditorForTest(t *testing.T, e *editor) (*cellbuf, int, int) {
	t.Helper()
	buf := newCellbuf(40, 8)
	e.draw(buf, rect{Row: 1, Col: 1, W: 40, H: 8}, false)
	gutter := e.gutterWidth()
	if gutter >= 38 {
		gutter = 0
	}
	return buf, 2, 2 + gutter
}

func mustCellAt(t *testing.T, buf *cellbuf, row, col int) *cell {
	t.Helper()
	c := buf.At(row, col)
	if c == nil {
		t.Fatalf("cell (%d,%d) out of bounds", row, col)
	}
	return c
}

func TestEditorDrawErrorLineMarker(t *testing.T) {
	t.Parallel()
	e := seedEditor("SELECT 1", "FROM widgets")
	e.SetErrorLocation(2, 0)

	buf, innerRow, _ := drawEditorForTest(t, e)
	lineRow := innerRow + 1

	if got := mustCellAt(t, buf, lineRow, 3).R; got != '2' {
		t.Fatalf("gutter line number = %q, want '2'", got)
	}
	if got := mustCellAt(t, buf, lineRow, 4).R; got != '!' {
		t.Fatalf("gutter marker = %q, want '!'", got)
	}
	if got := mustCellAt(t, buf, lineRow, 3).Style.FG; got != currentTheme.EditorError.FG {
		t.Fatalf("gutter line number fg = %d, want %d", got, currentTheme.EditorError.FG)
	}
}

func TestEditorDrawErrorTokenHighlightsWholeTokenInRed(t *testing.T) {
	t.Parallel()
	e := seedEditor("SELECT widgets FROM demo")
	e.SetErrorLocation(1, 9)

	buf, innerRow, bodyCol := drawEditorForTest(t, e)
	lineRow := innerRow
	for off := 7; off <= 13; off++ {
		if got := mustCellAt(t, buf, lineRow, bodyCol+off).Style.FG; got != currentTheme.EditorError.FG {
			t.Fatalf("token cell %d fg = %d, want %d", off, got, currentTheme.EditorError.FG)
		}
	}
	if got := mustCellAt(t, buf, lineRow, bodyCol+6).Style.FG; got == currentTheme.EditorError.FG {
		t.Fatalf("leading space fg = %d, want non-error color", got)
	}
	if got := mustCellAt(t, buf, lineRow, bodyCol+14).Style.FG; got == currentTheme.EditorError.FG {
		t.Fatalf("trailing space fg = %d, want non-error color", got)
	}
}

func TestEditorDrawErrorHighlightKeepsTokenAttrs(t *testing.T) {
	t.Parallel()
	e := seedEditor("SELECT 1")
	e.SetErrorLocation(1, 2)

	buf, innerRow, bodyCol := drawEditorForTest(t, e)
	c := mustCellAt(t, buf, innerRow, bodyCol)
	if c.Style.FG != currentTheme.EditorError.FG {
		t.Fatalf("fg = %d, want error fg %d", c.Style.FG, currentTheme.EditorError.FG)
	}
	if c.Style.Attrs&attrBold == 0 {
		t.Fatalf("attrs = %v, want keyword bold preserved", c.Style.Attrs)
	}
}

func TestEditorDrawWhitespaceFallbackUsesSingleColumn(t *testing.T) {
	t.Parallel()
	e := seedEditor("SELECT  FROM widgets")
	e.SetErrorLocation(1, 7)

	buf, innerRow, bodyCol := drawEditorForTest(t, e)
	cell := mustCellAt(t, buf, innerRow, bodyCol+6)
	if cell.Style.FG != currentTheme.EditorError.FG {
		t.Fatalf("fallback fg = %d, want %d", cell.Style.FG, currentTheme.EditorError.FG)
	}
	if cell.Style.Attrs&attrReverse == 0 {
		t.Fatalf("fallback attrs = %v, want reverse for visibility", cell.Style.Attrs)
	}
	if got := mustCellAt(t, buf, innerRow, bodyCol+7).Style.FG; got == currentTheme.EditorError.FG {
		t.Fatalf("adjacent whitespace fg = %d, want non-error color", got)
	}
}

func TestEditorDrawBlankFallbackUsesSingleColumn(t *testing.T) {
	t.Parallel()
	e := seedEditor("SELECT")
	e.SetErrorLocation(1, 7)

	buf, innerRow, bodyCol := drawEditorForTest(t, e)
	cell := mustCellAt(t, buf, innerRow, bodyCol+6)
	if cell.Style.FG != currentTheme.EditorError.FG {
		t.Fatalf("blank fallback fg = %d, want %d", cell.Style.FG, currentTheme.EditorError.FG)
	}
	if cell.Style.Attrs&attrReverse == 0 {
		t.Fatalf("blank fallback attrs = %v, want reverse", cell.Style.Attrs)
	}
}

func TestEditorDrawLineOnlyErrorOnlyMarksGutter(t *testing.T) {
	t.Parallel()
	e := seedEditor("SELECT 1")
	e.SetErrorLocation(1, 0)

	buf, innerRow, bodyCol := drawEditorForTest(t, e)
	if got := mustCellAt(t, buf, innerRow, 4).R; got != '!' {
		t.Fatalf("gutter marker = %q, want '!'", got)
	}
	for off := 0; off < len("SELECT 1"); off++ {
		if got := mustCellAt(t, buf, innerRow, bodyCol+off).Style.FG; got == currentTheme.EditorError.FG {
			t.Fatalf("body cell %d unexpectedly highlighted in error color", off)
		}
	}
}

func TestEditorEditClearsErrorMarker(t *testing.T) {
	t.Parallel()
	e := seedEditor("SELECT 1")
	e.SetErrorLocation(1, 1)

	e.handleInsert(nil, Key{Kind: KeyRune, Rune: ';'})

	if e.hasErrorLocation() {
		t.Fatal("error marker should clear after buffer edit")
	}
}

func TestHandleQueryEventClearsEditorMarkerOnSuccessAndCancel(t *testing.T) {
	t.Parallel()
	a := &app{}
	a.layers = []Layer{newMainLayer()}
	sess := a.mainLayerPtr().ensureActiveTab()

	sess.editor.SetErrorLocation(1, 1)
	a.handleQueryEvent(queryEvent{kind: evtResultSetDone, sess: sess, tab: sess.resultTab})
	if sess.editor.hasErrorLocation() {
		t.Fatal("success should clear editor marker")
	}

	sess.editor.SetErrorLocation(1, 1)
	a.handleQueryEvent(queryEvent{kind: evtResultSetDone, sess: sess, tab: sess.resultTab, err: context.Canceled})
	if sess.editor.hasErrorLocation() {
		t.Fatal("cancel should clear editor marker")
	}
}

func TestEditorErrorMarkerIsScopedPerTab(t *testing.T) {
	t.Parallel()
	m := newMainLayer()
	first := m.ensureActiveTab()
	first.editor.SetErrorLocation(1, 3)

	m.newTab()
	if m.session.editor.hasErrorLocation() {
		t.Fatal("new tab should not inherit another tab's error marker")
	}

	m.switchTab(0)
	if !m.session.editor.hasErrorLocation() {
		t.Fatal("original tab lost its error marker after tab switch")
	}
	if m.session.editor.errLine != 1 || m.session.editor.errCol != 3 {
		t.Fatalf("marker = (%d,%d), want (1,3)", m.session.editor.errLine, m.session.editor.errCol)
	}
}

func TestEditorDrawOffscreenErrorDoesNotMoveViewport(t *testing.T) {
	t.Parallel()
	lines := make([]string, 12)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	e := seedEditor(lines...)
	e.buf.SetCursor(10, 0)
	e.scrollRow = 9
	e.SetErrorLocation(1, 1)

	drawEditorForTest(t, e)

	if e.scrollRow != 9 {
		t.Fatalf("scrollRow = %d, want 9", e.scrollRow)
	}
}
