package tui

// buffer is a simple multi-line text buffer indexed in runes (not bytes).
// It is the data model behind the query editor: a slice of rune slices, one
// per line, plus a cursor. Operations mutate in place and clamp the cursor
// to valid positions, so callers never have to bounds-check.
//
// Not safe for concurrent use. The TUI only touches it from the main loop.
type buffer struct {
	lines [][]rune
	// row, col are 0-based. col is allowed to equal len(line) — that means
	// the cursor is sitting after the last character on the line.
	row int
	col int
}

func newBuffer() *buffer {
	return &buffer{lines: [][]rune{{}}}
}

// Text returns the buffer contents joined by '\n'.
func (b *buffer) Text() string {
	// pre-size for the common small-query case
	n := 0
	for i, ln := range b.lines {
		if i > 0 {
			n++
		}
		n += len(ln) * 2 // rough: runes may be multi-byte
	}
	out := make([]byte, 0, n)
	for i, ln := range b.lines {
		if i > 0 {
			out = append(out, '\n')
		}
		out = append(out, string(ln)...)
	}
	return string(out)
}

// LineCount returns the number of lines in the buffer (always >= 1).
func (b *buffer) LineCount() int { return len(b.lines) }

// Line returns the runes of line i. Callers must not mutate the slice.
func (b *buffer) Line(i int) []rune { return b.lines[i] }

// Cursor returns the current cursor position.
func (b *buffer) Cursor() (row, col int) { return b.row, b.col }

// Clear resets the buffer to a single empty line.
func (b *buffer) Clear() {
	b.lines = [][]rune{{}}
	b.row, b.col = 0, 0
}

// Insert writes r at the cursor and advances one column.
func (b *buffer) Insert(r rune) {
	line := b.lines[b.row]
	// grow by one
	line = append(line, 0)
	copy(line[b.col+1:], line[b.col:])
	line[b.col] = r
	b.lines[b.row] = line
	b.col++
}

// InsertNewline splits the current line at the cursor.
func (b *buffer) InsertNewline() {
	line := b.lines[b.row]
	head := append([]rune(nil), line[:b.col]...)
	tail := append([]rune(nil), line[b.col:]...)

	b.lines[b.row] = head
	// insert tail as a new line after row
	b.lines = append(b.lines, nil)
	copy(b.lines[b.row+2:], b.lines[b.row+1:])
	b.lines[b.row+1] = tail

	b.row++
	b.col = 0
}

// Backspace deletes the character before the cursor, or joins with the
// previous line if at column 0.
func (b *buffer) Backspace() {
	if b.col > 0 {
		line := b.lines[b.row]
		copy(line[b.col-1:], line[b.col:])
		b.lines[b.row] = line[:len(line)-1]
		b.col--
		return
	}
	if b.row == 0 {
		return
	}
	// join with previous line
	prev := b.lines[b.row-1]
	cur := b.lines[b.row]
	newCol := len(prev)
	b.lines[b.row-1] = append(prev, cur...)
	b.lines = append(b.lines[:b.row], b.lines[b.row+1:]...)
	b.row--
	b.col = newCol
}

// Delete removes the character under the cursor, or joins the next line
// into the current one if at end of line.
func (b *buffer) Delete() {
	line := b.lines[b.row]
	if b.col < len(line) {
		copy(line[b.col:], line[b.col+1:])
		b.lines[b.row] = line[:len(line)-1]
		return
	}
	if b.row == len(b.lines)-1 {
		return
	}
	next := b.lines[b.row+1]
	b.lines[b.row] = append(line, next...)
	b.lines = append(b.lines[:b.row+1], b.lines[b.row+2:]...)
}

// MoveLeft decrements the column, wrapping to the end of the previous line
// when at column 0.
func (b *buffer) MoveLeft() {
	if b.col > 0 {
		b.col--
		return
	}
	if b.row > 0 {
		b.row--
		b.col = len(b.lines[b.row])
	}
}

// MoveRight increments the column, wrapping to column 0 of the next line
// when past end-of-line.
func (b *buffer) MoveRight() {
	if b.col < len(b.lines[b.row]) {
		b.col++
		return
	}
	if b.row < len(b.lines)-1 {
		b.row++
		b.col = 0
	}
}

// MoveUp moves the cursor up one line, clamping the column to the new
// line's length.
func (b *buffer) MoveUp() {
	if b.row == 0 {
		b.col = 0
		return
	}
	b.row--
	if b.col > len(b.lines[b.row]) {
		b.col = len(b.lines[b.row])
	}
}

// MoveDown moves the cursor down one line, clamping the column.
func (b *buffer) MoveDown() {
	if b.row == len(b.lines)-1 {
		b.col = len(b.lines[b.row])
		return
	}
	b.row++
	if b.col > len(b.lines[b.row]) {
		b.col = len(b.lines[b.row])
	}
}

// MoveHome jumps to column 0 on the current line.
func (b *buffer) MoveHome() { b.col = 0 }

// MoveEnd jumps to the end of the current line.
func (b *buffer) MoveEnd() { b.col = len(b.lines[b.row]) }
