package tui

import (
	"github.com/Nulifyer/sqlgo/internal/db"
)

type explorerSQLAction int

const (
	explorerActionSelect explorerSQLAction = iota
	explorerActionInsert
	explorerActionUpdate
	explorerActionDelete
	explorerActionDesign
	explorerActionCopy
)

type objectAction struct {
	label  string
	key    rune
	action explorerSQLAction
}

type objectActionLayer struct {
	table   db.TableRef
	actions []objectAction
	cursor  int
}

func newObjectActionLayer(t db.TableRef) *objectActionLayer {
	return &objectActionLayer{
		table: t,
		actions: []objectAction{
			{label: "SELECT", key: 's', action: explorerActionSelect},
			{label: "INSERT", key: 'i', action: explorerActionInsert},
			{label: "UPDATE", key: 'u', action: explorerActionUpdate},
			{label: "DELETE", key: 'x', action: explorerActionDelete},
			{label: "Design", key: 'd', action: explorerActionDesign},
			{label: "Copy name", key: 'y', action: explorerActionCopy},
		},
	}
}

func (l *objectActionLayer) Draw(a *app, c *cellbuf) {
	boxW := 34
	if boxW > a.term.width-dialogMargin {
		boxW = a.term.width - dialogMargin
	}
	if boxW < 28 {
		boxW = 28
	}
	boxH := len(l.actions) + 4
	if boxH > a.term.height-dialogMargin {
		boxH = a.term.height - dialogMargin
	}
	row := (a.term.height - boxH) / 2
	col := (a.term.width - boxW) / 2
	if row < 1 {
		row = 1
	}
	if col < 1 {
		col = 1
	}
	r := rect{Row: row, Col: col, W: boxW, H: boxH}
	c.FillRect(r)
	drawFrame(c, r, "Object actions", true)
	innerCol := col + 2
	innerW := boxW - 4
	for i, action := range l.actions {
		line := string(action.key) + "  " + action.label
		lineRow := row + 1 + i
		if i == l.cursor {
			c.SetFg(colorTitleFocused)
			c.WriteAt(lineRow, innerCol, truncate("> "+line, innerW))
			c.ResetStyle()
		} else {
			c.WriteAt(lineRow, innerCol, truncate("  "+line, innerW))
		}
	}
}

func (l *objectActionLayer) HandleKey(a *app, k Key) {
	switch k.Kind {
	case KeyEsc:
		a.popLayer()
		return
	case KeyUp, KeyLeft:
		if l.cursor > 0 {
			l.cursor--
		}
		return
	case KeyDown, KeyRight, KeyTab:
		if l.cursor < len(l.actions)-1 {
			l.cursor++
		}
		return
	case KeyEnter:
		l.commit(a, l.actions[l.cursor].action)
		return
	}
	if k.Kind == KeyRune && !k.Ctrl && !k.Alt {
		for _, action := range l.actions {
			if k.Rune == action.key || k.Rune == upperASCII(action.key) {
				l.commit(a, action.action)
				return
			}
		}
	}
}

func (l *objectActionLayer) commit(a *app, action explorerSQLAction) {
	a.popLayer()
	if m := a.mainLayerPtr(); m != nil {
		m.runExplorerObjectAction(a, l.table, action)
	}
}

func (l *objectActionLayer) Hints(a *app) string {
	_ = a
	return joinHints("↑/↓=select", "↵=open", "s/i/u/x/d/y=action", "Esc=close")
}

func upperASCII(r rune) rune {
	if r >= 'a' && r <= 'z' {
		return r - ('a' - 'A')
	}
	return r
}
