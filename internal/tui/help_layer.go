package tui

// helpLayer is a modal overlay listing every keybind, grouped by
// context (global / Query / Explorer / Results / Command menu). It is
// opened by F1 from anywhere and closed by F1 or Esc. The contents
// are a static table; when a binding changes it must be updated here
// too.
type helpLayer struct {
	lines  []helpLine
	scroll int
}

// helpLine is one rendered row. Section rows have key == "" and are
// drawn as section headers; blank rows have both fields empty.
type helpLine struct {
	key  string
	desc string
}

func newHelpLayer() *helpLayer {
	return &helpLayer{lines: helpContent()}
}

func helpContent() []helpLine {
	section := func(name string) helpLine { return helpLine{desc: name} }
	bind := func(k, d string) helpLine { return helpLine{key: k, desc: d} }
	blank := helpLine{}
	return []helpLine{
		section("Global"),
		bind("F1", "this help"),
		bind("Ctrl+Q", "quit"),
		bind("Ctrl+K", "command menu"),
		bind("Alt+1 / 2 / 3", "focus Explorer / Query / Results"),
		bind("F11", "fullscreen editor"),
		bind("F8", "key-debug overlay"),
		blank,

		section("Query tabs"),
		bind("Ctrl+T", "new tab"),
		bind("Ctrl+W", "close tab"),
		bind("Ctrl+PgUp / PgDn", "prev / next tab (Query focus)"),
		bind("Left-click tab", "switch"),
		bind("Middle-click tab", "close"),
		blank,

		section("Query editor"),
		bind("F5", "run query"),
		bind("Ctrl+C", "cancel running query"),
		bind("Alt+F", "format buffer"),
		bind("Ctrl+Space", "autocomplete"),
		bind("Ctrl+F", "find / replace"),
		bind("Ctrl+L", "clear buffer"),
		bind("Ctrl+Z / Y", "undo / redo"),
		bind("Ctrl+X / C / V", "cut / copy / paste"),
		bind("Ctrl+A", "select all"),
		bind("Tab / Shift+Tab", "indent / dedent"),
		bind("Ctrl+Alt+Up / Dn", "add cursor above / below"),
		bind("Arrows, Home, End", "move caret"),
		bind("Ctrl+Left / Right", "word jump"),
		bind("Ctrl+Home / End", "buffer start / end"),
		blank,

		section("Explorer"),
		bind("Enter / s", "SELECT from table / view"),
		bind("Enter", "expand schema / group"),
		bind("Up / Dn / PgUp / PgDn", "move cursor"),
		bind("R", "refresh schema"),
		bind("Ctrl+K", "command menu"),
		blank,

		section("Results"),
		bind("Up / Dn / Lt / Rt", "move cell cursor"),
		bind("PgUp / PgDn", "page rows"),
		bind("Home / End", "first / last row"),
		bind("Enter", "inspect cell"),
		bind("y / Y", "copy cell / row"),
		bind("Alt+A", "copy all (TSV)"),
		bind("s", "cycle sort"),
		bind("/", "filter"),
		bind("w", "toggle wrap"),
		bind("Ctrl+PgUp / PgDn", "prev / next result set"),
		bind("Ctrl+K", "command menu"),
		blank,

		section("Results (error view)"),
		bind("Up / Dn / PgUp / PgDn", "scroll"),
		bind("y / Alt+A", "copy error text"),
		blank,

		section("Command menu (Ctrl+K)"),
		bind("c / x", "connect / disconnect"),
		bind("o", "open SQL file"),
		bind("e", "export results"),
		bind("h", "history"),
		bind("p", "explain plan"),
		bind("d", "set active database (per tab)"),
		bind("q", "quit"),
		bind("Esc", "cancel"),
	}
}

func (hl *helpLayer) Draw(a *app, c *cellbuf) {
	boxW := 80
	if boxW > a.term.width-dialogMargin {
		boxW = a.term.width - dialogMargin
	}
	if boxW < 40 {
		boxW = 40
	}
	boxH := a.term.height - dialogMargin
	if boxH < 12 {
		boxH = 12
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
	drawFrame(c, r, "Keybindings", true)

	innerCol := col + 3
	innerW := boxW - 6
	// Leave top padding (1), footer separator (1) and footer line (1).
	bodyTop := row + 2
	bodyH := boxH - 4
	if bodyH < 1 {
		return
	}

	maxScroll := len(hl.lines) - bodyH
	if maxScroll < 0 {
		maxScroll = 0
	}
	if hl.scroll > maxScroll {
		hl.scroll = maxScroll
	}
	if hl.scroll < 0 {
		hl.scroll = 0
	}

	keyColW := 24
	if keyColW > innerW/2 {
		keyColW = innerW / 2
	}
	gap := 3

	headerStyle := Style{FG: ansiBrightCyan, BG: ansiDefaultBG, Attrs: attrBold}
	keyStyle := Style{FG: ansiBrightYellow, BG: ansiDefaultBG}
	dimStyle := Style{FG: ansiBrightBlack, BG: ansiDefaultBG}

	for i := 0; i < bodyH; i++ {
		idx := hl.scroll + i
		if idx >= len(hl.lines) {
			break
		}
		line := hl.lines[idx]
		y := bodyTop + i
		if line.key == "" && line.desc == "" {
			continue
		}
		if line.key == "" {
			// Section header: bold cyan title, then a dim underline of
			// dashes the width of the title for a subtle separator.
			c.writeStyled(y, innerCol, truncate(line.desc, innerW), headerStyle)
			continue
		}
		c.writeStyled(y, innerCol, truncate(line.key, keyColW), keyStyle)
		descCol := innerCol + keyColW + gap
		descW := innerW - keyColW - gap
		if descW > 0 {
			c.writeStyled(y, descCol, truncate(line.desc, descW), dimStyle)
		}
	}

	// Footer separator + status line.
	sepRow := row + boxH - 3
	c.writeStyled(sepRow, innerCol, truncate(repeatRune('-', innerW), innerW), dimStyle)
	status := "Up/Dn=scroll  PgUp/PgDn=page  F1/Esc=close"
	c.writeAt(row+boxH-2, innerCol, truncate(status, innerW))
}

func repeatRune(r rune, n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]rune, n)
	for i := range b {
		b[i] = r
	}
	return string(b)
}

func (hl *helpLayer) HandleKey(a *app, k Key) {
	switch k.Kind {
	case KeyEsc, KeyF1:
		a.popLayer()
		return
	case KeyUp:
		hl.scroll--
	case KeyDown:
		hl.scroll++
	case KeyPgUp:
		hl.scroll -= 10
	case KeyPgDn:
		hl.scroll += 10
	case KeyHome:
		hl.scroll = 0
	case KeyEnd:
		hl.scroll = len(hl.lines)
	}
}

func (hl *helpLayer) Hints(a *app) string {
	_ = a
	return joinHints("Up/Dn=scroll", "F1/Esc=close")
}
