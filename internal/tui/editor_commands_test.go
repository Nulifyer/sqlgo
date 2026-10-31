package tui

import (
	"context"
	"strings"
	"testing"
)

func keyCtrl(e *editor, r rune) { e.handleInsert(nil, Key{Kind: KeyRune, Rune: r, Ctrl: true}) }
func TestNextOccurrenceSameLineEditsAndUndoRestoreSelections(t *testing.T) {
	e := newEditor()
	e.buf.ResetText("foo foo\nfoo")
	e.buf.SetCursor(0, 0)
	keyCtrl(e, 'd')
	keyCtrl(e, 'd')
	keyCtrl(e, 'd')
	keyCtrl(e, 'd')
	if len(e.extraCursors) != 2 {
		t.Fatalf("selection count=%d", len(e.extraCursors)+1)
	}
	e.handleInsert(nil, Key{Kind: KeyRune, Rune: 'x'})
	if e.buf.Text() != "x x\nx" {
		t.Fatalf("replacement=%q", e.buf.Text())
	}
	keyCtrl(e, 'z')
	if e.buf.Text() != "foo foo\nfoo" || len(e.extraCursors) != 2 {
		t.Fatal("undo lost text or selection set")
	}
	for _, selection := range e.selections() {
		start, end := cursorRange(selection)
		if e.buf.rangeText(start, end) != "foo" {
			t.Fatalf("selection lost: %+v", selection)
		}
	}
	keyCtrl(e, 'y')
	if e.buf.Text() != "x x\nx" {
		t.Fatal("redo did not restore transaction")
	}
}
func TestCursorUndoRedoRestoresSelectionAndScrollWithoutTextEdits(t *testing.T) {
	e := newEditor()
	e.buf.ResetText("one two")
	e.buf.SetCursor(0, 0)
	e.scrollRow, e.scrollCol = 0, 0
	e.handleInsert(nil, Key{Kind: KeyRight, Shift: true})
	keyCtrl(e, 'd')
	before := e.navigation()
	e.handleInsert(nil, Key{Kind: KeyEnd})
	keyCtrl(e, 'u')
	if !sameNavigation(before, e.navigation()) || e.buf.Text() != "one two" {
		t.Fatal("cursor undo changed text or failed to restore selections")
	}
	e.handleInsert(nil, Key{Kind: KeyRune, Rune: 'u', Alt: true})
	if _, col := e.buf.Cursor(); col != 7 {
		t.Fatalf("cursor redo col=%d", col)
	}
	e.handleInsert(nil, Key{Kind: KeyRune, Rune: '!'})
	keyCtrl(e, 'u')
	if e.buf.Text() != "one two!" || len(e.cursorUndo) != 0 {
		t.Fatal("cursor history survived content edit")
	}
}
func TestSurroundSelectedOccurrencesPreservesInnerSelection(t *testing.T) {
	e := newEditor()
	e.buf.ResetText("foo foo")
	e.buf.SetCursor(0, 0)
	keyCtrl(e, 'd')
	keyCtrl(e, 'd')
	e.handleInsert(nil, Key{Kind: KeyRune, Rune: '('})
	if e.buf.Text() != "(foo) (foo)" {
		t.Fatalf("surround=%q", e.buf.Text())
	}
	for _, s := range e.selections() {
		a, z := cursorRange(s)
		if e.buf.rangeText(a, z) != "foo" {
			t.Fatalf("inner selection=%+v", s)
		}
	}
	keyCtrl(e, 'z')
	if e.buf.Text() != "foo foo" {
		t.Fatal("surround undo split")
	}
}
func TestSQLCommentToggleHandlesIndentAndSelectedLineBoundary(t *testing.T) {
	e := newEditor()
	e.buf.ResetText("  SELECT 1;\n\tFROM t;\nkeep")
	e.buf.SetAnchor(0, 0)
	e.buf.SetCursor(2, 0)
	e.handleInsert(nil, Key{Kind: KeyRune, Rune: '/', Alt: true})
	if e.buf.Text() != "  -- SELECT 1;\n\t-- FROM t;\nkeep" {
		t.Fatalf("comment=%q", e.buf.Text())
	}
	e.handleInsert(nil, Key{Kind: KeyRune, Rune: '_', Ctrl: true})
	if e.buf.Text() != "  SELECT 1;\n\tFROM t;\nkeep" {
		t.Fatalf("uncomment=%q", e.buf.Text())
	}
	keyCtrl(e, 'z')
	if !strings.Contains(e.buf.Text(), "-- SELECT") {
		t.Fatal("toggle undo failed")
	}
}
func TestIncrementalSyntaxReusesStableSuffixAndCancels(t *testing.T) {
	text := strings.Repeat("SELECT 1;\n", 2000)
	old := buildEditorSyntax(text)
	updated := strings.Replace(text, "SELECT 1;", "SELECT 2;", 1)
	syntax, count := updateEditorSyntax(context.Background(), updated, old, 0, 0)
	if count != 1 || &syntax[100].tokens[0] != &old[100].tokens[0] {
		t.Fatalf("unchanged suffix rebuilt, lines=%d", count)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if syntax, _ := updateEditorSyntax(ctx, text, nil, 0, 0); syntax != nil {
		t.Fatal("cancelled analysis ran")
	}
}
func TestSavedIdentityDoesNotSerializeAndSurvivesUndoRedo(t *testing.T) {
	s := newSession()
	b := s.editor.buf
	b.ResetText("abc")
	s.setSavedIdentity("abc", b.stateID)
	b.breakUndoGroup()
	b.Insert('x')
	b.textCached = false
	if !s.IsDirty() || b.textCached {
		t.Fatal("dirty check serialized text")
	}
	b.Undo()
	if s.IsDirty() {
		t.Fatal("undo to save point stayed dirty")
	}
	b.Redo()
	if !s.IsDirty() {
		t.Fatal("redo lost dirty identity")
	}
}
