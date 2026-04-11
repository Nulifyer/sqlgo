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
		{"latin1-accent", 'é', false, 1},
		{"cjk-hani", '你', true, 2},
		{"cjk-hani2", '好', true, 2},
		{"hiragana", 'あ', true, 2},
		{"katakana", 'カ', true, 2},
		{"hangul", '안', true, 2},
		{"fullwidth-A", 'Ａ', true, 2},
		{"emoji-earth", '🌍', true, 2},
		{"emoji-party", '🎉', true, 2},
		{"combining-acute", '\u0301', false, 0},
	}
	for _, tc := range cases {
		if got := isWideRune(tc.r); got != tc.wantWide {
			t.Errorf("isWideRune(%q /*U+%04X*/) = %v, want %v",
				tc.r, tc.r, got, tc.wantWide)
		}
		if got := runeDisplayWidth(tc.r); got != tc.wantWidth {
			t.Errorf("runeDisplayWidth(%q /*U+%04X*/) = %d, want %d",
				tc.r, tc.r, got, tc.wantWidth)
		}
	}
}

// TestWriteStyledWideRunePopulatesContinuation pins the invariant
// that writing a wide rune also marks the next cell as wideCont.
// Every drawing path relies on this to keep the grid in sync with
// the terminal.
func TestWriteStyledWideRunePopulatesContinuation(t *testing.T) {
	t.Parallel()
	c := newCellbuf(4, 1)
	c.writeAt(1, 1, "你好")
	// Expected layout: col1 head '你', col2 cont, col3 head '好', col4 cont.
	if p := c.at(1, 1); p.r != '你' || p.wideCont || !p.touched {
		t.Errorf("(1,1) = %+v, want head 你", p)
	}
	if p := c.at(1, 2); p.r != 0 || !p.wideCont || !p.touched {
		t.Errorf("(1,2) = %+v, want wideCont", p)
	}
	if p := c.at(1, 3); p.r != '好' || p.wideCont || !p.touched {
		t.Errorf("(1,3) = %+v, want head 好", p)
	}
	if p := c.at(1, 4); p.r != 0 || !p.wideCont || !p.touched {
		t.Errorf("(1,4) = %+v, want wideCont", p)
	}
}

// TestWriteStyledCombiningMarkAttached: zero-width runes anchor
// on the previous cell's combining slice. "cafe\u0301" renders
// the 'e' cell carrying U+0301 as a combining mark so terminals
// compose it into 'é'.
func TestWriteStyledCombiningMarkAttached(t *testing.T) {
	t.Parallel()
	c := newCellbuf(6, 1)
	c.writeAt(1, 1, "cafe\u0301")
	expect := "cafe"
	for i, r := range expect {
		if p := c.at(1, 1+i); p.r != r {
			t.Errorf("(1,%d) = %+v, want %q", 1+i, p, r)
		}
	}
	e := c.at(1, 4)
	if len(e.combining) != 1 || e.combining[0] != '\u0301' {
		t.Errorf("(1,4) combining = %v, want [U+0301]", e.combining)
	}
	if p := c.at(1, 5); p.touched {
		t.Errorf("(1,5) unexpectedly touched: %+v", p)
	}
}

// TestScreenFlushClobbersStaleWideRightHalf pins the "ghost UTF-8
// cell" fix: when frame N has a wide rune at (1,1) and frame N+1
// replaces it with a narrow rune, the flush diff must also emit the
// cell at (1,2) so the terminal's stale right-half gets overwritten.
// Pre-fix, col 2's model value looked unchanged to the diff so the
// stale second half lingered.
//
// Setup note: writeStyled("你") populates col 1 (head) AND col 2
// (wideCont) in a single call. Callers must NOT also call
// writeStyled on col 2 -- that would stomp the continuation. The
// other cells (3, 4) are left untouched and composite() fills them
// with blank spaces.
func TestScreenFlushClobbersStaleWideRightHalf(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	s := newScreen(&out, 4, 1)

	// Frame 1: a wide CJK rune at col 1. writeStyled handles the
	// continuation slot at col 2 automatically. Cols 3/4 fall
	// through to composite's blank fill.
	b1 := newCellbuf(4, 1)
	b1.writeAt(1, 1, "你")
	s.composite([]*cellbuf{b1})
	if err := s.flush(); err != nil {
		t.Fatal(err)
	}
	// Sanity-check the prev buffer (what's now committed) has the
	// expected shape: head + wideCont.
	if p := s.prev.at(1, 1); p.r != '你' || p.wideCont {
		t.Fatalf("frame 1 col 1 = %+v, want head rune '你'", p)
	}
	if p := s.prev.at(1, 2); !p.wideCont {
		t.Fatalf("frame 1 col 2 = %+v, want wideCont", p)
	}
	out.Reset()

	// Frame 2: a narrow 'X' at col 1. No explicit write at col 2 --
	// composite fills col 2 with a blank. The diff must emit that
	// blank so the terminal's stale right-half of 你 disappears.
	b2 := newCellbuf(4, 1)
	b2.writeAt(1, 1, "X")
	s.composite([]*cellbuf{b2})
	if err := s.flush(); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !bytes.Contains([]byte(got), []byte("X")) {
		t.Errorf("flush missing new X rune: %q", got)
	}
	// The critical assertion: col 2 must have been touched. Strip
	// ANSI escape sequences and look for "X " (X immediately
	// followed by a space) in the plain-text residue. The flush
	// may emit style resets between the two runes, so a naive
	// bytes.Contains on the raw output would give a false
	// negative.
	plain := stripANSI(got)
	if !bytes.Contains([]byte(plain), []byte("X ")) {
		t.Errorf("flush did not clobber stale wide right-half: raw=%q plain=%q", got, plain)
	}
}

// stripANSI returns s with every CSI escape sequence removed. Used
// by flush-output tests that want to assert on the visible
// characters in rendering order without being confused by the SGR
// resets that appear between style transitions.
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
