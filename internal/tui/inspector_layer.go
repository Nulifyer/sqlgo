package tui

import (
	"fmt"
	"strings"
)

// inspectorLayer is the modal overlay that shows the full untruncated
// value of a single cell. Multi-line values keep their original line
// breaks (unlike the main table, which collapses them to dim `\n`),
// so the user can read real log lines or JSON blobs without the
// column-width constraint.
//
// Scrollable vertically when the value is taller than the box; horizontal
// scrolling is not supported for simplicity (long lines just wrap).
type inspectorLayer struct {
	colName string
	value   string
	lines   []string // value split into display lines, already wrapped to a max width
	scroll  int
	wrapW   int
}

func newInspectorLayer(colName, value string) *inspectorLayer {
	return &inspectorLayer{colName: colName, value: value}
}

// wrapText hard-wraps s to width w, preserving explicit newlines. Long
// unbroken tokens are chopped at w rather than overflowing. The result
// is what the inspector actually draws; the original value is kept
// separately so the "y" yank copies the untouched cell.
func wrapText(s string, w int) []string {
	if w <= 0 {
		return strings.Split(s, "\n")
	}
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if line == "" {
			out = append(out, "")
			continue
		}
		runes := []rune(line)
		for len(runes) > w {
			out = append(out, string(runes[:w]))
			runes = runes[w:]
		}
		out = append(out, string(runes))
	}
	return out
}

func (il *inspectorLayer) ensureWrapped(innerW int) {
	if innerW == il.wrapW && il.lines != nil {
		return
	}
	il.wrapW = innerW
	il.lines = wrapText(il.value, innerW)
	if il.scroll > len(il.lines)-1 {
		il.scroll = len(il.lines) - 1
	}
	if il.scroll < 0 {
		il.scroll = 0
	}
}

func (il *inspectorLayer) Draw(a *app, c *cellbuf) {
	boxW := 90
	if boxW > a.term.width-4 {
		boxW = a.term.width - 4
	}
	if boxW < 40 {
		boxW = 40
	}
	boxH := 24
	if boxH > a.term.height-4 {
		boxH = a.term.height - 4
	}
	if boxH < 10 {
		boxH = 10
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

	title := "Cell"
	if il.colName != "" {
		title = "Cell: " + il.colName
	}
	info := fmt.Sprintf("%d chars", len([]rune(il.value)))
	drawFrameInfo(c, r, title, info, true)

	innerCol := col + 2
	innerRow := row + 1
	innerW := boxW - 4
	innerH := boxH - 2
	if innerW <= 0 || innerH <= 0 {
		return
	}
	il.ensureWrapped(innerW)

	bodyH := innerH - 1 // last line reserved for hint
	if bodyH <= 0 {
		return
	}
	for i := 0; i < bodyH; i++ {
		idx := il.scroll + i
		if idx >= len(il.lines) {
			break
		}
		c.writeAt(innerRow+i, innerCol, truncate(il.lines[idx], innerW))
	}
}

func (il *inspectorLayer) HandleKey(a *app, k Key) {
	switch k.Kind {
	case KeyEsc:
		a.popLayer()
		return
	case KeyUp:
		if il.scroll > 0 {
			il.scroll--
		}
		return
	case KeyDown:
		if il.scroll < len(il.lines)-1 {
			il.scroll++
		}
		return
	case KeyPgUp:
		il.scroll -= 10
		if il.scroll < 0 {
			il.scroll = 0
		}
		return
	case KeyPgDn:
		il.scroll += 10
		if il.scroll > len(il.lines)-1 {
			il.scroll = len(il.lines) - 1
		}
		return
	case KeyHome:
		il.scroll = 0
		return
	case KeyEnd:
		il.scroll = len(il.lines) - 1
		if il.scroll < 0 {
			il.scroll = 0
		}
		return
	}
	if k.Kind == KeyRune && !k.Ctrl && k.Rune == 'y' {
		if err := a.clipboard.Copy(il.value); err != nil {
			a.mainLayerPtr().status = "copy: " + err.Error()
		} else {
			a.mainLayerPtr().status = fmt.Sprintf("copied cell (%d chars)", len([]rune(il.value)))
		}
	}
}

func (il *inspectorLayer) Hints(a *app) string {
	_ = a
	return joinHints("Up/Dn/PgUp/PgDn=scroll", "y=copy", "Esc=close")
}
