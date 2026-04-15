package tui

import (
	"strconv"
	"strings"
)

// gotoLayer is a small modal overlay that jumps the active editor to a
// given line. Invoked by Ctrl+G from the Query focus. Accepts a plain
// line number, or "line:col" to also place the caret on a column.
// Enter commits, Esc cancels. Out-of-range line clamps to the last line.
type gotoLayer struct {
	input *input
	err   string
}

func newGotoLayer(curRow int) *gotoLayer {
	return &gotoLayer{input: newInput(strconv.Itoa(curRow + 1))}
}

func (gl *gotoLayer) Draw(a *app, c *cellbuf) {
	boxW := 48
	if boxW > a.term.width-dialogMargin {
		boxW = a.term.width - dialogMargin
	}
	if boxW < 24 {
		boxW = 24
	}
	boxH := 6
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
	drawFrame(c, r, "Go to line", true)

	innerCol := col + 2
	m := a.mainLayerPtr()
	total := 0
	if m != nil && m.editor != nil {
		total = m.editor.buf.LineCount()
	}
	prompt := "Line (1-" + strconv.Itoa(total) + "):"
	c.writeAt(row+1, innerCol, prompt)
	valRow := row + 2
	valCol := innerCol
	maxVal := boxW - 4
	if maxVal < 1 {
		maxVal = 1
	}
	val := gl.input.String()
	rs := []rune(val)
	if len(rs) > maxVal {
		rs = rs[len(rs)-maxVal:]
	}
	c.writeAt(valRow, valCol, string(rs))
	c.placeCursor(valRow, valCol+len(rs))

	if gl.err != "" {
		errStyle := Style{FG: ansiBrightRed, BG: ansiDefaultBG}
		c.writeStyled(row+3, innerCol, truncate(gl.err, boxW-4), errStyle)
	}
	c.writeAt(row+boxH-2, innerCol, truncate("Enter=go  Esc=cancel  (accepts line or line:col)", boxW-4))
}

func (gl *gotoLayer) HandleKey(a *app, k Key) {
	switch k.Kind {
	case KeyEsc:
		a.popLayer()
		return
	case KeyEnter:
		line, column, ok := parseGotoTarget(gl.input.String())
		if !ok {
			gl.err = "invalid; try 42 or 42:10"
			return
		}
		m := a.mainLayerPtr()
		if m == nil || m.editor == nil {
			a.popLayer()
			return
		}
		e := m.editor
		e.collapseCursors()
		e.buf.ClearSelection()
		row := line - 1
		if row < 0 {
			row = 0
		}
		if max := e.buf.LineCount() - 1; row > max {
			row = max
		}
		col := column - 1
		if col < 0 {
			col = 0
		}
		if lineLen := len(e.buf.Line(row)); col > lineLen {
			col = lineLen
		}
		e.buf.SetCursor(row, col)
		m.focus = FocusQuery
		a.popLayer()
		return
	}
	gl.err = ""
	gl.input.handle(k)
}

func (gl *gotoLayer) Hints(a *app) string {
	_ = a
	return joinHints("type=line[:col]", "Enter=go", "Esc=cancel")
}

// parseGotoTarget accepts "N" or "N:C". Whitespace tolerated.
func parseGotoTarget(s string) (line, col int, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, false
	}
	col = 1
	if i := strings.IndexByte(s, ':'); i >= 0 {
		c, err := strconv.Atoi(strings.TrimSpace(s[i+1:]))
		if err != nil || c < 1 {
			return 0, 0, false
		}
		col = c
		s = strings.TrimSpace(s[:i])
	}
	l, err := strconv.Atoi(s)
	if err != nil || l < 1 {
		return 0, 0, false
	}
	return l, col, true
}
