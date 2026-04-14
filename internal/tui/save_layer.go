package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// saveLayer is the modal prompt for "Save As". Invoked directly when the
// user triggers Save on a tab with no sourcePath, or explicitly via
// Save As. Enter writes the buffer to the typed path, updates the
// session's sourcePath/savedText/title, and pops. Overwrites existing
// files without a separate prompt to match the export_layer pattern.
type saveLayer struct {
	path    *input
	tabIdx  int
	status  string
	confirm bool // true while waiting for Enter to confirm overwrite
}

func newSaveLayer(tabIdx int, seed string) *saveLayer {
	return &saveLayer{path: newInput(seed), tabIdx: tabIdx}
}

func (sl *saveLayer) Draw(a *app, c *cellbuf) {
	boxW := 64
	if boxW > a.term.width-dialogMargin {
		boxW = a.term.width - dialogMargin
	}
	if boxW < 40 {
		boxW = 40
	}
	boxH := 8
	row := (a.term.height - boxH) / 2
	col := (a.term.width - boxW) / 2
	if row < 1 {
		row = 1
	}
	if col < 1 {
		col = 1
	}
	r := rect{row: row, col: col, w: boxW, h: boxH}
	c.fillRect(r)
	drawFrame(c, r, "Save SQL file", true)

	innerCol := col + 2
	c.writeAt(row+1, innerCol, "Path:")
	valCol := innerCol + 6
	maxVal := boxW - 6 - 4
	if maxVal < 1 {
		maxVal = 1
	}
	val := sl.path.String()
	rs := []rune(val)
	if len(rs) > maxVal {
		rs = rs[len(rs)-maxVal:]
	}
	c.writeAt(row+1, valCol, string(rs))
	c.placeCursor(row+1, valCol+len(rs))

	if sl.status != "" {
		c.setFg(colorBorderFocused)
		c.writeAt(r.row+r.h-2, innerCol, truncate(sl.status, boxW-4))
		c.resetStyle()
	}
}

func (sl *saveLayer) HandleKey(a *app, k Key) {
	switch k.Kind {
	case KeyEsc:
		a.popLayer()
		return
	case KeyEnter:
		sl.save(a)
		return
	}
	// Any edit resets the overwrite-confirm state so a second Enter
	// after typing isn't interpreted as "yes, overwrite".
	sl.confirm = false
	sl.path.handle(k)
}

func (sl *saveLayer) save(a *app) {
	raw := strings.TrimSpace(sl.path.String())
	if raw == "" {
		sl.status = "path is required"
		return
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		sl.status = "invalid path: " + err.Error()
		return
	}
	abs = filepath.Clean(abs)

	m := a.mainLayerPtr()
	if sl.tabIdx < 0 || sl.tabIdx >= len(m.sessions) {
		sl.status = "tab no longer exists"
		return
	}
	sess := m.sessions[sl.tabIdx]

	if idx := m.findTabByPath(abs); idx >= 0 && idx != sl.tabIdx {
		sl.status = fmt.Sprintf("another tab already has %s open", filepath.Base(abs))
		return
	}

	if !sl.confirm && abs != sess.sourcePath {
		if _, err := os.Stat(abs); err == nil {
			sl.status = "file exists -- Enter to overwrite, edit path to cancel"
			sl.confirm = true
			return
		}
	}

	text := sess.editor.buf.Text()
	if err := os.WriteFile(abs, []byte(text), 0644); err != nil {
		sl.status = "write failed: " + err.Error()
		return
	}
	sess.sourcePath = abs
	sess.savedText = text
	sess.title = filepath.Base(abs)
	a.popLayer()
	m.status = fmt.Sprintf("saved %d bytes to %s", len(text), abs)
}

func (sl *saveLayer) Hints(a *app) string {
	_ = a
	if sl.confirm {
		return joinHints("Enter=overwrite", "edit=cancel", "Esc=close")
	}
	return joinHints("type=path", "Enter=save", "Esc=cancel")
}
