package tui

import (
	"fmt"
	"strings"
)

// debugLayer is a full-screen key-inspector toggled by F8. Two panes:
//
//   - Live feed (left, narrow): rolling log of every key / mouse event
//     the layer sees, rendered as a compact token so a single row fits
//     modifiers + key name.
//   - Bind checklist (right): every keybind the app publishes, grouped
//     by purpose and laid out in multiple columns so the whole catalog
//     is visible at once without scrolling. Pressing the matching key
//     flips a bind's checkbox -- game-controller-tester style.
//
// F8 closes. The three globally-reserved keys (Ctrl+Q quit, F1 help,
// F8 self) are omitted from the checklist: they never reach this layer.
type debugLayer struct {
	log   []debugEvent
	binds []debugBind
}

type debugEvent struct {
	desc     string
	sequence int
}

// debugBind is one checklist row. match reports whether a key event
// satisfies the bind; mouseBind does the same for mouse events. Either
// can be nil for binds that only apply to the other input type.
type debugBind struct {
	group     string
	label     string
	match     func(k Key) bool
	mouseBind func(m MouseMsg) bool
	done      bool
}

// checklistEntry is one rendered row in the checklist grid. Headers
// have bind == nil; they advertise the start of a new group.
type checklistEntry struct {
	header string
	bind   *debugBind
}

func debugLogCap(a *app) int {
	n := a.term.height - 6
	if n < 10 {
		n = 10
	}
	if n > 500 {
		n = 500
	}
	return n
}

func newDebugLayer() *debugLayer {
	return &debugLayer{binds: buildDebugBinds()}
}

func (d *debugLayer) Draw(a *app, c *cellbuf) {
	r := rect{row: 0, col: 0, w: a.term.width, h: a.term.height}
	c.fillRect(r)
	passed, total := d.tally()
	title := fmt.Sprintf("Key debug (F8)    %d / %d bindings verified", passed, total)
	drawFrame(c, r, title, true)

	innerRow := 1
	innerCol := 1
	innerW := a.term.width - 2
	innerH := a.term.height - 2
	if innerW < 10 || innerH < 4 {
		return
	}

	// Log is narrow on purpose so the checklist gets the room it needs.
	// 22 cols fits "Ctrl+Shift+Alt+PgDn" style tokens with some slack.
	logW := 22
	if logW > innerW/3 {
		logW = innerW / 3
	}
	if logW < 14 {
		logW = 14
	}
	listW := innerW - logW - 1
	if listW < 20 {
		// Pathologically narrow terminal: drop the checklist, show just
		// the log full-width.
		logW = innerW
		listW = 0
	}

	d.drawLog(c, innerRow, innerCol, logW, innerH)

	if listW > 0 {
		listCol := innerCol + logW + 1
		for i := 0; i < innerH; i++ {
			c.writeAt(innerRow+i, innerCol+logW, "│")
		}
		d.drawChecklist(c, innerRow, listCol, listW, innerH)
	}
}

func (d *debugLayer) drawLog(c *cellbuf, row, col, w, h int) {
	if w <= 0 || h <= 0 {
		return
	}
	headerStyle := Style{FG: ansiBrightCyan, BG: ansiDefaultBG, Attrs: attrBold}
	hintStyle := Style{FG: ansiBrightBlack, BG: ansiDefaultBG}

	c.writeStyled(row, col+1, truncate("Input feed", w-2), headerStyle)

	bodyTop := row + 2
	bodyH := h - 2
	if bodyH < 1 {
		return
	}

	if len(d.log) == 0 {
		c.writeStyled(bodyTop, col+1, truncate("(press any key)", w-2), hintStyle)
		return
	}
	n := len(d.log)
	for i := 0; i < bodyH && i < n; i++ {
		ev := d.log[n-1-i]
		line := fmt.Sprintf("%3d %s", ev.sequence%1000, ev.desc)
		c.writeAt(bodyTop+i, col+1, truncate(line, w-2))
	}
}

func (d *debugLayer) drawChecklist(c *cellbuf, row, col, w, h int) {
	if w <= 0 || h <= 0 {
		return
	}
	headerStyle := Style{FG: ansiBrightCyan, BG: ansiDefaultBG, Attrs: attrBold}
	groupStyle := Style{FG: ansiBrightYellow, BG: ansiDefaultBG, Attrs: attrBold}
	okStyle := Style{FG: ansiBrightGreen, BG: ansiDefaultBG}
	dimStyle := Style{FG: ansiBrightBlack, BG: ansiDefaultBG}

	c.writeStyled(row, col+1, truncate("Bind checklist", w-2), headerStyle)

	bodyTop := row + 2
	bodyH := h - 3
	if bodyH < 1 {
		return
	}

	entries := d.buildEntries()

	// Figure out how many columns the checklist needs to fit everything
	// in bodyH rows. Column-major flow so groups stay together visually
	// when they fit inside a single column.
	colW := 28
	gap := 2
	maxCols := (w - 1 + gap) / (colW + gap)
	if maxCols < 1 {
		maxCols = 1
	}
	ncols := 1
	for ncols < maxCols {
		perCol := (len(entries) + ncols - 1) / ncols
		if perCol <= bodyH {
			break
		}
		ncols++
	}
	perCol := (len(entries) + ncols - 1) / ncols
	if perCol < 1 {
		perCol = 1
	}
	// Recalculate colW so columns share the pane width evenly.
	if ncols > 1 {
		colW = (w - 1 - gap*(ncols-1)) / ncols
	} else {
		colW = w - 2
	}
	if colW < 10 {
		colW = 10
	}

	for ci := 0; ci < ncols; ci++ {
		cx := col + 1 + ci*(colW+gap)
		for ri := 0; ri < perCol; ri++ {
			idx := ci*perCol + ri
			if idx >= len(entries) {
				break
			}
			if ri >= bodyH {
				break
			}
			y := bodyTop + ri
			e := entries[idx]
			if e.bind == nil {
				c.writeStyled(y, cx, truncate(e.header, colW), groupStyle)
				continue
			}
			marker := "[ ]"
			style := dimStyle
			if e.bind.done {
				marker = "[✓]"
				style = okStyle
			}
			line := fmt.Sprintf("%s %s", marker, e.bind.label)
			c.writeStyled(y, cx, truncate(line, colW), style)
		}
	}

	footer := fmt.Sprintf("%d/%d verified  Ctrl+R=reset", countDone(d.binds), len(d.binds))
	c.writeStyled(row+h-1, col+1, truncate(footer, w-2), dimStyle)
}

// buildEntries flattens the bind catalog into a column-friendly sequence:
// one header row per group, followed by each of its binds. Groups keep
// their source order.
func (d *debugLayer) buildEntries() []checklistEntry {
	var entries []checklistEntry
	var current string
	for i := range d.binds {
		b := &d.binds[i]
		if b.group != current {
			entries = append(entries, checklistEntry{header: b.group})
			current = b.group
		}
		entries = append(entries, checklistEntry{bind: b})
	}
	return entries
}

func countDone(bs []debugBind) int {
	n := 0
	for _, b := range bs {
		if b.done {
			n++
		}
	}
	return n
}

func (d *debugLayer) tally() (int, int) {
	return countDone(d.binds), len(d.binds)
}

func (d *debugLayer) HandleKey(a *app, k Key) {
	// Ctrl+R: reset checklist. Kept out of the bind list to avoid a
	// chicken-and-egg situation with its own checkbox.
	if k.Ctrl && k.Kind == KeyRune && k.Rune == 'r' {
		for i := range d.binds {
			d.binds[i].done = false
		}
		d.push(a, "(reset)")
		return
	}
	d.push(a, compactKey(k))
	for i := range d.binds {
		if d.binds[i].match != nil && d.binds[i].match(k) {
			d.binds[i].done = true
		}
	}
}

func (d *debugLayer) View(a *app) View {
	_ = a
	return View{AltScreen: true, MouseEnabled: true}
}

func (d *debugLayer) HandleInput(a *app, msg InputMsg) bool {
	switch v := msg.(type) {
	case MouseMsg:
		d.push(a, compactMouse(v))
		for i := range d.binds {
			if d.binds[i].mouseBind != nil && d.binds[i].mouseBind(v) {
				d.binds[i].done = true
			}
		}
		return true
	case PasteMsg:
		d.push(a, fmt.Sprintf("paste %dB", len(v.Text)))
		return true
	}
	return false
}

func (d *debugLayer) push(a *app, desc string) {
	seq := 1
	if n := len(d.log); n > 0 {
		seq = d.log[n-1].sequence + 1
	}
	d.log = append(d.log, debugEvent{desc: desc, sequence: seq})
	ringCap := debugLogCap(a)
	if len(d.log) > ringCap {
		d.log = append(d.log[:0], d.log[len(d.log)-ringCap:]...)
	}
}

func (d *debugLayer) Hints(a *app) string {
	_ = a
	return joinHints("any=test bind", "Ctrl+R=reset", "F8=close")
}

// --- bind catalog ----------------------------------------------------------

func buildDebugBinds() []debugBind {
	kb := func(group, label string, kind KeyKind, ctrl, alt, shift bool) debugBind {
		return debugBind{group: group, label: label, match: func(k Key) bool {
			return k.Kind == kind && k.Ctrl == ctrl && k.Alt == alt && k.Shift == shift
		}}
	}
	rb := func(group, label string, r rune, ctrl, alt bool) debugBind {
		return debugBind{group: group, label: label, match: func(k Key) bool {
			if k.Kind != KeyRune || k.Ctrl != ctrl || k.Alt != alt {
				return false
			}
			// Shift+letter is observed via uppercase rune rather than
			// Shift=true for most terminals, so compare case-insensitively.
			return k.Rune == r || k.Rune == toggleCase(r)
		}}
	}
	mb := func(group, label string, btn MouseButton, action MouseAction) debugBind {
		return debugBind{group: group, label: label, mouseBind: func(m MouseMsg) bool {
			return m.Button == btn && m.Action == action
		}}
	}

	return []debugBind{
		// Movement
		kb("Movement", "Up", KeyUp, false, false, false),
		kb("Movement", "Down", KeyDown, false, false, false),
		kb("Movement", "Left", KeyLeft, false, false, false),
		kb("Movement", "Right", KeyRight, false, false, false),
		kb("Movement", "Home", KeyHome, false, false, false),
		kb("Movement", "End", KeyEnd, false, false, false),
		kb("Movement", "PgUp", KeyPgUp, false, false, false),
		kb("Movement", "PgDn", KeyPgDn, false, false, false),

		// Edit
		kb("Edit", "Enter", KeyEnter, false, false, false),
		kb("Edit", "Tab", KeyTab, false, false, false),
		kb("Edit", "Shift+Tab", KeyBackTab, false, false, false),
		kb("Edit", "Backspace", KeyBackspace, false, false, false),
		kb("Edit", "Delete", KeyDelete, false, false, false),
		kb("Edit", "Esc", KeyEsc, false, false, false),

		// Selection
		kb("Selection", "Shift+Up", KeyUp, false, false, true),
		kb("Selection", "Shift+Down", KeyDown, false, false, true),
		kb("Selection", "Shift+Left", KeyLeft, false, false, true),
		kb("Selection", "Shift+Right", KeyRight, false, false, true),
		kb("Selection", "Shift+Home", KeyHome, false, false, true),
		kb("Selection", "Shift+End", KeyEnd, false, false, true),
		kb("Selection", "Ctrl+Shift+Left", KeyLeft, true, false, true),
		kb("Selection", "Ctrl+Shift+Right", KeyRight, true, false, true),
		kb("Selection", "Ctrl+Shift+Home", KeyHome, true, false, true),
		kb("Selection", "Ctrl+Shift+End", KeyEnd, true, false, true),

		// Word / buffer jumps
		kb("Word/Buffer", "Ctrl+Left", KeyLeft, true, false, false),
		kb("Word/Buffer", "Ctrl+Right", KeyRight, true, false, false),
		kb("Word/Buffer", "Ctrl+Home", KeyHome, true, false, false),
		kb("Word/Buffer", "Ctrl+End", KeyEnd, true, false, false),
		kb("Word/Buffer", "Ctrl+Backspace", KeyBackspace, true, false, false),
		kb("Word/Buffer", "Ctrl+Delete", KeyDelete, true, false, false),

		// Tabs / result sets
		kb("Tabs", "Ctrl+PgUp", KeyPgUp, true, false, false),
		kb("Tabs", "Ctrl+PgDn", KeyPgDn, true, false, false),
		rb("Tabs", "Ctrl+T", 't', true, false),
		rb("Tabs", "Ctrl+W", 'w', true, false),

		// Clipboard
		rb("Clipboard", "Ctrl+A", 'a', true, false),
		rb("Clipboard", "Ctrl+C", 'c', true, false),
		rb("Clipboard", "Ctrl+X", 'x', true, false),
		rb("Clipboard", "Ctrl+V", 'v', true, false),

		// Edit actions
		rb("Actions", "Ctrl+Z", 'z', true, false),
		rb("Actions", "Ctrl+Y", 'y', true, false),
		rb("Actions", "Ctrl+D", 'd', true, false),
		rb("Actions", "Ctrl+U", 'u', true, false),
		rb("Actions", "Ctrl+F", 'f', true, false),
		rb("Actions", "Ctrl+G", 'g', true, false),
		rb("Actions", "Ctrl+L", 'l', true, false),
		rb("Actions", "Ctrl+O", 'o', true, false),
		rb("Actions", "Ctrl+R", 'r', true, false),
		rb("Actions", "Ctrl+S", 's', true, false),
		rb("Actions", "Ctrl+Space", ' ', true, false),

		// Menu / focus
		rb("Menu/Focus", "Ctrl+K", 'k', true, false),
		rb("Menu/Focus", "Ctrl+E", 'e', true, false),
		rb("Menu/Focus", "Alt+1", '1', false, true),
		rb("Menu/Focus", "Alt+2", '2', false, true),
		rb("Menu/Focus", "Alt+3", '3', false, true),
		rb("Menu/Focus", "Alt+F", 'f', false, true),
		rb("Menu/Focus", "Alt+S", 's', false, true),
		rb("Menu/Focus", "Alt+D", 'd', false, true),
		rb("Menu/Focus", "Alt+A", 'a', false, true),

		// Line ops
		kb("Line ops", "Alt+Up", KeyUp, false, true, false),
		kb("Line ops", "Alt+Down", KeyDown, false, true, false),
		kb("Line ops", "Shift+Alt+Up", KeyUp, false, true, true),
		kb("Line ops", "Shift+Alt+Down", KeyDown, false, true, true),
		kb("Line ops", "Ctrl+Alt+Up", KeyUp, true, true, false),
		kb("Line ops", "Ctrl+Alt+Down", KeyDown, true, true, false),

		// Function keys
		kb("Function", "F5", KeyF5, false, false, false),
		kb("Function", "F9", KeyF9, false, false, false),
		kb("Function", "F11", KeyF11, false, false, false),

		// Mouse
		mb("Mouse", "Left click", MouseButtonLeft, MouseActionPress),
		mb("Mouse", "Middle click", MouseButtonMiddle, MouseActionPress),
		mb("Mouse", "Wheel up", MouseButtonWheelUp, MouseActionPress),
		mb("Mouse", "Wheel down", MouseButtonWheelDown, MouseActionPress),
	}
}

// toggleCase swaps ASCII letter case. Used so Shift+<letter> is
// recognized regardless of whether the terminal reported the rune in
// uppercase form.
func toggleCase(r rune) rune {
	if r >= 'a' && r <= 'z' {
		return r - 32
	}
	if r >= 'A' && r <= 'Z' {
		return r + 32
	}
	return r
}

// compactKey renders a Key in "[Ctrl+][Alt+][Shift+]Name" form for the
// live feed. Wide enough to distinguish every bind, narrow enough for
// a 22-column pane.
func compactKey(k Key) string {
	var mods []string
	if k.Ctrl {
		mods = append(mods, "Ctrl")
	}
	if k.Alt {
		mods = append(mods, "Alt")
	}
	if k.Shift {
		mods = append(mods, "Shift")
	}
	var name string
	if k.Kind == KeyRune {
		switch {
		case k.Rune == ' ':
			name = "Space"
		case k.Rune >= 0x20 && k.Rune < 0x7f:
			name = string(k.Rune)
		default:
			name = fmt.Sprintf("U+%04X", k.Rune)
		}
	} else {
		name = debugKeyKindName(k.Kind)
	}
	if len(mods) == 0 {
		return name
	}
	return strings.Join(mods, "+") + "+" + name
}

func compactMouse(m MouseMsg) string {
	act := ""
	switch m.Action {
	case MouseActionPress:
		act = "↓"
	case MouseActionRelease:
		act = "↑"
	case MouseActionMotion:
		act = "~"
	}
	return fmt.Sprintf("%s%s @%d,%d", debugMouseButtonName(m.Button), act, m.X, m.Y)
}

func debugMouseButtonName(b MouseButton) string {
	switch b {
	case MouseButtonNone:
		return "None"
	case MouseButtonLeft:
		return "Left"
	case MouseButtonMiddle:
		return "Mid"
	case MouseButtonRight:
		return "Right"
	case MouseButtonWheelUp:
		return "WhUp"
	case MouseButtonWheelDown:
		return "WhDn"
	case MouseButtonWheelLeft:
		return "WhL"
	case MouseButtonWheelRight:
		return "WhR"
	}
	return fmt.Sprintf("B(%d)", int(b))
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
