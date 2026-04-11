package tui

// findLayer drives the editor's find/replace overlay. Two single-
// line inputs (Find, Replace) with Tab to toggle focus. Matches
// live on the editor so highlights draw alongside syntax/selection.
type findLayer struct {
	find        *input
	replace     *input
	activeField int // findFieldFind or findFieldReplace
	status      string
}

const (
	findFieldFind    = 0
	findFieldReplace = 1
)

func newFindLayer(seed string) *findLayer {
	return &findLayer{
		find:    newInput(seed),
		replace: newInput(""),
	}
}

func (fl *findLayer) Draw(a *app, c *cellbuf) {
	boxW := 64
	if boxW > a.term.width-4 {
		boxW = a.term.width - 4
	}
	if boxW < 40 {
		boxW = 40
	}
	boxH := 9
	// Dock near the top so the editor + highlighted matches stay
	// visible below.
	row := 2
	if row+boxH > a.term.height-2 {
		row = (a.term.height - boxH) / 2
	}
	col := (a.term.width - boxW) / 2
	if row < 1 {
		row = 1
	}
	if col < 1 {
		col = 1
	}
	r := rect{row: row, col: col, w: boxW, h: boxH}
	c.fillRect(r)
	drawFrame(c, r, "Find / Replace", true)

	innerCol := col + 2
	labelW := 9
	valCol := innerCol + labelW
	maxVal := boxW - labelW - 4
	if maxVal < 1 {
		maxVal = 1
	}

	c.writeAt(row+1, innerCol, "Find:")
	fl.drawInputSlice(c, row+1, valCol, maxVal, fl.find, fl.activeField == findFieldFind)
	c.writeAt(row+2, innerCol, "Replace:")
	fl.drawInputSlice(c, row+2, valCol, maxVal, fl.replace, fl.activeField == findFieldReplace)

	m := a.mainLayerPtr()
	statusText := fl.status
	if statusText == "" {
		statusText = fl.defaultStatus(m.editor)
	}
	c.writeAt(row+4, innerCol, truncate(statusText, boxW-4))
	c.writeAt(row+5, innerCol, truncate("Enter=next  Shift+Tab=prev  Tab=field  Ctrl+R=all  Esc=close", boxW-4))
}

// drawInputSlice paints a single-line input, tailing long values
// so the cursor stays on-screen.
func (fl *findLayer) drawInputSlice(c *cellbuf, row, col, maxVal int, in *input, active bool) {
	val := in.String()
	rs := []rune(val)
	start := 0
	if len(rs) > maxVal {
		start = len(rs) - maxVal
	}
	c.writeAt(row, col, string(rs[start:]))
	if active {
		curCol := col + (in.cur - start)
		if curCol < col {
			curCol = col
		}
		if curCol > col+maxVal {
			curCol = col + maxVal
		}
		c.placeCursor(row, curCol)
	}
}

func (fl *findLayer) defaultStatus(ed *editor) string {
	if fl.find.String() == "" {
		return "type to search; Esc closes"
	}
	if ed.MatchCount() == 0 {
		return "no matches"
	}
	return "match " + itoa(ed.CurrentMatchIndex()) + " of " + itoa(ed.MatchCount())
}

func (fl *findLayer) HandleKey(a *app, k Key) {
	m := a.mainLayerPtr()
	ed := m.editor

	switch k.Kind {
	case KeyEsc:
		ed.ClearSearch()
		a.popLayer()
		return
	case KeyTab:
		if fl.activeField == findFieldFind {
			fl.activeField = findFieldReplace
		} else {
			fl.activeField = findFieldFind
		}
		fl.status = ""
		return
	case KeyBackTab:
		ed.PrevMatch()
		fl.status = ""
		return
	case KeyEnter:
		if fl.activeField == findFieldReplace {
			if ed.ReplaceCurrent(fl.replace.String()) {
				fl.status = ""
			} else {
				fl.status = "nothing to replace"
			}
			return
		}
		ed.NextMatch()
		fl.status = ""
		return
	}

	// Ctrl+R: replace all. Handled before input.handle() because
	// Ctrl runes return false from handle.
	if k.Ctrl && k.Kind == KeyRune && (k.Rune == 'r' || k.Rune == 'R') {
		n := ed.ReplaceAll(fl.replace.String())
		if n == 0 {
			fl.status = "nothing to replace"
		} else {
			fl.status = "replaced " + itoa(n)
		}
		return
	}

	// Typing in active input. Find-field edits re-run search.
	var target *input
	if fl.activeField == findFieldFind {
		target = fl.find
	} else {
		target = fl.replace
	}
	if target.handle(k) {
		fl.status = ""
		if fl.activeField == findFieldFind {
			ed.SetSearch(fl.find.String())
		}
	}
}

func (fl *findLayer) Hints(a *app) string {
	_ = a
	return joinHints(
		"type=edit field",
		"Enter=next/replace",
		"Shift+Tab=prev",
		"Tab=field",
		"Ctrl+R=all",
		"Esc=close",
	)
}
