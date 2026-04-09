package tui

import (
	"fmt"

	"github.com/Nulifyer/sqlgo/internal/config"
)

// picker is the connection selection view. It lists saved connections and
// supports add/edit/delete plus Enter-to-connect.
type picker struct {
	conns    []config.Connection
	selected int
	status   string
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
	if boxW > termW-4 {
		boxW = termW - 4
	}
	if boxW < 30 {
		boxW = 30
	}
	boxH := len(p.conns) + 8
	if boxH < 12 {
		boxH = 12
	}
	if boxH > termH-4 {
		boxH = termH - 4
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
				s.writeAt(listTop+i, innerCol, "> "+line)
				s.resetStyle()
			} else {
				s.writeAt(listTop+i, innerCol, "  "+line)
			}
		}
	}

	// Status line (above hint) and hint line (bottom-inside).
	hintRow := r.row + r.h - 2
	if p.status != "" {
		s.setFg(colorBorderFocused)
		status := p.status
		if len(status) > boxW-4 {
			status = status[:boxW-4]
		}
		s.writeAt(hintRow-1, innerCol, status)
		s.resetStyle()
	}
	s.setFg(colorStatusBar)
	s.writeAt(hintRow, innerCol, "Enter=connect  a=add  e=edit  x=delete  Esc=back")
	s.resetStyle()
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
	p *picker
}

func newPickerLayer(conns []config.Connection) *pickerLayer {
	return &pickerLayer{p: newPicker(conns)}
}

func (pl *pickerLayer) Draw(a *app, c *cellbuf) {
	pl.p.setConns(a.confFile.Connections)
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
			a.pushLayer(newFormLayer("Add connection", nil, -1))
		case 'e':
			if len(pl.p.conns) == 0 {
				return
			}
			sel := pl.p.conns[pl.p.selected]
			a.pushLayer(newFormLayer("Edit connection", &sel, pl.p.selected))
		case 'x':
			if len(pl.p.conns) == 0 {
				return
			}
			pl.deleteSelected(a)
		}
	}
}

func (pl *pickerLayer) deleteSelected(a *app) {
	i := pl.p.selected
	a.confFile.Connections = append(a.confFile.Connections[:i], a.confFile.Connections[i+1:]...)
	if err := config.Save(a.confFile); err != nil {
		pl.p.status = "save failed: " + err.Error()
		return
	}
	pl.p.setConns(a.confFile.Connections)
	pl.p.status = "deleted"
}

// setStatus lets the app poke feedback (e.g. "connecting...") at the
// picker without reaching into its internals.
func (pl *pickerLayer) setStatus(s string) { pl.p.status = s }
