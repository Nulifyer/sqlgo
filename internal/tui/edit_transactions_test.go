package tui

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestEditorSaveMustDetectExternalChanges(t *testing.T) {
	d := documentService{}
	path := filepath.Join(t.TempDir(), "query.sql")
	os.WriteFile(path, []byte("original"), 0600)
	doc, _ := d.Load(path)
	s := newSession()
	d.ApplyLoaded(s, doc)
	s.editor.buf.InsertText(" local")
	os.WriteFile(path, []byte("external changes"), 0600)
	_, err := d.Save(s, path)
	data, _ := os.ReadFile(path)
	if err == nil {
		t.Fatalf("save silently overwrote external contents with %q", data)
	}
}
func TestEditorUTF16DocumentMustDecodeOrReject(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unicode.sql")
	raw := []byte{0xff, 0xfe, 'S', 0, 'E', 0, 'L', 0, 'E', 0, 'C', 0, 'T', 0, ' ', 0, '1', 0, ';', 0}
	os.WriteFile(path, raw, 0600)
	d := documentService{}
	doc, err := d.Load(path)
	if err != nil {
		return
	}
	s := newSession()
	d.ApplyLoaded(s, doc)
	if s.editor.buf.Text() != "SELECT 1;" {
		t.Fatalf("UTF-16 accepted as raw text %q", s.editor.buf.Text())
	}
}
func TestEditorInvalidUTF8DocumentMustNotSilentlyRewriteBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.sql")
	raw := []byte{'a', 0xff, 'b'}
	os.WriteFile(path, raw, 0600)
	d := documentService{}
	doc, err := d.Load(path)
	if err != nil {
		return
	}
	s := newSession()
	d.ApplyLoaded(s, doc)
	d.Save(s, path)
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, raw) {
		t.Fatalf("open/save silently changed bytes %x -> %x", raw, got)
	}
}
func TestEditorMultiCursorEditShouldUndoAtomically(t *testing.T) {
	e := newEditor()
	e.buf.SetText("foo\nbar")
	e.buf.SetCursor(0, 3)
	e.addCursorRelative(1)
	e.handleInsert(nil, Key{Kind: KeyRune, Rune: 'X'})
	e.handleInsert(nil, Key{Kind: KeyRune, Rune: 'z', Ctrl: true})
	if e.buf.Text() != "foo\nbar" {
		t.Fatalf("one undo left partial multi-cursor edit %q", e.buf.Text())
	}
}
func TestEditorCompletionShouldUndoToPrefix(t *testing.T) {
	e := newEditor()
	e.buf.SetText("sel")
	e.complete = &completionState{startCol: 0, items: []completionItem{{text: "select"}}}
	e.acceptCompletion()
	e.buf.Undo()
	if e.buf.Text() != "sel" {
		t.Fatalf("one undo after completion yielded %q instead of sel", e.buf.Text())
	}
}
func TestEditorReplaceAllShouldUndoAsOneCommand(t *testing.T) {
	e := newEditor()
	e.buf.SetText("foo foo")
	e.SetSearch("foo")
	e.ReplaceAll("bar")
	e.buf.Undo()
	if e.buf.Text() != "foo foo" {
		t.Fatalf("one undo after Replace All yielded %q", e.buf.Text())
	}
}
func TestEditorNoOpDeleteMustPreserveRedo(t *testing.T) {
	b := newBuffer()
	b.SetText("a")
	b.Insert('b')
	b.Undo()
	b.MoveEnd()
	b.Delete()
	if !b.Redo() {
		t.Fatalf("Delete at EOF discarded redo; text=%q", b.Text())
	}
}
func TestEditorCtrlBackspaceMustReplaceExistingSelection(t *testing.T) {
	e := newEditor()
	e.buf.SetText("abc def ghi")
	e.buf.SetAnchor(0, 1)
	e.buf.SetCursor(0, 7)
	e.handleInsert(nil, Key{Kind: KeyBackspace, Ctrl: true})
	if e.buf.Text() != "a ghi" {
		t.Fatalf("Ctrl+Backspace ignored selection; text=%q", e.buf.Text())
	}
}
