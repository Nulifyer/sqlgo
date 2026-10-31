package tui

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Nulifyer/sqlgo/internal/atomicfile"
)

type documentService struct{}

type loadedDocument struct {
	Path        string
	Text        string
	Size        int
	Format      documentFormat
	Fingerprint documentFingerprint
	DiskKnown   bool
	StateID     uint64
	lines       [][]rune
}

func (documentService) NormalizePath(path string) string {
	if path == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(abs)
}

func (d documentService) FindOpenTab(sessions []*session, path string) int {
	want := d.NormalizePath(path)
	if want == "" {
		return -1
	}
	for i, sess := range sessions {
		if sess == nil || sess.sourcePath == "" {
			continue
		}
		if actual := d.NormalizePath(sess.sourcePath); actual == want || (runtime.GOOS == "windows" && strings.EqualFold(actual, want)) {
			return i
		}
	}
	return -1
}

func (d documentService) Load(path string) (loadedDocument, error) {
	path = d.NormalizePath(path)
	if path == "" {
		return loadedDocument{}, fmt.Errorf("path is required")
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return loadedDocument{}, err
	}
	text, format, err := decodeDocument(data)
	if err != nil {
		return loadedDocument{}, err
	}
	return loadedDocument{Path: path, Text: text, Size: len(data), Format: format, Fingerprint: documentFingerprint{Exists: true, Hash: sha256.Sum256(data)}, DiskKnown: true, lines: splitBufferText(text)}, nil
}

func (d documentService) ApplyLoaded(sess *session, doc loadedDocument) error {
	if sess == nil || sess.editor == nil {
		return fmt.Errorf("document session is unavailable")
	}
	doc.Path = d.NormalizePath(doc.Path)
	sess.editor.resetTransientState()
	sess.editor.buf.resetPreparedText(doc.Text, doc.lines)
	sess.editor.buf.MoveBufferStart()
	sess.editor.ClearErrorLocation()
	sess.docGen++
	sess.format = doc.Format
	sess.diskFingerprint, sess.diskKnown = doc.Fingerprint, doc.DiskKnown
	sess.sourcePath = doc.Path
	sess.setSavedIdentity(normalizeEditorLineEndings(doc.Text), sess.editor.buf.stateID)
	sess.title = filepath.Base(doc.Path)
	return nil
}

// documentSnapshot is immutable and can be encoded/written on a worker while
// the session continues to accept edits on the UI goroutine.
type documentSnapshot struct {
	Path, Text    string
	Format        documentFormat
	Expected      documentFingerprint
	CheckExpected bool
	StateID       uint64
}
type documentFingerprint struct {
	Exists bool
	Hash   [32]byte
}

var errDocumentConflict = errors.New("file changed on disk; reload it or use Save As")

func fingerprintDocument(path string) (documentFingerprint, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return documentFingerprint{}, nil
	}
	if err != nil {
		return documentFingerprint{}, err
	}
	return documentFingerprint{Exists: true, Hash: sha256.Sum256(data)}, nil
}
func (d documentService) Snapshot(sess *session, path string) (documentSnapshot, error) {
	if sess == nil || sess.editor == nil {
		return documentSnapshot{}, fmt.Errorf("document session is unavailable")
	}
	path = d.NormalizePath(path)
	if path == "" {
		return documentSnapshot{}, fmt.Errorf("path is required")
	}
	sess.editor.buf.breakUndoGroup()
	return documentSnapshot{StateID: sess.editor.buf.stateID, Path: path, Text: sess.editor.buf.Text(), Format: sess.format,
		Expected: sess.diskFingerprint, CheckExpected: sess.diskKnown && path == sess.sourcePath}, nil
}
func (documentService) Write(snapshot documentSnapshot) (loadedDocument, error) {
	if resolved, err := filepath.EvalSymlinks(snapshot.Path); err == nil {
		snapshot.Path = resolved
	}
	before, err := fingerprintDocument(snapshot.Path)
	if err != nil {
		return loadedDocument{}, err
	}
	if snapshot.CheckExpected && before != snapshot.Expected {
		return loadedDocument{}, errDocumentConflict
	}
	data := encodeDocument(snapshot.Text, snapshot.Format)
	out, err := atomicfile.Create(snapshot.Path)
	if err != nil {
		return loadedDocument{}, err
	}
	defer out.Finish(false)
	if _, err = out.Writer().Write(data); err != nil {
		return loadedDocument{}, err
	}
	current, err := fingerprintDocument(snapshot.Path)
	if err != nil {
		return loadedDocument{}, err
	}
	if current != before {
		return loadedDocument{}, errDocumentConflict
	}
	if err = out.Finish(true); err != nil {
		return loadedDocument{}, err
	}
	return loadedDocument{Path: snapshot.Path, Text: snapshot.Text, Size: len(data), Format: snapshot.Format,
		Fingerprint: documentFingerprint{Exists: true, Hash: sha256.Sum256(data)}, DiskKnown: true, StateID: snapshot.StateID}, nil
}
func (documentService) ApplySaved(sess *session, doc loadedDocument) {
	sess.sourcePath = doc.Path
	sess.setSavedIdentity(doc.Text, doc.StateID)
	sess.format = doc.Format
	sess.diskFingerprint, sess.diskKnown = doc.Fingerprint, doc.DiskKnown
	sess.title = filepath.Base(doc.Path)
}
func (d documentService) Save(sess *session, path string) (loadedDocument, error) {
	snapshot, err := d.Snapshot(sess, path)
	if err != nil {
		return loadedDocument{}, err
	}
	doc, err := d.Write(snapshot)
	if err == nil {
		d.ApplySaved(sess, doc)
	}
	return doc, err
}
