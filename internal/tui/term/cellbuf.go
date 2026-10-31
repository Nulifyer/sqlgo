package term

import (
	"github.com/clipperhouse/uax29/v2/graphemes"
	"github.com/mattn/go-runewidth"
)

// Style bundles a cell's visual attributes. Zero value is the terminal
// default on every axis. Kept as a plain struct so layers can build and
// pass around styled runs without fiddling with the pen each time.
type Style struct {
	FG    int       // ANSI SGR foreground code; AnsiDefault = terminal default
	BG    int       // ANSI SGR background code; AnsiDefaultBG = terminal default
	Attrs CellAttrs // bold/italic/underline bitmask
}

// RuneDisplayWidth returns the number of terminal columns a rune
// occupies: 0 for combining marks and zero-width joiners, 1 for
// ordinary printable chars, 2 for East Asian Wide / Fullwidth / most
// emoji. Backed by github.com/mattn/go-runewidth, which keeps its
// Unicode table up to date. Control characters (< 0x20, 0x7f) should
// be filtered out by the caller before this is consulted -- they
// return 0 here but that's a side-effect of runewidth's default
// table, not a contract.
func RuneDisplayWidth(r rune) int {
	return runewidth.RuneWidth(r)
}

// StringDisplayWidth is the string analog of RuneDisplayWidth.
// Respects the same conventions (combining marks contribute 0).
func StringDisplayWidth(s string) int {
	return runewidth.StringWidth(s)
}

// IsWideRune reports whether r draws as 2 terminal columns. Kept as a
// convenience over RuneDisplayWidth so call sites that only care
// about the wide/narrow split stay readable.
func IsWideRune(r rune) bool {
	return runewidth.RuneWidth(r) == 2
}

// DefaultStyle returns a Style that resets to terminal defaults on every
// axis. Used as the pen's initial state and on Reset().
func DefaultStyle() Style {
	return Style{FG: AnsiDefault, BG: AnsiDefaultBG}
}

// CellAttrs is a bitmask of SGR toggles beyond color. Only the attrs sqlgo
// actually renders are defined; add to this as features land.
type CellAttrs uint8

const (
	AttrBold CellAttrs = 1 << iota
	AttrUnderline
	AttrReverse
)

// Cell is a single terminal cell. During compositing, touched=false means
// "this layer has nothing here" and the cell from the layer beneath shows
// through. In the final (post-composite) buffer every cell is touched.
//
// Wide-rune model: a wide rune (CJK, emoji, fullwidth) occupies two
// terminal columns. The head cell holds the rune and has wideCont=false;
// the cell immediately to its right is a continuation slot with r=0,
// wideCont=true, same style. Writes that target the continuation slot
// directly replace it with a narrow rune; the flush diff clobbers the
// stale glyph half automatically because the wideCont flag changed.
type Cell struct {
	R     rune
	Style Style
	FG    int
	// Combining holds the remaining code points of the grapheme headed by R.
	// This includes combining marks and the rest of emoji/ZWJ sequences.
	// Flush emits the complete cluster once; nil is the common ASCII case.
	Combining []rune
	Touched   bool
	WideCont  bool
}

// Cellbuf is a rectangular grid of cells. Layers draw into one each frame
// (via the write* methods below), then Screen.Composite merges all layer
// buffers into a single final frame. Coordinates are 1-based to match the
// rest of the TUI.
type Cellbuf struct {
	W, H  int
	Cells []Cell // row-major, len == W*H

	// pen -- style applied to subsequent writes when the caller doesn't
	// pass an explicit Style in WriteStyled.
	pen Style

	// Cursor placement request. Only the topmost layer's request survives
	// compositing.
	CursorRow    int
	CursorCol    int
	CursorWanted bool
}

func NewCellbuf(w, h int) *Cellbuf {
	return &Cellbuf{
		W:     w,
		H:     h,
		Cells: make([]Cell, w*h),
		pen:   DefaultStyle(),
	}
}

// Reset clears every cell to untouched and returns the pen to the terminal
// default. Called once per frame before a layer draws into this buffer.
func (c *Cellbuf) Reset() {
	for i := range c.Cells {
		c.Cells[i] = Cell{}
	}
	c.pen = DefaultStyle()
	c.CursorWanted = false
}

// At returns a pointer to the cell at (row, col), or nil if out of bounds.
// Coordinates are 1-based.
func (c *Cellbuf) At(row, col int) *Cell {
	if row < 1 || row > c.H || col < 1 || col > c.W {
		return nil
	}
	return &c.Cells[(row-1)*c.W+(col-1)]
}

// SetFg sets the foreground for subsequent writes.
func (c *Cellbuf) SetFg(fg int) { c.pen.FG = fg }

// ResetStyle returns the pen to the terminal default.
func (c *Cellbuf) ResetStyle() { c.pen = DefaultStyle() }

// WriteAt writes the runes of s starting at (row, col), clipped to the
// right edge. Each written cell is marked touched with the current pen.
func (c *Cellbuf) WriteAt(row, col int, s string) {
	c.WriteStyled(row, col, s, c.pen)
}

// WriteStyled writes the runes of s starting at (row, col) using the given
// Style, without touching the pen. This is the building block for layered
// syntax highlighters and tokenized editor rendering: a single draw pass
// can emit runs with different styles back-to-back without saving and
// restoring a pen each time.
//
// Grapheme clusters occupy one or two cells. The first rune occupies the
// head cell and the remaining runes are emitted with it, preserving combining
// accents and emoji sequences. Zero-width controls are ignored.
func (c *Cellbuf) WriteStyled(row, col int, s string, st Style) {
	it := graphemes.FromString(s)
	for it.Next() {
		cluster := it.Value()
		rs := []rune(cluster)
		if len(rs) == 0 {
			continue
		}
		width := StringDisplayWidth(cluster)
		if width == 0 {
			if rs[0] < 0x20 || (rs[0] >= 0x7f && rs[0] <= 0x9f) {
				continue
			}
			if p := c.At(row, col-1); p != nil && p.R != 0 && !p.WideCont {
				p.Combining = append(p.Combining, rs...)
			} else if p := c.At(row, col-2); p != nil && p.R != 0 {
				p.Combining = append(p.Combining, rs...)
			}
			continue
		}
		width = min(width, 2)
		if p := c.At(row, col); p != nil {
			*p = Cell{R: rs[0], Combining: append([]rune(nil), rs[1:]...), Style: st, FG: st.FG, Touched: true}
		}
		if width == 2 {
			if p := c.At(row, col+1); p != nil {
				*p = Cell{Style: st, FG: st.FG, Touched: true, WideCont: true}
			}
		}
		col += width
	}
}

// HLine draws a horizontal run of a single rune from col1..col2 inclusive
// on the given row.
func (c *Cellbuf) HLine(row, col1, col2 int, r rune) {
	if col1 > col2 {
		return
	}
	for col := col1; col <= col2; col++ {
		if p := c.At(row, col); p != nil {
			*p = Cell{R: r, Style: c.pen, FG: c.pen.FG, Touched: true}
		}
	}
}

// VLine draws a vertical run of a single rune from row1..row2 inclusive
// on the given column.
func (c *Cellbuf) VLine(col, row1, row2 int, r rune) {
	for row := row1; row <= row2; row++ {
		if p := c.At(row, col); p != nil {
			*p = Cell{R: r, Style: c.pen, FG: c.pen.FG, Touched: true}
		}
	}
}

// FillRect marks every cell inside r as a touched blank (space). Overlays
// previously needed this to clear the content beneath them; with cell-level
// compositing it simply reserves the rect as opaque. Useful for popups that
// want their footprint to block the main view even in otherwise-empty cells.
func (c *Cellbuf) FillRect(r Rect) {
	top := r.Row
	bot := r.Row + r.H - 1
	left := r.Col
	right := r.Col + r.W - 1
	for row := top; row <= bot; row++ {
		for col := left; col <= right; col++ {
			if p := c.At(row, col); p != nil {
				*p = Cell{R: ' ', Style: c.pen, FG: c.pen.FG, Touched: true}
			}
		}
	}
}

// PlaceCursor records where this layer would like the cursor drawn.
// Honored only if this layer ends up as the topmost after compositing.
func (c *Cellbuf) PlaceCursor(row, col int) {
	c.CursorWanted = true
	c.CursorRow = row
	c.CursorCol = col
}
