package tui

// findLayer is the modal overlay that drives the editor's
// find/replace feature. Two single-line inputs (Find and Replace)
// sit side-by-side in a centered box; Tab toggles focus between
// them. Enter in the Find field advances to the next match, Enter
// in the Replace field replaces the current match and advances,
// Shift+Tab (BackTab) steps backwards, Ctrl+R replaces every match,
// and Esc closes the overlay and clears all search state.
//
// Matches are owned by the editor -- findLayer only pokes at
// editor.SetSearch / NextMatch / ReplaceCurrent / ReplaceAll. That
// keeps the draw-time highlight painting close to the rest of the
// editor's styling (syntax, selection, completion popup) without
// having to marshal the match list across layers.
type findLayer struct {
	find    *input
	replace *input
	// activeField is 0 for Find, 1 for Replace. Tab toggles between
	// the two; Shift+Tab does not toggle (it steps through matches
	// instead, which is the more useful shortcut while the user's
	// focus is already on a field).
	activeField int
	// status is transient feedback shown below the input row:
	// "3 of 17", "no matches", "replaced 5", etc.
	status string
}

const (
	findFieldFind    = 0
	findFieldReplace = 1
)

func newFindLayer(seed string) *findLayer {
	fl := &findLayer{
		find:    newInput(seed),
		replace: newInput(""),
	}
	return fl
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
	// Dock near the top of the screen so the editor remains visible
	// below the overlay and the user can see which match is
	// highlighted while typing. Clamp to at least row 1 so the
	// overlay never bleeds into the frame border.
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
	labelW := 9 // "Replace: " is the widest label
	valCol := innerCol + labelW
	maxVal := boxW - labelW - 4
	if maxVal < 1 {
		maxVal = 1
	}

	// Find row.
	c.writeAt(row+1, innerCol, "Find:")
	fl.drawInputSlice(c, row+1, valCol, maxVal, fl.find, fl.activeField == findFieldFind)

	// Replace row.
	c.writeAt(row+2, innerCol, "Replace:")
	fl.drawInputSlice(c, row+2, valCol, maxVal, fl.replace, fl.activeField == findFieldReplace)

	// Status line: match count, empty-search hint, or operation
	// feedback from the last key.
	m := a.mainLayerPtr()
	ed := m.editor
	statusText := fl.status
	if statusText == "" {
		statusText = fl.defaultStatus(ed)
	}
	c.writeAt(row+4, innerCol, truncate(statusText, boxW-4))

	// Keybinding reminders inside the box so they don't disappear
	// when a cramped terminal truncates the footer hint line.
	c.writeAt(row+5, innerCol, truncate("Enter=next  Shift+Tab=prev  Tab=field  Ctrl+R=all  Esc=close", boxW-4))
}

// drawInputSlice paints one single-line input field with a visible
// cursor when active. Long values are tailed so the insertion point
// stays on-screen.
func (fl *findLayer) drawInputSlice(c *cellbuf, row, col, maxVal int, in *input, active bool) {
	val := in.String()
	rs := []rune(val)
	start := 0
	if len(rs) > maxVal {
		// Show the tail: same choice filterLayer makes, and it's
		// the right one because the cursor is almost always at the
		// end of the line while typing.
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

// defaultStatus renders the idle status line: match count when
// there's a search live, "type to search" when the Find field is
// empty, or "no matches" otherwise.
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
		// Shift+Tab walks backwards through matches -- a common
		// convention for find/replace UIs where Tab is already
		// doing field navigation.
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

	// Ctrl+R replaces every match with the current Replace field.
	// Handled before the input.handle() fallthrough because Ctrl
	// runes are swallowed by input.handle with a false return.
	if k.Ctrl && k.Kind == KeyRune && (k.Rune == 'r' || k.Rune == 'R') {
		n := ed.ReplaceAll(fl.replace.String())
		if n == 0 {
			fl.status = "nothing to replace"
		} else {
			fl.status = "replaced " + itoa(n)
		}
		return
	}

	// Typing into the active input. If it was the Find field, any
	// change re-runs the search so matches + highlights update live.
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
