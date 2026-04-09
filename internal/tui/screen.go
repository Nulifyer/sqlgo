package tui

import (
	"bytes"
	"io"
)

// screen is the TUI output device. It owns a pool of per-layer cell
// buffers, a composited final frame, and a prev frame used for cell-level
// diffing. Each Flush emits the minimum ANSI needed to bring the terminal
// into alignment with the final frame.
//
// The rendering pipeline per frame:
//  1. app.draw walks a.layers, calls screen.layerBuf(i) for each, and
//     passes the returned cellbuf to layer.Draw.
//  2. app.draw calls screen.composite with the slice of layer bufs.
//     composite walks every cell position, picks the topmost touched
//     cell from any layer, and writes it into s.final.
//  3. screen.flush diffs s.final against s.prev and emits ANSI for only
//     the changed cells, then swaps final/prev for the next frame.
type screen struct {
	out  io.Writer
	w, h int

	layerBufs []*cellbuf
	final     *cellbuf
	prev      *cellbuf

	emit bytes.Buffer // reused ANSI write buffer
}

func newScreen(out io.Writer, w, h int) *screen {
	return &screen{
		out:   out,
		w:     w,
		h:     h,
		final: newCellbuf(w, h),
		prev:  newCellbuf(w, h),
	}
}

// resize reallocates the final/prev buffers and invalidates the pooled
// layer buffers. The fresh prev is all zero cells, which does not match
// any drawn content — so the first post-resize flush emits every cell,
// producing a full redraw.
func (s *screen) resize(w, h int) {
	if s.w == w && s.h == h {
		return
	}
	s.w, s.h = w, h
	s.final = newCellbuf(w, h)
	s.prev = newCellbuf(w, h)
	for i := range s.layerBufs {
		s.layerBufs[i] = nil
	}
}

// layerBuf returns a cleared cellbuf for layer index i, allocating or
// resizing pool entries as needed. The caller must finish using the
// returned buffer before calling composite.
func (s *screen) layerBuf(i int) *cellbuf {
	for len(s.layerBufs) <= i {
		s.layerBufs = append(s.layerBufs, nil)
	}
	b := s.layerBufs[i]
	if b == nil || b.w != s.w || b.h != s.h {
		b = newCellbuf(s.w, s.h)
		s.layerBufs[i] = b
		return b
	}
	b.reset()
	return b
}

// composite merges the given per-layer buffers into s.final. For each
// cell position it walks top-down and takes the first touched cell.
// Positions no layer touched resolve to a blank (space) with the
// terminal default fg. Only the topmost layer's cursor request is honored
// — modal overlays that don't place a cursor effectively hide it.
func (s *screen) composite(bufs []*cellbuf) {
	for row := 1; row <= s.h; row++ {
		for col := 1; col <= s.w; col++ {
			var out cell
			found := false
			for i := len(bufs) - 1; i >= 0; i-- {
				if p := bufs[i].at(row, col); p != nil && p.touched {
					out = *p
					found = true
					break
				}
			}
			if !found {
				out = cell{r: ' ', fg: ansiDefault, touched: true}
			}
			*s.final.at(row, col) = out
		}
	}

	s.final.cursorWanted = false
	if n := len(bufs); n > 0 {
		top := bufs[n-1]
		if top.cursorWanted {
			s.final.cursorWanted = true
			s.final.cursorRow = top.cursorRow
			s.final.cursorCol = top.cursorCol
		}
	}
}

// flush emits the minimum ANSI to transform s.prev into s.final on the
// terminal, then swaps s.prev and s.final so the just-drawn frame becomes
// the baseline for the next flush. State tracked during emission:
//   - cur: the active Style (fg/bg/attrs), so we only emit SGR when one
//     of them changes;
//   - curRow/curCol: where we believe the terminal cursor is, so we only
//     emit a moveTo when we skip cells or cross a row boundary.
//
// Attrs transitions go through a full resetStyle + rebuild because SGR
// has no portable "turn off bold but keep underline" sequence. Pure
// fg/bg changes emit just the new code and leave attrs intact.
func (s *screen) flush() error {
	s.emit.Reset()
	s.emit.WriteString(cursorHide)

	// Sentinel: -1 for the color axes forces the first cell's style to
	// emit even if it matches defaults, so the terminal state always
	// matches our model after the first write.
	cur := Style{FG: -1, BG: -1}
	curRow, curCol := 0, 0

	for row := 1; row <= s.h; row++ {
		for col := 1; col <= s.w; col++ {
			newC := s.final.at(row, col)
			oldC := s.prev.at(row, col)
			if cellsEqual(newC, oldC) {
				continue
			}
			if row != curRow || col != curCol {
				moveTo(&s.emit, row, col)
				curRow, curCol = row, col
			}
			writeStyleTransition(&s.emit, cur, newC.style)
			cur = newC.style
			r := newC.r
			if r == 0 {
				r = ' '
			}
			s.emit.WriteRune(r)
			curCol++
		}
	}

	s.emit.WriteString(resetStyle)
	if s.final.cursorWanted {
		moveTo(&s.emit, s.final.cursorRow, s.final.cursorCol)
		s.emit.WriteString(cursorShow)
	} else {
		s.emit.WriteString(cursorHide)
	}

	if _, err := s.out.Write(s.emit.Bytes()); err != nil {
		return err
	}
	s.prev, s.final = s.final, s.prev
	return nil
}

// cellsEqual reports whether two cells render identically. Factored out so
// the diff check is explicit about which fields matter.
func cellsEqual(a, b *cell) bool {
	return a.r == b.r && a.style == b.style
}

// writeStyleTransition emits the SGR sequence to move the terminal pen
// from cur to next. When the attrs change we reset and re-emit fg+bg
// because SGR lacks portable "clear single attr" codes; otherwise we
// only emit the axis that differs.
func writeStyleTransition(w *bytes.Buffer, cur, next Style) {
	if cur.Attrs != next.Attrs {
		w.WriteString(resetStyle)
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
