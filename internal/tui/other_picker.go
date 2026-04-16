package tui

import "github.com/Nulifyer/sqlgo/internal/db"

// otherPickerFlow drives the two-step "Other..." connection setup:
// first pick a dialect (Profile), then a wire transport (Transport).
// On completion it calls onDone with both names so the form can
// populate Profile/Transport fields and defaults.
func openOtherPicker(a *app, onDone func(profile, transport string)) {
	profiles := db.RegisteredProfiles()
	a.pushLayer(newDriverPickerLayer(profiles, func(profile string) {
		transports := db.RegisteredTransports()
		a.pushLayer(newTransportPickerLayer(transports, profile, func(transport string) {
			onDone(profile, transport)
		}))
	}))
}

// transportPickerLayer reuses the same fuzzy-search modal as the
// driver picker but with a "Select transport" title and transport
// labels derived from the registry.
type transportPickerLayer struct {
	query    *input
	names    []string
	profile  string
	filtered []driverPickItem
	selected int
	onPick   func(string)
}

func newTransportPickerLayer(names []string, profile string, onPick func(string)) *transportPickerLayer {
	l := &transportPickerLayer{
		query:   newInput(""),
		names:   names,
		profile: profile,
		onPick:  onPick,
	}
	l.refilter()
	return l
}

func (l *transportPickerLayer) refilter() {
	q := l.query.String()
	items := make([]driverPickItem, 0, len(l.names))
	for _, n := range l.names {
		t, ok := db.GetTransport(n)
		label := n
		if ok {
			label = t.Name
			if t.DefaultPort > 0 {
				label += " (:" + itoa(t.DefaultPort) + ")"
			}
		}
		if q == "" {
			items = append(items, driverPickItem{name: n, label: label})
			continue
		}
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
	l.filtered = items
	if l.selected >= len(items) {
		l.selected = len(items) - 1
	}
	if l.selected < 0 {
		l.selected = 0
	}
}

func (l *transportPickerLayer) Draw(a *app, s *cellbuf) {
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
	drawFrame(s, r, "Select transport for "+l.profile, true)

	innerCol := col + 2
	innerW := boxW - 4

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

func (l *transportPickerLayer) HandleKey(a *app, k Key) {
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

func (l *transportPickerLayer) Hints(a *app) string {
	_ = a
	return joinHints(
		"type=filter",
		"Up/Dn=move",
		"Enter=select",
		"Esc=cancel",
	)
}
