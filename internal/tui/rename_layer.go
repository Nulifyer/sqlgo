package tui

import "strings"

// renameLayer is a small modal overlay that edits the title of a query
// tab. Invoked by Ctrl+R or by a double-click on the query tab strip. Enter
// commits, Esc cancels. Empty/whitespace input is treated as cancel so a
// user who clears the field and presses Enter doesn't end up with a
// blank tab label.
type renameLayer struct {
	idx   int
	input *input
}

func newRenameLayer(idx int, seed string) *renameLayer {
	return &renameLayer{idx: idx, input: newInput(seed)}
}

func (rl *renameLayer) Draw(a *app, c *cellbuf) {
	boxW := 48
	if boxW > a.term.width-dialogMargin {
		boxW = a.term.width - dialogMargin
	}
	if boxW < 24 {
		boxW = 24
	}
	boxH := 5
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
	drawFrame(c, r, "Rename query tab", true)

	innerCol := col + 2
	c.writeAt(row+1, innerCol, "Name:")
	valCol := innerCol + 6
	maxVal := boxW - 6 - 4
	if maxVal < 1 {
		maxVal = 1
	}
	val := rl.input.String()
	rs := []rune(val)
	if len(rs) > maxVal {
		rs = rs[len(rs)-maxVal:]
	}
	c.writeAt(row+1, valCol, string(rs))
	c.placeCursor(row+1, valCol+len(rs))

	c.writeAt(row+3, innerCol, truncate("Enter=save  Esc=cancel", boxW-4))
}

func (rl *renameLayer) HandleKey(a *app, k Key) {
	switch k.Kind {
	case KeyEsc:
		a.popLayer()
		return
	case KeyEnter:
		name := strings.TrimSpace(rl.input.String())
		if name != "" {
			m := a.mainLayerPtr()
			if rl.idx >= 0 && rl.idx < len(m.sessions) {
				m.sessions[rl.idx].title = name
			}
		}
		a.popLayer()
		return
	}
	rl.input.handle(k)
}

func (rl *renameLayer) Hints(a *app) string {
	_ = a
	return joinHints("type=name", "Enter=save", "Esc=cancel")
}
