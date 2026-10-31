package tui

import (
	"fmt"
	"os"

	"github.com/Nulifyer/sqlgo/internal/tui/widget"
)

type recoveryLayer struct {
	drafts  []recoveryDraft
	sources []string
}

func (l *recoveryLayer) Draw(a *app, c *cellbuf) {
	r := widget.CenterDialog(a.term.width, a.term.height, widget.DialogOpts{PrefW: 70, PrefH: 9, MinW: 32, MinH: 8, Margin: dialogMargin})
	widget.DrawDialog(c, r, "Unsaved queries recovered", true)
	c.WriteAt(r.Row+2, r.Col+2, truncate(fmt.Sprintf("%d unsaved query tab(s) are available.", len(l.drafts)), r.W-4))
	c.WriteAt(r.Row+4, r.Col+2, truncate("r=restore in new tabs  d=discard  Esc=later", r.W-4))
}
func (l *recoveryLayer) HandleKey(a *app, k Key) {
	if k.Kind == KeyEsc {
		a.popLayer()
		return
	}
	if k.Kind != KeyRune || k.Ctrl || k.Alt {
		return
	}
	switch k.Rune {
	case 'r', 'R':
		m := a.mainLayerPtr()
		last := -1
		for _, draft := range l.drafts {
			s := newSession()
			doc := loadedDocument{Path: draft.Path, Text: draft.Text, Format: draft.Format, Fingerprint: draft.Fingerprint, DiskKnown: draft.DiskKnown, lines: draft.lines}
			if err := a.documents.ApplyLoaded(s, doc); err != nil {
				continue
			}
			s.title = draft.Title
			// Recovered history has no version corresponding to the disk baseline.
			s.setSavedIdentity(draft.Baseline, ^uint64(0))
			if draft.Text == draft.Baseline {
				s.savedStateID = s.editor.buf.stateID
			}
			s.activeCatalog = draft.Catalog
			m.sessions = append(m.sessions, s)
			last = len(m.sessions) - 1
		}
		if last >= 0 {
			m.switchTab(last)
		}
		a.popLayer()
		a.persistRestoredDrafts(l.sources)
	case 'd', 'D':
		paths := append([]string(nil), l.sources...)
		a.popLayer()
		a.runBackground(func() func(*app) {
			var lastErr error
			for _, path := range paths {
				if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
					lastErr = err
				}
			}
			return func(a *app) {
				if lastErr != nil {
					a.mainLayerPtr().status = "discard recovery: " + lastErr.Error()
				}
			}
		})
	}
}
func (l *recoveryLayer) Hints(*app) string { return joinHints("r=restore", "d=discard", "Esc=later") }
