package tui

import (
	"fmt"
	"os"
	"path/filepath"
)

type documentService struct{}

type loadedDocument struct {
	Path string
	Text string
	Size int
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
		if d.NormalizePath(sess.sourcePath) == want {
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
	data, err := os.ReadFile(path)
	if err != nil {
		return loadedDocument{}, err
	}
	return loadedDocument{Path: path, Text: string(data), Size: len(data)}, nil
}

func (d documentService) ApplyLoaded(sess *session, doc loadedDocument) error {
	if sess == nil || sess.editor == nil {
		return fmt.Errorf("document session is unavailable")
	}
	doc.Path = d.NormalizePath(doc.Path)
	sess.editor.buf.SetText(doc.Text)
	sess.editor.ClearErrorLocation()
	sess.sourcePath = doc.Path
	sess.savedText = doc.Text
	sess.title = filepath.Base(doc.Path)
	return nil
}

func (d documentService) Save(sess *session, path string) (loadedDocument, error) {
	if sess == nil || sess.editor == nil {
		return loadedDocument{}, fmt.Errorf("document session is unavailable")
	}
	path = d.NormalizePath(path)
	if path == "" {
		return loadedDocument{}, fmt.Errorf("path is required")
	}
	text := sess.editor.buf.Text()
	if err := os.WriteFile(path, []byte(text), 0644); err != nil {
		return loadedDocument{}, err
	}
	doc := loadedDocument{Path: path, Text: text, Size: len(text)}
	sess.sourcePath = doc.Path
	sess.savedText = doc.Text
	sess.title = filepath.Base(doc.Path)
	return doc, nil
}
