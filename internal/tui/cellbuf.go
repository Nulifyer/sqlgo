package tui

// cell is a single terminal cell. During compositing, touched=false means
// "this layer has nothing here" and the cell from the layer beneath shows
// through. In the final (post-composite) buffer every cell is touched.
type cell struct {
	r       rune
	fg      int // ANSI SGR foreground code; ansiDefault = terminal default
	touched bool
}

// cellbuf is a rectangular grid of cells. Layers draw into one each frame
// (via the write* methods below), then screen.composite merges all layer
// buffers into a single final frame. Coordinates are 1-based to match the
// rest of the TUI.
type cellbuf struct {
	w, h  int
	cells []cell // row-major, len == w*h

	// pen — style applied to subsequent writes
	fg int

	// Cursor placement request. Only the topmost layer's request survives
	// compositing.
	cursorRow    int
	cursorCol    int
	cursorWanted bool
}

func newCellbuf(w, h int) *cellbuf {
	return &cellbuf{
		w:     w,
		h:     h,
		cells: make([]cell, w*h),
		fg:    ansiDefault,
	}
}

// reset clears every cell to untouched and returns the pen to the terminal
// default. Called once per frame before a layer draws into this buffer.
func (c *cellbuf) reset() {
	for i := range c.cells {
		c.cells[i] = cell{}
	}
	c.fg = ansiDefault
	c.cursorWanted = false
}

// at returns a pointer to the cell at (row, col), or nil if out of bounds.
// Coordinates are 1-based.
func (c *cellbuf) at(row, col int) *cell {
	if row < 1 || row > c.h || col < 1 || col > c.w {
		return nil
	}
	return &c.cells[(row-1)*c.w+(col-1)]
}

// setFg sets the foreground for subsequent writes.
func (c *cellbuf) setFg(fg int) { c.fg = fg }

// resetStyle returns the pen to the terminal default.
func (c *cellbuf) resetStyle() { c.fg = ansiDefault }

// writeAt writes the runes of s starting at (row, col), clipped to the
// right edge. Each written cell is marked touched with the current pen.
func (c *cellbuf) writeAt(row, col int, s string) {
	for _, r := range s {
		if p := c.at(row, col); p != nil {
			*p = cell{r: r, fg: c.fg, touched: true}
		}
		col++
	}
}

// hLine draws a horizontal run of a single rune from col1..col2 inclusive
// on the given row.
func (c *cellbuf) hLine(row, col1, col2 int, r rune) {
	if col1 > col2 {
		return
	}
	for col := col1; col <= col2; col++ {
		if p := c.at(row, col); p != nil {
			*p = cell{r: r, fg: c.fg, touched: true}
		}
	}
}

// vLine draws a vertical run of a single rune from row1..row2 inclusive
// on the given column.
func (c *cellbuf) vLine(col, row1, row2 int, r rune) {
	for row := row1; row <= row2; row++ {
		if p := c.at(row, col); p != nil {
			*p = cell{r: r, fg: c.fg, touched: true}
		}
	}
}

// fillRect marks every cell inside r as a touched blank (space). Overlays
// previously needed this to clear the content beneath them; with cell-level
// compositing it simply reserves the rect as opaque. Useful for popups that
// want their footprint to block the main view even in otherwise-empty cells.
func (c *cellbuf) fillRect(r rect) {
	top := r.row
	bot := r.row + r.h - 1
	left := r.col
	right := r.col + r.w - 1
	for row := top; row <= bot; row++ {
		for col := left; col <= right; col++ {
			if p := c.at(row, col); p != nil {
				*p = cell{r: ' ', fg: c.fg, touched: true}
			}
		}
	}
}

// placeCursor records where this layer would like the cursor drawn.
// Honored only if this layer ends up as the topmost after compositing.
func (c *cellbuf) placeCursor(row, col int) {
	c.cursorWanted = true
	c.cursorRow = row
	c.cursorCol = col
}
