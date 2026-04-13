package tui

import (
	"fmt"
	"os"
)

// openLayer is the modal overlay that prompts for a SQL file path and
// replaces the editor buffer with its contents. Opened from the Space
// menu via 'o'. Any file contents are accepted -- the editor treats
// whatever lands in the buffer as plain text.
type openLayer struct {
	path   *input
	status string
}

func newOpenLayer(seed string) *openLayer {
	return &openLayer{path: newInput(seed)}
}

func (ol *openLayer) Draw(a *app, c *cellbuf) {
	boxW := 64
	if boxW > a.term.width-dialogMargin {
		boxW = a.term.width - dialogMargin
	}
	if boxW < 40 {
		boxW = 40
	}
	boxH := 9
	if boxH > a.term.height-dialogMargin {
		boxH = a.term.height - dialogMargin
	}
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
	drawFrame(c, r, "Open SQL file", true)

	innerCol := col + 2
	cur := row + 1

	c.writeAt(cur+1, innerCol, truncate("Path:", boxW-4))
	valCol := innerCol + 7
	maxVal := boxW - 7 - 4
	if maxVal < 1 {
		maxVal = 1
	}
	val := ol.path.String()
	rs := []rune(val)
	if len(rs) > maxVal {
		rs = rs[len(rs)-maxVal:]
	}
	c.writeAt(cur+1, valCol, string(rs))
	c.placeCursor(cur+1, valCol+len(rs))

	c.writeAt(cur+3, innerCol, truncate("Replaces the current query buffer.", boxW-4))

	if ol.status != "" {
		c.setFg(colorBorderFocused)
		c.writeAt(r.row+r.h-2, innerCol, truncate(ol.status, boxW-4))
		c.resetStyle()
	}
}

func (ol *openLayer) HandleKey(a *app, k Key) {
	if k.Kind == KeyEsc {
		a.popLayer()
		return
	}
	if k.Kind == KeyEnter {
		ol.load(a)
		return
	}
	ol.path.handle(k)
}

func (ol *openLayer) load(a *app) {
	path := ol.path.String()
	if path == "" {
		ol.status = "path is required"
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		ol.status = "read failed: " + err.Error()
		return
	}
	m := a.mainLayerPtr()
	m.editor.buf.SetText(string(data))
	a.popLayer()
	m.status = fmt.Sprintf("loaded %d bytes from %s", len(data), path)
}

func (ol *openLayer) Hints(a *app) string {
	_ = a
	return joinHints("type=path", "Enter=load", "Esc=cancel")
}
