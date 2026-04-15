package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/Nulifyer/sqlgo/internal/db"
)

// catalogLayer is the SSMS-style "change active database" picker. Opens
// from the editor (Ctrl+K d) without requiring the user to move cursor
// focus into the explorer. Lists user-visible databases for the active
// cross-DB connection, filterable by typing, Enter commits the pick to
// the active query tab's activeCatalog. The first entry is a sentinel
// that clears the pin (session falls back to the login default).
type catalogLayer struct {
	all      []string
	entries  []string
	search   *input
	selected int
	scroll   int
	status   string

	lastListTop int
	lastListH   int
}

// catalogClear is the sentinel label rendered above the real DB list.
// Picking it clears the tab's activeCatalog so the next run uses the
// login default database. Keeps the "unpin" action in the same picker
// instead of forcing a separate keybind.
const catalogClear = "(default)"

func newCatalogLayer(names []string) *catalogLayer {
	cl := &catalogLayer{all: names, search: newInput("")}
	cl.refilter()
	return cl
}

func (cl *catalogLayer) refilter() {
	q := strings.ToLower(strings.TrimSpace(cl.search.String()))
	cl.entries = cl.entries[:0]
	cl.entries = append(cl.entries, catalogClear)
	for _, n := range cl.all {
		if q == "" || strings.Contains(strings.ToLower(n), q) {
			cl.entries = append(cl.entries, n)
		}
	}
	if cl.selected >= len(cl.entries) {
		cl.selected = len(cl.entries) - 1
	}
	if cl.selected < 0 {
		cl.selected = 0
	}
	if len(cl.all) == 0 {
		cl.status = "no databases available"
	} else {
		cl.status = fmt.Sprintf("%d database(s)", len(cl.all))
	}
}

func (cl *catalogLayer) Draw(a *app, c *cellbuf) {
	boxW := 60
	if boxW > a.term.width-dialogMargin {
		boxW = a.term.width - dialogMargin
	}
	if boxW < 30 {
		boxW = 30
	}
	boxH := 16
	if boxH > a.term.height-dialogMargin {
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

	title := "Change database"
	m := a.mainLayerPtr()
	if m != nil && m.session != nil && m.session.activeCatalog != "" {
		title = title + " (current: " + m.session.activeCatalog + ")"
	}
	drawFrame(c, r, title, true)

	innerCol := col + 2
	c.writeAt(row+1, innerCol, "Search:")
	searchCol := innerCol + 8
	searchW := boxW - 8 - 4
	if searchW < 1 {
		searchW = 1
	}
	val := cl.search.String()
	rs := []rune(val)
	if len(rs) > searchW {
		rs = rs[len(rs)-searchW:]
	}
	c.writeAt(row+1, searchCol, string(rs))
	c.placeCursor(row+1, searchCol+len(rs))

	c.hLine(row+2, col+1, col+r.w-2, '─')

	listTop := row + 3
	listBot := row + r.h - 3
	listH := listBot - listTop + 1
	if listH < 1 {
		listH = 1
	}
	cl.lastListTop = listTop
	cl.lastListH = listH

	if cl.selected < cl.scroll {
		cl.scroll = cl.selected
	}
	if cl.selected >= cl.scroll+listH {
		cl.scroll = cl.selected - listH + 1
	}
	if cl.scroll < 0 {
		cl.scroll = 0
	}

	for i := 0; i < listH; i++ {
		idx := cl.scroll + i
		if idx >= len(cl.entries) {
			break
		}
		label := cl.entries[idx]
		if idx == cl.selected {
			c.setFg(colorBorderFocused)
			c.writeAt(listTop+i, innerCol, truncate("▶ "+label, boxW-4))
			c.resetStyle()
		} else {
			c.writeAt(listTop+i, innerCol, truncate("  "+label, boxW-4))
		}
	}

	if cl.status != "" {
		c.setFg(colorStatusBar)
		c.writeAt(row+r.h-2, innerCol, truncate(cl.status, boxW-4))
		c.resetStyle()
	}
}

func (cl *catalogLayer) HandleKey(a *app, k Key) {
	switch k.Kind {
	case KeyEsc:
		a.popLayer()
		return
	case KeyUp:
		if cl.selected > 0 {
			cl.selected--
		}
		return
	case KeyDown:
		if cl.selected < len(cl.entries)-1 {
			cl.selected++
		}
		return
	case KeyPgUp:
		cl.selected -= 10
		if cl.selected < 0 {
			cl.selected = 0
		}
		return
	case KeyPgDn:
		cl.selected += 10
		if cl.selected > len(cl.entries)-1 {
			cl.selected = len(cl.entries) - 1
		}
		return
	case KeyEnter:
		cl.apply(a)
		return
	}
	cl.search.handle(k)
	cl.selected = 0
	cl.scroll = 0
	cl.refilter()
}

func (cl *catalogLayer) apply(a *app) {
	if cl.selected < 0 || cl.selected >= len(cl.entries) {
		return
	}
	pick := cl.entries[cl.selected]
	m := a.mainLayerPtr()
	if m == nil || m.session == nil {
		a.popLayer()
		return
	}
	if pick == catalogClear {
		m.session.activeCatalog = ""
		m.status = "tab uses login default database"
	} else {
		m.session.activeCatalog = pick
		m.status = "tab now uses " + pick
	}
	a.popLayer()
}

func (cl *catalogLayer) Hints(a *app) string {
	_ = a
	return joinHints("type=filter", "Up/Dn=move", "Enter=use", "Esc=cancel")
}

// openCatalogLayer gathers the DB list from the explorer cache when
// possible, else issues ListDatabases on the active connection. Opens
// a catalogLayer populated with the result. No-op when the connection
// isn't cross-DB capable.
func (a *app) openCatalogLayer() {
	m := a.mainLayerPtr()
	if a.conn == nil {
		m.status = "not connected"
		return
	}
	if !a.conn.Capabilities().SupportsCrossDatabase {
		m.status = "driver has a single database per connection"
		return
	}
	names := append([]string(nil), m.explorer.databases...)
	if len(names) == 0 {
		lister, ok := a.conn.(db.DatabaseLister)
		if !ok {
			m.status = "driver does not list databases"
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), storeReadTimeout)
		defer cancel()
		got, err := lister.ListDatabases(ctx)
		if err != nil {
			m.status = "list databases: " + err.Error()
			return
		}
		names = got
	}
	a.pushLayer(newCatalogLayer(names))
}
