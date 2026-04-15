package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/output"
	"github.com/Nulifyer/sqlgo/internal/sqltok"
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
	// session is the active tab's state (editor, table, last-query
	// summary). Embedded so promoted fields keep m.editor / m.table /
	// m.lastErr working without touching the existing call sites.
	// Switching query tabs just swaps this pointer.
	*session

	// sessions is the ordered list of query tabs; activeTab indexes into
	// it. Each session owns its own editor + result tabs + runner state
	// so a long query in one tab doesn't stall another. There is always
	// at least one session -- closing the last tab opens a blank one.
	sessions  []*session
	activeTab int

	explorer     *explorer
	focus        FocusTarget
	pendingSpace bool

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
			m.editor.buf.InsertText(v.Text)
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
			r := rect{row: 1, col: 1, w: a.term.width, h: a.term.height - statusBarH}
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
	case p.explorer.contains(msg.Y, msg.X):
		target = FocusExplorer
	case p.query.contains(msg.Y, msg.X):
		target = FocusQuery
	case p.results.contains(msg.Y, msg.X):
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
			m.pendingSpace = false
			count := m.clicks.bump(msg)
			m.handleLeftClick(a, target, msg, count)
			if target == FocusQuery {
				m.dragTarget = FocusQuery
			}
		case MouseActionRelease:
			m.dragTarget = -1
		}
	case MouseButtonMiddle:
		if msg.Action == MouseActionPress && target == FocusQuery && p.query.h > 3 {
			strip := queryTabStripRect(p.query)
			if msg.Y == strip.row {
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
			case itemSchema, itemSubgroup:
				m.explorer.Toggle()
			default:
				m.prefillSelectFromExplorer(a)
			}
		}
	case FocusQuery:
		if p.query.h > 3 {
			strip := queryTabStripRect(p.query)
			if msg.Y == strip.row {
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
			if msg.Y == strip.row {
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
	for i := 0; i < n; i++ {
		m.editor.handleInsert(a, Key{Kind: kind})
	}
}

func newMainLayer() *mainLayer {
	sess := newSession()
	m := &mainLayer{
		session:   sess,
		sessions:  []*session{sess},
		activeTab: 0,
		explorer:  newExplorer(),
		focus:     FocusQuery,
	}
	for _, r := range "SELECT @@VERSION AS version;" {
		m.editor.buf.Insert(r)
	}
	return m
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
		a.pushLayer(newSaveLayer(m.activeTab, seed))
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
	sess := newSession()
	sess.title = m.nextTabTitle()
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
// tab opens a fresh blank one so the invariant len(sessions) >= 1
// holds everywhere else.
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
		fresh := newSession()
		m.sessions = []*session{fresh}
		m.activeTab = 0
		m.session = fresh
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
	drawFrame(c, p.explorer, "Explorer", m.focus == FocusExplorer)
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
	m.editor.draw(c, p.query, m.focus == FocusQuery)

	// Paint result tab labels onto the top border of the Results frame,
	// mirroring the query tab treatment. Keeps the full inner panel for
	// the table / error view.
	if len(m.results) > 0 {
		m.drawResultTabs(c, resultTabStripRect(p.results))
	}
	if !m.running && m.lastErr != "" && m.lastErr != "cancelled" {
		m.drawResultsError(c, p.results)
	} else if m.inSuccessView() {
		m.drawResultsSuccess(c, p.results)
	} else {
		m.table.draw(c, p.results)
	}

	// Bottom status bar reflects the topmost layer's hints, so modal
	// overlays can show their own keys here without touching the main
	// view's hint logic.
	c.setFg(colorStatusBar)
	c.writeAt(p.status.row, p.status.col, m.statusText(a, p.status.w))
	c.resetStyle()
}

// queryTabStripRect returns the top-border row of the Query panel,
// offset 2 cols in from the left corner. The tab strip is painted
// directly on the frame border in place of a static title.
func queryTabStripRect(q rect) rect {
	if q.w < 5 {
		return rect{row: q.row, col: q.col, w: 0, h: 1}
	}
	return rect{row: q.row, col: q.col + 2, w: q.w - 4, h: 1}
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
	if r.w < 3 {
		return nil
	}
	hits := make([]queryTabHit, 0, len(m.sessions))
	col := r.col
	for i, s := range m.sessions {
		lbl := queryTabLabel(s, i == m.activeTab)
		remaining := r.col + r.w - col
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
	if r.w < 3 || len(m.sessions) == 0 {
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
		c.writeStyled(r.row, h.startCol, lbl, st)
	}
}

// resultTabStripRect returns the top-border row of the Results panel,
// offset 2 cols in from the left corner. Mirrors queryTabStripRect --
// result tabs paint on the frame border in place of a static title.
func resultTabStripRect(p rect) rect {
	if p.w < 5 {
		return rect{row: p.row, col: p.col, w: 0, h: 1}
	}
	return rect{row: p.row, col: p.col + 2, w: p.w - 4, h: 1}
}

// resultTabLabel formats a result tab's label. Active tab gets square
// brackets so it reads without color.
func resultTabLabel(t *resultTab, active bool) string {
	lbl := fmt.Sprintf(" %s ", resultTabTitle(t))
	if active {
		lbl = "[" + lbl[1:len(lbl)-1] + "]"
	}
	return lbl
}

// resultTabHits returns per-tab click targets given the strip rect.
func (m *mainLayer) resultTabHits(r rect) []queryTabHit {
	if r.w < 3 {
		return nil
	}
	hits := make([]queryTabHit, 0, len(m.results))
	col := r.col
	for i, t := range m.results {
		lbl := resultTabLabel(t, i == m.activeResult)
		remaining := r.col + r.w - col
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
	if r.w < 3 || len(m.results) == 0 {
		return
	}
	for _, h := range m.resultTabHits(r) {
		lbl := resultTabLabel(m.results[h.idx], h.idx == m.activeResult)
		if len(lbl) > h.endCol-h.startCol+1 {
			lbl = lbl[:h.endCol-h.startCol+1]
		}
		fg := colorTitleUnfocused
		if h.idx == m.activeResult {
			fg = colorTitleFocused
		}
		c.writeStyled(r.row, h.startCol, lbl, Style{FG: fg, BG: ansiDefaultBG})
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
	queryRect := rect{row: 1, col: 1, w: termW, h: bodyH}
	drawFrameInfo(c, queryRect, "Query [fullscreen]", m.queryRightInfo(), true)
	m.editor.draw(c, queryRect, true)

	statusRow := bodyH + 1
	c.setFg(colorStatusBar)
	c.writeAt(statusRow, 1, m.statusText(a, termW))
	c.resetStyle()
}

func (m *mainLayer) HandleKey(a *app, k Key) {
	// Ctrl+C cancels a running query. When no query is running, it
	// falls through so the editor can use Ctrl+C as "copy selection"
	// without stealing it back from the cancel binding.
	if k.Ctrl && k.Rune == 'c' && m.running {
		a.cancelQuery()
		return
	}
	if k.Kind == KeyF5 {
		a.runQuery()
		return
	}
	// Ctrl+S saves the active tab. If the tab was loaded from or
	// previously saved to a file, write to that path directly; otherwise
	// push the Save As dialog to prompt for one.
	if k.Ctrl && k.Rune == 's' {
		m.saveActive(a)
		return
	}
	if k.Kind == KeyF2 {
		sess := m.sessions[m.activeTab]
		a.pushLayer(newRenameLayer(m.activeTab, sess.title))
		return
	}
	// Query-tab management. Global so the user can switch tabs from
	// any focus without first returning to the Query pane. Ctrl+T new,
	// Ctrl+W closes the active tab, Ctrl+PgUp/PgDn cycle.
	if k.Ctrl && k.Kind == KeyRune && k.Rune == 't' {
		m.newTab()
		m.focus = FocusQuery
		return
	}
	if k.Ctrl && k.Kind == KeyRune && k.Rune == 'w' {
		m.closeTab(m.activeTab)
		m.focus = FocusQuery
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
	if k.Ctrl && k.Rune == 'k' {
		m.pendingSpace = true
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
			if m.editor.buf.LineCount() > 1 || len(m.editor.buf.Line(0)) > 0 {
				m.formatQuery()
			}
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
			m.promoteActiveIfPreview()
			return
		}
		// Ctrl+F: find/replace. Seed with current selection if any.
		if k.Ctrl && k.Rune == 'f' {
			seed := m.editor.buf.Selection()
			fl := newFindLayer(seed)
			if seed != "" {
				m.editor.SetSearch(seed)
			}
			a.pushLayer(fl)
			return
		}
		before := m.editor.buf.Text()
		m.editor.handleInsert(a, k)
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
		case itemView, itemProcedure, itemFunction, itemTrigger:
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
		case 'R':
			a.loadSchema()
			return
		}
	}
}

// editObjectFromExplorer fetches the DDL for the view/procedure/function/
// trigger under the cursor and opens a new non-preview tab pre-filled with
// it. Tags the session with editKind/editSchema/editName so the Apply flow
// (Phase 1.5) can diff + re-run against the source object. Sync fetch with
// a short timeout; if the driver returns ErrDefinitionUnsupported the
// status line explains and no tab is opened.
func (m *mainLayer) editObjectFromExplorer(a *app) {
	if a.conn == nil {
		m.status = "not connected"
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	body, err := a.conn.Definition(ctx, kind, schema, name)
	if err != nil {
		if errors.Is(err, db.ErrDefinitionUnsupported) {
			m.status = "edit: " + kind + " not supported by this driver"
			return
		}
		m.status = "edit: " + err.Error()
		return
	}
	sess := newSession()
	label := name
	if schema != "" {
		label = schema + "." + name
	}
	sess.title = label
	sess.editor.buf.SetText(body)
	sess.editKind = kind
	sess.editSchema = schema
	sess.editName = name
	sess.editOriginal = body
	m.sessions = append(m.sessions, sess)
	m.activeTab = len(m.sessions) - 1
	m.session = sess
	m.focus = FocusQuery
}

// handleResultsKey processes keys when the Results panel is focused.
// Navigation moves the cell cursor (Up/Dn/Lt/Rt); PgUp/PgDn page the
// row cursor. Home/End jump to the first/last row. 'w' toggles wrap.
// 's' cycles sort state on the current column; '/' opens the filter
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
	innerRow := r.row + 1
	innerCol := r.col + 1
	innerW := r.w - 2
	innerH := r.h - 2
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
	c.setFg(colorOK)
	c.writeAt(row, col, truncate(msg, innerW))
	c.resetStyle()
}

// drawResultsError renders the last query error in place of the table.
// The error text is hard-wrapped to the inner width and scrolled by
// resultsErrScroll. Up/Dn adjust the scroll; 'y' and Alt+A copy the
// full error string to the clipboard.
func (m *mainLayer) drawResultsError(c *cellbuf, r rect) {
	innerRow := r.row + 1
	innerCol := r.col + 1
	innerW := r.w - 2
	innerH := r.h - 2
	if innerW <= 0 || innerH <= 0 {
		return
	}
	header := "Query error:"
	if m.lastErrLine > 0 {
		header = fmt.Sprintf("Query error (line %d):", m.lastErrLine)
	}
	c.setFg(colorError)
	c.writeAt(innerRow, innerCol, truncate(header, innerW))
	c.resetStyle()
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
		c.writeAt(bodyRow+i, innerCol, truncate(line, innerW))
	}
}

// formatQuery reformats the editor's current buffer using the
// sqltok heuristic formatter. The buffer's SetText path pushes a
// snapshot for undo, so Ctrl+Z restores the original text if the
// formatted output isn't what the user wanted. Empty buffers short
// out here and in the Alt+F binding above.
func (m *mainLayer) formatQuery() {
	src := m.editor.buf.Text()
	formatted := sqltok.Format(src)
	if formatted == src {
		m.status = "already formatted"
		return
	}
	m.editor.buf.SetText(formatted)
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
		sess := newSession()
		sess.preview = true
		m.sessions = append(m.sessions, sess)
		prev = len(m.sessions) - 1
	}
	sess := m.sessions[prev]
	sess.title = t.Name
	sess.editor.buf.SetText(sql)
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
	case 'o':
		a.pushLayer(newOpenLayer(""))
	case 's':
		m.saveActive(a)
	case 'S':
		seed := ""
		if m.activeTab >= 0 && m.activeTab < len(m.sessions) {
			sess := m.sessions[m.activeTab]
			if sess.sourcePath != "" {
				seed = sess.sourcePath
			} else {
				seed = sess.title
				if !strings.HasSuffix(strings.ToLower(seed), ".sql") {
					seed += ".sql"
				}
			}
		}
		a.pushLayer(newSaveLayer(m.activeTab, seed))
	case 'p':
		// Explain plan for current editor SQL.
		sql := strings.TrimSpace(m.editor.buf.Text())
		if sql == "" {
			m.status = "nothing to explain"
			return
		}
		if a.conn == nil {
			m.status = "not connected"
			return
		}
		tree, err := a.runExplain(sql)
		if err != nil {
			m.status = "explain: " + err.Error()
			return
		}
		a.pushLayer(newExplainLayer(tree))
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
// queryRightInfo returns the "Ln N, Col M" label shown in the top-right
// corner of the Query frame. Values are 1-based to match common editor
// conventions (VS Code, vim status line).
func (m *mainLayer) queryRightInfo() string {
	row, col := m.editor.buf.Cursor()
	return fmt.Sprintf("Ln %d, Col %d", row+1, col+1)
}

func (m *mainLayer) resultsRightInfo(_ *app) string {
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

// statusText builds the footer line. Layout:
//
//	 [focus]  connection  |  <hints from topmost layer>    (<transient status>)
//
// Hints come first so critical keys (Ctrl+Q=quit, Alt+1/2/3=focus) survive
// right-edge truncation on narrow terminals. The parenthesized status is
// query feedback like "running..." or "3 row(s) in 12ms" and is allowed to
// be clipped because the Results panel itself shows the real outcome.
func (m *mainLayer) statusText(a *app, width int) string {
	conn := "○ (not connected)"
	if a.activeConn != nil {
		conn = "● " + a.activeConn.Name
	}
	hints := a.topLayer().Hints(a)
	prefix := fmt.Sprintf(" [%s]  %s  │  ", m.focus, conn)
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

func (m *mainLayer) explorerHints(_ *app) string {
	enterHint := ""
	sHint := ""
	switch m.explorer.SelectedKind() {
	case itemTable:
		enterHint = "Enter=SELECT"
	case itemView:
		enterHint = "Enter=edit"
		sHint = "s=SELECT"
	case itemProcedure, itemFunction, itemTrigger:
		enterHint = "Enter=edit"
	case itemSchema, itemSubgroup:
		enterHint = "Enter=expand"
	}
	return joinHints(
		"F1=help",
		"Ctrl+Q=quit",
		enterHint,
		sHint,
		"Space=menu",
	)
}

func (m *mainLayer) queryHints(a *app) string {
	connected := a.conn != nil
	running := m.running
	hasText := m.editor.buf.LineCount() > 1 || len(m.editor.buf.Line(0)) > 0
	return joinHints(
		"F1=help",
		"Ctrl+Q=quit",
		hintIf(connected && !running, "F5=run"),
		hintIf(running, "Ctrl+C=cancel"),
		hintIf(hasText, "Alt+F=format"),
		"Ctrl+T=new tab",
	)
}

func (m *mainLayer) resultsHints(a *app) string {
	if m.inErrorView(a) {
		return joinHints(
			"F1=help",
			"Ctrl+Q=quit",
			"y=copy error",
			"Space=menu",
		)
	}
	hasRows := m.table.RowCount() > 0
	return joinHints(
		"F1=help",
		"Ctrl+Q=quit",
		hintIf(hasRows, "Enter=inspect"),
		hintIf(hasRows, "y=copy"),
		"Space=menu",
	)
}

func (m *mainLayer) spaceMenuHints(a *app) string {
	hasText := m.editor.buf.LineCount() > 1 || len(m.editor.buf.Line(0)) > 0
	return joinHints(
		"c=connect",
		hintIf(a.conn != nil, "x=disconnect"),
		"o=open",
		"s=save",
		"S=save as",
		hintIf(m.table.HasColumns(), "e=export"),
		"h=history",
		hintIf(a.conn != nil && hasText, "p=explain"),
		"q=quit",
		"Esc=cancel",
	)
}
