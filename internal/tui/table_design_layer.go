package tui

import (
	"fmt"
	"strings"

	"github.com/Nulifyer/sqlgo/internal/db"
)

type tableDesignLayer struct {
	caps   db.Capabilities
	design db.TableDesign
	cursor int
	scroll int
}

func newTableDesignLayer(caps db.Capabilities, design db.TableDesign) *tableDesignLayer {
	return &tableDesignLayer{caps: caps, design: design}
}

func (l *tableDesignLayer) Draw(a *app, c *cellbuf) {
	boxW := 110
	if boxW > a.term.width-dialogMargin {
		boxW = a.term.width - dialogMargin
	}
	if boxW < 54 {
		boxW = 54
	}
	boxH := 26
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
	r := rect{Row: row, Col: col, W: boxW, H: boxH}
	c.FillRect(r)
	drawFrameInfo(c, r, "Table design", QualifiedName(l.caps, l.design.Table), true)

	innerCol := col + 2
	innerRow := row + 1
	innerW := boxW - 4
	bodyH := boxH - 3
	if innerW <= 0 || bodyH <= 0 {
		return
	}
	header := fmt.Sprintf("%-4s %-26s %-20s %-6s %-12s %-18s %s", "#", "Name", "Type", "Null", "Key", "Default", "Extra")
	c.SetFg(colorStatusBar)
	c.WriteAt(innerRow, innerCol, truncate(header, innerW))
	c.ResetStyle()

	if l.cursor < l.scroll {
		l.scroll = l.cursor
	}
	if l.cursor >= l.scroll+bodyH {
		l.scroll = l.cursor - bodyH + 1
	}
	if l.scroll < 0 {
		l.scroll = 0
	}
	for i := 0; i < bodyH; i++ {
		idx := l.scroll + i
		if idx >= len(l.design.Columns) {
			break
		}
		line := tableDesignColumnLine(l.design.Columns[idx])
		selected := idx == l.cursor
		if selected {
			c.SetFg(colorTitleFocused)
		}
		c.WriteAt(innerRow+1+i, innerCol, truncate(line, innerW))
		if selected {
			c.ResetStyle()
		}
	}
}

func tableDesignColumnLine(col db.ColumnDetail) string {
	nullText := "?"
	if col.NullableKnown {
		nullText = "NULL"
		if !col.Nullable {
			nullText = "NN"
		}
	}
	keyText := tableDesignKeyText(col)
	defText := ""
	if col.DefaultKnown {
		defText = col.Default
	}
	extraText := tableDesignExtraText(col)
	return fmt.Sprintf("%-4d %-26s %-20s %-6s %-12s %-18s %s",
		col.Ordinal,
		col.Name,
		col.TypeName,
		nullText,
		keyText,
		defText,
		extraText,
	)
}

func tableDesignKeyText(col db.ColumnDetail) string {
	var parts []string
	if col.PrimaryKey {
		parts = append(parts, "PK")
	}
	if col.ForeignKey {
		parts = append(parts, "FK")
	}
	if col.Unique {
		parts = append(parts, "UQ")
	}
	return strings.Join(parts, " ")
}

func tableDesignExtraText(col db.ColumnDetail) string {
	var parts []string
	if col.Identity {
		parts = append(parts, "identity")
	}
	if col.Computed {
		parts = append(parts, "computed")
	}
	return strings.Join(parts, " ")
}

func (l *tableDesignLayer) HandleKey(a *app, k Key) {
	switch k.Kind {
	case KeyEsc:
		a.popLayer()
		return
	case KeyUp:
		if l.cursor > 0 {
			l.cursor--
		}
		return
	case KeyDown:
		if l.cursor < len(l.design.Columns)-1 {
			l.cursor++
		}
		return
	case KeyPgUp:
		l.cursor -= 10
		if l.cursor < 0 {
			l.cursor = 0
		}
		return
	case KeyPgDn:
		l.cursor += 10
		if l.cursor > len(l.design.Columns)-1 {
			l.cursor = len(l.design.Columns) - 1
		}
		if l.cursor < 0 {
			l.cursor = 0
		}
		return
	case KeyHome:
		l.cursor = 0
		return
	case KeyEnd:
		l.cursor = len(l.design.Columns) - 1
		if l.cursor < 0 {
			l.cursor = 0
		}
		return
	}
	if k.Kind == KeyRune && !k.Ctrl && !k.Alt && (k.Rune == 'y' || k.Rune == 'Y') {
		if l.cursor < 0 || l.cursor >= len(l.design.Columns) {
			return
		}
		if a.clipboard == nil {
			a.mainLayerPtr().status = "copy column: clipboard unavailable"
			return
		}
		name := l.design.Columns[l.cursor].Name
		if err := a.clipboard.Copy(name); err != nil {
			a.mainLayerPtr().status = "copy column: " + err.Error()
		} else {
			a.mainLayerPtr().status = "copied " + name
		}
	}
}

func (l *tableDesignLayer) Hints(a *app) string {
	_ = a
	return joinHints("↑/↓/PgUp/PgDn=scroll", "Home/End=top/bottom", "y=copy column", "Esc=close")
}
