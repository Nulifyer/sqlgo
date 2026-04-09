package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/Nulifyer/sqlgo/internal/db"
)

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

// mainLayer is the three-panel Explorer/Query/Results view. It is always
// layers[0] and is never popped. Its state (editor, table, focus, status)
// is the main-view state of the app.
//
// There is no NORMAL/INSERT mode — the Query panel is always a live text
// editor. Panel focus switches are bound to Alt+1/2/3 so every printable
// key stays available to the editor.
type mainLayer struct {
	editor       *editor
	table        *table
	explorer     *explorer
	focus        FocusTarget
	status       string // transient query feedback ("running...", "3 row(s) in 12ms"); never replaces the hint line
	pendingSpace bool

	// Last-query summary surfaced on the results panel's top-right
	// border. lastHasResult is the gate: zero on startup / after a
	// disconnect so no stale "0 rows / 0ms" shows up before any query.
	lastRowCount  int
	lastElapsed   time.Duration
	lastHasResult bool
	lastCapped    bool
	lastErr       string
}

func newMainLayer() *mainLayer {
	m := &mainLayer{
		editor:   newEditor(),
		table:    newTable(),
		explorer: newExplorer(),
		focus:    FocusQuery,
	}
	for _, r := range "SELECT @@VERSION AS version;" {
		m.editor.buf.Insert(r)
	}
	return m
}

func (m *mainLayer) Draw(a *app, c *cellbuf) {
	p := computeLayout(a.term.width, a.term.height)
	drawFrame(c, p.explorer, "Explorer", m.focus == FocusExplorer)
	drawFrame(c, p.query, "Query", m.focus == FocusQuery)
	drawFrameInfo(c, p.results, m.resultsTitle(), m.resultsRightInfo(a), m.focus == FocusResults)

	// Show the editor cursor whenever the Query panel is focused. If an
	// overlay is stacked on top of us, its cell buffer will be the topmost
	// one during compositing and the main layer's cursor request gets
	// discarded automatically.
	m.explorer.draw(c, p.explorer, m.focus == FocusExplorer)
	m.editor.draw(c, p.query, m.focus == FocusQuery)
	m.table.draw(c, p.results)

	// Bottom status bar reflects the topmost layer's hints, so modal
	// overlays can show their own keys here without touching the main
	// view's hint logic.
	c.setFg(colorStatusBar)
	c.writeAt(p.status.row, p.status.col, m.statusText(a, p.status.w))
	c.resetStyle()
}

func (m *mainLayer) HandleKey(a *app, k Key) {
	// Ctrl+C cancels a running query. When no query is running, it
	// falls through so the editor can use Ctrl+C as "copy selection"
	// without stealing it back from the cancel binding.
	if k.Ctrl && k.Rune == 'c' && a.running {
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

	// Pending space-menu dispatch. Only reachable from Explorer/Results
	// focus (space is a literal character in the Query editor).
	if m.pendingSpace {
		m.pendingSpace = false
		m.handleSpace(a, k)
		return
	}

	// Query panel is non-modal: every keystroke goes straight to the
	// editor. The editor ignores Ctrl+<rune> combos so global shortcuts
	// like Ctrl+L (clear) can still be handled below if needed.
	if m.focus == FocusQuery {
		if k.Ctrl && k.Rune == 'l' {
			m.editor.buf.Clear()
			return
		}
		m.editor.handleInsert(a, k)
		return
	}

	// Explorer/Results focus: space opens the command menu. The footer
	// hint line flips to spaceMenuHints() automatically via Hints().
	if k.Kind == KeyRune && !k.Ctrl && !k.Alt && k.Rune == ' ' {
		m.pendingSpace = true
		return
	}

	switch m.focus {
	case FocusExplorer:
		m.handleExplorerKey(a, k)
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

// handleResultsKey processes keys when the Results panel is focused.
// Navigation moves the cell cursor (Up/Dn/Lt/Rt); PgUp/PgDn page the
// row cursor. Home/End jump to the first/last row. 'w' toggles wrap.
// 's' cycles sort state on the current column; '/' opens the filter
// prompt; 'y' / 'Y' copy cell / row to the system clipboard; Enter
// opens the cell inspector.
func (m *mainLayer) handleResultsKey(a *app, k Key) {
	switch k.Kind {
	case KeyUp:
		m.table.MoveCellBy(-1, 0)
		return
	case KeyDown:
		m.table.MoveCellBy(1, 0)
		return
	case KeyLeft:
		m.table.MoveCellBy(0, -1)
		return
	case KeyRight:
		m.table.MoveCellBy(0, 1)
		return
	case KeyPgUp:
		m.table.MoveCellPage(-1)
		return
	case KeyPgDn:
		m.table.MoveCellPage(1)
		return
	case KeyHome:
		m.table.MoveCellHome()
		return
	case KeyEnd:
		m.table.MoveCellEnd()
		return
	case KeyEnter:
		if m.table.HasColumns() {
			a.pushLayer(newInspectorLayer(m.table.CursorColumn().Name, m.table.CursorCell()))
		}
		return
	}
	if k.Kind != KeyRune || k.Ctrl {
		return
	}
	switch k.Rune {
	case 'w':
		m.table.ToggleWrap()
	case 's':
		col, desc, active := m.table.CycleSortAtCursor()
		if !active {
			m.status = "sort cleared"
		} else {
			dir := "asc"
			if desc {
				dir = "desc"
			}
			name := ""
			if col >= 0 {
				name = m.table.CursorColumn().Name
			}
			m.status = fmt.Sprintf("sort: %s %s", name, dir)
		}
	case '/':
		a.pushLayer(newFilterLayer(m.table.Filter()))
	case 'y':
		if !m.table.HasColumns() {
			return
		}
		cell := m.table.CursorCell()
		if err := a.clipboard.Copy(cell); err != nil {
			m.status = "copy: " + err.Error()
		} else {
			m.status = fmt.Sprintf("copied cell (%d chars)", len(cell))
		}
	case 'Y':
		if !m.table.HasColumns() {
			return
		}
		row := m.table.CursorRow()
		line := strings.Join(row, "\t")
		if err := a.clipboard.Copy(line); err != nil {
			m.status = "copy: " + err.Error()
		} else {
			m.status = fmt.Sprintf("copied row (%d cells)", len(row))
		}
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
	var caps db.Capabilities
	if a.conn != nil {
		caps = a.conn.Capabilities()
	}
	m.editor.buf.SetText(BuildSelect(caps, t, 100))
	m.focus = FocusQuery
}

// handleSpace dispatches the second key of the space-menu prefix.
func (m *mainLayer) handleSpace(a *app, k Key) {
	if k.Kind != KeyRune {
		return
	}
	switch k.Rune {
	case 'c':
		// Refresh from store on open so stale in-memory state from a
		// background import/export can't shadow the latest list.
		if err := a.refreshConnections(); err != nil {
			m.status = "load connections: " + err.Error()
			return
		}
		a.pushLayer(newPickerLayer(a.connCache))
	case 'x':
		a.disconnect()
	case 'e':
		// Export is only meaningful with a live result buffer. Silently
		// ignoring on an empty buffer matches how the space menu treats
		// the rest of the actions -- the hint line already hides the
		// key in that state.
		if !m.table.HasColumns() {
			m.status = "nothing to export"
			return
		}
		a.pushLayer(newExportLayer("results.csv"))
	case 'h':
		hl := newHistoryLayer()
		hl.reload(a)
		a.pushLayer(hl)
	case 'q':
		a.quit = true
	}
}

func (m *mainLayer) resultsTitle() string {
	if m.table.Wrap() {
		return "Results  [wrap]"
	}
	return "Results"
}

// resultsRightInfo builds the top-right border label on the results panel.
// While a query is running it streams the live row count; once a query
// finishes the final row count + elapsed time stays pinned until the next
// run. Errors collapse to a short tag so the border doesn't grow.
func (m *mainLayer) resultsRightInfo(a *app) string {
	if a.running {
		return fmt.Sprintf("streaming %d rows", m.table.RowCount())
	}
	if !m.lastHasResult {
		return ""
	}
	if m.lastErr != "" {
		return "error"
	}
	suffix := ""
	if m.lastCapped {
		suffix = " (capped)"
	}
	return fmt.Sprintf("%d rows / %s%s", m.lastRowCount, m.lastElapsed.Round(time.Millisecond), suffix)
}

// statusText builds the footer line. Layout:
//
//	 [focus]  connection  |  <hints from topmost layer>    (<transient status>)
//
// Hints come first so critical keys (Ctrl+Q=quit, Alt+1/2/3=focus) survive
// right-edge truncation on narrow terminals. The parenthesized status is
// query feedback like "running..." or "3 row(s) in 12ms" and is allowed to
// be clipped because the Results panel itself shows the real outcome.
func (m *mainLayer) statusText(a *app, width int) string {
	conn := "(not connected)"
	if a.activeConn != nil {
		conn = a.activeConn.Name
	}
	hints := a.topLayer().Hints(a)
	s := fmt.Sprintf(" [%s]  %s  |  %s", m.focus, conn, hints)
	if m.status != "" {
		s += "    (" + m.status + ")"
	}
	if len(s) > width {
		s = s[:width]
	}
	return s
}

// Hints is the Layer interface entry point for mainLayer. It dispatches on
// the pendingSpace prefix and the focused panel, letting each branch build
// a context-aware line that hides keys that wouldn't currently do anything.
func (m *mainLayer) Hints(a *app) string {
	if m.pendingSpace {
		return m.spaceMenuHints(a)
	}
	switch m.focus {
	case FocusExplorer:
		return m.explorerHints(a)
	case FocusQuery:
		return m.queryHints(a)
	case FocusResults:
		return m.resultsHints(a)
	}
	return joinHints("Ctrl+Q=quit", hintAlwaysFocus())
}

// hintAlwaysFocus is the universal panel-switch hint shown on every line.
func hintAlwaysFocus() string { return "Alt+1/2/3=focus" }

// joinHints concatenates non-empty pieces with two spaces between them.
// Empty strings are dropped so callers can write `hint(cond, "...")`
// helpers and pass their results straight in.
func joinHints(parts ...string) string {
	out := ""
	for _, p := range parts {
		if p == "" {
			continue
		}
		if out == "" {
			out = p
		} else {
			out += "  " + p
		}
	}
	return out
}

// hintIf returns h when cond is true, "" otherwise. Keeps the branches in
// Hints builders readable.
func hintIf(cond bool, h string) string {
	if cond {
		return h
	}
	return ""
}

func (m *mainLayer) explorerHints(a *app) string {
	connected := a.conn != nil
	selectHint := ""
	switch m.explorer.SelectedKind() {
	case itemTable, itemView:
		selectHint = "Enter/s=SELECT"
	case itemSchema, itemSubgroup:
		selectHint = "Enter=expand"
	}
	return joinHints(
		"Ctrl+Q=quit",
		hintAlwaysFocus(),
		hintIf(len(m.explorer.items) > 0, "Up/Dn/PgUp/PgDn=move"),
		selectHint,
		hintIf(connected, "R=refresh"),
		"Space=menu",
	)
}

func (m *mainLayer) queryHints(a *app) string {
	connected := a.conn != nil
	running := a.running
	hasText := m.editor.buf.LineCount() > 1 || len(m.editor.buf.Line(0)) > 0
	hasSel := m.editor.buf.HasSelection()
	return joinHints(
		"Ctrl+Q=quit",
		hintAlwaysFocus(),
		hintIf(connected && !running, "F5=run"),
		hintIf(running, "Ctrl+C=cancel"),
		hintIf(hasSel, "Ctrl+C/X=copy/cut"),
		hintIf(!hasSel, "Ctrl+V=paste"),
		"Ctrl+Z/Y=undo/redo",
		hintIf(hasText, "Ctrl+L=clear"),
	)
}

func (m *mainLayer) resultsHints(a *app) string {
	_ = a
	hasRows := m.table.RowCount() > 0
	hasCols := m.table.HasColumns()
	return joinHints(
		"Ctrl+Q=quit",
		hintAlwaysFocus(),
		hintIf(hasRows, "Up/Dn/Lt/Rt=cell"),
		hintIf(hasRows, "Enter=inspect"),
		hintIf(hasRows, "y=cell Y=row"),
		hintIf(hasRows, "s=sort"),
		hintIf(hasCols, "/=filter"),
		hintIf(hasCols, "w=wrap"),
		"Space=menu",
	)
}

func (m *mainLayer) spaceMenuHints(a *app) string {
	return joinHints(
		"c=connect",
		hintIf(a.conn != nil, "x=disconnect"),
		hintIf(m.table.HasColumns(), "e=export"),
		"h=history",
		"q=quit",
		"Esc=cancel",
	)
}
