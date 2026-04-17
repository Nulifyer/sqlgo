package widget

import "github.com/Nulifyer/sqlgo/internal/tui/term"

// DialogOpts configures CenterDialog sizing. PrefW/PrefH is the
// desired size; MinW/MinH is a floor (never shrink below). Margin
// is the per-side reservation against the terminal edge, so the
// dialog is capped at (termW - margin) x (termH - margin). Zero
// fields pick sensible defaults (MinW=24, MinH=5, PrefW=MinW,
// PrefH=MinH, Margin=4).
type DialogOpts struct {
	PrefW, PrefH int
	MinW, MinH   int
	Margin       int
}

// CenterDialog returns a Rect centered in the terminal, clamped to
// [Min, term-Margin]. Row/Col are floored to 1 so the dialog never
// overlaps the topmost line.
func CenterDialog(termW, termH int, opts DialogOpts) term.Rect {
	minW := opts.MinW
	if minW <= 0 {
		minW = 24
	}
	minH := opts.MinH
	if minH <= 0 {
		minH = 5
	}
	prefW := opts.PrefW
	if prefW <= 0 {
		prefW = minW
	}
	prefH := opts.PrefH
	if prefH <= 0 {
		prefH = minH
	}
	margin := opts.Margin
	if margin < 0 {
		margin = 0
	}
	w := prefW
	if w > termW-margin {
		w = termW - margin
	}
	if w < minW {
		w = minW
	}
	h := prefH
	if h > termH-margin {
		h = termH - margin
	}
	if h < minH {
		h = minH
	}
	row := (termH - h) / 2
	col := (termW - w) / 2
	if row < 1 {
		row = 1
	}
	if col < 1 {
		col = 1
	}
	return term.Rect{Row: row, Col: col, W: w, H: h}
}

// DrawDialog paints the dialog background + frame + title. Combined
// helper for the very common FillRect + drawFrame idiom used by
// every modal. Caller still owns inner content (inputs, lists, etc).
func DrawDialog(c *term.Cellbuf, r term.Rect, title string, focused bool) {
	c.FillRect(r)
	term.DrawFrame(c, r, title, focused)
}
