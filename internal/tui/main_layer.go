package tui

import "fmt"

// FocusTarget identifies which panel owns keyboard input in the main view.
type FocusTarget int

const (
	FocusExplorer FocusTarget = iota
	FocusQuery
	FocusResults
)

func (f FocusTarget) String() string {
	switch f {
	case FocusExplorer:
		return "Explorer"
	case FocusQuery:
		return "Query"
	case FocusResults:
		return "Results"
	}
	return "?"
}

// Mode is the modal editing state of the Query panel.
type Mode int

const (
	ModeNormal Mode = iota
	ModeInsert
)

func (m Mode) String() string {
	switch m {
	case ModeNormal:
		return "NORMAL"
	case ModeInsert:
		return "INSERT"
	}
	return "?"
}

// mainLayer is the three-panel Explorer/Query/Results view. It is always
// layers[0] and is never popped. Its state (editor, table, focus, mode,
// status) is the main-view state of the app.
type mainLayer struct {
	editor       *editor
	table        *table
	explorer     *explorer
	focus        FocusTarget
	mode         Mode
	status       string
	pendingSpace bool
}

func newMainLayer() *mainLayer {
	m := &mainLayer{
		editor:   newEditor(),
		table:    newTable(),
		explorer: newExplorer(),
		focus:    FocusQuery,
		mode:     ModeNormal,
	}
	for _, r := range "SELECT @@VERSION AS version;" {
		m.editor.buf.Insert(r)
	}
	return m
}

func (m *mainLayer) Draw(a *app, c *cellbuf) {
	p := computeLayout(a.term.width, a.term.height)
	drawFrame(c, p.explorer, "Explorer", m.focus == FocusExplorer)
	drawFrame(c, p.query, m.queryTitle(), m.focus == FocusQuery)
	drawFrame(c, p.results, m.resultsTitle(), m.focus == FocusResults)

	// Place the editor cursor unconditionally when in insert mode. If an
	// overlay is stacked on top of us, its cell buffer will be the topmost
	// one during compositing and the main layer's cursor request gets
	// discarded automatically.
	editorCursorVisible := m.focus == FocusQuery && m.mode == ModeInsert
	m.explorer.draw(c, p.explorer, m.focus == FocusExplorer)
	m.editor.draw(c, p.query, editorCursorVisible)
	m.table.draw(c, p.results)

	c.setFg(colorStatusBar)
	c.writeAt(p.status.row, p.status.col, m.statusText(a, p.status.w))
	c.resetStyle()
}

func (m *mainLayer) HandleKey(a *app, k Key) {
	// Query cancellation and F5 are global to the main view.
	if k.Ctrl && k.Rune == 'c' {
		a.cancelQuery()
		return
	}
	if k.Kind == KeyF5 {
		a.runQuery()
		return
	}

	// Alt+1/2/3 is the global panel-switch shortcut. It fires before any
	// mode-specific routing so the user can switch out of INSERT mode
	// without reaching for Esc first — the letter keys in the editor all
	// stay available as literal input.
	if k.Alt && k.Kind == KeyRune {
		switch k.Rune {
		case '1':
			m.focus = FocusExplorer
			return
		case '2':
			m.focus = FocusQuery
			return
		case '3':
			m.focus = FocusResults
			return
		}
	}

	// Pending space-menu dispatch. The prefix is only recognized in NORMAL
	// mode (in INSERT, space is a literal character).
	if m.pendingSpace {
		m.pendingSpace = false
		m.handleSpace(a, k)
		return
	}

	// INSERT mode in the Query panel routes everything to the editor.
	if m.mode == ModeInsert && m.focus == FocusQuery {
		if k.Kind == KeyEsc {
			m.mode = ModeNormal
			return
		}
		m.editor.handleInsert(k)
		return
	}

	// NORMAL-mode space-menu prefix. Panel switching has moved to Alt+N
	// so letters stay free for mnemonic commands inside each panel.
	if k.Kind == KeyRune && !k.Ctrl && !k.Alt && k.Rune == ' ' {
		m.pendingSpace = true
		m.status = "-- SPACE --"
		return
	}

	switch m.focus {
	case FocusExplorer:
		m.handleExplorerKey(a, k)
	case FocusQuery:
		m.handleQueryNormalKey(a, k)
	case FocusResults:
		m.handleResultsKey(a, k)
	}
}

// handleExplorerKey processes keys when the Explorer panel is focused in
// NORMAL mode. Up/Down move, Enter expands a schema or prefills a SELECT
// for the highlighted table, and 's' does the same without needing Enter.
func (m *mainLayer) handleExplorerKey(a *app, k Key) {
	switch k.Kind {
	case KeyUp:
		m.explorer.MoveCursor(-1)
		return
	case KeyDown:
		m.explorer.MoveCursor(1)
		return
	case KeyPgUp:
		m.explorer.MoveCursor(-10)
		return
	case KeyPgDn:
		m.explorer.MoveCursor(10)
		return
	case KeyEnter:
		switch m.explorer.SelectedKind() {
		case itemSchema, itemSubgroup:
			m.explorer.Toggle()
		default:
			m.prefillSelectFromExplorer(a)
		}
		return
	}
	if k.Kind == KeyRune && !k.Ctrl {
		switch k.Rune {
		case 's':
			m.prefillSelectFromExplorer(a)
			return
		case 'R':
			a.loadSchema()
			return
		}
	}
}

// handleQueryNormalKey processes keys when the Query panel is focused in
// NORMAL mode. INSERT-mode keys are handled before this runs.
func (m *mainLayer) handleQueryNormalKey(a *app, k Key) {
	_ = a
	if k.Kind == KeyRune && !k.Ctrl {
		switch k.Rune {
		case 'i':
			m.mode = ModeInsert
		case 'd':
			m.editor.buf.Clear()
		}
	}
}

// handleResultsKey processes keys when the Results panel is focused. Scroll
// keys move the viewport; 'w' toggles between truncate and wrap rendering.
func (m *mainLayer) handleResultsKey(a *app, k Key) {
	_ = a
	switch k.Kind {
	case KeyUp:
		m.table.ScrollBy(-1)
		return
	case KeyDown:
		m.table.ScrollBy(1)
		return
	case KeyPgUp:
		m.table.ScrollBy(-10)
		return
	case KeyPgDn:
		m.table.ScrollBy(10)
		return
	case KeyHome:
		m.table.ScrollBy(-1 << 30)
		return
	case KeyEnd:
		m.table.ScrollBy(1 << 30)
		return
	}
	if k.Kind == KeyRune && !k.Ctrl && k.Rune == 'w' {
		m.table.ToggleWrap()
	}
}

// prefillSelectFromExplorer writes a driver-aware SELECT for the highlighted
// table into the editor and moves focus to Query. No-op if nothing selectable
// is under the cursor.
func (m *mainLayer) prefillSelectFromExplorer(a *app) {
	t, ok := m.explorer.Selected()
	if !ok {
		return
	}
	driverName := ""
	if a.conn != nil {
		driverName = a.conn.Driver()
	}
	m.editor.buf.SetText(BuildSelect(driverName, t, 100))
	m.focus = FocusQuery
	m.mode = ModeNormal
}

// handleSpace dispatches the second key of the space-menu prefix.
func (m *mainLayer) handleSpace(a *app, k Key) {
	if k.Kind == KeyEsc {
		m.status = ""
		return
	}
	if k.Kind != KeyRune {
		m.status = ""
		return
	}
	switch k.Rune {
	case 'c':
		a.pushLayer(newPickerLayer(a.confFile.Connections))
		m.status = ""
	case 'x':
		a.disconnect()
		m.status = "disconnected"
	case 'q':
		a.quit = true
	default:
		m.status = ""
	}
}

func (m *mainLayer) queryTitle() string {
	if m.focus == FocusQuery {
		return "Query  " + m.mode.String()
	}
	return "Query"
}

func (m *mainLayer) resultsTitle() string {
	if m.table.Wrap() {
		return "Results  [wrap]"
	}
	return "Results"
}

func (m *mainLayer) statusText(a *app, width int) string {
	conn := "(not connected)"
	if a.activeConn != nil {
		conn = a.activeConn.Name
	}
	msg := m.status
	if msg == "" {
		msg = m.focusHints()
	}
	s := fmt.Sprintf(" [%s %s]  %s  |  %s", m.mode, m.focus, conn, msg)
	if len(s) > width {
		s = s[:width]
	}
	return s
}

// focusHints returns the keybind hint line for the currently focused panel.
// Global prefixes (Alt+N panel switch, <space> menu, Ctrl+Q) are shown
// everywhere; the rest is panel-specific so the hint line stays short.
func (m *mainLayer) focusHints() string {
	global := "Alt+1/2/3=focus <space>=menu Ctrl+Q=quit"
	switch m.focus {
	case FocusExplorer:
		return "Enter/s=SELECT R=refresh " + global
	case FocusQuery:
		if m.mode == ModeInsert {
			return "Esc=normal F5=run Ctrl+C=cancel Alt+1/2/3=focus"
		}
		return "i=insert d=clear F5=run " + global
	case FocusResults:
		return "Up/Dn/PgUp/PgDn=scroll Home/End w=wrap " + global
	}
	return global
}
