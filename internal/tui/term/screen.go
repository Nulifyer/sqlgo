package term

import (
	"bytes"
	"io"
)

// Screen is the TUI output device. It owns a pool of per-layer cell
// buffers, a composited final frame, and a prev frame used for cell-level
// diffing. Each Flush emits the minimum ANSI needed to bring the terminal
// into alignment with the final frame.
//
// The rendering pipeline per frame:
//  1. app.draw walks a.layers, calls Screen.LayerBuf(i) for each, and
//     passes the returned Cellbuf to layer.Draw.
//  2. app.draw calls Screen.Composite with the slice of layer bufs.
//     Composite walks every cell position, picks the topmost touched
//     cell from any layer, and writes it into s.final.
//  3. Screen.Flush diffs s.final against s.prev and emits ANSI for only
//     the changed cells, then swaps final/prev for the next frame.
type Screen struct {
	out  io.Writer
	w, h int

	layerBufs []*Cellbuf
	final     *Cellbuf
	prev      *Cellbuf

	emit bytes.Buffer // reused ANSI write buffer

	// view tracks which terminal modes we've told the terminal are
	// active. ApplyView diffs a new View against it and emits only
	// the sequences for flags that changed. viewSet is false until
	// the first ApplyView so the initial call always emits.
	view    View
	viewSet bool
}

func NewScreen(out io.Writer, w, h int) *Screen {
	return &Screen{
		out:   out,
		w:     w,
		h:     h,
		final: NewCellbuf(w, h),
		prev:  NewCellbuf(w, h),
	}
}

// Resize reallocates the final/prev buffers and invalidates the pooled
// layer buffers. The fresh prev is all zero cells, which does not match
// any drawn content -- so the first post-resize Flush emits every cell,
// producing a full redraw.
func (s *Screen) Resize(w, h int) {
	if s.w == w && s.h == h {
		return
	}
	s.w, s.h = w, h
	s.final = NewCellbuf(w, h)
	s.prev = NewCellbuf(w, h)
	for i := range s.layerBufs {
		s.layerBufs[i] = nil
	}
}

// LayerBuf returns a cleared Cellbuf for layer index i, allocating or
// resizing pool entries as needed. The caller must finish using the
// returned buffer before calling Composite.
func (s *Screen) LayerBuf(i int) *Cellbuf {
	for len(s.layerBufs) <= i {
		s.layerBufs = append(s.layerBufs, nil)
	}
	b := s.layerBufs[i]
	if b == nil || b.W != s.w || b.H != s.h {
		b = NewCellbuf(s.w, s.h)
		s.layerBufs[i] = b
		return b
	}
	b.Reset()
	return b
}

// Composite merges the given per-layer buffers into s.final. For each
// cell position it walks top-down and takes the first touched cell.
// Positions no layer touched resolve to a blank (space) with the
// terminal default fg. Only the topmost layer's cursor request is honored
// -- modal overlays that don't place a cursor effectively hide it.
func (s *Screen) Composite(bufs []*Cellbuf) {
	for row := 1; row <= s.h; row++ {
		for col := 1; col <= s.w; col++ {
			var out Cell
			found := false
			for i := len(bufs) - 1; i >= 0; i-- {
				if p := bufs[i].At(row, col); p != nil && p.Touched {
					out = *p
					found = true
					break
				}
			}
			if !found {
				out = Cell{R: ' ', FG: AnsiDefault, Touched: true}
			}
			*s.final.At(row, col) = out
		}
	}

	s.final.CursorWanted = false
	if n := len(bufs); n > 0 {
		top := bufs[n-1]
		if top.CursorWanted {
			s.final.CursorWanted = true
			s.final.CursorRow = top.CursorRow
			s.final.CursorCol = top.CursorCol
		}
	}
}

// Flush emits the minimum ANSI to transform s.prev into s.final on the
// terminal, then swaps s.prev and s.final so the just-drawn frame becomes
// the baseline for the next Flush. State tracked during emission:
//   - cur: the active Style (fg/bg/attrs), so we only emit SGR when one
//     of them changes;
//   - curRow/curCol: where we believe the terminal cursor is, so we only
//     emit a moveTo when we skip cells or cross a row boundary.
//
// Attrs transitions go through a full ResetStyle + rebuild because SGR
// has no portable "turn off bold but keep underline" sequence. Pure
// fg/bg changes emit just the new code and leave attrs intact.
//
// Wide-rune handling: a cell can be (a) a normal rune, (b) a wide
// rune "head" (the terminal draws a 2-cell-wide glyph starting here),
// or (c) a WideCont "tail" placed by the head in the cell to the
// left. Tail cells are skipped in the emit loop because the head's
// WriteRune covers their terminal column already. cellsEqual
// compares the WideCont flag so a stale tail in prev vs a non-tail
// in new (the glyph was overwritten by something narrow) emits a
// clobber automatically -- no forceNext hack needed.
func (s *Screen) Flush() error {
	// Fast-path: if no cell differs and the cursor state is identical
	// to the previous frame, emit nothing. Without this guard every
	// idle frame still writes CursorHide + a moveTo + CursorShow,
	// which some terminals render as a visible cursor flicker and
	// which pollutes `strace`/recorded sessions with noise.
	if s.framesEqual() {
		return nil
	}

	s.emit.Reset()
	s.emit.WriteString(CursorHide)

	// Sentinel: -1 for the color axes forces the first cell's style to
	// emit even if it matches defaults, so the terminal state always
	// matches our model after the first write.
	cur := Style{FG: -1, BG: -1}
	curRow, curCol := 0, 0

	for row := 1; row <= s.h; row++ {
		for col := 1; col <= s.w; col++ {
			newC := s.final.At(row, col)
			oldC := s.prev.At(row, col)
			if cellsEqual(newC, oldC) {
				continue
			}
			// Continuation cells are handled implicitly by the head
			// at col-1: the terminal draws the wide glyph across
			// this column already, so there's nothing to emit here.
			// We still participate in the equality check above so a
			// WideCont-to-not transition triggers emission on the
			// following iteration (the old head has already been
			// replaced; the tail slot now needs a space or new
			// rune, which is the NON-WideCont branch below).
			if newC.WideCont {
				continue
			}
			if row != curRow || col != curCol {
				moveTo(&s.emit, row, col)
				curRow, curCol = row, col
			}
			writeStyleTransition(&s.emit, cur, newC.Style)
			cur = newC.Style
			r := newC.R
			if r == 0 {
				r = ' '
			}
			s.emit.WriteRune(r)
			for _, cm := range newC.Combining {
				s.emit.WriteRune(cm)
			}
			if IsWideRune(r) {
				// Terminal advances the cursor by 2 columns for a
				// wide glyph; keep our model in sync so the next
				// non-skipped column's moveTo (or lack thereof)
				// lines up.
				curCol += 2
			} else {
				curCol++
			}
		}
	}

	s.emit.WriteString(ResetStyle)
	if s.final.CursorWanted {
		moveTo(&s.emit, s.final.CursorRow, s.final.CursorCol)
		s.emit.WriteString(CursorShow)
	} else {
		s.emit.WriteString(CursorHide)
	}

	if _, err := s.out.Write(s.emit.Bytes()); err != nil {
		return err
	}
	s.prev, s.final = s.final, s.prev
	return nil
}

// ApplyView brings the terminal's mode state in line with v, emitting
// only the deltas. Safe to call every frame; a no-op when v matches the
// last applied view. WindowTitle is re-emitted only when the string
// changes (OSC 2 is cheap but noisy in recorded terminals).
//
// Alt-screen toggles should happen before any cell emission in the
// same frame -- callers invoke this at the top of Flush so the diff
// loop writes into the correct buffer.
func (s *Screen) ApplyView(v View) error {
	var buf bytes.Buffer
	if !s.viewSet || s.view.AltScreen != v.AltScreen {
		if v.AltScreen {
			buf.WriteString(AltScreenOn)
		} else {
			buf.WriteString(AltScreenOff)
		}
	}
	if !s.viewSet || s.view.MouseEnabled != v.MouseEnabled {
		if v.MouseEnabled {
			buf.WriteString(MouseOn)
		} else {
			buf.WriteString(MouseOff)
		}
	}
	if !s.viewSet || s.view.PasteEnabled != v.PasteEnabled {
		if v.PasteEnabled {
			buf.WriteString(PasteOn)
		} else {
			buf.WriteString(PasteOff)
		}
	}
	if !s.viewSet || s.view.WindowTitle != v.WindowTitle {
		if v.WindowTitle != "" {
			// OSC 2 sets the window title; terminated by BEL for the
			// broadest terminal support (ST works too but a few
			// terminals don't recognize it).
			buf.WriteString(esc + "]2;" + sanitizeWindowTitle(v.WindowTitle) + "\x07")
		}
	}
	if buf.Len() > 0 {
		if _, err := s.out.Write(buf.Bytes()); err != nil {
			return err
		}
	}
	s.view = v
	s.viewSet = true
	return nil
}

// sanitizeWindowTitle strips control characters from a WindowTitle so it
// cannot prematurely terminate the OSC 2 sequence or inject further
// escape codes. Connection names flow from user config; a malicious or
// accidental \x1b / \x07 / \x9c would otherwise escape into the
// terminal command stream.
func sanitizeWindowTitle(s string) string {
	clean := make([]rune, 0, len(s))
	for _, r := range s {
		if r < 0x20 || r == 0x7f || r == 0x9c {
			continue
		}
		clean = append(clean, r)
	}
	return string(clean)
}

// TeardownView restores the terminal modes we turned on during Run so
// the user's shell doesn't inherit mouse tracking / bracketed paste /
// alt-screen. Called from Run's defer chain on clean exit; the panic
// handler does the same work inline.
func (s *Screen) TeardownView() {
	if !s.viewSet {
		return
	}
	var buf bytes.Buffer
	if s.view.PasteEnabled {
		buf.WriteString(PasteOff)
	}
	if s.view.MouseEnabled {
		buf.WriteString(MouseOff)
	}
	if s.view.AltScreen {
		buf.WriteString(AltScreenOff)
	}
	if buf.Len() > 0 {
		_, _ = s.out.Write(buf.Bytes())
	}
	s.viewSet = false
}

// framesEqual reports whether s.final exactly matches s.prev, cursor
// state included. Used by Flush as an early-exit to skip emitting the
// CursorHide/CursorShow envelope on idle frames. O(w*h) worst case --
// on par with the diff loop itself, so the cost is bounded.
func (s *Screen) framesEqual() bool {
	if s.final.CursorWanted != s.prev.CursorWanted ||
		s.final.CursorRow != s.prev.CursorRow ||
		s.final.CursorCol != s.prev.CursorCol {
		return false
	}
	for row := 1; row <= s.h; row++ {
		for col := 1; col <= s.w; col++ {
			if !cellsEqual(s.final.At(row, col), s.prev.At(row, col)) {
				return false
			}
		}
	}
	return true
}

// cellsEqual reports whether two cells render identically. Factored out so
// the diff check is explicit about which fields matter. WideCont is part
// of the comparison so a stale continuation slot in prev gets caught
// when new has a non-WideCont value (or vice versa).
func cellsEqual(a, b *Cell) bool {
	if a.R != b.R || a.Style != b.Style || a.WideCont != b.WideCont {
		return false
	}
	if len(a.Combining) != len(b.Combining) {
		return false
	}
	for i, r := range a.Combining {
		if b.Combining[i] != r {
			return false
		}
	}
	return true
}

// writeStyleTransition emits the SGR sequence to move the terminal pen
// from cur to next. When the attrs change we reset and re-emit fg+bg
// because SGR lacks portable "clear single attr" codes; otherwise we
// only emit the axis that differs.
func writeStyleTransition(w *bytes.Buffer, cur, next Style) {
	if cur.Attrs != next.Attrs {
		w.WriteString(ResetStyle)
		fgColor(w, next.FG)
		bgColor(w, next.BG)
		setAttrs(w, next.Attrs)
		return
	}
	if cur.FG != next.FG {
		fgColor(w, next.FG)
	}
	if cur.BG != next.BG {
		bgColor(w, next.BG)
	}
}
