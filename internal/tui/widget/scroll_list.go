package widget

import "github.com/Nulifyer/sqlgo/internal/tui/term"

// ScrollList is the shared selection + scroll bookkeeping used by every
// vertical-list modal in the TUI (catalog, history, driver/transport
// picker, open file, completion popup, etc). Callers set Len before
// each frame, call Clamp + ViewportScroll to compute the visible slice,
// and delegate keyboard/mouse input through HandleKey/HandleMouse.
//
// ScrollList renders no cells itself. Owners iterate
// VisibleRange()/IsSelected() and draw whatever row content they want.
// This keeps the widget reusable for labels, rows-with-metadata, and
// icon+text styles.
type ScrollList struct {
	// Selected is the currently highlighted row index into the
	// caller's item slice. Clamp() keeps it within [0, Len-1].
	Selected int
	// Scroll is the first visible row index. Updated by ViewportScroll
	// so the selected row stays on screen.
	Scroll int
	// Len is the total number of rows. Owners must set this before
	// calling Clamp / ViewportScroll / HandleKey / HandleMouse.
	Len int
	// PageStep is the number of rows PgUp/PgDn moves. Zero falls back
	// to a default of 10.
	PageStep int

	// ListTop / ListH record the last-rendered list viewport so mouse
	// handlers can map Y coordinates back to row indices without the
	// caller re-deriving the box layout. Owners write these during
	// Draw before calling HandleMouse.
	ListTop int
	ListH   int
}

// Clamp snaps Selected / Scroll back into range. Call after mutating
// Len (refilter) or Selected.
func (s *ScrollList) Clamp() {
	if s.Len <= 0 {
		s.Selected = 0
		s.Scroll = 0
		return
	}
	if s.Selected >= s.Len {
		s.Selected = s.Len - 1
	}
	if s.Selected < 0 {
		s.Selected = 0
	}
	if s.Scroll < 0 {
		s.Scroll = 0
	}
	if s.Scroll >= s.Len {
		s.Scroll = s.Len - 1
		if s.Scroll < 0 {
			s.Scroll = 0
		}
	}
}

// ViewportScroll slides Scroll so the selected row is visible inside a
// viewport of listH rows. Call with the rendered list height on every
// Draw before iterating VisibleRange.
func (s *ScrollList) ViewportScroll(listH int) {
	if listH < 1 {
		listH = 1
	}
	if s.Selected < s.Scroll {
		s.Scroll = s.Selected
	}
	if s.Selected >= s.Scroll+listH {
		s.Scroll = s.Selected - listH + 1
	}
	if s.Scroll < 0 {
		s.Scroll = 0
	}
}

// VisibleRange returns [start, end) into the caller's item slice for
// the rows that should render given the last recorded ListH. Works
// after ViewportScroll has been called.
func (s *ScrollList) VisibleRange() (start, end int) {
	h := s.ListH
	if h < 1 {
		h = 1
	}
	start = s.Scroll
	end = start + h
	if end > s.Len {
		end = s.Len
	}
	if start > end {
		start = end
	}
	return start, end
}

// IsSelected reports whether idx is the currently selected row.
func (s *ScrollList) IsSelected(idx int) bool { return idx == s.Selected }

// MoveUp / MoveDown step by one. No-op at the edges.
func (s *ScrollList) MoveUp() {
	if s.Selected > 0 {
		s.Selected--
	}
}
func (s *ScrollList) MoveDown() {
	if s.Selected < s.Len-1 {
		s.Selected++
	}
}

// PageUp / PageDown step by PageStep (default 10). Clamps to [0, Len-1].
func (s *ScrollList) PageUp() {
	step := s.PageStep
	if step <= 0 {
		step = 10
	}
	s.Selected -= step
	if s.Selected < 0 {
		s.Selected = 0
	}
}
func (s *ScrollList) PageDown() {
	step := s.PageStep
	if step <= 0 {
		step = 10
	}
	s.Selected += step
	if s.Selected > s.Len-1 {
		s.Selected = s.Len - 1
	}
	if s.Selected < 0 {
		s.Selected = 0
	}
}

// HandleKey consumes the standard navigation keys (Up/Down/PgUp/PgDn)
// and returns true when the key was consumed. Callers route unhandled
// keys elsewhere (typically a search field).
func (s *ScrollList) HandleKey(k term.Key) bool {
	switch k.Kind {
	case term.KeyUp:
		s.MoveUp()
		return true
	case term.KeyDown:
		s.MoveDown()
		return true
	case term.KeyPgUp:
		s.PageUp()
		return true
	case term.KeyPgDn:
		s.PageDown()
		return true
	}
	return false
}

// HandleMouse maps a MouseMsg to a list action. Returns clickedIdx >= 0
// for left-button press events inside the viewport; caller may then
// assign s.Selected = clickedIdx and check for double-click via its
// own ClickTracker. Wheel events adjust Selected directly. Returns
// consumed==true when the widget handled the event.
//
// The caller is responsible for setting s.ListTop / s.ListH during
// Draw so this function can hit-test. When ListH <= 0, no action is
// taken.
func (s *ScrollList) HandleMouse(mm term.MouseMsg) (clickedIdx int, consumed bool) {
	clickedIdx = -1
	switch mm.Button {
	case term.MouseButtonWheelUp:
		s.MoveUp()
		return -1, true
	case term.MouseButtonWheelDown:
		s.MoveDown()
		return -1, true
	case term.MouseButtonLeft:
		if mm.Action != term.MouseActionPress {
			return -1, false
		}
		if s.ListH <= 0 {
			return -1, false
		}
		rowIdx := mm.Y - s.ListTop
		if rowIdx < 0 || rowIdx >= s.ListH {
			return -1, false
		}
		idx := s.Scroll + rowIdx
		if idx < 0 || idx >= s.Len {
			return -1, false
		}
		return idx, true
	}
	return -1, false
}
