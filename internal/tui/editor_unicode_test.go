package tui

import (
	"testing"
)

func TestEditorCombiningMarksMustRender(t *testing.T) {
	e := newEditor()
	e.buf.SetText("e\u0301")
	c, row, col := drawEditorForTest(t, e)
	cell := c.At(row, col)
	if string(cell.Combining) != "\u0301" {
		t.Fatalf("text=%q rendered base=%q combining=%q", e.buf.Text(), cell.R, string(cell.Combining))
	}
}
func TestEditorBackspaceShouldDeleteWholeEmoji(t *testing.T) {
	e := newEditor()
	e.buf.SetText("👩‍💻")
	e.handleInsert(nil, Key{Kind: KeyBackspace})
	if e.buf.Text() != "" {
		t.Fatalf("one Backspace left partial grapheme %q", e.buf.Text())
	}
}
func TestEditorVerticalMovementShouldKeepPreferredColumn(t *testing.T) {
	e := newEditor()
	e.buf.SetText("abcdefghij\nx\nabcdefghij")
	e.buf.SetCursor(0, 8)
	e.handleInsert(nil, Key{Kind: KeyDown})
	e.handleInsert(nil, Key{Kind: KeyDown})
	r, c := e.buf.Cursor()
	if r != 2 || c != 8 {
		t.Fatalf("long/short/long moved to %d/%d, want 2/8", r, c)
	}
}
func TestEditorLeftShouldCollapseToSelectionStart(t *testing.T) {
	e := newEditor()
	e.buf.SetText("abcdef")
	e.buf.SetAnchor(0, 1)
	e.buf.SetCursor(0, 5)
	e.handleInsert(nil, Key{Kind: KeyLeft})
	_, c := e.buf.Cursor()
	if c != 1 {
		t.Fatalf("Left over selection [1,5) landed at col=%d", c)
	}
}
func TestEditorUnicodeSearchMustFindIdenticalText(t *testing.T) {
	e := newEditor()
	e.buf.SetText("Ä")
	e.SetSearch("Ä")
	if e.MatchCount() != 1 {
		t.Fatalf("search for identical Ä found %d matches", e.MatchCount())
	}
}
func TestEditorTabShouldUseVisibleColumn(t *testing.T) {
	e := newEditor()
	e.buf.SetText("\t")
	e.handleInsert(nil, Key{Kind: KeyTab})
	_, c := e.buf.Cursor()
	visual := visualColumnForRune(e.buf.Line(0), c)
	if visual != 8 {
		t.Fatalf("Tab after a literal tab ended at visual col %d, want 8", visual)
	}
}
func TestEditorUnicodeTabHitMustUseDisplayWidth(t *testing.T) {
	m := newMainLayer()
	m.ensureActiveTab().title = "漢字"
	m.editor.buf.SetText("")
	m.newTab()
	r := rect{Row: 1, Col: 1, W: 80, H: 1}
	hits := m.queryTabHits(r)
	label := queryTabLabel(m.sessions[0], false, m.sessionNeedsDatabase(m.sessions[0]))
	want := r.Col + stringDisplayWidth(label)
	if len(hits) > 1 && hits[1].startCol != want {
		t.Fatalf("second tab starts at %d, first label display ends at %d; byte-len=%d cell-width=%d", hits[1].startCol, want, len(label), stringDisplayWidth(label))
	}
}

func TestAddedCursorUsesVisualColumnAndWholeGrapheme(t *testing.T) {
	e := newEditor()
	e.buf.ResetText("abcde\n\t👩‍💻x")
	e.buf.SetCursor(0, 5)
	e.addCursorRelative(1)
	if len(e.extraCursors) != 1 || e.extraCursors[0].col != 1 {
		t.Fatalf("cursor inside emoji instead of at its leading cell: %+v", e.extraCursors)
	}
	e.applyToAllCursors(func() { e.buf.InsertText("!") })
	if e.buf.Text() != "abcde!\n\t!👩‍💻x" {
		t.Fatalf("column edit split grapheme: %q", e.buf.Text())
	}
}
