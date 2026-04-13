package tui

// helpLayer is a modal overlay listing every keybind, grouped by
// context (global / Query / Explorer / Results / Space menu). It is
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
		bind("Ctrl+Q", "quit"),
		bind("F1", "toggle this help"),
		bind("F8", "key-debug overlay"),
		bind("F11", "toggle query fullscreen"),
		bind("Alt+1 / Alt+2 / Alt+3", "focus Explorer / Query / Results"),
		blank,

		section("Query editor"),
		bind("Arrows / Home / End", "move cursor"),
		bind("Ctrl+Home / Ctrl+End", "buffer start / end"),
		bind("Ctrl+Left / Ctrl+Right", "word jump"),
		bind("Enter", "new line"),
		bind("Tab / Shift+Tab", "indent / dedent"),
		bind("Ctrl+Z / Ctrl+Y", "undo / redo"),
		bind("Ctrl+X / Ctrl+C / Ctrl+V", "cut / copy / paste"),
		bind("Ctrl+A", "select all"),
		bind("Ctrl+Enter", "run query"),
		bind("Esc", "cancel running query"),
		bind("Alt+F", "format query"),
		blank,

		section("Explorer"),
		bind("Up / Dn", "move cursor"),
		bind("Enter / Right", "expand / drill in"),
		bind("Left", "collapse / go up"),
		bind("Space", "open command menu"),
		blank,

		section("Results"),
		bind("Up/Dn/Lt/Rt", "move cell cursor"),
		bind("PgUp / PgDn", "page rows"),
		bind("Home / End", "first / last row"),
		bind("Enter", "inspect cell"),
		bind("y / Y", "copy cell / row"),
		bind("Alt+A", "copy all as TSV"),
		bind("s", "cycle sort on column"),
		bind("/", "open filter prompt"),
		bind("w", "toggle wrap"),
		bind("Space", "open command menu"),
		blank,

		section("Results (error view)"),
		bind("Up/Dn/PgUp/PgDn/Home", "scroll"),
		bind("y / Alt+A", "copy error text"),
		blank,

		section("Space menu"),
		bind("c / x", "connect / disconnect"),
		bind("o", "open SQL file"),
		bind("e", "export results"),
		bind("h", "history"),
		bind("p", "explain plan"),
		bind("q", "quit"),
		bind("Esc", "cancel menu"),
	}
}

func (hl *helpLayer) Draw(a *app, c *cellbuf) {
	boxW := 72
	if boxW > a.term.width-dialogMargin {
		boxW = a.term.width - dialogMargin
	}
	if boxW < 40 {
		boxW = 40
	}
	boxH := a.term.height - dialogMargin
	if boxH > len(hl.lines)+5 {
		boxH = len(hl.lines) + 5
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
	drawFrame(c, r, "Keybindings", true)

	innerCol := col + 2
	innerW := boxW - 4
	bodyH := boxH - 3
	if bodyH < 1 {
		return
	}

	// Clamp scroll against the max offset that still leaves content visible.
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

	keyColW := 28
	if keyColW > innerW/2 {
		keyColW = innerW / 2
	}

	headerStyle := Style{FG: ansiBrightCyan, BG: ansiDefaultBG, Attrs: attrBold}
	keyStyle := Style{FG: ansiBrightYellow, BG: ansiDefaultBG}

	for i := 0; i < bodyH; i++ {
		idx := hl.scroll + i
		if idx >= len(hl.lines) {
			break
		}
		line := hl.lines[idx]
		if line.key == "" && line.desc == "" {
			continue
		}
		if line.key == "" {
			c.writeStyled(row+1+i, innerCol, truncate(line.desc, innerW), headerStyle)
			continue
		}
		c.writeStyled(row+1+i, innerCol, truncate(line.key, keyColW), keyStyle)
		descCol := innerCol + keyColW + 2
		descW := innerW - keyColW - 2
		if descW > 0 {
			c.writeAt(row+1+i, descCol, truncate(line.desc, descW))
		}
	}

	status := "Up/Dn=scroll  F1/Esc=close"
	c.writeAt(row+boxH-2, innerCol, truncate(status, innerW))
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
