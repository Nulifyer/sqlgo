package tui

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func documentCallback(t *testing.T, a *app) func(*app) {
	t.Helper()
	select {
	case fn := <-a.asyncCh:
		return fn
	case <-time.After(3 * time.Second):
		t.Fatal("document worker did not return")
		return nil
	}
}
func TestDocumentWorkerKeepsUIAvailable(t *testing.T) {
	m := newMainLayer()
	m.ensureActiveTab().editor.buf.SetText("abc")
	m.editor.buf.SetCursor(0, 1)
	a := &app{layers: []Layer{m}, asyncCh: make(chan func(*app), 1)}
	gate := make(chan struct{})
	a.runBackground(func() func(*app) { <-gate; return func(*app) {} })
	m.HandleKey(a, Key{Kind: KeyRight})
	_, col := m.editor.buf.Cursor()
	if col != 2 {
		t.Fatal("navigation was not available during background work")
	}
	close(gate)
	documentCallback(t, a)(a)
}
func TestDocumentSaveKeepsEditsMadeWhileSavingDirty(t *testing.T) {
	m := newMainLayer()
	s := m.ensureActiveTab()
	s.editor.buf.SetText("before")
	a := &app{layers: []Layer{m}, asyncCh: make(chan func(*app), 1)}
	path := filepath.Join(t.TempDir(), "query.sql")
	a.saveDocument(s, path)
	s.editor.buf.InsertText(" after")
	documentCallback(t, a)(a)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "before" || s.savedText != "before" || s.editor.buf.Text() != "before after" || !s.IsDirty() {
		t.Fatalf("snapshot=%q baseline=%q text=%q dirty=%v", data, s.savedText, s.editor.buf.Text(), s.IsDirty())
	}
}
func TestDocumentSaveCallbackIgnoresClosedSession(t *testing.T) {
	m := newMainLayer()
	s := m.ensureActiveTab()
	s.editor.buf.SetText("before")
	a := &app{layers: []Layer{m}, asyncCh: make(chan func(*app), 1)}
	a.saveDocument(s, filepath.Join(t.TempDir(), "query.sql"))
	m.closeTab(0)
	documentCallback(t, a)(a)
	if m.session.sourcePath != "" {
		t.Fatal("save callback changed a replacement session")
	}
}

func TestQuitRemainsInteractiveDuringSave(t *testing.T) {
	m := newMainLayer()
	s := m.ensureActiveTab()
	s.saveBusy = true
	a := &app{layers: []Layer{m}}
	a.requestQuit()
	if a.quit || len(a.layers) != 1 {
		t.Fatal("quit interrupted an accepted save")
	}
	s.saveBusy = false
	a.requestQuit()
	if !a.quit {
		t.Fatal("completed save prevented quit")
	}
}

func TestSaveAsInspectionIgnoresEditedTarget(t *testing.T) {
	m := newMainLayer()
	s := m.ensureActiveTab()
	s.editor.buf.ResetText("SELECT 1;")
	a := &app{layers: []Layer{m}, asyncCh: make(chan func(*app), 4)}
	path := filepath.Join(t.TempDir(), "query.sql")
	sl := newSaveLayer(a, 0, path, nil)
	a.pushLayer(sl)
	sl.save(a)
	sl.picker.NameInput.SetString("changed")
	documentCallback(t, a)(a)
	if s.saveBusy || s.sourcePath != "" {
		t.Fatal("stale target inspection started a save")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("stale target written: %v", err)
	}
}
func TestDocumentFormatRoundTrips(t *testing.T) {
	for _, encoding := range []string{"utf-8", "utf-16le", "utf-16be"} {
		for _, eol := range []string{"\n", "\r\n", "\r"} {
			t.Run(encoding+eol, func(t *testing.T) {
				text := "SELECT 'é😀';\n\nSELECT 2;\n"
				format := documentFormat{Encoding: encoding, BOM: true, EOL: eol}
				raw := encodeDocument(text, format)
				path := filepath.Join(t.TempDir(), "query.sql")
				if err := os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
				d := documentService{}
				doc, err := d.Load(path)
				if err != nil {
					t.Fatal(err)
				}
				s := newSession()
				d.ApplyLoaded(s, doc)
				if s.editor.buf.Text() != text || s.IsDirty() {
					t.Fatal("decoded document was not clean normalized text")
				}
				if _, err := d.Save(s, path); err != nil {
					t.Fatal(err)
				}
				got, _ := os.ReadFile(path)
				if !bytes.Equal(got, raw) {
					t.Fatalf("roundtrip changed %x -> %x", raw, got)
				}
			})
		}
	}
}

func TestDocumentRejectsUTF32AndKeepsPredominantMixedEOL(t *testing.T) {
	if _, _, err := decodeDocument([]byte{0xff, 0xfe, 0, 0, 'a', 0, 0, 0}); err == nil {
		t.Fatal("UTF-32LE was misidentified as UTF-16")
	}
	_, format, err := decodeDocument([]byte("a\r\nb\r\nc\r\nd\r\ne\nf\ng\nh\ri\r"))
	if err != nil || format.EOL != "\r\n" {
		t.Fatalf("predominant CRLF lost: %+v, %v", format, err)
	}
}
func TestEditHistoryStoresChangesAndRestoresSelections(t *testing.T) {
	b := newBuffer()
	b.ResetText(string(bytes.Repeat([]byte{'x'}, 1<<20)))
	for i := 0; i < 32; i++ {
		b.InsertText(";")
	}
	if b.historyBytes > 1024 {
		t.Fatalf("small edits retained %d bytes", b.historyBytes)
	}
	b.ResetText("abc")
	b.SetAnchor(0, 0)
	b.SetCursor(0, 2)
	b.InsertText("z")
	b.Undo()
	if b.Text() != "abc" || b.Selection() != "ab" {
		t.Fatalf("undo text=%q selection=%q", b.Text(), b.Selection())
	}
}
