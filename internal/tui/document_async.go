package tui

import (
	"fmt"
	"path/filepath"

	"github.com/Nulifyer/sqlgo/internal/store"
)

// runBackground owns slow I/O on a worker. Only its callback touches sessions.
// Tests without a running event loop retain a synchronous service path.
func (a *app) runBackground(work func() func(*app)) {
	if a.asyncCh == nil {
		work()(a)
		return
	}
	go func() {
		callback := work()
		select {
		case a.asyncCh <- callback:
		case <-a.doneCh:
		}
	}()
}

func (a *app) openDocumentPaths(paths []string) {
	m := a.mainLayerPtr()
	owner := m.session
	catalog := owner.activeCatalog
	paths = append([]string(nil), paths...)
	documents := a.documents
	m.status = fmt.Sprintf("opening %d file(s)…", len(paths))
	a.runBackground(func() func(*app) {
		docs := make([]loadedDocument, 0, len(paths))
		var lastErr error
		for _, path := range paths {
			doc, err := documents.Load(path)
			if err != nil {
				lastErr = err
				continue
			}
			docs = append(docs, doc)
		}
		return func(a *app) {
			m := a.mainLayerPtr()
			activate := m.session == owner
			last := -1
			for _, doc := range docs {
				if idx := documents.FindOpenTab(m.sessions, doc.Path); idx >= 0 {
					last = idx
					continue
				}
				sess := newSession()
				sess.activeCatalog = catalog
				if err := documents.ApplyLoaded(sess, doc); err != nil {
					lastErr = err
					continue
				}
				m.sessions = append(m.sessions, sess)
				last = len(m.sessions) - 1
				a.recordDocumentDir(store.LastDirOpen, filepath.Dir(doc.Path))
			}
			if (activate || m.activeTab < 0) && last >= 0 {
				m.switchTab(last)
				m.focus = FocusQuery
			}
			if lastErr != nil {
				m.status = "open: " + lastErr.Error()
			} else {
				m.status = fmt.Sprintf("opened %d file(s)", len(docs))
			}
		}
	})
}

func (a *app) hasSession(sess *session) bool {
	for _, s := range a.mainLayerPtr().sessions {
		if s == sess {
			return true
		}
	}
	return false
}

func (a *app) saveDocument(sess *session, path string) {
	m := a.mainLayerPtr()
	if sess.saveBusy {
		m.status = "save already in progress; editing remains available"
		return
	}
	snapshot, err := a.documents.Snapshot(sess, path)
	if err != nil {
		m.status = "save: " + err.Error()
		return
	}
	a.saveDocumentSnapshot(sess, snapshot)
}

func (a *app) saveDocumentSnapshot(sess *session, snapshot documentSnapshot) {
	m := a.mainLayerPtr()
	sess.saveBusy = true
	sess.saveGen++
	gen, docGen := sess.saveGen, sess.docGen
	documents := a.documents
	m.status = "saving " + filepath.Base(snapshot.Path) + "…"
	a.documentWrites.Add(1)
	a.runBackground(func() func(*app) {
		defer a.documentWrites.Done()
		doc, err := documents.Write(snapshot)
		return func(a *app) {
			if !a.hasSession(sess) || sess.docGen != docGen || sess.saveGen != gen {
				return
			}
			sess.saveBusy = false
			if err != nil {
				sess.status = "save: " + err.Error()
				a.mainLayerPtr().status = sess.status
				return
			}
			// savedText is the captured snapshot. Concurrent edits stay dirty.
			documents.ApplySaved(sess, doc)
			a.recordDocumentDir(store.LastDirSave, filepath.Dir(doc.Path))
			sess.status = fmt.Sprintf("saved %d bytes to %s", doc.Size, doc.Path)
			a.mainLayerPtr().status = sess.status
		}
	})
}

func (a *app) recordDocumentDir(kind store.LastDirKind, path string) {
	if a.store != nil {
		go recordDir(a, kind, path)
	}
}
