package tui

import "time"

// filterDebounce is the quiet window after the last keystroke before the
// table-wide filter recompute fires. Tuned for "feels live" but still
// avoids reflowing a million-row result on every character.
const filterDebounce = 120 * time.Millisecond

// filterLayer is the modal overlay that prompts for a filter on the
// current results buffer. Three syntaxes are recognized:
//
//   - plain substring (case-insensitive) matches any cell in the row
//   - "col:text" restricts the substring match to the named column
//   - "/regex/" treats the contents as a case-insensitive regex
//
// Typing updates the filter live via the table widget's SetFilter,
// so results narrow as the user keeps typing. Any parse warnings
// (bad regex, unknown column) come back from the table via
// FilterNote() and render as a dimmed note line inside the box.
type filterLayer struct {
	input *input
	// gen counts keystrokes since the layer opened. The debounce
	// goroutine captures the gen at scheduling time; on fire it
	// re-checks against the current gen and skips the SetFilter call
	// if a newer keystroke has arrived. Avoids piling up stale
	// filter recomputes on a fast typist.
	gen int
}

func newFilterLayer(seed string) *filterLayer {
	return &filterLayer{input: newInput(seed)}
}

func (fl *filterLayer) Draw(a *app, c *cellbuf) {
	boxW := 64
	if boxW > a.term.width-4 {
		boxW = a.term.width - dialogMargin
	}
	if boxW < 40 {
		boxW = 40
	}
	boxH := 9
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

	// Syntax hint line so the user can discover column / regex mode
	// without having to read the docs.
	c.writeAt(row+3, innerCol, truncate("syntax: text  |  col:text  |  /regex/", boxW-4))

	m := a.mainLayerPtr()
	msg := ""
	if val == "" {
		msg = "type to filter; empty clears"
	} else {
		msg = formatFilterStatus(m.table.RowCount(), m.table.Filter())
	}
	c.writeAt(row+4, innerCol, truncate(msg, boxW-4))

	// Any parse warning from SetFilter lives one line below the
	// status. Dimmed so it doesn't compete with the match count.
	if note := m.table.FilterNote(); note != "" {
		c.setFg(colorBorderFocused)
		c.writeAt(r.row+r.h-2, innerCol, truncate("⚠ "+note, boxW-4))
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
		// Apply any pending edit immediately so a fast typist who hits
		// Enter before the debounce fires doesn't close the layer with
		// a stale filter applied.
		a.mainLayerPtr().table.SetFilter(fl.input.String())
		a.popLayer()
		return
	}
	fl.input.handle(k)
	fl.gen++
	gen := fl.gen
	want := fl.input.String()
	go func() {
		time.Sleep(filterDebounce)
		a.asyncCh <- func(a *app) {
			// Layer may have been popped, or another keystroke may
			// have superseded this one -- in either case the newer
			// path will (or did) apply the right filter.
			top, ok := a.topLayer().(*filterLayer)
			if !ok || top != fl || fl.gen != gen {
				return
			}
			a.mainLayerPtr().table.SetFilter(want)
		}
	}()
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
