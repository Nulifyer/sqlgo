package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/tui/widget"
)

// catalogLayer is the SSMS-style "change active database" picker. Opens
// from the editor (Ctrl+K d) without requiring the user to move cursor
// focus into the explorer. Lists user-visible databases for the active
// cross-DB connection, filterable by typing, Enter commits the pick to
// the active query tab's activeCatalog. The first entry is a sentinel
// that clears the pin (session falls back to the login default).
type catalogLayer struct {
	all     []string
	entries []string
	search  *input
	list    widget.ScrollList
	status  string
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
	cl.list.Len = len(cl.entries)
	cl.list.Clamp()
	if len(cl.all) == 0 {
		cl.status = "no databases available"
	} else {
		cl.status = fmt.Sprintf("%d database(s)", len(cl.all))
	}
}

func (cl *catalogLayer) Draw(a *app, c *cellbuf) {
	r := widget.CenterDialog(a.term.width, a.term.height, widget.DialogOpts{
		PrefW: 60, PrefH: 16, MinW: 30, MinH: 10, Margin: dialogMargin,
	})
	row, col := r.Row, r.Col

	title := "Change database"
	m := a.mainLayerPtr()
	if m != nil && m.session != nil && m.session.activeCatalog != "" {
		title = title + " (current: " + m.session.activeCatalog + ")"
	}
	widget.DrawDialog(c, r, title, true)

	innerCol := col + 2
	c.WriteAt(row+1, innerCol, "Search:")
	searchCol := innerCol + 8
	searchW := r.W - 8 - 4
	if searchW < 1 {
		searchW = 1
	}
	drawInput(c, cl.search, row+1, searchCol, searchW)

	c.HLine(row+2, col+1, col+r.W-2, '─')

	listTop := row + 3
	listBot := row + r.H - 3
	listH := listBot - listTop + 1
	if listH < 1 {
		listH = 1
	}
	cl.list.ListTop = listTop
	cl.list.ListH = listH
	cl.list.ViewportScroll(listH)

	start, end := cl.list.VisibleRange()
	for i := start; i < end; i++ {
		label := cl.entries[i]
		y := listTop + (i - start)
		if cl.list.IsSelected(i) {
			c.SetFg(colorBorderFocused)
			c.WriteAt(y, innerCol, truncate("▶ "+label, r.W-4))
			c.ResetStyle()
		} else {
			c.WriteAt(y, innerCol, truncate("  "+label, r.W-4))
		}
	}

	if cl.status != "" {
		c.SetFg(colorStatusBar)
		c.WriteAt(row+r.H-2, innerCol, truncate(cl.status, r.W-4))
		c.ResetStyle()
	}
}

func (cl *catalogLayer) HandleKey(a *app, k Key) {
	switch k.Kind {
	case KeyEsc:
		a.popLayer()
		return
	case KeyEnter:
		cl.apply(a)
		return
	}
	if cl.list.HandleKey(k) {
		return
	}
	cl.search.Handle(k)
	cl.list.Selected = 0
	cl.list.Scroll = 0
	cl.refilter()
}

func (cl *catalogLayer) apply(a *app) {
	if cl.list.Selected < 0 || cl.list.Selected >= len(cl.entries) {
		return
	}
	pick := cl.entries[cl.list.Selected]
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
		if m.explorer != nil && m.explorer.dbMode {
			if _, loading := m.explorer.dbLoading[pick]; !loading {
				if _, loaded := m.explorer.dbSchemas[pick]; !loaded {
					a.loadDatabaseSchema(pick)
				}
			}
		}
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
