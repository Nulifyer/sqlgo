package tui

import (
	"context"
	"errors"
	"fmt"

	"github.com/Nulifyer/sqlgo/internal/config"
	"github.com/Nulifyer/sqlgo/internal/store"
)

// picker is the connection selection view. It lists saved connections and
// supports add/edit/delete plus Enter-to-connect.
type picker struct {
	conns    []config.Connection
	selected int
	status   string

	// lastListTop / lastVisible record the last-rendered list geometry
	// so the mouse hit test can map a Y coordinate to a row index
	// without recomputing the dialog box layout. Populated by draw.
	lastListTop int
	lastVisible int
}

func newPicker(conns []config.Connection) *picker {
	return &picker{conns: conns}
}

// setConns refreshes the list (e.g. after save) and clamps the selection.
func (p *picker) setConns(cs []config.Connection) {
	p.conns = cs
	if p.selected >= len(cs) {
		p.selected = len(cs) - 1
	}
	if p.selected < 0 {
		p.selected = 0
	}
}

func (p *picker) moveUp() {
	if p.selected > 0 {
		p.selected--
	}
}

func (p *picker) moveDown() {
	if p.selected < len(p.conns)-1 {
		p.selected++
	}
}

func (p *picker) draw(s *cellbuf, termW, termH int) {
	boxW := 70
	if boxW > termW - dialogMargin {
		boxW = termW - dialogMargin
	}
	if boxW < 30 {
		boxW = 30
	}
	boxH := len(p.conns) + 8
	if boxH < 12 {
		boxH = 12
	}
	if boxH > termH - dialogMargin {
		boxH = termH - dialogMargin
	}
	row := (termH - boxH) / 2
	col := (termW - boxW) / 2
	if row < 1 {
		row = 1
	}
	if col < 1 {
		col = 1
	}
	r := rect{row: row, col: col, w: boxW, h: boxH}
	// Blank the overlay's footprint so the main view behind it doesn't
	// bleed through on cells this picker doesn't explicitly draw to.
	s.fillRect(r)
	drawFrame(s, r, "Connect", true)

	innerCol := col + 2
	cur := row + 1

	if len(p.conns) == 0 {
		s.writeAt(cur+1, innerCol, "No saved connections.")
		s.writeAt(cur+3, innerCol, "Press 'a' to add a connection.")
		s.writeAt(cur+4, innerCol, "Press Ctrl+Q to quit.")
	} else {
		s.writeAt(cur, innerCol, "Select a connection:")
		listTop := cur + 2
		maxRows := boxH - 6
		if maxRows < 1 {
			maxRows = 1
		}
		p.lastListTop = listTop
		p.lastVisible = maxRows
		if p.lastVisible > len(p.conns) {
			p.lastVisible = len(p.conns)
		}
		for i, c := range p.conns {
			if i >= maxRows {
				break
			}
			line := formatConn(c)
			if len(line) > boxW-6 {
				line = line[:boxW-6]
			}
			if i == p.selected {
				s.setFg(colorBorderFocused)
				s.writeAt(listTop+i, innerCol, "▶ "+line)
				s.resetStyle()
			} else {
				s.writeAt(listTop+i, innerCol, "  "+line)
			}
		}
	}

	// Transient status line inside the box (e.g. "connecting...",
	// "save failed"). Key hints live in the bottom footer via Hints().
	// Use the rune-aware truncate so a long DSN error doesn't cut a
	// UTF-8 sequence mid-byte or drop without an ellipsis.
	if p.status != "" {
		s.setFg(colorBorderFocused)
		s.writeAt(r.row+r.h-2, innerCol, truncate(p.status, boxW-4))
		s.resetStyle()
	}
}

func formatConn(c config.Connection) string {
	db := c.Database
	if db == "" {
		db = "-"
	}
	return fmt.Sprintf("%-20s  %s://%s@%s:%d/%s",
		c.Name, c.Driver, c.User, c.Host, c.Port, db)
}

// pickerLayer adapts picker to the Layer interface. It reads saved
// connections from a.confFile on each key so an add/edit through the
// form reflects immediately on return.
type pickerLayer struct {
	p      *picker
	clicks clickTracker
}

func newPickerLayer(conns []config.Connection) *pickerLayer {
	return &pickerLayer{p: newPicker(conns)}
}

func (pl *pickerLayer) Draw(a *app, c *cellbuf) {
	pl.p.setConns(a.connCache)
	pl.p.draw(c, a.term.width, a.term.height)
}

func (pl *pickerLayer) HandleKey(a *app, k Key) {
	if k.Kind == KeyEsc {
		// Only allow dismissing the picker if there's an active connection.
		if a.conn != nil {
			a.popLayer()
		}
		return
	}
	switch k.Kind {
	case KeyUp:
		pl.p.moveUp()
		return
	case KeyDown:
		pl.p.moveDown()
		return
	case KeyEnter:
		if len(pl.p.conns) == 0 {
			return
		}
		sel := pl.p.conns[pl.p.selected]
		a.connectTo(sel)
		return
	}
	if k.Kind == KeyRune && !k.Ctrl {
		switch k.Rune {
		case 'a':
			a.pushLayer(newFormLayer("Add connection", nil))
		case 'e':
			if len(pl.p.conns) == 0 {
				return
			}
			sel := pl.p.conns[pl.p.selected]
			// Resolve the keyring placeholder so the form shows the
			// real password for editing. If the keyring read fails we
			// still open the form -- the user can clear and retype.
			if pass, err := a.resolvePassword(sel); err == nil {
				sel.Password = pass
			}
			a.pushLayer(newFormLayer("Edit connection", &sel))
		case 'x':
			if len(pl.p.conns) == 0 {
				return
			}
			pl.deleteSelected(a)
		case 'K':
			if len(pl.p.conns) == 0 {
				return
			}
			pl.unlinkKeyring(a)
		}
	}
}

// unlinkKeyring wipes the keyring entries for the selected connection
// without touching its store row. Used when the keyring copy has
// gotten out of sync (e.g. user changed passwords outside sqlgo) or
// when they want to force re-entry on next connect.
func (pl *pickerLayer) unlinkKeyring(a *app) {
	i := pl.p.selected
	if i < 0 || i >= len(pl.p.conns) {
		return
	}
	name := pl.p.conns[i].Name
	ctx, cancel := context.WithTimeout(context.Background(), storeReadTimeout)
	defer cancel()
	if err := a.unlinkSecret(ctx, name); err != nil {
		pl.p.status = "unlink: " + err.Error()
		return
	}
	if err := a.refreshConnections(); err != nil {
		pl.p.status = "refresh: " + err.Error()
		return
	}
	pl.p.setConns(a.connCache)
	pl.p.status = "keyring cleared for " + name
}

func (pl *pickerLayer) deleteSelected(a *app) {
	i := pl.p.selected
	if i < 0 || i >= len(pl.p.conns) {
		return
	}
	name := pl.p.conns[i].Name
	ctx, cancel := context.WithTimeout(context.Background(), storeReadTimeout)
	defer cancel()
	if err := a.deleteConnection(ctx, name); err != nil {
		if errors.Is(err, store.ErrConnectionNotFound) {
			pl.p.status = "already gone"
		} else {
			pl.p.status = "delete failed: " + err.Error()
		}
		return
	}
	if err := a.refreshConnections(); err != nil {
		pl.p.status = "refresh failed: " + err.Error()
		return
	}
	pl.p.setConns(a.connCache)
	pl.p.status = "deleted"
}

// HandleInput routes mouse events: left-click selects the row under
// the pointer; double-click on a valid row connects. Wheel is a no-op
// because the visible list is currently capped at maxRows with no
// scroll -- if scrolling is ever added, route wheel here.
func (pl *pickerLayer) HandleInput(a *app, msg InputMsg) bool {
	mm, ok := msg.(MouseMsg)
	if !ok {
		return false
	}
	if mm.Button != MouseButtonLeft || mm.Action != MouseActionPress {
		return false
	}
	p := pl.p
	if p.lastVisible <= 0 {
		return false
	}
	rowIdx := mm.Y - p.lastListTop
	if rowIdx < 0 || rowIdx >= p.lastVisible {
		return false
	}
	p.selected = rowIdx
	count := pl.clicks.bump(mm)
	if count >= 2 && len(p.conns) > 0 {
		a.connectTo(p.conns[p.selected])
	}
	return true
}

// View enables mouse reporting while the picker is on top.
func (pl *pickerLayer) View(a *app) View {
	return View{AltScreen: true, MouseEnabled: true}
}

// setStatus lets the app poke feedback (e.g. "connecting...") at the
// picker without reaching into its internals.
func (pl *pickerLayer) setStatus(s string) { pl.p.status = s }

// Hints builds the footer hint line for the picker. Keys that wouldn't do
// anything in the current state (edit/delete with an empty list, Esc when
// not yet connected) are omitted so the footer reflects only what works.
func (pl *pickerLayer) Hints(a *app) string {
	hasList := len(pl.p.conns) > 0
	return joinHints(
		"Ctrl+Q=quit",
		hintIf(hasList, "Up/Dn=move"),
		hintIf(hasList, "Enter=connect"),
		"a=add",
		hintIf(hasList, "e=edit"),
		hintIf(hasList, "x=delete"),
		hintIf(hasList, "K=unlink-keyring"),
		hintIf(a.conn != nil, "Esc=back"),
	)
}
