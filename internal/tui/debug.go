package tui

import "fmt"

// debugLayer is a hidden key inspector toggled by F8. Every key it
// receives (except Esc, which closes) is added to a rolling log of the
// most recent events with their decoded Kind/Rune/Ctrl/Alt so tracing
// terminal input issues doesn't require a rebuild with printfs.
//
// It's drawn as a modal box centered in the viewport. The layer blocks
// input to everything beneath it, which is deliberate: you're here to
// see what you're typing.
type debugLayer struct {
	log []debugEvent
}

type debugEvent struct {
	key      Key
	sequence int // 1-based ordinal for display
}

// debugLayerCap returns the ring size for the captured-key log. It
// scales with terminal height so the log holds roughly the number of
// lines the box can actually display. Clamped to [10, 200].
func debugLayerCap(a *app) int {
	n := a.term.height - 6
	if n < 10 {
		n = 10
	}
	if n > 200 {
		n = 200
	}
	return n
}

func newDebugLayer() *debugLayer { return &debugLayer{} }

func (d *debugLayer) Draw(a *app, c *cellbuf) {
	boxW := 60
	if boxW > a.term.width-4 {
		boxW = a.term.width - dialogMargin
	}
	if boxW < 30 {
		boxW = 30
	}
	boxH := debugLayerCap(a) + 6
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
	r := rect{row: row, col: col, w: boxW, h: boxH}
	c.fillRect(r)
	drawFrame(c, r, "Key debug (F8)", true)

	innerCol := col + 2
	cur := row + 1

	if len(d.log) == 0 {
		c.writeAt(cur+1, innerCol, truncate("Press any key to inspect it.", boxW-4))
		c.writeAt(cur+3, innerCol, truncate("Esc closes this panel.", boxW-4))
		return
	}

	c.writeAt(cur, innerCol, truncate("Last keys (newest first):", boxW-4))
	// Render newest first so the most recent press is always at the top.
	listTop := cur + 2
	maxRows := boxH - 4
	if maxRows < 1 {
		maxRows = 1
	}
	n := len(d.log)
	for i := 0; i < maxRows && i < n; i++ {
		ev := d.log[n-1-i]
		line := fmt.Sprintf("%3d  %s", ev.sequence, formatDebugKey(ev.key))
		c.writeAt(listTop+i, innerCol, truncate(line, boxW-4))
	}
}

func (d *debugLayer) HandleKey(a *app, k Key) {
	if k.Kind == KeyEsc {
		a.popLayer()
		return
	}
	seq := 1
	if n := len(d.log); n > 0 {
		seq = d.log[n-1].sequence + 1
	}
	d.log = append(d.log, debugEvent{key: k, sequence: seq})
	ringCap := debugLayerCap(a)
	if len(d.log) > ringCap {
		// Drop oldest; keep tail. Simple shift -- bounded size keeps this
		// cheap.
		d.log = append(d.log[:0], d.log[len(d.log)-ringCap:]...)
	}
}

func (d *debugLayer) Hints(a *app) string {
	_ = a
	return joinHints("any=log", "Esc=close")
}

// formatDebugKey renders a Key as a single-line "kind=... rune=... ctrl=... alt=..."
// summary. Non-printable runes are shown as \xNN so the output is
// always single-width-safe.
func formatDebugKey(k Key) string {
	kind := debugKeyKindName(k.Kind)
	runeRepr := "-"
	if k.Kind == KeyRune {
		if k.Rune >= 0x20 && k.Rune < 0x7f {
			runeRepr = fmt.Sprintf("%q (U+%04X)", k.Rune, k.Rune)
		} else {
			runeRepr = fmt.Sprintf("U+%04X", k.Rune)
		}
	}
	return fmt.Sprintf("kind=%s rune=%s ctrl=%v alt=%v", kind, runeRepr, k.Ctrl, k.Alt)
}

func debugKeyKindName(k KeyKind) string {
	switch k {
	case KeyRune:
		return "Rune"
	case KeyEnter:
		return "Enter"
	case KeyTab:
		return "Tab"
	case KeyBackTab:
		return "BackTab"
	case KeyBackspace:
		return "Backspace"
	case KeyEsc:
		return "Esc"
	case KeyUp:
		return "Up"
	case KeyDown:
		return "Down"
	case KeyLeft:
		return "Left"
	case KeyRight:
		return "Right"
	case KeyHome:
		return "Home"
	case KeyEnd:
		return "End"
	case KeyPgUp:
		return "PgUp"
	case KeyPgDn:
		return "PgDn"
	case KeyDelete:
		return "Delete"
	case KeyF1:
		return "F1"
	case KeyF2:
		return "F2"
	case KeyF3:
		return "F3"
	case KeyF4:
		return "F4"
	case KeyF5:
		return "F5"
	case KeyF6:
		return "F6"
	case KeyF7:
		return "F7"
	case KeyF8:
		return "F8"
	case KeyF9:
		return "F9"
	case KeyF10:
		return "F10"
	case KeyF11:
		return "F11"
	case KeyF12:
		return "F12"
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}
