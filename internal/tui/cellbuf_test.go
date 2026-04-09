package tui

import (
	"bytes"
	"testing"
)

func TestCellbufWriteAtMarksTouched(t *testing.T) {
	t.Parallel()
	c := newCellbuf(10, 3)
	c.writeAt(2, 3, "hi")

	if p := c.at(2, 3); p == nil || p.r != 'h' || !p.touched {
		t.Errorf("(2,3) = %+v, want 'h' touched", p)
	}
	if p := c.at(2, 4); p == nil || p.r != 'i' || !p.touched {
		t.Errorf("(2,4) = %+v, want 'i' touched", p)
	}
	if p := c.at(1, 1); p == nil || p.touched {
		t.Errorf("(1,1) = %+v, want untouched", p)
	}
}

func TestCellbufWriteAtClipsOffRight(t *testing.T) {
	t.Parallel()
	c := newCellbuf(5, 1)
	// Start at col 4, write "abcd": 'a' lands in col 4, 'b' in col 5, 'c'/'d' are clipped.
	c.writeAt(1, 4, "abcd")
	if p := c.at(1, 4); p == nil || p.r != 'a' {
		t.Errorf("(1,4) = %+v, want 'a'", p)
	}
	if p := c.at(1, 5); p == nil || p.r != 'b' {
		t.Errorf("(1,5) = %+v, want 'b'", p)
	}
	// No col 6 exists; writing there must not panic.
}

func TestCellbufFillRect(t *testing.T) {
	t.Parallel()
	c := newCellbuf(5, 5)
	c.setFg(42)
	c.fillRect(rect{row: 2, col: 2, w: 3, h: 2})

	for row := 1; row <= 5; row++ {
		for col := 1; col <= 5; col++ {
			p := c.at(row, col)
			inside := row >= 2 && row <= 3 && col >= 2 && col <= 4
			if inside {
				if !p.touched || p.r != ' ' || p.fg != 42 {
					t.Errorf("(%d,%d) inside = %+v, want space touched fg=42", row, col, *p)
				}
			} else {
				if p.touched {
					t.Errorf("(%d,%d) outside = touched, want untouched", row, col)
				}
			}
		}
	}
}

func TestCellbufResetClearsTouched(t *testing.T) {
	t.Parallel()
	c := newCellbuf(4, 2)
	c.writeAt(1, 1, "abcd")
	c.placeCursor(1, 2)
	c.reset()
	for row := 1; row <= 2; row++ {
		for col := 1; col <= 4; col++ {
			if p := c.at(row, col); p.touched {
				t.Errorf("(%d,%d) still touched after reset", row, col)
			}
		}
	}
	if c.cursorWanted {
		t.Errorf("cursorWanted still set after reset")
	}
}

// Composite behavior: top layer's touched cell wins; untouched cells fall
// through to the layer below; untouched-everywhere cells become blanks.
func TestScreenCompositeTopWins(t *testing.T) {
	t.Parallel()
	s := newScreen(&bytes.Buffer{}, 3, 1)
	// layer 0: "abc"
	b0 := newCellbuf(3, 1)
	b0.writeAt(1, 1, "abc")
	// layer 1: "X" in the middle, rest untouched
	b1 := newCellbuf(3, 1)
	b1.writeAt(1, 2, "X")

	s.composite([]*cellbuf{b0, b1})

	got := make([]rune, 3)
	for col := 1; col <= 3; col++ {
		got[col-1] = s.final.at(1, col).r
	}
	if string(got) != "aXc" {
		t.Errorf("composited row = %q, want %q", string(got), "aXc")
	}
}

func TestScreenCompositeUntouchedEverywhereBlanks(t *testing.T) {
	t.Parallel()
	s := newScreen(&bytes.Buffer{}, 3, 1)
	b0 := newCellbuf(3, 1) // nothing touched
	s.composite([]*cellbuf{b0})
	for col := 1; col <= 3; col++ {
		if r := s.final.at(1, col).r; r != ' ' {
			t.Errorf("(1,%d) = %q, want space", col, r)
		}
	}
}

// Only the topmost layer's cursor request survives compositing.
func TestScreenCompositeTopCursorWins(t *testing.T) {
	t.Parallel()
	s := newScreen(&bytes.Buffer{}, 5, 5)
	b0 := newCellbuf(5, 5)
	b0.placeCursor(1, 1)
	b1 := newCellbuf(5, 5)
	// b1 does NOT place a cursor
	s.composite([]*cellbuf{b0, b1})
	if s.final.cursorWanted {
		t.Errorf("cursor should be hidden when topmost layer has no request")
	}

	b0.placeCursor(1, 1)
	b1.placeCursor(3, 4)
	s.composite([]*cellbuf{b0, b1})
	if !s.final.cursorWanted || s.final.cursorRow != 3 || s.final.cursorCol != 4 {
		t.Errorf("cursor = (%d,%d,%v), want (3,4,true)",
			s.final.cursorRow, s.final.cursorCol, s.final.cursorWanted)
	}
}

// writeStyled puts runs with different Style values side-by-side without
// saving/restoring the pen in between. The pen itself must not change.
func TestCellbufWriteStyledLeavesPenAlone(t *testing.T) {
	t.Parallel()
	c := newCellbuf(10, 1)
	c.setFg(31) // pen fg = 31
	red := Style{FG: 31, BG: ansiDefaultBG}
	green := Style{FG: 32, BG: ansiDefaultBG, Attrs: attrBold}
	c.writeStyled(1, 1, "ab", red)
	c.writeStyled(1, 3, "cd", green)

	if p := c.at(1, 1); p.style != red {
		t.Errorf("(1,1).style = %+v, want %+v", p.style, red)
	}
	if p := c.at(1, 3); p.style != green {
		t.Errorf("(1,3).style = %+v, want %+v", p.style, green)
	}
	if c.pen.FG != 31 || c.pen.Attrs != 0 {
		t.Errorf("pen mutated by writeStyled: %+v", c.pen)
	}
}

// Changing bg alone (no fg change) must be emitted by the flush diff.
func TestScreenFlushEmitsBGTransition(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	s := newScreen(&out, 3, 1)
	b := newCellbuf(3, 1)
	b.writeStyled(1, 1, "a", Style{FG: ansiDefault, BG: 41})
	b.writeStyled(1, 2, "b", Style{FG: ansiDefault, BG: 42})
	b.writeStyled(1, 3, "c", Style{FG: ansiDefault, BG: 41})
	s.composite([]*cellbuf{b})
	if err := s.flush(); err != nil {
		t.Fatal(err)
	}
	// Two distinct bg codes should appear in the emitted stream.
	got := out.String()
	if !bytes.Contains([]byte(got), []byte("41m")) {
		t.Errorf("missing bg 41 in flush output: %q", got)
	}
	if !bytes.Contains([]byte(got), []byte("42m")) {
		t.Errorf("missing bg 42 in flush output: %q", got)
	}
}

// Flush emits nothing for the second identical frame -- verifies the diff.
func TestScreenFlushDiffingSkipsUnchanged(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	s := newScreen(&out, 3, 1)
	b := newCellbuf(3, 1)
	b.writeAt(1, 1, "abc")
	s.composite([]*cellbuf{b})
	if err := s.flush(); err != nil {
		t.Fatal(err)
	}
	firstLen := out.Len()
	if firstLen == 0 {
		t.Fatalf("first flush emitted nothing")
	}

	// Second frame identical. The diff should skip every cell.
	out.Reset()
	b2 := newCellbuf(3, 1)
	b2.writeAt(1, 1, "abc")
	s.composite([]*cellbuf{b2})
	if err := s.flush(); err != nil {
		t.Fatal(err)
	}
	// Some overhead still written (cursor hide/show, resetStyle), but no
	// per-cell moveTo sequences.
	if bytes.Contains(out.Bytes(), []byte("abc")) {
		t.Errorf("second flush re-emitted unchanged content: %q", out.String())
	}
}
