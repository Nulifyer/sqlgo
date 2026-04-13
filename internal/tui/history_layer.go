package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Nulifyer/sqlgo/internal/store"
)

// historyScope selects which connection's history the browser shows.
type historyScope int

const (
	// scopeCurrent lists only the active connection's entries. This
	// is the default on open because it's the common case.
	scopeCurrent historyScope = iota
	// scopeAll lists every recorded query across every connection.
	// The connection name is prepended in the list so users can
	// distinguish them.
	scopeAll
)

// historyLayer is the modal overlay for browsing stored query history.
// It loads the last N entries for the current connection on open, lets
// the user filter with the FTS5 index by typing in the search field,
// pastes the selected entry's SQL back into the editor on Enter,
// deletes the selected entry with 'd', and wipes the whole current
// scope with 'X' (two-press confirmation). Tab toggles between the
// current-connection scope and the all-connections scope.
type historyLayer struct {
	search    *input
	entries   []store.HistoryEntry
	selected  int
	scroll    int
	scope     historyScope
	status    string
	// clearArmed is a transient flag: pressing 'X' the first time
	// arms the confirmation, a second press within the confirmation
	// window actually wipes. Any other keypress disarms.
	clearArmed bool
}

// historyFetchSize scales the row pull with the terminal height so
// taller terminals load enough rows to fill the visible list plus a
// scroll buffer, while short terminals don't waste a round trip.
// Clamped to [20, 200] so a giant terminal doesn't pull unbounded
// history and a tiny one still has something to scroll through.
func historyFetchSize(a *app) int {
	n := (a.term.height - 6) * 2
	if n < 20 {
		n = 20
	}
	if n > 200 {
		n = 200
	}
	return n
}

func newHistoryLayer() *historyLayer {
	return &historyLayer{search: newInput("")}
}

// reload re-runs the current search (or lists recent entries when the
// search box is empty). Called on open and after every keystroke in the
// search field so results follow the user's typing. The store query
// receives an empty connection name when scope==scopeAll, which makes
// ListRecentHistory / SearchHistory return entries across every
// connection.
func (h *historyLayer) reload(a *app) {
	if a.store == nil {
		h.status = "no store open"
		return
	}
	connName := ""
	if h.scope == scopeCurrent && a.activeConn != nil {
		connName = a.activeConn.Name
	}
	ctx, cancel := context.WithTimeout(context.Background(), storeQuickTimeout)
	defer cancel()

	q := strings.TrimSpace(h.search.String())
	var (
		entries []store.HistoryEntry
		err     error
	)
	limit := historyFetchSize(a)
	if q == "" {
		entries, err = a.store.ListRecentHistory(ctx, connName, limit)
	} else {
		entries, err = a.store.SearchHistory(ctx, connName, q, limit)
	}
	if err != nil {
		h.status = "history: " + err.Error()
		h.entries = nil
		h.selected = 0
		h.scroll = 0
		return
	}
	h.entries = entries
	if h.selected >= len(entries) {
		h.selected = len(entries) - 1
	}
	if h.selected < 0 {
		h.selected = 0
	}
	if len(entries) == 0 {
		h.status = "no matches"
	} else {
		h.status = fmt.Sprintf("%d entries", len(entries))
	}
}

func (h *historyLayer) Draw(a *app, c *cellbuf) {
	boxW := 100
	if boxW > a.term.width-4 {
		boxW = a.term.width - dialogMargin
	}
	if boxW < 50 {
		boxW = 50
	}
	boxH := 20
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

	title := "History"
	switch h.scope {
	case scopeAll:
		title = "History (all connections)"
	case scopeCurrent:
		if a.activeConn != nil {
			title = "History (" + a.activeConn.Name + ")"
		}
	}
	drawFrame(c, r, title, true)

	innerCol := col + 2
	// Search field on the first inner row.
	c.writeAt(row+1, innerCol, "Search:")
	searchCol := innerCol + 8
	searchW := boxW - 8 - 4
	if searchW < 1 {
		searchW = 1
	}
	val := h.search.String()
	rs := []rune(val)
	if len(rs) > searchW {
		rs = rs[len(rs)-searchW:]
	}
	c.writeAt(row+1, searchCol, string(rs))
	c.placeCursor(row+1, searchCol+len(rs))

	// Separator row of dashes under the search field.
	c.hLine(row+2, col+1, col+r.w-2, '─')

	// Results list: rows 3..(h-2) inclusive inside the box.
	listTop := row + 3
	listBot := row + r.h - 3
	listH := listBot - listTop + 1
	if listH < 1 {
		listH = 1
	}

	if len(h.entries) == 0 {
		msg := "(no history)"
		if strings.TrimSpace(h.search.String()) != "" {
			msg = "(no matches)"
		}
		c.writeAt(listTop, innerCol, truncate(msg, boxW-4))
	} else {
		// Keep the selected row visible.
		if h.selected < h.scroll {
			h.scroll = h.selected
		}
		if h.selected >= h.scroll+listH {
			h.scroll = h.selected - listH + 1
		}
		if h.scroll < 0 {
			h.scroll = 0
		}

		for i := 0; i < listH; i++ {
			idx := h.scroll + i
			if idx >= len(h.entries) {
				break
			}
			e := h.entries[idx]
			line := formatHistoryLine(e, boxW-4, h.scope == scopeAll)
			if idx == h.selected {
				c.setFg(colorBorderFocused)
				c.writeAt(listTop+i, innerCol, truncate("▶ "+line, boxW-4))
				c.resetStyle()
			} else {
				c.writeAt(listTop+i, innerCol, truncate("  "+line, boxW-4))
			}
		}
	}

	// Status line at bottom of the box.
	if h.status != "" {
		c.setFg(colorStatusBar)
		c.writeAt(row+r.h-2, innerCol, truncate(h.status, boxW-4))
		c.resetStyle()
	}
}

func (h *historyLayer) HandleKey(a *app, k Key) {
	switch k.Kind {
	case KeyEsc:
		a.popLayer()
		return
	case KeyUp:
		if h.selected > 0 {
			h.selected--
		}
		return
	case KeyDown:
		if h.selected < len(h.entries)-1 {
			h.selected++
		}
		return
	case KeyPgUp:
		h.selected -= 10
		if h.selected < 0 {
			h.selected = 0
		}
		return
	case KeyPgDn:
		h.selected += 10
		if h.selected > len(h.entries)-1 {
			h.selected = len(h.entries) - 1
		}
		return
	case KeyEnter:
		h.useSelected(a)
		return
	case KeyTab:
		// Flip between current-connection and all-connections scope.
		if h.scope == scopeCurrent {
			h.scope = scopeAll
		} else {
			h.scope = scopeCurrent
		}
		h.selected = 0
		h.scroll = 0
		h.clearArmed = false
		h.reload(a)
		return
	}
	// 'd' deletes the selected entry; 'X' wipes the whole current
	// scope with a two-press confirmation. Both disarm the clear
	// confirmation if it was set by something other than another X.
	if k.Kind == KeyRune && !k.Ctrl && !k.Alt {
		switch k.Rune {
		case 'd':
			h.clearArmed = false
			h.deleteSelected(a)
			return
		case 'X':
			h.confirmClear(a)
			return
		}
	}
	// Anything else goes to the search field; reload on every edit so
	// results follow the user's typing live. Any non-X key also
	// disarms the clear confirmation so the user can't accidentally
	// confirm by typing in the search box.
	h.clearArmed = false
	h.search.handle(k)
	h.selected = 0
	h.scroll = 0
	h.reload(a)
}

// deleteSelected removes the currently highlighted history entry via
// the store and reloads the visible list. Status line carries the
// outcome so the user sees which id went away.
func (h *historyLayer) deleteSelected(a *app) {
	if h.selected < 0 || h.selected >= len(h.entries) {
		return
	}
	target := h.entries[h.selected]
	ctx, cancel := context.WithTimeout(context.Background(), storeQuickTimeout)
	defer cancel()
	if err := a.store.DeleteHistory(ctx, target.ID); err != nil {
		h.status = "delete: " + err.Error()
		return
	}
	// Stay on roughly the same position after reload: if we just
	// deleted the last row, step back by one.
	prev := h.selected
	h.reload(a)
	if prev >= len(h.entries) {
		h.selected = len(h.entries) - 1
		if h.selected < 0 {
			h.selected = 0
		}
	} else {
		h.selected = prev
	}
	h.status = fmt.Sprintf("deleted entry #%d", target.ID)
}

// confirmClear implements the two-press clear-all flow. First press
// arms the confirmation and updates the status line; second press
// actually wipes (scoped to the current connection or global,
// depending on scope); any other key disarms.
func (h *historyLayer) confirmClear(a *app) {
	if !h.clearArmed {
		h.clearArmed = true
		if h.scope == scopeAll {
			h.status = "press X again to clear ALL history"
		} else {
			name := "(disconnected)"
			if a.activeConn != nil {
				name = a.activeConn.Name
			}
			h.status = "press X again to clear history for " + name
		}
		return
	}
	h.clearArmed = false
	connName := ""
	if h.scope == scopeCurrent && a.activeConn != nil {
		connName = a.activeConn.Name
	}
	ctx, cancel := context.WithTimeout(context.Background(), storeReadTimeout)
	defer cancel()
	n, err := a.store.ClearHistory(ctx, connName)
	if err != nil {
		h.status = "clear: " + err.Error()
		return
	}
	h.selected = 0
	h.scroll = 0
	h.reload(a)
	h.status = fmt.Sprintf("cleared %d entries", n)
}

func (h *historyLayer) useSelected(a *app) {
	if h.selected < 0 || h.selected >= len(h.entries) {
		return
	}
	sql := h.entries[h.selected].SQL
	m := a.mainLayerPtr()
	m.editor.buf.SetText(sql)
	m.focus = FocusQuery
	a.popLayer()
}

func (h *historyLayer) Hints(a *app) string {
	_ = a
	hasEntries := len(h.entries) > 0
	return joinHints(
		"type=search",
		hintIf(hasEntries, "Up/Dn/PgUp/PgDn=move"),
		hintIf(hasEntries, "Enter=use"),
		hintIf(hasEntries, "d=delete"),
		"X=clear",
		"Tab=scope",
		"Esc=close",
	)
}

// formatHistoryLine builds a compact single-line summary: timestamp,
// elapsed, row count, status tag, optionally the connection name,
// and the first line of the SQL. maxW limits how much SQL gets
// appended so the layer's render loop doesn't have to know about the
// left-column widths. When showConn is true, the connection is
// prepended to the row so the "all connections" scope can tell
// entries apart.
func formatHistoryLine(e store.HistoryEntry, maxW int, showConn bool) string {
	_ = maxW
	ts := e.ExecutedAt.Local().Format("2006-01-02 15:04:05")
	status := fmt.Sprintf("%4dr %5s", e.RowCount, e.Elapsed.Round(time.Millisecond))
	if e.Error != "" {
		status = fmt.Sprintf("ERR   %5s", e.Elapsed.Round(time.Millisecond))
	}
	// First non-empty line of the SQL so multi-line queries stay on a
	// single row in the list view.
	first := ""
	for _, line := range strings.Split(e.SQL, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			first = line
			break
		}
	}
	if showConn {
		conn := e.ConnectionName
		if conn == "" {
			conn = "?"
		}
		// Truncate long connection names so the layout stays aligned.
		if len(conn) > 12 {
			conn = conn[:12]
		}
		return fmt.Sprintf("%s  %-12s  %s  %s", ts, conn, status, first)
	}
	return fmt.Sprintf("%s  %s  %s", ts, status, first)
}
