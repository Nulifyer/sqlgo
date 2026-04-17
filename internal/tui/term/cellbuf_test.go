package term

import (
	"bytes"
	"testing"
)

func TestCellbufWriteAtMarksTouched(t *testing.T) {
	t.Parallel()
	c := NewCellbuf(10, 3)
	c.WriteAt(2, 3, "hi")

	if p := c.At(2, 3); p == nil || p.R != 'h' || !p.Touched {
		t.Errorf("(2,3) = %+v, want 'h' touched", p)
	}
	if p := c.At(2, 4); p == nil || p.R != 'i' || !p.Touched {
		t.Errorf("(2,4) = %+v, want 'i' touched", p)
	}
	if p := c.At(1, 1); p == nil || p.Touched {
		t.Errorf("(1,1) = %+v, want untouched", p)
	}
}

func TestCellbufWriteAtClipsOffRight(t *testing.T) {
	t.Parallel()
	c := NewCellbuf(5, 1)
	// Start at col 4, write "abcd": 'a' lands in col 4, 'b' in col 5, 'c'/'d' are clipped.
	c.WriteAt(1, 4, "abcd")
	if p := c.At(1, 4); p == nil || p.R != 'a' {
		t.Errorf("(1,4) = %+v, want 'a'", p)
	}
	if p := c.At(1, 5); p == nil || p.R != 'b' {
		t.Errorf("(1,5) = %+v, want 'b'", p)
	}
	// No col 6 exists; writing there must not panic.
}

func TestCellbufFillRect(t *testing.T) {
	t.Parallel()
	c := NewCellbuf(5, 5)
	c.SetFg(42)
	c.FillRect(Rect{Row: 2, Col: 2, W: 3, H: 2})

	for row := 1; row <= 5; row++ {
		for col := 1; col <= 5; col++ {
			p := c.At(row, col)
			inside := row >= 2 && row <= 3 && col >= 2 && col <= 4
			if inside {
				if !p.Touched || p.R != ' ' || p.Style.FG != 42 {
					t.Errorf("(%d,%d) inside = %+v, want space touched fg=42", row, col, *p)
				}
			} else {
				if p.Touched {
					t.Errorf("(%d,%d) outside = touched, want untouched", row, col)
				}
			}
		}
	}
}

func TestCellbufResetClearsTouched(t *testing.T) {
	t.Parallel()
	c := NewCellbuf(4, 2)
	c.WriteAt(1, 1, "abcd")
	c.PlaceCursor(1, 2)
	c.Reset()
	for row := 1; row <= 2; row++ {
		for col := 1; col <= 4; col++ {
			if p := c.At(row, col); p.Touched {
				t.Errorf("(%d,%d) still touched after reset", row, col)
			}
		}
	}
	if c.CursorWanted {
		t.Errorf("CursorWanted still set after reset")
	}
}

// Composite behavior: top layer's touched cell wins; untouched cells fall
// through to the layer below; untouched-everywhere cells become blanks.
func TestScreenCompositeTopWins(t *testing.T) {
	t.Parallel()
	s := NewScreen(&bytes.Buffer{}, 3, 1)
	// layer 0: "abc"
	b0 := NewCellbuf(3, 1)
	b0.WriteAt(1, 1, "abc")
	// layer 1: "X" in the middle, rest untouched
	b1 := NewCellbuf(3, 1)
	b1.WriteAt(1, 2, "X")

	s.Composite([]*Cellbuf{b0, b1})

	got := make([]rune, 3)
	for col := 1; col <= 3; col++ {
		got[col-1] = s.final.At(1, col).R
	}
	if string(got) != "aXc" {
		t.Errorf("composited row = %q, want %q", string(got), "aXc")
	}
}

func TestScreenCompositeUntouchedEverywhereBlanks(t *testing.T) {
	t.Parallel()
	s := NewScreen(&bytes.Buffer{}, 3, 1)
	b0 := NewCellbuf(3, 1) // nothing touched
	s.Composite([]*Cellbuf{b0})
	for col := 1; col <= 3; col++ {
		if r := s.final.At(1, col).R; r != ' ' {
			t.Errorf("(1,%d) = %q, want space", col, r)
		}
	}
}

// Only the topmost layer's cursor request survives compositing.
func TestScreenCompositeTopCursorWins(t *testing.T) {
	t.Parallel()
	s := NewScreen(&bytes.Buffer{}, 5, 5)
	b0 := NewCellbuf(5, 5)
	b0.PlaceCursor(1, 1)
	b1 := NewCellbuf(5, 5)
	// b1 does NOT place a cursor
	s.Composite([]*Cellbuf{b0, b1})
	if s.final.CursorWanted {
		t.Errorf("cursor should be hidden when topmost layer has no request")
	}

	b0.PlaceCursor(1, 1)
	b1.PlaceCursor(3, 4)
	s.Composite([]*Cellbuf{b0, b1})
	if !s.final.CursorWanted || s.final.CursorRow != 3 || s.final.CursorCol != 4 {
		t.Errorf("cursor = (%d,%d,%v), want (3,4,true)",
			s.final.CursorRow, s.final.CursorCol, s.final.CursorWanted)
	}
}

// WriteStyled puts runs with different Style values side-by-side without
// saving/restoring the pen in between. The pen itself must not change.
func TestCellbufWriteStyledLeavesPenAlone(t *testing.T) {
	t.Parallel()
	c := NewCellbuf(10, 1)
	c.SetFg(31) // pen fg = 31
	red := Style{FG: 31, BG: AnsiDefaultBG}
	green := Style{FG: 32, BG: AnsiDefaultBG, Attrs: AttrBold}
	c.WriteStyled(1, 1, "ab", red)
	c.WriteStyled(1, 3, "cd", green)

	if p := c.At(1, 1); p.Style != red {
		t.Errorf("(1,1).Style = %+v, want %+v", p.Style, red)
	}
	if p := c.At(1, 3); p.Style != green {
		t.Errorf("(1,3).Style = %+v, want %+v", p.Style, green)
	}
	if c.pen.FG != 31 || c.pen.Attrs != 0 {
		t.Errorf("pen mutated by WriteStyled: %+v", c.pen)
	}
}

// Changing bg alone (no fg change) must be emitted by the flush diff.
func TestScreenFlushEmitsBGTransition(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	s := NewScreen(&out, 3, 1)
	b := NewCellbuf(3, 1)
	b.WriteStyled(1, 1, "a", Style{FG: AnsiDefault, BG: 41})
	b.WriteStyled(1, 2, "b", Style{FG: AnsiDefault, BG: 42})
	b.WriteStyled(1, 3, "c", Style{FG: AnsiDefault, BG: 41})
	s.Composite([]*Cellbuf{b})
	if err := s.Flush(); err != nil {
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

// TestIsWideRune spot-checks the wide-rune classification sourced
// from go-runewidth. Covers every glyph kind the test_notes table
// exercises plus a combining accent (width 0).
func TestIsWideRune(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		r         rune
		wantWide  bool
		wantWidth int
	}{
		{"ascii-lower", 'a', false, 1},
		{"ascii-digit", '5', false, 1},
		{"ascii-sym", '$', false, 1},
		{"latin1-accent", '\u00e9', false, 1},
		{"cjk-hani", '\u4f60', true, 2},
		{"cjk-hani2", '\u597d', true, 2},
		{"hiragana", '\u3042', true, 2},
		{"katakana", '\u30ab', true, 2},
		{"hangul", '\uc548', true, 2},
		{"fullwidth-A", '\uff21', true, 2},
		{"emoji-earth", '\U0001f30d', true, 2},
		{"emoji-party", '\U0001f389', true, 2},
		{"combining-acute", '\u0301', false, 0},
	}
	for _, tc := range cases {
		if got := IsWideRune(tc.r); got != tc.wantWide {
			t.Errorf("IsWideRune(%q /*U+%04X*/) = %v, want %v",
				tc.r, tc.r, got, tc.wantWide)
		}
		if got := RuneDisplayWidth(tc.r); got != tc.wantWidth {
			t.Errorf("RuneDisplayWidth(%q /*U+%04X*/) = %d, want %d",
				tc.r, tc.r, got, tc.wantWidth)
		}
	}
}

// TestWriteStyledWideRunePopulatesContinuation pins the invariant
// that writing a wide rune also marks the next cell as WideCont.
// Every drawing path relies on this to keep the grid in sync with
// the terminal.
func TestWriteStyledWideRunePopulatesContinuation(t *testing.T) {
	t.Parallel()
	c := NewCellbuf(4, 1)
	c.WriteAt(1, 1, "\u4f60\u597d")
	// Expected layout: col1 head, col2 cont, col3 head, col4 cont.
	if p := c.At(1, 1); p.R != '\u4f60' || p.WideCont || !p.Touched {
		t.Errorf("(1,1) = %+v, want head rune", p)
	}
	if p := c.At(1, 2); p.R != 0 || !p.WideCont || !p.Touched {
		t.Errorf("(1,2) = %+v, want WideCont", p)
	}
	if p := c.At(1, 3); p.R != '\u597d' || p.WideCont || !p.Touched {
		t.Errorf("(1,3) = %+v, want head rune", p)
	}
	if p := c.At(1, 4); p.R != 0 || !p.WideCont || !p.Touched {
		t.Errorf("(1,4) = %+v, want WideCont", p)
	}
}

// TestWriteStyledCombiningMarkAttached: zero-width runes anchor
// on the previous cell's combining slice. "cafe\u0301" renders
// the 'e' cell carrying U+0301 as a combining mark so terminals
// compose it into 'e'.
func TestWriteStyledCombiningMarkAttached(t *testing.T) {
	t.Parallel()
	c := NewCellbuf(6, 1)
	c.WriteAt(1, 1, "cafe\u0301")
	expect := "cafe"
	for i, r := range expect {
		if p := c.At(1, 1+i); p.R != r {
			t.Errorf("(1,%d) = %+v, want %q", 1+i, p, r)
		}
	}
	e := c.At(1, 4)
	if len(e.Combining) != 1 || e.Combining[0] != '\u0301' {
		t.Errorf("(1,4) Combining = %v, want [U+0301]", e.Combining)
	}
	if p := c.At(1, 5); p.Touched {
		t.Errorf("(1,5) unexpectedly touched: %+v", p)
	}
}

// TestScreenFlushClobbersStaleWideRightHalf pins the "ghost UTF-8
// cell" fix: when frame N has a wide rune at (1,1) and frame N+1
// replaces it with a narrow rune, the flush diff must also emit the
// cell at (1,2) so the terminal's stale right-half gets overwritten.
func TestScreenFlushClobbersStaleWideRightHalf(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	s := NewScreen(&out, 4, 1)

	// Frame 1: a wide CJK rune at col 1.
	b1 := NewCellbuf(4, 1)
	b1.WriteAt(1, 1, "\u4f60")
	s.Composite([]*Cellbuf{b1})
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	// Sanity-check the prev buffer has the expected shape: head + WideCont.
	if p := s.prev.At(1, 1); p.R != '\u4f60' || p.WideCont {
		t.Fatalf("frame 1 col 1 = %+v, want head rune", p)
	}
	if p := s.prev.At(1, 2); !p.WideCont {
		t.Fatalf("frame 1 col 2 = %+v, want WideCont", p)
	}
	out.Reset()

	// Frame 2: a narrow 'X' at col 1. No explicit write at col 2 --
	// composite fills col 2 with a blank. The diff must emit that
	// blank so the terminal's stale right-half disappears.
	b2 := NewCellbuf(4, 1)
	b2.WriteAt(1, 1, "X")
	s.Composite([]*Cellbuf{b2})
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !bytes.Contains([]byte(got), []byte("X")) {
		t.Errorf("flush missing new X rune: %q", got)
	}
	plain := stripANSI(got)
	if !bytes.Contains([]byte(plain), []byte("X ")) {
		t.Errorf("flush did not clobber stale wide right-half: raw=%q plain=%q", got, plain)
	}
}

// stripANSI returns s with every CSI escape sequence removed.
func stripANSI(s string) string {
	var b bytes.Buffer
	i := 0
	for i < len(s) {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			i += 2
			for i < len(s) {
				c := s[i]
				i++
				if c >= 0x40 && c <= 0x7e {
					break
				}
			}
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// Flush emits nothing for the second identical frame -- verifies the diff.
func TestScreenFlushDiffingSkipsUnchanged(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	s := NewScreen(&out, 3, 1)
	b := NewCellbuf(3, 1)
	b.WriteAt(1, 1, "abc")
	s.Composite([]*Cellbuf{b})
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	firstLen := out.Len()
	if firstLen == 0 {
		t.Fatalf("first flush wrote nothing")
	}

	// Second frame identical. The diff should skip every cell.
	out.Reset()
	b2 := NewCellbuf(3, 1)
	b2.WriteAt(1, 1, "abc")
	s.Composite([]*Cellbuf{b2})
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out.Bytes(), []byte("abc")) {
		t.Errorf("second flush re-emitted unchanged content: %q", out.String())
	}
}
