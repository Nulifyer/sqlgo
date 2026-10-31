package tui

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRecoveryGenerationsAndExplicitDiscard(t *testing.T) {
	dir := t.TempDir()
	store, err := newRecoveryStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	drafts := []recoveryDraft{{Title: "unsaved", Text: "SELECT '😀';", Baseline: ""}}
	if err := store.write(2, drafts); err != nil {
		t.Fatal(err)
	}
	if err := store.write(1, nil); err != nil {
		t.Fatal(err)
	}
	got, sources, err := readRecoveryDrafts(dir, func(int) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Text != drafts[0].Text || len(sources) != 1 {
		t.Fatalf("drafts=%+v sources=%v", got, sources)
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(store.path)
		if info.Mode().Perm() != 0600 {
			t.Fatalf("snapshot permissions=%o", info.Mode().Perm())
		}
	}
	active, _, err := readRecoveryDrafts(dir, func(int) bool { return true })
	if err != nil || len(active) != 0 {
		t.Fatalf("active process drafts=%v err=%v", active, err)
	}
	store.close(true)
	if _, err := os.Stat(store.path); !os.IsNotExist(err) {
		t.Fatal("explicit discard did not remove snapshot")
	}
	if err := store.write(3, drafts); err == nil {
		t.Fatal("closed recovery store accepted a late write")
	}
}
func TestRecoveryRestorePreservesExistingTabAndSaveConflict(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "query.sql")
	os.WriteFile(source, []byte("old"), 0600)
	docs := documentService{}
	loaded, err := docs.Load(source)
	if err != nil {
		t.Fatal(err)
	}
	store, err := newRecoveryStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	draft := recoveryDraft{Title: "recovered", Path: source, Text: "new work", Baseline: "old", Format: loaded.Format, Fingerprint: loaded.Fingerprint, DiskKnown: true, lines: splitBufferText("new work")}
	old, err := newRecoveryStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	old.write(1, []recoveryDraft{draft})
	m := newMainLayer()
	original := m.ensureActiveTab()
	original.editor.buf.SetText("keep this")
	layer := &recoveryLayer{drafts: []recoveryDraft{draft}, sources: []string{old.path}}
	a := &app{layers: []Layer{m, layer}, recovery: store}
	layer.HandleKey(a, Key{Kind: KeyRune, Rune: 'r'})
	if len(m.sessions) != 2 || original.editor.buf.Text() != "keep this" || m.session.editor.buf.Text() != "new work" || !m.session.IsDirty() {
		t.Fatal("restore replaced work or lost the dirty baseline")
	}
	if _, err := os.Stat(old.path); !os.IsNotExist(err) {
		t.Fatal("source snapshot was not retired after the replacement snapshot")
	}
	if _, err := os.Stat(store.path); err != nil {
		t.Fatal("restored drafts were not persisted before source removal")
	}
	os.WriteFile(source, []byte("external edit"), 0600)
	if _, err := docs.Save(m.session, source); err != errDocumentConflict {
		t.Fatalf("restored document failed to protect disk changes: %v", err)
	}
}
