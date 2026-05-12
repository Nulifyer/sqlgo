package tui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDocumentServiceApplyLoadedSetsCleanBaseline(t *testing.T) {
	t.Parallel()
	svc := documentService{}
	sess := newSession()
	doc := loadedDocument{
		Path: filepath.Join("tmp", "loaded.sql"),
		Text: "select 1;",
		Size: len("select 1;"),
	}

	if err := svc.ApplyLoaded(sess, doc); err != nil {
		t.Fatalf("ApplyLoaded: %v", err)
	}
	if got := sess.editor.buf.Text(); got != doc.Text {
		t.Fatalf("buffer = %q, want %q", got, doc.Text)
	}
	if sess.IsDirty() {
		t.Fatal("loaded document should start clean")
	}

	sess.editor.buf.Insert(';')
	if !sess.IsDirty() {
		t.Fatal("editing a loaded document should mark it dirty")
	}
}

func TestDocumentServiceSaveNormalizesPathAndBaseline(t *testing.T) {
	t.Parallel()
	svc := documentService{}
	sess := newSession()
	sess.editor.buf.SetText("select 2;")

	dir := t.TempDir()
	path := filepath.Join(dir, ".", "query.sql")
	doc, err := svc.Save(sess, path)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	wantPath := filepath.Clean(filepath.Join(dir, "query.sql"))
	if doc.Path != wantPath {
		t.Fatalf("path = %q, want %q", doc.Path, wantPath)
	}
	if sess.sourcePath != wantPath {
		t.Fatalf("session path = %q, want %q", sess.sourcePath, wantPath)
	}
	if sess.IsDirty() {
		t.Fatal("saved document should be clean")
	}
	data, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if got := string(data); got != "select 2;" {
		t.Fatalf("file = %q, want select 2;", got)
	}
}

func TestDocumentServiceFindOpenTabNormalizesLookup(t *testing.T) {
	t.Parallel()
	svc := documentService{}
	sess := newSession()
	base := filepath.Join(t.TempDir(), "query.sql")
	if _, err := svc.Save(sess, base); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if got := svc.FindOpenTab([]*session{sess}, filepath.Join(filepath.Dir(base), ".", filepath.Base(base))); got != 0 {
		t.Fatalf("FindOpenTab = %d, want 0", got)
	}
}
