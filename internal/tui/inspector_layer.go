package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"
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

// sanitizeInspectorText strips or escapes control chars that
// would wreck terminal layout. \n stays as a line break. \r and
// \t render as visible \r \t. Other sub-0x20 chars are dropped.
// NUL (0x00), 0x7f DEL likewise stripped.
func sanitizeInspectorText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r := rune(s[i])
		// Fast path for ASCII (covers all control chars we care about).
		if r < 0x80 {
			i++
		} else {
			var size int
			r, size = utf8.DecodeRuneInString(s[i:])
			i += size
		}
		switch r {
		case '\n':
			b.WriteRune('\n')
		case '\r':
			if i < len(s) && s[i] == '\n' {
				// \r\n pair: emit one newline, skip the \n.
				b.WriteRune('\n')
				i++
			} else {
				b.WriteString(`\r`)
			}
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f {
				continue
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}

// wrapText hard-wraps s to width w, preserving explicit newlines.
// Control chars are sanitized first so \r can't send the terminal
// cursor back to column 0.
func wrapText(s string, w int) []string {
	s = sanitizeInspectorText(s)
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
		boxW = a.term.width - dialogMargin
	}
	if boxW < 40 {
		boxW = 40
	}
	boxH := 24
	if boxH > a.term.height-4 {
		boxH = a.term.height - dialogMargin
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
	r := rect{Row: row, Col: col, W: boxW, H: boxH}
	c.FillRect(r)

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
		c.WriteAt(innerRow+i, innerCol, truncate(il.lines[idx], innerW))
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
	return joinHints("↑/↓/PgUp/PgDn=scroll", "Home/End=top/bottom", "y=copy", "Esc=close")
}
