package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Nulifyer/sqlgo/internal/store"
)

// historyLayer is the modal overlay for browsing stored query history.
// It loads the last N entries for the current connection on open, lets
// the user filter with the FTS5 index by typing in the search field,
// and pastes the selected entry's SQL back into the editor on Enter.
//
// Scope: per-connection only for now -- cross-connection browsing can
// come later if users ask. Keeping the scope narrow makes the layout
// simpler (no connection column) and matches how the ring buffer is
// already partitioned.
type historyLayer struct {
	search   *input
	entries  []store.HistoryEntry
	selected int
	scroll   int
	status   string
}

const historyPageSize = 50

func newHistoryLayer() *historyLayer {
	return &historyLayer{search: newInput("")}
}

// reload re-runs the current search (or lists recent entries when the
// search box is empty). Called on open and after every keystroke in the
// search field so results follow the user's typing.
func (h *historyLayer) reload(a *app) {
	if a.store == nil {
		h.status = "no store open"
		return
	}
	connName := ""
	if a.activeConn != nil {
		connName = a.activeConn.Name
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	q := strings.TrimSpace(h.search.String())
	var (
		entries []store.HistoryEntry
		err     error
	)
	if q == "" {
		entries, err = a.store.ListRecentHistory(ctx, connName, historyPageSize)
	} else {
		entries, err = a.store.SearchHistory(ctx, connName, q, historyPageSize)
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
		boxW = a.term.width - 4
	}
	if boxW < 50 {
		boxW = 50
	}
	boxH := 20
	if boxH > a.term.height-4 {
		boxH = a.term.height - 4
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
	if a.activeConn != nil {
		title = "History (" + a.activeConn.Name + ")"
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
	c.hLine(row+2, col+1, col+r.w-2, '-')

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
			line := formatHistoryLine(e, boxW-4)
			if idx == h.selected {
				c.setFg(colorBorderFocused)
				c.writeAt(listTop+i, innerCol, truncate("> "+line, boxW-4))
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
	}
	// Anything else goes to the search field; reload on every edit so
	// results follow the user's typing live.
	h.search.handle(k)
	h.selected = 0
	h.scroll = 0
	h.reload(a)
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
	return joinHints(
		"type=search",
		hintIf(len(h.entries) > 0, "Up/Dn/PgUp/PgDn=move"),
		hintIf(len(h.entries) > 0, "Enter=use"),
		"Esc=close",
	)
}

// formatHistoryLine builds a compact single-line summary: timestamp,
// elapsed, row count, status tag, and the first line of the SQL. maxW
// limits how much SQL gets appended so the layer's render loop doesn't
// have to know about the left-column widths.
func formatHistoryLine(e store.HistoryEntry, maxW int) string {
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
	return fmt.Sprintf("%s  %s  %s", ts, status, first)
}
