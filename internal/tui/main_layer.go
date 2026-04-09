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
	focus        FocusTarget
	mode         Mode
	status       string
	pendingSpace bool
}

func newMainLayer() *mainLayer {
	m := &mainLayer{
		editor: newEditor(),
		table:  newTable(),
		focus:  FocusQuery,
		mode:   ModeNormal,
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
	drawFrame(c, p.results, "Results", m.focus == FocusResults)

	// Place the editor cursor unconditionally when in insert mode. If an
	// overlay is stacked on top of us, its cell buffer will be the topmost
	// one during compositing and the main layer's cursor request gets
	// discarded automatically.
	editorCursorVisible := m.focus == FocusQuery && m.mode == ModeInsert
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

	// NORMAL mode: panel switching and commands.
	if k.Kind == KeyRune && !k.Ctrl {
		switch k.Rune {
		case ' ':
			m.pendingSpace = true
			m.status = "-- SPACE --"
			return
		case 'e':
			m.focus = FocusExplorer
			return
		case 'q':
			m.focus = FocusQuery
			return
		case 'r':
			m.focus = FocusResults
			return
		case 'i':
			if m.focus == FocusQuery {
				m.mode = ModeInsert
			}
			return
		case 'd':
			if m.focus == FocusQuery {
				m.editor.buf.Clear()
			}
			return
		}
	}

	// Results scrolling.
	if m.focus == FocusResults {
		switch k.Kind {
		case KeyUp:
			m.table.ScrollBy(-1)
		case KeyDown:
			m.table.ScrollBy(1)
		case KeyPgUp:
			m.table.ScrollBy(-10)
		case KeyPgDn:
			m.table.ScrollBy(10)
		}
	}
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

func (m *mainLayer) statusText(a *app, width int) string {
	conn := "(not connected)"
	if a.activeConn != nil {
		conn = a.activeConn.Name
	}
	msg := m.status
	if msg == "" {
		msg = "i=insert Esc=normal F5=run <space>c=connect Ctrl+Q=quit"
	}
	s := fmt.Sprintf(" [%s %s]  %s  |  %s", m.mode, m.focus, conn, msg)
	if len(s) > width {
		s = s[:width]
	}
	return s
}
