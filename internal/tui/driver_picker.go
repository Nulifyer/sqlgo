package tui

import (
	"sort"
	"strings"
)

// driverPickerLayer is a searchable fuzzy menu for choosing a driver.
// Typing filters the list; Up/Down moves; Enter commits via onPick;
// Esc cancels.
type driverPickerLayer struct {
	query    *input
	names    []string
	filtered []driverPickItem
	selected int
	onPick   func(string)
}

type driverPickItem struct {
	name  string
	label string
	score int
}

func newDriverPickerLayer(names []string, onPick func(string)) *driverPickerLayer {
	l := &driverPickerLayer{
		query:  newInput(""),
		names:  names,
		onPick: onPick,
	}
	l.refilter()
	return l
}

func (l *driverPickerLayer) refilter() {
	q := l.query.String()
	items := make([]driverPickItem, 0, len(l.names))
	for _, n := range l.names {
		label := engineSpecFor(n).label
		if q == "" {
			items = append(items, driverPickItem{name: n, label: label})
			continue
		}
		// Score against both label and driver name; take the better.
		sL, _, okL := fuzzyScore(q, label)
		sN, _, okN := fuzzyScore(q, n)
		if !okL && !okN {
			continue
		}
		s := sL
		if !okL || (okN && sN > sL) {
			s = sN
		}
		items = append(items, driverPickItem{name: n, label: label, score: s})
	}
	if q == "" {
		sort.SliceStable(items, func(i, j int) bool {
			return strings.ToLower(items[i].label) < strings.ToLower(items[j].label)
		})
	} else {
		sort.SliceStable(items, func(i, j int) bool {
			if items[i].score != items[j].score {
				return items[i].score > items[j].score
			}
			return strings.ToLower(items[i].label) < strings.ToLower(items[j].label)
		})
	}
	l.filtered = items
	if l.selected >= len(items) {
		l.selected = len(items) - 1
	}
	if l.selected < 0 {
		l.selected = 0
	}
}

func (l *driverPickerLayer) Draw(a *app, s *cellbuf) {
	termW, termH := a.term.width, a.term.height
	boxW := 50
	if boxW > termW-dialogMargin {
		boxW = termW - dialogMargin
	}
	if boxW < 24 {
		boxW = 24
	}
	boxH := len(l.filtered) + 6
	if boxH < 10 {
		boxH = 10
	}
	if boxH > termH-dialogMargin {
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
	s.fillRect(r)
	drawFrame(s, r, "Select driver", true)

	innerCol := col + 2
	innerW := boxW - 4

	// Search input row.
	searchRow := row + 1
	s.setFg(colorTitleUnfocused)
	s.writeAt(searchRow, innerCol, "Search:")
	s.resetStyle()
	qCol := innerCol + 8
	qMax := innerW - 8
	if qMax < 1 {
		qMax = 1
	}
	qs := []rune(l.query.String())
	if len(qs) > qMax {
		qs = qs[len(qs)-qMax:]
	}
	s.writeAt(searchRow, qCol, string(qs))
	s.placeCursor(searchRow, qCol+len(qs))

	// List.
	listTop := row + 3
	maxRows := boxH - 5
	if maxRows < 1 {
		maxRows = 1
	}
	scroll := 0
	if l.selected >= maxRows {
		scroll = l.selected - maxRows + 1
	}
	for i := 0; i < maxRows && i+scroll < len(l.filtered); i++ {
		item := l.filtered[i+scroll]
		line := item.label
		if item.label != item.name {
			line = item.label + "  (" + item.name + ")"
		}
		if len([]rune(line)) > innerW {
			line = string([]rune(line)[:innerW])
		}
		y := listTop + i
		if i+scroll == l.selected {
			s.setFg(colorBorderFocused)
			s.writeAt(y, innerCol, "> "+line)
			s.resetStyle()
		} else {
			s.writeAt(y, innerCol, "  "+line)
		}
	}
	if len(l.filtered) == 0 {
		s.writeAt(listTop, innerCol, "(no matches)")
	}
}

func (l *driverPickerLayer) HandleKey(a *app, k Key) {
	switch k.Kind {
	case KeyEsc:
		a.popLayer()
		return
	case KeyUp:
		if l.selected > 0 {
			l.selected--
		}
		return
	case KeyDown:
		if l.selected < len(l.filtered)-1 {
			l.selected++
		}
		return
	case KeyEnter:
		if l.selected >= 0 && l.selected < len(l.filtered) {
			name := l.filtered[l.selected].name
			a.popLayer()
			if l.onPick != nil {
				l.onPick(name)
			}
		}
		return
	}
	if l.query.handle(k) {
		l.refilter()
	}
}

func (l *driverPickerLayer) Hints(a *app) string {
	_ = a
	return joinHints(
		"type=filter",
		"Up/Dn=move",
		"Enter=select",
		"Esc=cancel",
	)
}
