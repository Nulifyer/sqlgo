package tui

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type failedEditorClipboard struct{}

func (failedEditorClipboard) Copy(string) error      { return errors.New("clipboard unavailable") }
func (failedEditorClipboard) Paste() (string, error) { return "", errors.New("clipboard unavailable") }
func TestEditorCutMustPreserveTextOnClipboardFailure(t *testing.T) {
	e := newEditor()
	e.buf.SetText("valuable SQL")
	e.buf.SelectAll()
	e.handleInsert(&app{clipboard: failedEditorClipboard{}}, Key{Kind: KeyRune, Rune: 'x', Ctrl: true})
	if e.buf.Text() != "valuable SQL" {
		t.Fatalf("clipboard failed; document became %q", e.buf.Text())
	}
}
func TestEditorOpenMustProtectDirtyTab(t *testing.T) {
	m := newMainLayer()
	s := m.ensureActiveTab()
	s.editor.buf.SetText("unsaved work")
	a := &app{layers: []Layer{m}}
	path := filepath.Join(t.TempDir(), "other.sql")
	os.WriteFile(path, []byte("SELECT 2;"), 0600)
	ol := newOpenLayer(a, path)
	a.layers = append(a.layers, ol)
	ol.load(a)
	if len(m.sessions) == 1 && s.editor.buf.Text() != "unsaved work" {
		t.Fatalf("dirty tab replaced without prompt; text=%q dirty=%v layers=%d", s.editor.buf.Text(), s.IsDirty(), len(a.layers))
	}
}
func TestEditorLoadMustNotReuseAnotherDocumentsUndo(t *testing.T) {
	s := newSession()
	s.editor.buf.SetText("file A")
	d := documentService{}
	d.ApplyLoaded(s, loadedDocument{Path: filepath.Join(t.TempDir(), "b.sql"), Text: "file B"})
	if s.editor.buf.Undo() {
		t.Fatalf("undo restored %q under path %q", s.editor.buf.Text(), s.sourcePath)
	}
}
func TestEditorClearMustNotLeaveInvalidCursors(t *testing.T) {
	m := newMainLayer()
	m.ensureActiveTab()
	e := m.editor
	e.buf.SetText("a\nb")
	e.buf.SetCursor(0, 1)
	e.addCursorRelative(1)
	a := &app{layers: []Layer{m}}
	m.HandleKey(a, Key{Kind: KeyRune, Rune: 'l', Ctrl: true})
	defer func() {
		if v := recover(); v != nil {
			t.Fatalf("clear then draw panicked: %v", v)
		}
	}()
	e.draw(newCellbuf(40, 8), rect{Row: 1, Col: 1, W: 40, H: 8}, true)
}
func TestEditorTerminalPasteShouldCollapseStaleCursors(t *testing.T) {
	m := newMainLayer()
	m.ensureActiveTab()
	e := m.editor
	e.buf.SetText("a\nb")
	e.buf.SetCursor(0, 1)
	e.addCursorRelative(1)
	a := &app{layers: []Layer{m}}
	m.HandleInput(a, PasteMsg{Text: "\nnew\n"})
	if e.hasMultiCursor() {
		t.Fatalf("terminal paste retained old extra positions %+v", e.extraCursors)
	}
}
func TestEditorMultiCursorHorizontalMoveMustKeepRowInvariant(t *testing.T) {
	e := newEditor()
	e.buf.SetText("a\nb")
	e.buf.SetCursor(0, 1)
	e.addCursorRelative(1)
	e.handleInsert(nil, Key{Kind: KeyRight})
	r, _ := e.buf.Cursor()
	for _, c := range e.extraCursors {
		if r == c.row {
			t.Fatalf("Right across EOL put primary and secondary on row %d", r)
		}
	}
}
