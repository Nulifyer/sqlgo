package tui

import (
	"strings"

	"github.com/Nulifyer/sqlgo/internal/search/fzfmatch"
)

// helpLayer is a modal overlay listing every keybind, grouped by
// context (global / Query / Explorer / Results / Command menu). It is
// opened by F1 from anywhere and closed by F1 or Esc. The contents
// are a static table; when a binding changes it must be updated here
// too.
type helpLayer struct {
	all    []helpLine
	lines  []helpLine
	search *input
	scroll int
}

// helpLine is one rendered row. Section rows have key == "" and are
// drawn as section headers; blank rows have both fields empty.
type helpLine struct {
	key  string
	desc string
}

func newHelpLayer() *helpLayer {
	hl := &helpLayer{all: helpContent(), search: newInput("")}
	hl.refilter()
	return hl
}

func (hl *helpLayer) refilter() {
	hl.lines = filterHelpLines(hl.all, hl.search.String())
	if hl.scroll >= len(hl.lines) {
		hl.scroll = len(hl.lines) - 1
	}
	if hl.scroll < 0 {
		hl.scroll = 0
	}
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

		section("Help overlay"),
		bind("type", "search help"),
		bind("Backspace / Delete", "edit search"),
		bind("Ctrl+L", "clear search"),
		bind("↑ / ↓ / PgUp / PgDn", "scroll"),
		bind("Home / End", "scroll to top / bottom"),
		bind("F1 / Esc", "close"),
		blank,

		section("Key debug (F8)"),
		bind("any key / mouse", "mark matching bind as verified"),
		bind("Ctrl+R", "reset checklist"),
		bind("F8", "close"),
		bind("Ctrl+Q / F1", "reserved globals (shown, not capturable here)"),
		blank,

		section("Query tabs"),
		bind("Ctrl+T", "new tab"),
		bind("Ctrl+W", "close tab"),
		bind("Ctrl+S", "save tab"),
		bind("Ctrl+R", "rename tab"),
		bind("Ctrl+PgUp / PgDn", "prev / next tab (Query focus)"),
		bind("Left-click tab", "switch"),
		bind("Double-click tab", "rename"),
		bind("Middle-click tab", "close"),
		blank,

		section("Query editor"),
		bind("F5", "run query"),
		bind("F9", "explain plan"),
		bind("Ctrl+O", "open SQL file"),
		bind("Ctrl+S", "save tab"),
		bind("Alt+S", "save as"),
		bind("Alt+D", "set active database (per tab)"),
		bind("Alt+F", "format buffer"),
		bind("Ctrl+␣", "autocomplete"),
		bind("Ctrl+F", "find / replace"),
		bind("Ctrl+G", "go to line"),
		bind("Ctrl+L", "clear buffer"),
		bind("Ctrl+Z / Y", "undo / redo"),
		bind("Ctrl+X / C / V", "cut / copy / paste"),
		bind("Ctrl+A", "select all"),
		bind("⇥ / ⇤", "indent / dedent"),
		bind("Ctrl+Alt+↑ / ↓", "add cursor above / below"),
		bind("Alt+↑ / ↓", "move line up / down"),
		bind("Shift+Alt+↑ / ↓", "duplicate line up / down"),
		bind("Esc", "collapse multi-cursor"),
		bind("Ctrl+D", "select word under cursor"),
		bind("Ctrl+U", "clear selection"),
		bind("Home", "smart home (toggle indent / col 0)"),
		bind("↑ / ↓ / ← / →, End", "move caret"),
		bind("Ctrl+← / →", "word jump"),
		bind("Ctrl+Home / End", "buffer start / end"),
		bind("Ctrl+Backspace / Delete", "delete word left / right"),
		bind("Shift+↑ / ↓ / ← / → / Home / End", "extend selection"),
		bind("Ctrl+Shift+← / →", "extend selection by word"),
		bind("Ctrl+Shift+Home / End", "extend selection to buffer start / end"),
		blank,

		section("Find / Replace"),
		bind("type", "edit active field"),
		bind("⇥", "toggle Find / Replace field"),
		bind("↵", "next match (or replace current when on Replace)"),
		bind("⇤", "previous match"),
		bind("Ctrl+R", "replace all"),
		bind("Esc", "close"),
		blank,

		section("Go To"),
		bind("type line[:col]", "edit target"),
		bind("↵", "jump"),
		bind("Esc", "cancel"),
		blank,

		section("Rename Tab"),
		bind("type name", "edit title"),
		bind("↵", "save"),
		bind("Esc", "cancel"),
		blank,

		section("Explorer"),
		bind("Ctrl+F", "search object names"),
		bind("F", "deep-search unloaded columns in loaded scope"),
		bind("Esc", "close search"),
		bind("↵ / s", "SELECT from table / view"),
		bind("a", "open SELECT / INSERT / UPDATE / DELETE actions"),
		bind("d", "open table design"),
		bind("␣", "expand database / schema / group / table columns"),
		bind("← / →", "collapse / expand or move parent / child"),
		bind("↵", "expand folders; edit DDL for routines / triggers"),
		bind("e", "edit DDL for view / routine / trigger"),
		bind("y", "copy qualified object name"),
		bind("u", "pin active database to cursor"),
		bind("↑ / ↓ / PgUp / PgDn", "move cursor"),
		bind("R", "refresh schema / database"),
		bind("Ctrl+K", "command menu"),
		blank,

		section("Active Database Picker"),
		bind("type", "filter databases"),
		bind("↑ / ↓", "move"),
		bind("↵", "use selected database"),
		bind("Esc", "cancel"),
		blank,

		section("Results"),
		bind("Ctrl+C", "cancel running query"),
		bind("Ctrl+F", "filter rows"),
		bind("↑ / ↓ / ← / →", "move cell cursor"),
		bind("PgUp / PgDn", "page rows"),
		bind("Home / End", "first / last row"),
		bind("↵", "inspect cell"),
		bind("y / Y", "copy cell / row"),
		bind("Alt+A", "copy all (TSV)"),
		bind("Alt+Shift+A", "copy all (Markdown)"),
		bind("s", "cycle sort"),
		bind("w", "toggle wrap"),
		bind("Left-click result tab", "switch result set"),
		bind("Ctrl+PgUp / PgDn", "prev / next result set"),
		bind("Ctrl+E", "export results"),
		bind("Shift+double-click", "copy row"),
		bind("Ctrl+K", "command menu"),
		blank,

		section("Results (error view)"),
		bind("↑ / ↓ / PgUp / PgDn", "scroll"),
		bind("Home / End", "scroll to top / bottom"),
		bind("y / Y / Alt+A", "copy error text"),
		blank,

		section("Results Filter"),
		bind("type", "edit filter"),
		bind("↵", "keep filtered results"),
		bind("Esc", "clear filter + close"),
		blank,

		section("Cell inspector"),
		bind("↑ / ↓ / PgUp / PgDn", "scroll"),
		bind("Home / End", "scroll to top / bottom"),
		bind("y", "copy cell"),
		bind("Esc", "close"),
		blank,

		section("Table design"),
		bind("↑ / ↓ / PgUp / PgDn", "scroll columns"),
		bind("Home / End", "scroll to top / bottom"),
		bind("y", "copy selected column"),
		bind("Esc", "close"),
		blank,

		section("EXPLAIN overlay"),
		bind("↑ / ↓ / PgUp / PgDn", "move selection"),
		bind("Home / End", "first / last node"),
		bind("␣", "toggle collapse node"),
		bind("r", "toggle raw output"),
		bind("Esc", "close"),
		blank,

		section("Open SQL"),
		bind("⇥ / ⇤", "next / previous field"),
		bind("↑ / ↓ / PgUp / PgDn", "move file list"),
		bind("␣", "mark file for multi-open"),
		bind("↵", "descend / pick / open"),
		bind("Esc", "cancel"),
		blank,

		section("Save As"),
		bind("⇥ / ⇤", "next / previous field"),
		bind("↑ / ↓", "move list / cycle extension"),
		bind("↵", "descend / pick / save / overwrite"),
		bind("Esc", "cancel"),
		blank,

		section("Export Results"),
		bind("⇥ / ⇤", "next / previous field"),
		bind("↑ / ↓", "move list / cycle format"),
		bind("↵", "descend / pick / save / overwrite"),
		bind("Esc", "cancel / close"),
		blank,

		section("Query history"),
		bind("↑ / ↓ / PgUp / PgDn", "move selection"),
		bind("type", "search filter"),
		bind("↵", "paste into editor"),
		bind("d", "delete entry"),
		bind("X", "clear all (two-press)"),
		bind("⇥", "toggle scope (this conn / all)"),
		bind("Esc", "close"),
		blank,

		section("Connection Picker"),
		bind("↑ / ↓", "move"),
		bind("↵", "connect"),
		bind("a / e / x", "add / edit / delete connection"),
		bind("K", "unlink keyring entry"),
		bind("Esc", "back"),
		blank,

		section("Connection Form"),
		bind("⇥ / ↓", "next field"),
		bind("⇤ / ↑", "previous field"),
		bind("← / →", "cycle selected choice"),
		bind("↵", "pick driver / submit"),
		bind("Ctrl+T", "test network (in edit form)"),
		bind("Ctrl+L", "test auth (in edit form)"),
		bind("Ctrl+S", "save form"),
		bind("Esc", "cancel"),
		blank,

		section("Driver / Transport Picker"),
		bind("type", "filter"),
		bind("↑ / ↓", "move"),
		bind("↵", "select"),
		bind("Esc", "cancel"),
		blank,

		section("Safety prompts"),
		bind("confirm run", "y=run   n / Esc=cancel   ⇥ / ← / →=switch   ↵=confirm"),
		bind("SSH trust", "y=trust   n / Esc=reject   ↵=arm / confirm"),
		blank,

		section("Command menu (Ctrl+K)"),
		bind("c / x", "connect / disconnect"),
		bind("h", "history"),
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
	r := rect{Row: row, Col: col, W: boxW, H: boxH}
	c.FillRect(r)
	drawFrame(c, r, "Keybindings", true)

	innerCol := col + 3
	innerW := boxW - 6

	searchRow := row + 1
	c.SetFg(colorTitleUnfocused)
	c.WriteAt(searchRow, innerCol, "Search:")
	c.ResetStyle()
	searchCol := innerCol + 8
	searchW := innerW - 8
	if searchW < 1 {
		searchW = 1
	}
	drawInput(c, hl.search, searchRow, searchCol, searchW)

	dimStyle := Style{FG: ansiBrightBlack, BG: ansiDefaultBG}
	c.WriteStyled(row+2, innerCol, truncate(repeatRune('-', innerW), innerW), dimStyle)

	// Leave search row (1), separator (1), footer separator (1) and
	// footer line (1), plus padding around the frame.
	bodyTop := row + 3
	bodyH := boxH - 6
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

	if len(hl.lines) == 0 {
		c.WriteStyled(bodyTop, innerCol, "(no matches)", dimStyle)
	} else {
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
				c.WriteStyled(y, innerCol, truncate(line.desc, innerW), headerStyle)
				continue
			}
			c.WriteStyled(y, innerCol, truncate(line.key, keyColW), keyStyle)
			descCol := innerCol + keyColW + gap
			descW := innerW - keyColW - gap
			if descW > 0 {
				c.WriteStyled(y, descCol, truncate(line.desc, descW), dimStyle)
			}
		}
	}

	// Footer separator + status line.
	sepRow := row + boxH - 3
	c.WriteStyled(sepRow, innerCol, truncate(repeatRune('-', innerW), innerW), dimStyle)
	status := "type=search  ↑/↓/PgUp/PgDn=scroll  Ctrl+L=clear  F1/Esc=close"
	c.WriteAt(row+boxH-2, innerCol, truncate(status, innerW))
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
	case KeyRune:
		if k.Ctrl && k.Rune == 'l' {
			hl.search.SetString("")
			hl.scroll = 0
			hl.refilter()
			return
		}
	case KeyUp:
		hl.scroll--
		return
	case KeyDown:
		hl.scroll++
		return
	case KeyPgUp:
		hl.scroll -= 10
		return
	case KeyPgDn:
		hl.scroll += 10
		return
	case KeyHome:
		hl.scroll = 0
		return
	case KeyEnd:
		hl.scroll = len(hl.lines)
		return
	}
	if hl.search.Handle(k) {
		hl.scroll = 0
		hl.refilter()
	}
}

func (hl *helpLayer) HandleInput(a *app, msg InputMsg) bool {
	p, ok := msg.(PasteMsg)
	if !ok {
		return false
	}
	if hl.search.PasteText(p.Text) {
		hl.scroll = 0
		hl.refilter()
	}
	return true
}

func (hl *helpLayer) Hints(a *app) string {
	_ = a
	return joinHints("type=search", "↑/↓/PgUp/PgDn=scroll", "Ctrl+L=clear", "F1/Esc=close")
}

func filterHelpLines(lines []helpLine, query string) []helpLine {
	q := strings.TrimSpace(query)
	if q == "" {
		return append([]helpLine(nil), lines...)
	}

	var out []helpLine
	for i := 0; i < len(lines); {
		for i < len(lines) && lines[i].key == "" && lines[i].desc == "" {
			i++
		}
		if i >= len(lines) {
			break
		}
		if lines[i].key != "" {
			if helpLineMatches(q, "", lines[i]) {
				out = append(out, lines[i])
			}
			i++
			continue
		}

		section := lines[i]
		sectionName := section.desc
		sectionMatches := helpLineMatches(q, "", section)
		i++

		var matches []helpLine
		for i < len(lines) {
			line := lines[i]
			if line.key == "" && line.desc != "" {
				break
			}
			i++
			if line.key == "" && line.desc == "" {
				continue
			}
			if sectionMatches || helpLineMatches(q, sectionName, line) {
				matches = append(matches, line)
			}
		}
		if len(matches) == 0 {
			continue
		}
		if len(out) > 0 {
			out = append(out, helpLine{})
		}
		out = append(out, section)
		out = append(out, matches...)
	}
	return out
}

func helpLineMatches(query, section string, line helpLine) bool {
	candidates := []string{line.key, line.desc}
	if section != "" {
		candidates = append(candidates, section+" "+line.key+" "+line.desc)
	}
	_, _, ok := fzfmatch.BestMatch(query, candidates...)
	return ok
}
