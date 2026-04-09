package tui

// filterLayer is the modal overlay that prompts for a substring filter
// on the current results buffer. The filter is case-insensitive and
// applies to all cells in a row (match-any). Typing updates the filter
// live via the table widget's SetFilter, so results narrow as the user
// keeps typing.
type filterLayer struct {
	input  *input
	status string
}

func newFilterLayer(seed string) *filterLayer {
	return &filterLayer{input: newInput(seed)}
}

func (fl *filterLayer) Draw(a *app, c *cellbuf) {
	boxW := 60
	if boxW > a.term.width-4 {
		boxW = a.term.width - 4
	}
	if boxW < 30 {
		boxW = 30
	}
	boxH := 7
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
	drawFrame(c, r, "Filter results", true)

	innerCol := col + 2
	c.writeAt(row+1, innerCol, "Filter:")
	valCol := innerCol + 8
	maxVal := boxW - 8 - 4
	if maxVal < 1 {
		maxVal = 1
	}
	val := fl.input.String()
	rs := []rune(val)
	if len(rs) > maxVal {
		rs = rs[len(rs)-maxVal:]
	}
	c.writeAt(row+1, valCol, string(rs))
	c.placeCursor(row+1, valCol+len(rs))

	m := a.mainLayerPtr()
	msg := ""
	if val == "" {
		msg = "type to filter; empty clears"
	} else {
		msg = formatFilterStatus(m.table.RowCount(), m.table.Filter())
	}
	c.writeAt(row+3, innerCol, truncate(msg, boxW-4))

	if fl.status != "" {
		c.setFg(colorBorderFocused)
		c.writeAt(r.row+r.h-2, innerCol, truncate(fl.status, boxW-4))
		c.resetStyle()
	}
}

func (fl *filterLayer) HandleKey(a *app, k Key) {
	if k.Kind == KeyEsc {
		// Esc clears the filter on its way out so the user can abandon
		// a mistyped filter without leaving the result set narrowed.
		a.mainLayerPtr().table.SetFilter("")
		a.popLayer()
		return
	}
	if k.Kind == KeyEnter {
		// Commit the current filter (already live) and close.
		a.popLayer()
		return
	}
	fl.input.handle(k)
	a.mainLayerPtr().table.SetFilter(fl.input.String())
}

func (fl *filterLayer) Hints(a *app) string {
	_ = a
	return joinHints("type=filter", "Enter=keep", "Esc=clear")
}

// formatFilterStatus builds a human-readable summary for the filter
// box: N rows visible / filter text. Kept small to fit inside the
// overlay without wrapping.
func formatFilterStatus(visible int, filter string) string {
	if filter == "" {
		return "no filter"
	}
	return "matches: " + itoa(visible)
}
