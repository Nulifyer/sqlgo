package tui

import "strings"

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
		boxW = a.term.width - dialogMargin
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
	r := rect{Row: row, Col: col, W: boxW, H: boxH}
	c.FillRect(r)
	drawFrame(c, r, "Find / Replace", true)

	innerCol := col + 2
	labelW := 9
	valCol := innerCol + labelW
	maxVal := boxW - labelW - 4
	if maxVal < 1 {
		maxVal = 1
	}

	c.WriteAt(row+1, innerCol, "Find:")
	fl.drawInputSlice(c, row+1, valCol, maxVal, fl.find, fl.activeField == findFieldFind)
	c.WriteAt(row+2, innerCol, "Replace:")
	fl.drawInputSlice(c, row+2, valCol, maxVal, fl.replace, fl.activeField == findFieldReplace)

	m := a.mainLayerPtr()
	statusText := fl.status
	if statusText == "" {
		statusText = fl.defaultStatus(m.editor)
	}
	options := "Alt+C match case: off  Alt+W whole word: off"
	if m.editor.searchMatchCase {
		options = "Alt+C match case: on  Alt+W whole word: off"
	}
	if m.editor.searchWholeWord {
		options = strings.ReplaceAll(options, "word: off", "word: on")
	}
	c.WriteAt(row+3, innerCol, truncate(options, boxW-4))
	c.WriteAt(row+4, innerCol, truncate(statusText, boxW-4))
	c.WriteAt(row+5, innerCol, truncate("↵=next  ⇤=prev  ⇥=field  Ctrl+R=all  Esc=close", boxW-4))
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
	c.WriteAt(row, col, string(rs[start:]))
	if active {
		curCol := col + (in.Cursor() - start)
		if curCol < col {
			curCol = col
		}
		if curCol > col+maxVal {
			curCol = col + maxVal
		}
		c.PlaceCursor(row, curCol)
	}
}

func (fl *findLayer) defaultStatus(ed *editor) string {
	if fl.find.String() == "" {
		return "type to search; Esc closes"
	}
	if ed.searchBusy {
		return "searching…"
	}
	if ed.MatchCount() == 0 {
		return "no matches"
	}
	if ed.searchLimited {
		return "first " + itoa(ed.MatchCount()) + " matches; narrow search to replace all"
	}
	return "match " + itoa(ed.CurrentMatchIndex()) + " of " + itoa(ed.MatchCount())
}

func (fl *findLayer) HandleKey(a *app, k Key) {
	m := a.mainLayerPtr()
	ed := m.editor

	if k.Alt && k.Kind == KeyRune {
		switch k.Rune {
		case 'c', 'C':
			ed.searchMatchCase = !ed.searchMatchCase
		case 'w', 'W':
			ed.searchWholeWord = !ed.searchWholeWord
		default:
			return
		}
		ed.requestSearch(a, fl.find.String())
		fl.status = ""
		return
	}
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
			if ed.searchBusy {
				fl.status = "searching…"
				return
			}
			if ed.replaceCurrentInUI(a, fl.replace.String()) {
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
		if ed.searchBusy {
			fl.status = "searching…"
			return
		}
		if ed.searchLimited {
			fl.status = "too many matches; narrow the search before replacing all"
			return
		}
		if ed.largeAnalysis() && a.asyncCh != nil {
			ed.replaceAllAsync(a, fl.replace.String(), func(n int, err error) {
				if err != nil {
					fl.status = err.Error()
				} else {
					fl.status = "replaced " + itoa(n)
				}
			})
			return
		}
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
	if target.Handle(k) {
		fl.status = ""
		if fl.activeField == findFieldFind {
			ed.requestSearch(a, fl.find.String())
		}
	}
}

func (fl *findLayer) HandleInput(a *app, msg InputMsg) bool {
	p, ok := msg.(PasteMsg)
	if !ok {
		return false
	}
	var target *input
	if fl.activeField == findFieldFind {
		target = fl.find
	} else {
		target = fl.replace
	}
	if !target.PasteText(p.Text) {
		return true
	}
	fl.status = ""
	if fl.activeField == findFieldFind {
		a.mainLayerPtr().editor.requestSearch(a, fl.find.String())
	}
	return true
}

func (fl *findLayer) Hints(a *app) string {
	_ = a
	return joinHints(
		"type=edit field",
		"↵=next/replace",
		"⇤=prev",
		"⇥=field",
		"Ctrl+R=all",
		"Esc=close",
	)
}
