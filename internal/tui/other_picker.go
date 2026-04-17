package tui

import (
	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/tui/widget"
)

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
// labels derived from the registry. Registry order is preserved
// (SortAlpha=false) so the transport list shows in the same order
// the registry declares them.
type transportPickerLayer struct {
	picker  *widget.FuzzyPicker
	profile string
	onPick  func(string)
}

func newTransportPickerLayer(names []string, profile string, onPick func(string)) *transportPickerLayer {
	items := make([]widget.FuzzyPickerItem, 0, len(names))
	for _, n := range names {
		label := n
		if t, ok := db.GetTransport(n); ok {
			label = t.Name
			if t.DefaultPort > 0 {
				label += " (:" + itoa(t.DefaultPort) + ")"
			}
		}
		items = append(items, widget.FuzzyPickerItem{Key: n, Label: label})
	}
	fp := widget.NewFuzzyPicker(items)
	fp.SortAlpha = false
	fp.SecondaryKey = true
	fp.Refilter()
	return &transportPickerLayer{picker: fp, profile: profile, onPick: onPick}
}

func (l *transportPickerLayer) Draw(a *app, s *cellbuf) {
	r := widget.CenterDialog(a.term.width, a.term.height, widget.DialogOpts{
		PrefW: 50, PrefH: len(l.picker.Filtered) + 6,
		MinW: 24, MinH: 10, Margin: dialogMargin,
	})
	row, col := r.Row, r.Col
	boxH := r.H
	widget.DrawDialog(s, r, "Select transport for "+l.profile, true)

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

func (l *transportPickerLayer) HandleKey(a *app, k Key) {
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

func (l *transportPickerLayer) Hints(a *app) string {
	_ = a
	return joinHints(
		"type=filter",
		"Up/Dn=move",
		"Enter=select",
		"Esc=cancel",
	)
}
