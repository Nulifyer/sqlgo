package tui

import "github.com/Nulifyer/sqlgo/internal/tui/widget"

// driverPickerLayer is a searchable fuzzy menu for choosing a driver.
// Typing filters the list; Up/Down moves; Enter commits via onPick;
// Esc cancels. The fuzzy matching, query input, and selection/scroll
// bookkeeping live in widget.FuzzyPicker; this layer only owns the
// frame, row formatting (label + "(name)" suffix), and enter/esc
// semantics.
type driverPickerLayer struct {
	picker *widget.FuzzyPicker
	onPick func(string)
}

func newDriverPickerLayer(names []string, onPick func(string)) *driverPickerLayer {
	items := make([]widget.FuzzyPickerItem, 0, len(names))
	for _, n := range names {
		items = append(items, widget.FuzzyPickerItem{Key: n, Label: engineSpecFor(n).label})
	}
	fp := widget.NewFuzzyPicker(items)
	fp.SortAlpha = true
	fp.SecondaryKey = true
	fp.Refilter()
	return &driverPickerLayer{picker: fp, onPick: onPick}
}

func (l *driverPickerLayer) Draw(a *app, s *cellbuf) {
	r := widget.CenterDialog(a.term.width, a.term.height, widget.DialogOpts{
		PrefW: 50, PrefH: len(l.picker.Filtered) + 6,
		MinW: 24, MinH: 10, Margin: dialogMargin,
	})
	row, col := r.Row, r.Col
	boxH := r.H
	widget.DrawDialog(s, r, "Select driver", true)

	innerCol := col + 2
	innerW := r.W - 4

	searchRow := row + 1
	s.SetFg(colorTitleUnfocused)
	s.WriteAt(searchRow, innerCol, "Search:")
	s.ResetStyle()
	qCol := innerCol + 8
	qMax := innerW - 8
	if qMax < 1 {
		qMax = 1
	}
	drawInput(s, l.picker.Query, searchRow, qCol, qMax)

	listTop := row + 3
	listH := boxH - 5
	if listH < 1 {
		listH = 1
	}
	l.picker.List.ListTop = listTop
	l.picker.List.ListH = listH
	l.picker.List.ViewportScroll(listH)

	start, end := l.picker.List.VisibleRange()
	for i := start; i < end; i++ {
		item := l.picker.Filtered[i]
		line := item.Label
		if item.Label != item.Key {
			line = item.Label + "  (" + item.Key + ")"
		}
		if len([]rune(line)) > innerW {
			line = string([]rune(line)[:innerW])
		}
		y := listTop + (i - start)
		if l.picker.List.IsSelected(i) {
			s.SetFg(colorBorderFocused)
			s.WriteAt(y, innerCol, "> "+line)
			s.ResetStyle()
		} else {
			s.WriteAt(y, innerCol, "  "+line)
		}
	}
	if len(l.picker.Filtered) == 0 {
		s.WriteAt(listTop, innerCol, "(no matches)")
	}
}

func (l *driverPickerLayer) HandleKey(a *app, k Key) {
	switch k.Kind {
	case KeyEsc:
		a.popLayer()
		return
	case KeyEnter:
		if it, ok := l.picker.Selected(); ok {
			a.popLayer()
			if l.onPick != nil {
				l.onPick(it.Key)
			}
		}
		return
	}
	if l.picker.HandleNav(k) {
		return
	}
	l.picker.HandleQuery(k)
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
