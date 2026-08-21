package tui

import (
	"fmt"

	"github.com/Nulifyer/sqlgo/internal/tui/widget"
)

type dirtyAction int

const (
	dirtyCloseTab dirtyAction = iota
	dirtyQuit
)

type dirtyConfirmLayer struct {
	action dirtyAction
	tabIdx int
	count  int
}

func newDirtyCloseLayer(tabIdx int) *dirtyConfirmLayer {
	return &dirtyConfirmLayer{action: dirtyCloseTab, tabIdx: tabIdx, count: 1}
}

func newDirtyQuitLayer(count int) *dirtyConfirmLayer {
	return &dirtyConfirmLayer{action: dirtyQuit, tabIdx: -1, count: count}
}

func (l *dirtyConfirmLayer) Draw(a *app, c *cellbuf) {
	r := widget.CenterDialog(a.term.width, a.term.height, widget.DialogOpts{
		PrefW: 64, PrefH: 8, MinW: 32, MinH: 7, Margin: dialogMargin,
	})
	widget.DrawDialog(c, r, "Unsaved query", true)
	message := "This query has unsaved changes."
	if l.action == dirtyQuit {
		message = fmt.Sprintf("%d query tab(s) have unsaved changes.", l.count)
	}
	c.SetFg(colorError)
	c.WriteAt(r.Row+2, r.Col+2, truncate(message, r.W-4))
	c.ResetStyle()
	c.WriteAt(r.Row+4, r.Col+2, truncate("Discard changes?  y=discard  n/Esc=cancel", r.W-4))
}

func (l *dirtyConfirmLayer) HandleKey(a *app, k Key) {
	if k.Kind == KeyEsc {
		a.popLayer()
		return
	}
	if k.Kind != KeyRune || k.Ctrl || k.Alt {
		return
	}
	switch k.Rune {
	case 'n', 'N':
		a.popLayer()
	case 'y', 'Y':
		a.popLayer()
		if l.action == dirtyQuit {
			a.quit = true
			return
		}
		a.mainLayerPtr().closeTab(l.tabIdx)
	}
}

func (l *dirtyConfirmLayer) Hints(a *app) string {
	_ = a
	return joinHints("y=discard", "n/Esc=cancel")
}

func (a *app) requestQuit() {
	dirty := 0
	for _, sess := range a.mainLayerPtr().sessions {
		if sess.IsDirty() {
			dirty++
		}
	}
	if dirty == 0 {
		a.quit = true
		return
	}
	a.pushLayer(newDirtyQuitLayer(dirty))
}
