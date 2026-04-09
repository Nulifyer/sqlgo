package tui

import (
	"bytes"
	"io"
)

// screen is a tiny back-buffer wrapper. Drawing functions append to buf;
// Flush writes the whole frame in one syscall to reduce flicker.
type screen struct {
	out io.Writer
	buf bytes.Buffer
	w   int
	h   int
}

func newScreen(out io.Writer, w, h int) *screen {
	return &screen{out: out, w: w, h: h}
}

func (s *screen) resize(w, h int) {
	s.w, s.h = w, h
}

func (s *screen) beginFrame() {
	s.buf.Reset()
	s.buf.WriteString(cursorHide)
	s.buf.WriteString(clearScreen)
}

// setFg sets the foreground color for subsequent writes. The color persists
// until resetStyle is called or another color is set.
func (s *screen) setFg(color int) {
	fgColor(&s.buf, color)
}

// resetStyle clears all SGR attributes on subsequent writes.
func (s *screen) resetStyle() {
	s.buf.WriteString(resetStyle)
}

func (s *screen) flush() error {
	s.buf.WriteString(resetStyle)
	_, err := s.out.Write(s.buf.Bytes())
	return err
}

// writeAt draws plain text at row,col (1-based). No wrapping; caller clips.
func (s *screen) writeAt(row, col int, text string) {
	if row < 1 || row > s.h {
		return
	}
	moveTo(&s.buf, row, col)
	s.buf.WriteString(text)
}

// hLine draws a horizontal run of a single rune at row from col1..col2 inclusive.
func (s *screen) hLine(row, col1, col2 int, r rune) {
	if col1 > col2 {
		return
	}
	moveTo(&s.buf, row, col1)
	for i := col1; i <= col2; i++ {
		s.buf.WriteRune(r)
	}
}

// vLine draws a vertical run of a single rune at col from row1..row2 inclusive.
func (s *screen) vLine(col, row1, row2 int, r rune) {
	for row := row1; row <= row2; row++ {
		moveTo(&s.buf, row, col)
		s.buf.WriteRune(r)
	}
}
