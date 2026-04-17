package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/output"
	"github.com/Nulifyer/sqlgo/internal/sqltok"
	"github.com/Nulifyer/sqlgo/internal/tui/widget"
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
	// session is the active query frame state. When there are visible
	// query tabs it points at the active tab's state; when there are no
	// tabs it points at a detached frame state so promoted fields keep
	// m.editor / m.table / m.lastErr working without forcing every call
	// site to nil-check. Creating the first tab promotes that detached
	// state into sessions[0].
	*session

	// sessions is the ordered list of visible query tabs; activeTab
	// indexes into it. Each session owns its own editor + result tabs +
	// runner state so a long query in one tab doesn't stall another.
	// When the list is empty the Query pane still exists, but it's just
	// the detached frame state above rather than a visible tab.
	sessions  []*session
	activeTab int

	explorer      *explorer
	focus         FocusTarget
	pendingMenu   bool
	pendingMenuID int

	// editorFullscreen hides the explorer and results panels and
	// expands the query editor to fill the terminal (minus the status
	// line). Toggled by F11. When on, focus is locked to FocusQuery
	// so Alt+1/3 silently no-op.
	editorFullscreen bool

	// clicks tracks click counts for double/triple detection across the
	// three panels. Shared because a click outside the last-clicked panel
	// naturally resets the count (coordinate differs).
	clicks clickTracker

	// dragTarget is the panel that is currently tracking a held
	// left-button drag (FocusQuery today; -1 when idle). Motion events
	// route to the editor only while this is set so wandering off the
	// panel mid-drag still extends the selection instead of scrolling
	// something else.
	dragTarget FocusTarget

	// ddlBusy gates the Explorer 'e' action while a Definition fetch
	// is in flight, so a repeat press can't stack duplicate goroutines
	// or open duplicate tabs when the DB is slow.
	ddlBusy bool
}

// View turns on bracketed paste so multi-line SQL pasted into the
// editor arrives as one PasteMsg (and thus one undo snapshot) instead
// of a flood of KeyRune/KeyEnter events. Alt-screen stays on as usual.
func (m *mainLayer) View(a *app) View {
	return View{AltScreen: true, PasteEnabled: true, MouseEnabled: true}
}

// HandleInput routes a PasteMsg into the editor buffer when the Query
// panel has focus. Other non-Key events are ignored -- we don't use
// mouse or focus events yet. Returning false is harmless; the caller
// only reads the return value for consumption tracking.
func (m *mainLayer) HandleInput(a *app, msg InputMsg) bool {
	switch v := msg.(type) {
	case PasteMsg:
		if m.focus == FocusQuery && v.Text != "" {
			hadDetachedFrame := len(m.sessions) == 0
			beforeRev := m.editor.buf.Revision()
			m.editor.buf.InsertText(v.Text)
			if m.editor.buf.Revision() != beforeRev {
				m.editor.ClearErrorLocation()
			}
			if hadDetachedFrame && m.editor.buf.Revision() != beforeRev {
				m.ensureActiveTab()
			}
			m.promoteActiveIfPreview()
			return true
		}
	case MouseMsg:
		return m.handleMouse(a, v)
	}
	return false
}

// handleMouse routes mouse events to panels via a rect hit test. Left
// press sets focus to whichever panel the click lands in; wheel scrolls
// the panel under the pointer without changing focus. Fullscreen mode
// collapses everything to the Query panel, so clicks there just keep
// focus on Query and wheel drives the editor.
func (m *mainLayer) handleMouse(a *app, msg MouseMsg) bool {
	if m.editorFullscreen {
		switch msg.Button {
		case MouseButtonWheelUp:
			m.wheelQuery(a, -3)
		case MouseButtonWheelDown:
			m.wheelQuery(a, 3)
		case MouseButtonLeft:
			r := rect{Row: 1, Col: 1, W: a.term.width, H: a.term.height - statusBarH}
			if msg.Action == MouseActionPress {
				count := m.clicks.bump(msg)
				m.editor.clickAt(r, msg.Y, msg.X, count)
				m.dragTarget = FocusQuery
			} else if msg.Action == MouseActionMotion && m.dragTarget == FocusQuery {
				m.editor.dragTo(r, msg.Y, msg.X)
			} else if msg.Action == MouseActionRelease {
				m.dragTarget = -1
			}
		}
		return true
	}
	// Drag in progress: route motion directly to the drag target even
	// when the pointer has left the panel, so the selection grows
	// instead of stalling at the panel edge.
	if m.dragTarget == FocusQuery && msg.Button == MouseButtonLeft {
		switch msg.Action {
		case MouseActionMotion:
			p := computeLayout(a.term.width, a.term.height)
			m.editor.dragTo(p.query, msg.Y, msg.X)
			return true
		case MouseActionRelease:
			m.dragTarget = -1
			return true
		}
	}
	p := computeLayout(a.term.width, a.term.height)
	var target FocusTarget = -1
	switch {
	case p.explorer.Contains(msg.Y, msg.X):
		target = FocusExplorer
	case p.query.Contains(msg.Y, msg.X):
		target = FocusQuery
	case p.results.Contains(msg.Y, msg.X):
		target = FocusResults
	}
	if target < 0 {
		return false
	}
	switch msg.Button {
	case MouseButtonLeft:
		switch msg.Action {
		case MouseActionPress:
			m.focus = target
			m.pendingMenu = false
			count := m.clicks.bump(msg)
			m.handleLeftClick(a, target, msg, count)
			if target == FocusQuery {
				m.dragTarget = FocusQuery
			}
		case MouseActionRelease:
			m.dragTarget = -1
		}
	case MouseButtonMiddle:
		if msg.Action == MouseActionPress && target == FocusQuery && p.query.H > 3 {
			strip := queryTabStripRect(p.query)
			if msg.Y == strip.Row {
				for _, h := range m.queryTabHits(strip) {
					if msg.X >= h.startCol && msg.X <= h.endCol {
						m.closeTab(h.idx)
						break
					}
				}
			}
		}
	case MouseButtonWheelUp:
		m.wheelPanel(a, target, -3, msg.Shift || msg.Alt)
	case MouseButtonWheelDown:
		m.wheelPanel(a, target, 3, msg.Shift || msg.Alt)
	case MouseButtonWheelLeft:
		m.wheelPanel(a, target, -3, true)
	case MouseButtonWheelRight:
		m.wheelPanel(a, target, 3, true)
	}
	return true
}

// handleLeftClick dispatches a left-button press on the named panel.
// Single-click selects (moves the panel's cursor to the row under the
// pointer); double-click triggers the panel's "activate" action --
// expand/drill for Explorer, inspector for Results. Query-panel click
// is handled separately in a later pass (caret positioning).
func (m *mainLayer) handleLeftClick(a *app, t FocusTarget, msg MouseMsg, count int) {
	p := computeLayout(a.term.width, a.term.height)
	switch t {
	case FocusExplorer:
		idx := m.explorer.ItemAt(p.explorer, msg.Y)
		if idx < 0 {
			return
		}
		m.explorer.SetCursor(idx)
		if count >= 2 {
			switch m.explorer.SelectedKind() {
			case itemDatabase:
				m.explorer.Toggle()
				if cat, need := m.explorer.NeedsDatabaseLoad(); need {
					a.loadDatabaseSchema(cat)
				}
			case itemSchema, itemSubgroup:
				m.explorer.Toggle()
			case itemProcedure, itemFunction, itemTrigger:
				m.editObjectFromExplorer(a)
			default:
				m.prefillSelectFromExplorer(a)
			}
		}
	case FocusQuery:
		if p.query.H > 3 {
			strip := queryTabStripRect(p.query)
			if msg.Y == strip.Row {
				for _, h := range m.queryTabHits(strip) {
					if msg.X >= h.startCol && msg.X <= h.endCol {
						if count >= 2 {
							sess := m.sessions[h.idx]
							a.pushLayer(newRenameLayer(h.idx, sess.title))
						} else {
							m.switchTab(h.idx)
						}
						break
					}
				}
				return
			}
		}
		m.editor.clickAt(p.query, msg.Y, msg.X, count)
	case FocusResults:
		if len(m.results) > 1 {
			strip := resultTabStripRect(p.results)
			if msg.Y == strip.Row {
				for _, h := range m.resultTabHits(strip) {
					if msg.X >= h.startCol && msg.X <= h.endCol {
						m.switchResult(h.idx)
						return
					}
				}
			}
		}
		if m.inErrorView(a) {
			return
		}
		if !m.table.CellAt(p.results, msg.Y, msg.X) {
			return
		}
		if count >= 2 && m.table.HasColumns() {
			if msg.Shift {
				row := m.table.CursorRow()
				line := strings.Join(row, "\t")
				if err := a.clipboard.Copy(line); err != nil {
					m.status = "copy: " + err.Error()
				} else {
					m.status = fmt.Sprintf("copied row (%d cells)", len(row))
				}
				return
			}
			a.pushLayer(newInspectorLayer(m.table.CursorColumn().Name, m.table.CursorCell()))
		}
	}
}

func (m *mainLayer) wheelPanel(a *app, t FocusTarget, delta int, shift bool) {
	switch t {
	case FocusExplorer:
		m.explorer.MoveCursor(delta)
	case FocusQuery:
		m.wheelQuery(a, delta)
	case FocusResults:
		if m.inErrorView(a) {
			m.resultsErrScroll += delta
			if m.resultsErrScroll < 0 {
				m.resultsErrScroll = 0
			}
			return
		}
		if shift {
			step := 1
			if delta < 0 {
				step = -1
			}
			m.table.MoveCellBy(0, step)
			return
		}
		m.table.MoveCellBy(delta, 0)
	}
}

func (m *mainLayer) wheelQuery(a *app, delta int) {
	kind := KeyUp
	if delta > 0 {
		kind = KeyDown
	}
	n := delta
	if n < 0 {
		n = -n
	}
	if len(m.sessions) == 0 {
		return
	}
	for i := 0; i < n; i++ {
		m.editor.handleInsert(a, Key{Kind: kind})
	}
}

func newMainLayer() *mainLayer {
	return &mainLayer{
		session:   newDetachedSession(),
		activeTab: -1,
		explorer:  newExplorer(),
		focus:     FocusQuery,
	}
}

func newDetachedSession() *session {
	s := newSession()
	s.title = ""
	return s
}

func (m *mainLayer) ensureActiveTab() *session {
	if m.activeTab >= 0 && m.activeTab < len(m.sessions) {
		m.session = m.sessions[m.activeTab]
		return m.session
	}
	if m.session == nil {
		m.session = newDetachedSession()
	}
	if m.session.title == "" {
		m.session.title = m.nextTabTitle()
	}
	m.sessions = append(m.sessions, m.session)
	m.activeTab = len(m.sessions) - 1
	return m.session
}

func (m *mainLayer) detachQueryFrame(activeCatalog string) {
	sess := newDetachedSession()
	sess.activeCatalog = activeCatalog
	m.session = sess
	m.sessions = nil
	m.activeTab = -1
}

// saveActive writes the active tab's buffer to its sourcePath. If the
// tab has no sourcePath yet, pushes the Save As dialog instead. A zero
// sourcePath is the "never saved" state; any non-empty path means the
// file is known and Ctrl+S should just overwrite without prompting.
func (m *mainLayer) saveActive(a *app) {
	if m.activeTab < 0 || m.activeTab >= len(m.sessions) {
		return
	}
	sess := m.sessions[m.activeTab]
	if sess.sourcePath == "" {
		seed := sess.title
		if !strings.HasSuffix(strings.ToLower(seed), ".sql") {
			seed += ".sql"
		}
		a.pushLayer(newSaveLayer(a, m.activeTab, seed, []string{".sql"}))
		return
	}
	text := sess.editor.buf.Text()
	if err := os.WriteFile(sess.sourcePath, []byte(text), 0644); err != nil {
		m.status = "save failed: " + err.Error()
		return
	}
	sess.savedText = text
	m.status = fmt.Sprintf("saved %d bytes to %s", len(text), sess.sourcePath)
}

// saveAsActive pushes a Save As dialog seeded from the active tab's
// current source path (if any) or its title. Extracted so the editor-
// focus Alt+S bind and any future menu path share the exact same seed
// logic.
func (m *mainLayer) saveAsActive(a *app) {
	if m.activeTab < 0 || m.activeTab >= len(m.sessions) {
		return
	}
	sess := m.sessions[m.activeTab]
	seed := ""
	if sess.sourcePath != "" {
		seed = sess.sourcePath
	} else {
		seed = sess.title
		if !strings.HasSuffix(strings.ToLower(seed), ".sql") {
			seed += ".sql"
		}
	}
	a.pushLayer(newSaveLayer(a, m.activeTab, seed, []string{".sql"}))
}

// runExplainPlan kicks off an EXPLAIN for the active editor buffer.
// Runs off the main loop because some drivers (MSSQL SHOWPLAN_XML,
// Postgres on a big query) take a noticeable beat; the spinner keeps
// the status line live. No-op if there's no connection, no SQL, or an
// explain is already in flight on this session.
func (m *mainLayer) runExplainPlan(a *app) {
	sess := m.session
	if sess == nil || sess.explainBusy {
		return
	}
	sql := strings.TrimSpace(m.editor.buf.Text())
	if sql == "" {
		m.status = "nothing to explain"
		return
	}
	if a.conn == nil {
		m.status = "not connected"
		return
	}
	sess.explainBusy = true
	sess.explainFrame = spinnerFrames[0]
	m.status = "explain " + sess.explainFrame
	conn := a.conn
	done := make(chan struct{})
	go runSpinner(a, done, func(a *app, frame string) {
		if sess.explainBusy {
			sess.explainFrame = frame
			if a.conn == conn {
				m := a.mainLayerPtr()
				if m != nil && m.session == sess {
					m.status = "explain " + frame
				}
			}
		}
	})
	go func() {
		catalog := ""
		if a.catalogPreamble(sess) != "" {
			catalog = sess.activeCatalog
		}
		tree, err := a.runExplain(conn, catalog, sql)
		close(done)
		a.asyncCh <- func(a *app) {
			sess.explainBusy = false
			if a.conn != conn {
				return
			}
			m := a.mainLayerPtr()
			if err != nil {
				if m != nil && m.session == sess {
					m.status = "explain: " + err.Error()
				}
				return
			}
			if m != nil && m.session == sess {
				m.status = ""
			}
			a.pushLayer(newExplainLayer(tree))
		}
	}()
}

// findTabByPath returns the index of a session whose sourcePath matches
// the given absolute path, or -1 if no tab has that file open. Used by
// the open dialog to switch to an already-open file instead of
// duplicating the tab (and dropping any unsaved edits).
func (m *mainLayer) findTabByPath(abs string) int {
	if abs == "" {
		return -1
	}
	for i, s := range m.sessions {
		if s.sourcePath == abs {
			return i
		}
	}
	return -1
}

// newTab appends a fresh session and activates it. The embedded *session
// swap keeps promoted fields (m.editor, m.table, ...) pointed at the new
// tab so the existing call sites continue to resolve against the active
// session.
func (m *mainLayer) newTab() {
	if len(m.sessions) == 0 {
		m.ensureActiveTab()
		return
	}
	sess := newSession()
	sess.title = m.nextTabTitle()
	if m.session != nil && m.session.activeCatalog != "" {
		sess.activeCatalog = m.session.activeCatalog
	}
	m.sessions = append(m.sessions, sess)
	m.activeTab = len(m.sessions) - 1
	m.session = sess
}

// nextTabTitle returns the lowest "Query N" label not already taken by
// an existing tab. Closing tab 2 and opening a new one reuses "Query 2"
// rather than marching the counter forward -- matches SSMS.
func (m *mainLayer) nextTabTitle() string {
	used := make(map[string]bool, len(m.sessions))
	for _, s := range m.sessions {
		used[s.title] = true
	}
	for i := 1; ; i++ {
		t := fmt.Sprintf("Query %d", i)
		if !used[t] {
			return t
		}
	}
}

// switchTab activates the tab at idx (clamped). No-op if out of range or
// already active. Swaps the embedded *session pointer.
func (m *mainLayer) switchTab(idx int) {
	if idx < 0 || idx >= len(m.sessions) || idx == m.activeTab {
		return
	}
	m.activeTab = idx
	m.session = m.sessions[idx]
}

// closeTab removes the tab at idx. A running query on that tab is
// cancelled first so the goroutine unwinds cleanly. Closing the last
// visible tab leaves the query pane in its detached empty-frame state.
func (m *mainLayer) closeTab(idx int) {
	if idx < 0 || idx >= len(m.sessions) {
		return
	}
	s := m.sessions[idx]
	if s.running && s.cancel != nil {
		s.cancel()
	}
	m.sessions = append(m.sessions[:idx], m.sessions[idx+1:]...)
	if len(m.sessions) == 0 {
		m.detachQueryFrame(s.activeCatalog)
		return
	}
	if m.activeTab >= len(m.sessions) {
		m.activeTab = len(m.sessions) - 1
	} else if idx < m.activeTab {
		m.activeTab--
	}
	m.session = m.sessions[m.activeTab]
}

func (m *mainLayer) Draw(a *app, c *cellbuf) {
	if m.editorFullscreen {
		m.drawFullscreen(a, c)
		return
	}
	p := computeLayout(a.term.width, a.term.height)
	drawFrame(c, p.explorer, m.explorerTitle(a), m.focus == FocusExplorer)
	drawFrameInfo(c, p.query, "", m.queryRightInfo(), m.focus == FocusQuery)
	resultsTitle := ""
	if len(m.results) == 0 {
		resultsTitle = m.resultsTitle()
	}
	drawFrameInfo(c, p.results, resultsTitle, m.resultsRightInfo(a), m.focus == FocusResults)

	// Show the editor cursor whenever the Query panel is focused. If an
	// overlay is stacked on top of us, its cell buffer will be the topmost
	// one during compositing and the main layer's cursor request gets
	// discarded automatically.
	m.explorer.draw(c, p.explorer, m.focus == FocusExplorer)

	// Paint query tab labels directly onto the top border row of the
	// Query frame, replacing the static "Query" title. Saves a content
	// row; the editor keeps the full inner panel.
	m.drawQueryTabs(c, queryTabStripRect(p.query))
	if len(m.sessions) == 0 {
		m.drawQueryEmpty(c, p.query)
		m.drawResultsEmpty(c, p.results)
	} else {
		m.editor.draw(c, p.query, m.focus == FocusQuery)

		// Paint result tab labels onto the top border of the Results frame,
		// mirroring the query tab treatment. Keeps the full inner panel for
		// the table / error view.
		if len(m.results) > 0 {
			m.drawResultTabs(c, resultTabStripRect(p.results))
		}
		if !m.running && m.lastErr != "" && m.lastErr != "cancelled" {
			m.drawResultsError(c, p.results)
		} else if m.running && m.table.ColCount() == 0 {
			m.drawResultsRunning(c, p.results)
		} else if m.inSuccessView() {
			m.drawResultsSuccess(c, p.results)
		} else {
			m.table.draw(c, p.results)
		}
	}

	// Bottom status bar reflects the topmost layer's hints, so modal
	// overlays can show their own keys here without touching the main
	// view's hint logic.
	c.SetFg(colorStatusBar)
	c.WriteAt(p.status.Row, p.status.Col, m.statusText(a, p.status.W))
	c.ResetStyle()
}

// queryTabStripRect returns the top-border row of the Query panel,
// offset 2 cols in from the left corner. The tab strip is painted
// directly on the frame border in place of a static title.
func queryTabStripRect(q rect) rect {
	if q.W < 5 {
		return rect{Row: q.Row, Col: q.Col, W: 0, H: 1}
	}
	return rect{Row: q.Row, Col: q.Col + 2, W: q.W - 4, H: 1}
}

// queryTabLabel formats a session's label for the query tab strip.
// Wraps the title in " " for spacing and brackets the active tab so it
// still reads on terminals without color. A trailing "+" hint tab is
// rendered separately by drawQueryTabs.
func queryTabLabel(s *session, active bool) string {
	title := s.title
	if s.IsDirty() {
		title = "● " + title
	}
	if s.activeCatalog != "" {
		title = title + " (" + s.activeCatalog + ")"
	}
	lbl := fmt.Sprintf(" %s ", title)
	if active {
		lbl = "[" + lbl[1:len(lbl)-1] + "]"
	}
	return lbl
}

// queryTabHits returns the (sessionIndex, startCol, endCol) of each
// visible query tab given the strip rect. Used by mouse hit-tests.
// endCol is inclusive.
func (m *mainLayer) queryTabHits(r rect) []queryTabHit {
	if r.W < 3 {
		return nil
	}
	hits := make([]queryTabHit, 0, len(m.sessions))
	col := r.Col
	for i, s := range m.sessions {
		lbl := queryTabLabel(s, i == m.activeTab)
		remaining := r.Col + r.W - col
		if remaining < 3 {
			break
		}
		if len(lbl) > remaining {
			lbl = lbl[:remaining]
		}
		hits = append(hits, queryTabHit{idx: i, startCol: col, endCol: col + len(lbl) - 1})
		col += len(lbl)
	}
	return hits
}

type queryTabHit struct {
	idx      int
	startCol int
	endCol   int
}

// drawQueryTabs renders the tab strip at the top of the Query pane.
// Mirrors drawResultTabs but indexes m.sessions/m.activeTab.
func (m *mainLayer) drawQueryTabs(c *cellbuf, r rect) {
	if r.W < 3 || len(m.sessions) == 0 {
		return
	}
	for _, h := range m.queryTabHits(r) {
		s := m.sessions[h.idx]
		lbl := queryTabLabel(s, h.idx == m.activeTab)
		if len(lbl) > h.endCol-h.startCol+1 {
			lbl = lbl[:h.endCol-h.startCol+1]
		}
		fg := colorTitleUnfocused
		if h.idx == m.activeTab {
			fg = colorTitleFocused
		}
		st := Style{FG: fg, BG: ansiDefaultBG}
		if s.preview {
			// Underline signals "preview tab — will be replaced by the
			// next explorer click unless you edit". Mirrors VSCode's
			// italic preview-tab title, which terminals here don't
			// reliably support.
			st.Attrs |= attrUnderline
		}
		c.WriteStyled(r.Row, h.startCol, lbl, st)
	}
}

// resultTabStripRect returns the top-border row of the Results panel,
// offset 2 cols in from the left corner. Mirrors queryTabStripRect --
// result tabs paint on the frame border in place of a static title.
func resultTabStripRect(p rect) rect {
	if p.W < 5 {
		return rect{Row: p.Row, Col: p.Col, W: 0, H: 1}
	}
	return rect{Row: p.Row, Col: p.Col + 2, W: p.W - 4, H: 1}
}

// resultTabLabel formats a result tab's label. Active tab gets square
// brackets so it reads without color.
func resultTabLabel(t *resultTab, active bool) string {
	return resultTabLabelWith(t, active, "")
}

// resultTabLabelWith mirrors resultTabLabel but lets the caller inject a
// trailing annotation (e.g. a filter glyph). Centralised so tab-hit
// width and the drawn label stay in sync -- if they drifted the
// click targets would miss the visible tab ends.
func resultTabLabelWith(t *resultTab, active bool, suffix string) string {
	title := resultTabTitle(t)
	if suffix != "" {
		title = title + " " + suffix
	}
	lbl := fmt.Sprintf(" %s ", title)
	if active {
		lbl = "[" + lbl[1:len(lbl)-1] + "]"
	}
	return lbl
}

// resultTabSuffix returns the annotation glyph for tab idx. Filter
// state lives on the active m.table only, so inactive tabs never
// carry a filter marker -- switching to them resets the table filter.
// While the session is running, tab 0 shows the braille spinner frame
// so the user has an unambiguous indicator that results are pending.
func (m *mainLayer) resultTabSuffix(idx int) string {
	if idx == 0 && m.session != nil && m.session.running && m.session.runnerFrame != "" {
		return m.session.runnerFrame
	}
	if idx == m.activeResult && m.table.Filter() != "" {
		return "⚲"
	}
	return ""
}

// resultTabHits returns per-tab click targets given the strip rect.
func (m *mainLayer) resultTabHits(r rect) []queryTabHit {
	if r.W < 3 {
		return nil
	}
	hits := make([]queryTabHit, 0, len(m.results))
	col := r.Col
	for i, t := range m.results {
		lbl := resultTabLabelWith(t, i == m.activeResult, m.resultTabSuffix(i))
		remaining := r.Col + r.W - col
		if remaining < 3 {
			break
		}
		if len(lbl) > remaining {
			lbl = lbl[:remaining]
		}
		hits = append(hits, queryTabHit{idx: i, startCol: col, endCol: col + len(lbl) - 1})
		col += len(lbl)
	}
	return hits
}

// drawResultTabs renders the tab strip that appears across the top of the
// Results pane when a query produced more than one result set.
func (m *mainLayer) drawResultTabs(c *cellbuf, r rect) {
	if r.W < 3 || len(m.results) == 0 {
		return
	}
	for _, h := range m.resultTabHits(r) {
		lbl := resultTabLabelWith(m.results[h.idx], h.idx == m.activeResult, m.resultTabSuffix(h.idx))
		if len(lbl) > h.endCol-h.startCol+1 {
			lbl = lbl[:h.endCol-h.startCol+1]
		}
		fg := colorTitleUnfocused
		if h.idx == m.activeResult {
			fg = colorTitleFocused
		}
		c.WriteStyled(r.Row, h.startCol, lbl, Style{FG: fg, BG: ansiDefaultBG})
	}
}

// drawFullscreen renders the editor filling the entire terminal (minus
// the footer status line). Explorer and results panels are hidden.
// Focus is forced to the query editor -- toggleFullscreen takes care
// of the forward set, and HandleKey ignores Alt+1/3 while in this
// mode.
func (m *mainLayer) drawFullscreen(a *app, c *cellbuf) {
	termW := a.term.width
	termH := a.term.height
	bodyH := termH - statusBarH
	if bodyH < bodyMinH {
		bodyH = bodyMinH
	}
	queryRect := rect{Row: 1, Col: 1, W: termW, H: bodyH}
	drawFrameInfo(c, queryRect, "Query [fullscreen]", m.queryRightInfo(), true)
	if len(m.sessions) == 0 {
		m.drawQueryEmpty(c, queryRect)
	} else {
		m.editor.draw(c, queryRect, true)
	}

	statusRow := bodyH + 1
	c.SetFg(colorStatusBar)
	c.WriteAt(statusRow, 1, m.statusText(a, termW))
	c.ResetStyle()
}

func (m *mainLayer) HandleKey(a *app, k Key) {
	// Pending command-menu dispatch runs FIRST. Ctrl+K arms the menu;
	// the very next keystroke belongs to it regardless of content, so no
	// other binding can leak through and leave pendingMenu set. A non-
	// rune or unrecognized second key falls through handleMenuPrefix as
	// a silent no-op, which doubles as "cancel the menu".
	if m.pendingMenu {
		m.pendingMenu = false
		m.handleMenuPrefix(a, k)
		return
	}
	// Ctrl+C cancels a running query. When no query is running, it
	// falls through so the editor can use Ctrl+C as "copy selection"
	// without stealing it back from the cancel binding.
	if k.Ctrl && k.Rune == 'c' && m.running {
		a.cancelQuery()
		return
	}
	// F5 runs the current editor buffer. Gated on editor focus so it
	// doesn't fire while the user is navigating the Explorer tree or
	// paging through results.
	if k.Kind == KeyF5 && m.focus == FocusQuery {
		a.runQuery()
		return
	}
	// F9 opens an EXPLAIN plan for the current editor buffer. Editor-
	// focus only for the same reason as F5.
	if k.Kind == KeyF9 && m.focus == FocusQuery {
		m.runExplainPlan(a)
		return
	}
	// Ctrl+S saves the active tab. Gated on editor focus so the key
	// stays free to mean "copy selection"-ish things (or nothing) when
	// other panes are focused. If the tab was loaded from or previously
	// saved to a file, write to that path directly; otherwise push the
	// Save As dialog to prompt for one.
	if k.Ctrl && k.Rune == 's' && m.focus == FocusQuery {
		m.saveActive(a)
		return
	}
	// Ctrl+O opens a file picker rooted at the current directory. Editor
	// focus only -- "o" in the Explorer tree or Results grid is already
	// used as a typeable character in filters.
	if k.Ctrl && k.Rune == 'o' && m.focus == FocusQuery {
		a.pushLayer(newOpenLayer(a, ""))
		return
	}
	// Ctrl+E exports the active result buffer. Gated on results focus so
	// 'e' typed in the editor isn't eaten when the user accidentally
	// holds Ctrl.
	if k.Ctrl && k.Rune == 'e' && m.focus == FocusResults {
		if !m.table.HasColumns() {
			m.status = "nothing to export"
			return
		}
		a.pushLayer(newExportLayer(a))
		return
	}
	// Ctrl+R renames the active tab. Editor-focus only so it doesn't
	// clash with the Explorer "R = refresh schema" muscle memory.
	if k.Ctrl && k.Rune == 'r' && m.focus == FocusQuery {
		if m.activeTab < 0 || m.activeTab >= len(m.sessions) {
			return
		}
		sess := m.sessions[m.activeTab]
		a.pushLayer(newRenameLayer(m.activeTab, sess.title))
		return
	}
	if k.Ctrl && k.Kind == KeyRune && k.Rune == 'g' && m.focus == FocusQuery {
		if len(m.sessions) == 0 {
			return
		}
		row, _ := m.editor.buf.Cursor()
		a.pushLayer(newGotoLayer(row))
		return
	}
	// Query-tab management. Editor-focus only so the tab keys don't fire
	// while the user is navigating another pane. Ctrl+PgUp/PgDn still
	// works from Results (for result-set cycling) via the block below.
	if k.Ctrl && k.Kind == KeyRune && k.Rune == 't' && m.focus == FocusQuery {
		m.newTab()
		return
	}
	if k.Ctrl && k.Kind == KeyRune && k.Rune == 'w' && m.focus == FocusQuery {
		m.closeTab(m.activeTab)
		return
	}
	// Ctrl+PgUp / Ctrl+PgDn cycle the "tab-like thing" in the focused
	// pane: Query focus cycles session tabs, Results focus cycles
	// result sets. Explorer focus ignores them.
	if k.Ctrl && (k.Kind == KeyPgUp || k.Kind == KeyPgDn) {
		dir := 1
		if k.Kind == KeyPgUp {
			dir = -1
		}
		switch m.focus {
		case FocusQuery:
			if n := len(m.sessions); n > 1 {
				m.switchTab((m.activeTab + dir + n) % n)
			}
			return
		case FocusResults:
			if n := len(m.results); n > 1 {
				m.switchResult((m.activeResult + dir + n) % n)
			}
			return
		}
	}

	// Ctrl+K is the global command-menu prefix. Works from any focus.
	// Arming bumps pendingMenuID so the timeout goroutine can tell whether
	// the chord is still the one it was spawned for (a fresh Ctrl+K while
	// already armed resets the clock; an action that consumes the chord
	// leaves a stale ID the timeout will ignore).
	if k.Ctrl && k.Rune == 'k' {
		m.pendingMenu = true
		m.pendingMenuID++
		id := m.pendingMenuID
		go func() {
			time.Sleep(chordTimeout)
			a.asyncCh <- func(a *app) {
				if m.pendingMenu && m.pendingMenuID == id {
					m.pendingMenu = false
				}
			}
		}()
		return
	}
	// F11 toggles fullscreen editor mode. Available from any focus
	// so the user can jump straight into a distraction-free editor.
	if k.Kind == KeyF11 {
		m.editorFullscreen = !m.editorFullscreen
		if m.editorFullscreen {
			m.focus = FocusQuery
		}
		return
	}

	// Alt+1/2/3 is the global panel-switch shortcut. Locked out in
	// fullscreen so the user has to press F11 to exit (avoids the
	// confusing case where Alt+1 "does nothing" visually).
	if k.Alt && k.Kind == KeyRune {
		switch k.Rune {
		case '1':
			if !m.editorFullscreen {
				m.focus = FocusExplorer
			}
			return
		case '2':
			m.focus = FocusQuery
			return
		case '3':
			if !m.editorFullscreen {
				m.focus = FocusResults
			}
			return
		case 'f', 'F':
			// Alt+F reformats the query buffer using the sqltok
			// heuristic formatter. Only meaningful when the editor
			// has content; silently ignored otherwise. The buffer's
			// own undo stack covers Alt+Z for "that looks worse,
			// give me my original back".
			if m.focus == FocusQuery && (m.editor.buf.LineCount() > 1 || len(m.editor.buf.Line(0)) > 0) {
				m.formatQuery()
			}
			return
		case 's', 'S':
			// Alt+S = Save As. Terminals collapse Ctrl+Shift+S to
			// Ctrl+S, so Alt+S stands in for the "save a copy" bind.
			if m.focus == FocusQuery {
				m.saveAsActive(a)
			}
			return
		case 'd', 'D':
			// Alt+D opens the active-database picker for the focused
			// tab. Editor-gated so the Explorer's typeable 'd' stays
			// free.
			if m.focus == FocusQuery {
				a.openCatalogLayer()
			}
			return
		}
	}

	// Ctrl+F is routed by the focused panel so the user gets the
	// find/search affordance that fits whatever they're looking at:
	// editor -> find/replace overlay, explorer -> inline name search,
	// results grid -> filter overlay.
	if k.Ctrl && k.Rune == 'f' {
		switch m.focus {
		case FocusQuery:
			seed := m.editor.buf.Selection()
			fl := newFindLayer(seed)
			if seed != "" {
				m.editor.SetSearch(seed)
			}
			a.pushLayer(fl)
			return
		case FocusExplorer:
			m.explorer.ActivateSearch()
			return
		case FocusResults:
			a.pushLayer(newFilterLayer(m.table.Filter()))
			return
		}
	}

	// Query panel is non-modal: every keystroke goes straight to the
	// editor. The editor ignores Ctrl+<rune> combos so global shortcuts
	// like Ctrl+L (clear) can still be handled below if needed.
	if m.focus == FocusQuery {
		if k.Ctrl && k.Rune == 'l' {
			if len(m.sessions) == 0 {
				return
			}
			before := m.editor.buf.Text()
			m.editor.buf.Clear()
			if m.editor.buf.Text() != before {
				m.editor.ClearErrorLocation()
			}
			m.promoteActiveIfPreview()
			return
		}
		before := m.editor.buf.Text()
		m.editor.handleInsert(a, k)
		if len(m.sessions) == 0 && m.editor.buf.Text() != before {
			m.ensureActiveTab()
		}
		if m.editor.buf.Text() != before {
			m.promoteActiveIfPreview()
		}
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
// NORMAL mode. Up/Down move, Enter expands a schema, prefills a SELECT for
// tables and views, or opens the DDL for routines/triggers. 's' always
// prefills a SELECT; 'e' opens the DDL for views/routines/triggers.
func (m *mainLayer) handleExplorerKey(a *app, k Key) {
	// When the explorer's inline search bar is open, typed runes and
	// Backspace go to the input. Nav keys (Up/Down/PgUp/PgDn/Enter) and
	// Esc fall through to the handlers below -- Esc closes the bar,
	// everything else drives the filtered cursor.
	if m.explorer.IsSearching() {
		if m.explorer.HandleSearchKey(k) {
			return
		}
	}
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
		case itemDatabase:
			m.explorer.Toggle()
			if cat, need := m.explorer.NeedsDatabaseLoad(); need {
				a.loadDatabaseSchema(cat)
			}
		case itemSchema, itemSubgroup:
			m.explorer.Toggle()
		case itemProcedure, itemFunction, itemTrigger:
			m.editObjectFromExplorer(a)
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
		case 'e':
			switch m.explorer.SelectedKind() {
			case itemView, itemProcedure, itemFunction, itemTrigger:
				m.editObjectFromExplorer(a)
			}
			return
		case 'R':
			a.loadSchema()
			return
		case 'u':
			// Pin the active query tab to the DB under the cursor
			// (SSMS-style). Only meaningful on cross-DB drivers; on
			// single-DB engines CursorCatalog returns "" and the
			// tab's catalog is cleared, matching the "login default"
			// state.
			if m.session != nil {
				m.session.activeCatalog = m.explorer.CursorCatalog()
				if m.session.activeCatalog == "" {
					if len(m.sessions) == 0 {
						m.status = "new tabs use login default database"
					} else {
						m.status = "tab uses login default database"
					}
				} else {
					if len(m.sessions) == 0 {
						m.status = "new tabs will use " + m.session.activeCatalog
					} else {
						m.status = "tab now uses " + m.session.activeCatalog
					}
				}
			}
			return
		}
	}
}

// editObjectFromExplorer fetches the DDL for the view/procedure/function/
// trigger under the cursor and opens a new non-preview tab pre-filled with
// it. Tags the session with editKind/editSchema/editName so the Apply flow
// (Phase 1.5) can diff + re-run against the source object. The fetch runs
// in a background goroutine; the tab is opened in the asyncCh callback so
// a slow driver can't stall the main loop.
func (m *mainLayer) editObjectFromExplorer(a *app) {
	if a.conn == nil {
		m.status = "not connected"
		return
	}
	if m.ddlBusy {
		return
	}
	var kind, schema, name string
	switch m.explorer.SelectedKind() {
	case itemView:
		t, ok := m.explorer.Selected()
		if !ok {
			return
		}
		kind, schema, name = "view", t.Schema, t.Name
	case itemProcedure:
		r, ok := m.explorer.SelectedRoutine()
		if !ok {
			return
		}
		kind, schema, name = "procedure", r.Schema, r.Name
	case itemFunction:
		r, ok := m.explorer.SelectedRoutine()
		if !ok {
			return
		}
		kind, schema, name = "function", r.Schema, r.Name
	case itemTrigger:
		tr, ok := m.explorer.SelectedTrigger()
		if !ok {
			return
		}
		kind, schema, name = "trigger", tr.Schema, tr.Name
	default:
		m.status = "edit: select a view, routine, or trigger"
		return
	}
	label := name
	if schema != "" {
		label = schema + "." + name
	}
	catalog := m.explorer.CursorCatalog()
	conn := a.conn
	m.ddlBusy = true
	m.status = "edit " + label + " " + spinnerFrames[0]
	done := make(chan struct{})
	go runSpinner(a, done, func(a *app, frame string) {
		mm := a.mainLayerPtr()
		if mm == nil || !mm.ddlBusy || a.conn != conn {
			return
		}
		mm.status = "edit " + label + " " + frame
	})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		body, err := conn.Definition(ctx, kind, schema, name)
		close(done)
		a.asyncCh <- func(a *app) {
			mm := a.mainLayerPtr()
			if mm == nil {
				return
			}
			mm.ddlBusy = false
			if a.conn != conn {
				return
			}
			if err != nil {
				if errors.Is(err, db.ErrDefinitionUnsupported) {
					mm.status = "edit: " + kind + " not supported by this driver"
					return
				}
				mm.status = "edit: " + err.Error()
				return
			}
			var sess *session
			if len(mm.sessions) == 0 {
				sess = mm.ensureActiveTab()
			} else {
				sess = newSession()
				mm.sessions = append(mm.sessions, sess)
				mm.activeTab = len(mm.sessions) - 1
			}
			sess.title = label
			sess.editor.buf.SetText(body)
			sess.editKind = kind
			sess.editSchema = schema
			sess.editName = name
			sess.editOriginal = body
			if catalog != "" {
				sess.activeCatalog = catalog
			}
			mm.session = sess
			mm.focus = FocusQuery
			mm.status = ""
		}
	}()
}

// handleResultsKey processes keys when the Results panel is focused.
// Navigation moves the cell cursor (Up/Dn/Lt/Rt); PgUp/PgDn page the
// row cursor. Home/End jump to the first/last row. 'w' toggles wrap.
// 's' cycles sort state on the current column; Ctrl+F opens the filter
// prompt; 'y' / 'Y' copy cell / row to the system clipboard; Enter
// opens the cell inspector.
func (m *mainLayer) handleResultsKey(a *app, k Key) {
	if m.inErrorView(a) {
		m.handleResultsErrorKey(a, k)
		return
	}
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
	// Alt+A copies the entire visible (filtered + sorted) result set
	// as TSV -- sibling of the cell-level 'y' and row-level 'Y'
	// yanks. Alt-prefixed rather than Ctrl because Ctrl+Y is VDSUSP
	// on BSD/macOS and can be swallowed by shell job control before
	// reaching the raw tty.
	if k.Alt && k.Kind == KeyRune && (k.Rune == 'a' || k.Rune == 'A') {
		m.copyAllResults(a)
		return
	}
	if k.Kind != KeyRune || k.Ctrl || k.Alt {
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

// copyAllResults serializes the entire visible result buffer (after
// filter + sort) as TSV and places it on the system clipboard. Sibling
// of the cell-level 'y' and row-level 'Y' yanks; uses the shared
// output.Write so the clipboard payload matches what the TSV
// export-to-file path would produce.
func (m *mainLayer) copyAllResults(a *app) {
	if !m.table.HasColumns() {
		m.status = "nothing to copy"
		return
	}
	cols, rows := m.table.Snapshot()
	var buf bytes.Buffer
	if err := output.Write(&buf, cols, rows, output.TSV); err != nil {
		m.status = "copy all: " + err.Error()
		return
	}
	if err := a.clipboard.Copy(buf.String()); err != nil {
		m.status = "copy all: " + err.Error()
		return
	}
	m.status = fmt.Sprintf("copied %d row(s) as TSV", len(rows))
}

// inErrorView reports whether the Results pane is currently showing the
// error view instead of the table.
func (m *mainLayer) inErrorView(_ *app) bool {
	return !m.running && m.lastErr != "" && m.lastErr != "cancelled"
}

// handleResultsErrorKey processes keys while the results pane is in
// error-view mode. Up/Dn scroll the wrapped error; 'y' and Alt+A copy
// the full error string to the clipboard.
func (m *mainLayer) handleResultsErrorKey(a *app, k Key) {
	switch k.Kind {
	case KeyUp:
		if m.resultsErrScroll > 0 {
			m.resultsErrScroll--
		}
		return
	case KeyDown:
		m.resultsErrScroll++
		return
	case KeyPgUp:
		m.resultsErrScroll -= 10
		if m.resultsErrScroll < 0 {
			m.resultsErrScroll = 0
		}
		return
	case KeyPgDn:
		m.resultsErrScroll += 10
		return
	case KeyHome:
		m.resultsErrScroll = 0
		return
	case KeyEnd:
		// Drawing clamps resultsErrScroll to len(lines)-1, so an
		// intentionally-huge value is the simplest "scroll to bottom".
		m.resultsErrScroll = math.MaxInt32
		return
	}
	if k.Alt && k.Kind == KeyRune && (k.Rune == 'a' || k.Rune == 'A') {
		m.copyErrorText(a)
		return
	}
	if k.Kind == KeyRune && !k.Ctrl && !k.Alt && (k.Rune == 'y' || k.Rune == 'Y') {
		m.copyErrorText(a)
	}
}

func (m *mainLayer) copyErrorText(a *app) {
	if err := a.clipboard.Copy(m.lastErr); err != nil {
		m.status = "copy: " + err.Error()
		return
	}
	m.status = fmt.Sprintf("copied error (%d chars)", len(m.lastErr))
}

// inSuccessView reports whether the Results pane should show the
// non-result "statement completed" view: the last run finished cleanly
// but produced no columns (DDL/DML like CREATE/UPDATE/DELETE).
func (m *mainLayer) inSuccessView() bool {
	return !m.running && m.lastErr == "" && m.lastHasResult && m.table.ColCount() == 0
}

// drawResultsSuccess renders a prominent "statement completed" message
// when a non-result query (DDL/DML) finishes without error, so the user
// gets more obvious feedback than the default "(no results)" placeholder.
func (m *mainLayer) drawResultsSuccess(c *cellbuf, r rect) {
	innerRow := r.Row + 1
	innerCol := r.Col + 1
	innerW := r.W - 2
	innerH := r.H - 2
	if innerW <= 0 || innerH <= 0 {
		return
	}
	msg := "OK - statement completed"
	if m.lastElapsed > 0 {
		msg = fmt.Sprintf("OK - statement completed in %s", m.lastElapsed.Round(time.Millisecond))
	}
	row := innerRow + innerH/2
	col := innerCol + (innerW-len([]rune(msg)))/2
	if col < innerCol {
		col = innerCol
	}
	c.SetFg(colorOK)
	c.WriteAt(row, col, truncate(msg, innerW))
	c.ResetStyle()
}

// drawResultsRunning renders a centered "running query…" placeholder
// with the current spinner frame while a query is in flight and has
// not yet produced columns. Keeps the Results panel visually alive
// even when the session has no previous result set to fall back to.
func (m *mainLayer) drawResultsRunning(c *cellbuf, r rect) {
	innerRow := r.Row + 1
	innerCol := r.Col + 1
	innerW := r.W - 2
	innerH := r.H - 2
	if innerW <= 0 || innerH <= 0 {
		return
	}
	frame := ""
	if m.session != nil {
		frame = m.session.runnerFrame
	}
	msg := "running query…"
	if frame != "" {
		msg = frame + " " + msg
	}
	row := innerRow + innerH/2
	col := innerCol + (innerW-len([]rune(msg)))/2
	if col < innerCol {
		col = innerCol
	}
	c.SetFg(colorStatusBar)
	c.WriteAt(row, col, truncate(msg, innerW))
	c.ResetStyle()
}

// drawResultsError renders the last query error in place of the table.
// The error text is hard-wrapped to the inner width and scrolled by
// resultsErrScroll. Up/Dn adjust the scroll; 'y' and Alt+A copy the
// full error string to the clipboard.
func (m *mainLayer) drawResultsError(c *cellbuf, r rect) {
	innerRow := r.Row + 1
	innerCol := r.Col + 1
	innerW := r.W - 2
	innerH := r.H - 2
	if innerW <= 0 || innerH <= 0 {
		return
	}
	header := "Query error:"
	if m.lastErrLine > 0 && m.lastErrCol > 0 {
		header = fmt.Sprintf("Query error (line %d, col %d):", m.lastErrLine, m.lastErrCol)
	} else if m.lastErrLine > 0 {
		header = fmt.Sprintf("Query error (line %d):", m.lastErrLine)
	}
	c.SetFg(colorError)
	c.WriteAt(innerRow, innerCol, truncate(header, innerW))
	c.ResetStyle()
	bodyRow := innerRow + 2
	bodyH := innerH - 2
	if bodyH <= 0 {
		return
	}
	lines := wrapText(m.lastErr, innerW)
	if m.resultsErrScroll > len(lines)-1 {
		m.resultsErrScroll = len(lines) - 1
	}
	if m.resultsErrScroll < 0 {
		m.resultsErrScroll = 0
	}
	visible := lines[m.resultsErrScroll:]
	if len(visible) > bodyH {
		visible = visible[:bodyH]
	}
	for i, line := range visible {
		c.WriteAt(bodyRow+i, innerCol, truncate(line, innerW))
	}
}

// formatQuery reformats the editor's current buffer using the
// sqltok heuristic formatter. The buffer's SetText path pushes a
// snapshot for undo, so Ctrl+Z restores the original text if the
// formatted output isn't what the user wanted. Empty buffers short
// out here and in the Alt+F binding above.
func (m *mainLayer) formatQuery() {
	if len(m.sessions) == 0 {
		return
	}
	src := m.editor.buf.Text()
	formatted := sqltok.Format(src)
	if formatted == src {
		m.status = "already formatted"
		return
	}
	m.editor.buf.SetText(formatted)
	m.editor.ClearErrorLocation()
	m.promoteActiveIfPreview()
	m.status = "formatted"
}

// prefillSelectFromExplorer writes a driver-aware SELECT for the highlighted
// table into the editor and moves focus to Query. Uses a preview tab (VSCode-
// style): if a preview tab already exists its buffer is replaced in place,
// otherwise a new preview tab is opened. The tab is promoted to a permanent
// tab on the first real edit. No-op if nothing selectable is under the cursor.
func (m *mainLayer) prefillSelectFromExplorer(a *app) {
	t, ok := m.explorer.Selected()
	if !ok {
		return
	}
	var caps db.Capabilities
	if a.conn != nil {
		caps = a.conn.Capabilities()
	}
	sql := sqltok.Format(BuildSelect(caps, t, defaultSelectLimit))

	// Reuse an existing preview tab if one is open, else spawn a new
	// one. Permanent tabs are never clobbered.
	prev := -1
	for i, s := range m.sessions {
		if s.preview {
			prev = i
			break
		}
	}
	if prev < 0 {
		if len(m.sessions) == 0 {
			sess := m.ensureActiveTab()
			sess.preview = true
			prev = m.activeTab
		} else {
			sess := newSession()
			sess.preview = true
			m.sessions = append(m.sessions, sess)
			prev = len(m.sessions) - 1
		}
	}
	sess := m.sessions[prev]
	sess.title = t.Name
	sess.editor.buf.SetText(sql)
	sess.editor.ClearErrorLocation()
	if t.Catalog != "" {
		sess.activeCatalog = t.Catalog
	}
	m.activeTab = prev
	m.session = sess
	m.focus = FocusQuery
}

// promoteActiveIfPreview marks the active tab as permanent. Called after
// any real editor mutation so a user typing into a preview tab converts
// it to a regular tab, matching VSCode's single-click preview promotion.
func (m *mainLayer) promoteActiveIfPreview() {
	if m.session != nil && m.session.preview {
		m.session.preview = false
	}
}

// handleMenuPrefix dispatches the second key of the Ctrl+K command-menu
// prefix.
func (m *mainLayer) handleMenuPrefix(a *app, k Key) {
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
	case 'h':
		hl := newHistoryLayer()
		hl.reload(a)
		a.pushLayer(hl)
	case 'q':
		a.quit = true
	}
}

func (m *mainLayer) resultsTitle() string {
	if len(m.sessions) == 0 {
		return "Results"
	}
	tags := ""
	if m.table.Filter() != "" {
		tags += " ⚲"
	}
	if m.table.Wrap() {
		tags += "  [wrap]"
	}
	return "Results" + tags
}

// resultsRightInfo builds the top-right border label on the results panel.
// While a query is running it streams the live row count; once a query
// finishes the final row count + elapsed time stays pinned until the next
// run. Errors collapse to a short tag so the border doesn't grow.
// queryRightInfo returns the "Ln N, Col M" label shown in the top-right
// corner of the Query frame. Values are 1-based to match common editor
// conventions (VS Code, vim status line).
func (m *mainLayer) queryRightInfo() string {
	if len(m.sessions) == 0 {
		return ""
	}
	row, col := m.editor.buf.Cursor()
	return fmt.Sprintf("Ln %d, Col %d", row+1, col+1)
}

func (m *mainLayer) resultsRightInfo(_ *app) string {
	if len(m.sessions) == 0 {
		return ""
	}
	if m.running {
		return fmt.Sprintf("streaming %d rows / %d cols", m.table.RowCount(), m.table.ColCount())
	}
	if !m.lastHasResult {
		return ""
	}
	if m.lastErr != "" {
		return "error"
	}
	suffix := ""
	if m.lastCapped {
		if m.lastCapReason != "" {
			suffix = " (capped: " + m.lastCapReason + ")"
		} else {
			suffix = " (capped)"
		}
	}
	return fmt.Sprintf("%d rows / %d cols / %s%s", m.lastRowCount, m.lastColCount, m.lastElapsed.Round(time.Millisecond), suffix)
}

// explorerTitle returns the text painted on the Explorer panel's top
// border. Reuses the connection indicator that used to live in the
// footer so the active connection (and per-tab catalog) stays visible
// while freeing footer room for hints.
func (m *mainLayer) explorerTitle(a *app) string {
	if a.activeConn == nil {
		return "○ (not connected)"
	}
	title := "● " + a.activeConn.Name
	if m.session != nil && m.session.activeCatalog != "" {
		title += " [" + m.session.activeCatalog + "]"
	}
	return title
}

// statusText builds the footer line. Layout:
//
//	[focus]  |  <hints from topmost layer>    (<transient status>)
//
// The connection name lives on the Explorer frame title instead of the
// footer. Hints come first so critical keys (Ctrl+Q=quit, Alt+1/2/3=focus)
// survive right-edge truncation on narrow terminals. The parenthesized
// status is query feedback like "running..." or "3 row(s) in 12ms" and is
// allowed to be clipped because the Results panel itself shows the real
// outcome.
func (m *mainLayer) statusText(a *app, width int) string {
	hints := a.topLayer().Hints(a)
	prefix := fmt.Sprintf(" [%s]  │  ", m.focus)
	suffix := ""
	if m.status != "" {
		suffix = "    (" + m.status + ")"
	}
	// Drop trailing hint entries (two-space separated) until the whole
	// line fits. Earlier entries are higher priority so this preserves
	// critical keys like Ctrl+Q and focus switches.
	for displayWidth(prefix+hints+suffix) > width && hints != "" {
		idx := strings.LastIndex(hints, "  ")
		if idx < 0 {
			hints = ""
			break
		}
		hints = hints[:idx]
	}
	return truncate(prefix+hints+suffix, width)
}

// Hints is the Layer interface entry point for mainLayer. It dispatches on
// the pendingMenu prefix and the focused panel, letting each branch build
// a context-aware line that hides keys that wouldn't currently do anything.
func (m *mainLayer) Hints(a *app) string {
	if m.pendingMenu {
		return m.commandMenuHints(a)
	}
	switch m.focus {
	case FocusExplorer:
		return m.explorerHints(a)
	case FocusQuery:
		return m.queryHints(a)
	case FocusResults:
		return m.resultsHints(a)
	}
	return joinHints("F1=help", "Ctrl+Q=quit")
}

// joinHints and hintIf forward to widget. Kept as local aliases so
// Hints() builders in this package don't each import widget.
func joinHints(parts ...string) string  { return widget.JoinHints(parts...) }
func hintIf(cond bool, h string) string { return widget.HintIf(cond, h) }

func (m *mainLayer) explorerHints(_ *app) string {
	searching := m.explorer.IsSearching()
	searchFocused := m.explorer.IsSearchFocused()
	enterHint := ""
	eHint := ""
	if !searchFocused {
		switch m.explorer.SelectedKind() {
		case itemTable:
			enterHint = "Enter=SELECT"
		case itemView:
			enterHint = "Enter=SELECT"
			eHint = "e=edit"
		case itemProcedure, itemFunction, itemTrigger:
			enterHint = "Enter=edit"
			eHint = "e=edit"
		case itemSchema, itemSubgroup:
			enterHint = "Enter=expand"
		}
	}
	return joinHints(
		"F1=help",
		"Ctrl+Q=quit",
		enterHint,
		eHint,
		hintIf(!searching, "Ctrl+F=search"),
		hintIf(searching && !searchFocused, "Ctrl+F=edit query"),
		hintIf(searching, "Esc=close search"),
		"Ctrl+K=menu",
	)
}

func (m *mainLayer) queryHints(a *app) string {
	if len(m.sessions) == 0 {
		return joinHints(
			"F1=help",
			"Ctrl+Q=quit",
			"Ctrl+T=new tab",
			"Ctrl+O=open file",
		)
	}
	connected := a.conn != nil
	running := m.running
	hasText := m.editor.buf.LineCount() > 1 || len(m.editor.buf.Line(0)) > 0
	hasSource := false
	if m.activeTab >= 0 && m.activeTab < len(m.sessions) {
		hasSource = m.sessions[m.activeTab].sourcePath != ""
	}
	return joinHints(
		"F1=help",
		"Ctrl+Q=quit",
		hintIf(connected && !running, "F5=run"),
		hintIf(running, "Ctrl+C=cancel"),
		hintIf(hasText, "Alt+F=format"),
		hintIf(hasText || hasSource, "Ctrl+S=save"),
		hintIf(hasSource, "Ctrl+K S=save as"),
		"Ctrl+T=new tab",
	)
}

func (m *mainLayer) resultsHints(a *app) string {
	if len(m.sessions) == 0 {
		return joinHints("F1=help", "Ctrl+Q=quit", "Ctrl+K=menu")
	}
	if m.inErrorView(a) {
		return joinHints(
			"F1=help",
			"Ctrl+Q=quit",
			"y=copy error",
			"Ctrl+K=menu",
		)
	}
	hasRows := m.table.RowCount() > 0
	return joinHints(
		"F1=help",
		"Ctrl+Q=quit",
		hintIf(hasRows, "Enter=inspect"),
		hintIf(hasRows, "y=copy"),
		hintIf(m.table.HasColumns(), "Ctrl+E=export"),
		"Ctrl+K=menu",
	)
}

func (m *mainLayer) commandMenuHints(a *app) string {
	_ = m
	return joinHints(
		"c=connect",
		hintIf(a.conn != nil, "x=disconnect"),
		"h=history",
		"q=quit",
		"Esc=cancel",
	)
}

func (m *mainLayer) drawQueryEmpty(c *cellbuf, r rect) {
	innerRow := r.Row + 1
	innerCol := r.Col + 1
	innerW := r.W - 2
	innerH := r.H - 2
	if innerW <= 0 || innerH <= 0 {
		return
	}
	lines := []string{
		"No query tabs open.",
		"Press Ctrl+T to open a tab, or select a table in Explorer.",
	}
	startRow := innerRow + innerH/2 - len(lines)/2
	if startRow < innerRow {
		startRow = innerRow
	}
	for i, line := range lines {
		if startRow+i >= innerRow+innerH {
			break
		}
		col := innerCol + (innerW-len([]rune(line)))/2
		if col < innerCol {
			col = innerCol
		}
		c.SetFg(colorStatusBar)
		c.WriteAt(startRow+i, col, truncate(line, innerW))
		c.ResetStyle()
	}
}

func (m *mainLayer) drawResultsEmpty(c *cellbuf, r rect) {
	innerRow := r.Row + 1
	innerCol := r.Col + 1
	innerW := r.W - 2
	innerH := r.H - 2
	if innerW <= 0 || innerH <= 0 {
		return
	}
	msg := "Results appear here after you run a query."
	row := innerRow + innerH/2
	col := innerCol + (innerW-len([]rune(msg)))/2
	if col < innerCol {
		col = innerCol
	}
	c.SetFg(colorStatusBar)
	c.WriteAt(row, col, truncate(msg, innerW))
	c.ResetStyle()
}
