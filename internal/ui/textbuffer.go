package ui

import (
	"strings"
	"unicode/utf8"
)

// TextBuffer is the source-of-truth document model for the custom SQL editor
// widget. It stores text as a slice of rune slices (one per line) so that
// in-line edits are cheap and cursor positions can be expressed in (row, col)
// without worrying about UTF-8 byte boundaries. Newlines are NOT stored
// inside the line slices — the line break is implicit between adjacent
// entries.
//
// Byte offsets used by the existing editor package (tokenizer / autocomplete)
// are computed on demand via ByteOffset / PositionFromByteOffset so the
// buffer remains interchangeable with code that still speaks in byte
// positions.
type TextBuffer struct {
	lines [][]rune
}

// NewTextBuffer returns an empty buffer containing one empty line. An empty
// document still has a single line so the cursor always has a valid home.
func NewTextBuffer() *TextBuffer {
	return &TextBuffer{lines: [][]rune{{}}}
}

// SetText replaces the entire buffer contents. \r\n and bare \r are
// normalized to \n so editing is consistent across pasted Windows content.
func (b *TextBuffer) SetText(text string) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	parts := strings.Split(text, "\n")
	lines := make([][]rune, len(parts))
	for i, p := range parts {
		lines[i] = []rune(p)
	}
	if len(lines) == 0 {
		lines = [][]rune{{}}
	}
	b.lines = lines
}

// GetText joins the buffer back into a single string with \n separators.
func (b *TextBuffer) GetText() string {
	if len(b.lines) == 0 {
		return ""
	}
	var sb strings.Builder
	for i, line := range b.lines {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(string(line))
	}
	return sb.String()
}

// LineCount returns the number of lines in the buffer (always >= 1).
func (b *TextBuffer) LineCount() int {
	if len(b.lines) == 0 {
		return 1
	}
	return len(b.lines)
}

// Line returns a copy of the runes on the given row, or nil if row is out of
// range. Callers must not mutate the buffer's internal storage; the copy
// guards against accidental aliasing.
func (b *TextBuffer) Line(row int) []rune {
	if row < 0 || row >= len(b.lines) {
		return nil
	}
	out := make([]rune, len(b.lines[row]))
	copy(out, b.lines[row])
	return out
}

// LineLen returns the rune count of the given row. Out-of-range rows return 0.
func (b *TextBuffer) LineLen(row int) int {
	if row < 0 || row >= len(b.lines) {
		return 0
	}
	return len(b.lines[row])
}

// ClampPosition forces (row, col) into a valid in-buffer position.
func (b *TextBuffer) ClampPosition(row, col int) (int, int) {
	if len(b.lines) == 0 {
		return 0, 0
	}
	if row < 0 {
		row = 0
	}
	if row >= len(b.lines) {
		row = len(b.lines) - 1
	}
	if col < 0 {
		col = 0
	}
	if col > len(b.lines[row]) {
		col = len(b.lines[row])
	}
	return row, col
}

// Insert inserts the (possibly multi-line) text at (row, col) and returns
// the new cursor position immediately after the inserted text.
func (b *TextBuffer) Insert(row, col int, text string) (int, int) {
	row, col = b.ClampPosition(row, col)
	if text == "" {
		return row, col
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")

	// Tail of the current line (after the cursor) needs to follow the
	// final segment of the inserted text.
	current := b.lines[row]
	head := append([]rune{}, current[:col]...)
	tail := append([]rune{}, current[col:]...)

	parts := strings.Split(text, "\n")
	if len(parts) == 1 {
		// Single-line insert: splice runes into the current line.
		merged := make([]rune, 0, len(current)+utf8.RuneCountInString(parts[0]))
		merged = append(merged, head...)
		merged = append(merged, []rune(parts[0])...)
		newCol := len(merged)
		merged = append(merged, tail...)
		b.lines[row] = merged
		return row, newCol
	}

	// Multi-line: first segment joins head, last segment joins tail, middle
	// segments become whole new lines.
	first := append(head, []rune(parts[0])...)
	lastSegment := []rune(parts[len(parts)-1])
	endCol := len(lastSegment)
	last := append(lastSegment, tail...)

	middle := make([][]rune, 0, len(parts)-2)
	for _, segment := range parts[1 : len(parts)-1] {
		middle = append(middle, []rune(segment))
	}

	newLines := make([][]rune, 0, len(b.lines)+len(parts)-1)
	newLines = append(newLines, b.lines[:row]...)
	newLines = append(newLines, first)
	newLines = append(newLines, middle...)
	newLines = append(newLines, last)
	newLines = append(newLines, b.lines[row+1:]...)
	b.lines = newLines

	return row + len(parts) - 1, endCol
}

// DeleteRange removes the text between two positions (which may be passed in
// any order) and returns the resulting cursor position.
func (b *TextBuffer) DeleteRange(fromRow, fromCol, toRow, toCol int) (int, int) {
	fromRow, fromCol = b.ClampPosition(fromRow, fromCol)
	toRow, toCol = b.ClampPosition(toRow, toCol)
	if fromRow > toRow || (fromRow == toRow && fromCol > toCol) {
		fromRow, fromCol, toRow, toCol = toRow, toCol, fromRow, fromCol
	}
	if fromRow == toRow && fromCol == toCol {
		return fromRow, fromCol
	}

	if fromRow == toRow {
		line := b.lines[fromRow]
		merged := make([]rune, 0, len(line)-(toCol-fromCol))
		merged = append(merged, line[:fromCol]...)
		merged = append(merged, line[toCol:]...)
		b.lines[fromRow] = merged
		return fromRow, fromCol
	}

	head := append([]rune{}, b.lines[fromRow][:fromCol]...)
	tail := append([]rune{}, b.lines[toRow][toCol:]...)
	merged := append(head, tail...)

	newLines := make([][]rune, 0, len(b.lines)-(toRow-fromRow))
	newLines = append(newLines, b.lines[:fromRow]...)
	newLines = append(newLines, merged)
	newLines = append(newLines, b.lines[toRow+1:]...)
	b.lines = newLines
	return fromRow, fromCol
}

// ByteOffset returns the byte offset of (row, col) inside GetText().
func (b *TextBuffer) ByteOffset(row, col int) int {
	row, col = b.ClampPosition(row, col)
	offset := 0
	for i := 0; i < row; i++ {
		offset += byteLen(b.lines[i]) + 1 // +1 for the implicit newline
	}
	if col == 0 {
		return offset
	}
	line := b.lines[row]
	if col > len(line) {
		col = len(line)
	}
	for i := 0; i < col; i++ {
		offset += utf8.RuneLen(line[i])
	}
	return offset
}

// PositionFromByteOffset converts an absolute byte offset into the matching
// (row, col) position. Out-of-range offsets are clamped.
func (b *TextBuffer) PositionFromByteOffset(offset int) (int, int) {
	if offset <= 0 || len(b.lines) == 0 {
		return 0, 0
	}
	pos := 0
	for row, line := range b.lines {
		lineBytes := byteLen(line)
		if offset <= pos+lineBytes {
			// Walk runes inside the line until we hit the offset.
			localOffset := offset - pos
			col := 0
			for i, r := range line {
				if localOffset == 0 {
					return row, i
				}
				localOffset -= utf8.RuneLen(r)
				col = i + 1
			}
			return row, col
		}
		pos += lineBytes
		if row == len(b.lines)-1 {
			return row, len(line)
		}
		// Account for the implicit newline between lines.
		pos++
		if offset == pos-1 {
			return row, len(line)
		}
	}
	last := len(b.lines) - 1
	return last, len(b.lines[last])
}

// TotalByteLen returns the total byte length of the document — equivalent to
// len(GetText()) but cheaper because it does not allocate the joined string.
func (b *TextBuffer) TotalByteLen() int {
	if len(b.lines) == 0 {
		return 0
	}
	total := 0
	for i, line := range b.lines {
		total += byteLen(line)
		if i < len(b.lines)-1 {
			total++
		}
	}
	return total
}

func byteLen(line []rune) int {
	n := 0
	for _, r := range line {
		n += utf8.RuneLen(r)
	}
	return n
}
