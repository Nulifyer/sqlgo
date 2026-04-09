package tui

import (
	"fmt"
	"strings"
	"sync"

	"github.com/Nulifyer/sqlgo/internal/db"
)

// table renders a streamed query result as a scrollable aligned grid. Rows
// arrive via Append() on a background goroutine (the query runner in tui.go)
// while the UI goroutine reads them in draw(); the mutex keeps both sides
// consistent. Column widths are computed from the header and up to
// sampleRows of data so a very tall result doesn't stall width updates.
//
// wrap=false (default): cells are padded to their measured width and the
// whole row is clipped at the panel's right edge -- wide columns simply run
// off-screen. wrap=true: each row spans as many terminal rows as the widest
// cell's line count requires, so nothing is hidden but fewer rows fit.
type table struct {
	mu        sync.Mutex
	cols      []db.Column
	widths    []int
	rendered  [][]string // stringified cells, one entry per streamed row
	streaming bool       // cursor drain in progress
	capped    bool       // stopped appending because we hit maxBufferedRows
	err       error      // last streaming error, if any
	scrollRow int
	scrollCol int
	wrap      bool
}

const (
	sampleRows      = 200
	maxBufferedRows = 100_000
)

func newTable() *table { return &table{} }

// Clear wipes all result state. Called on disconnect and before starting a
// new query.
func (t *table) Clear() {
	t.mu.Lock()
	t.cols = nil
	t.widths = nil
	t.rendered = nil
	t.streaming = false
	t.capped = false
	t.err = nil
	t.scrollRow = 0
	t.scrollCol = 0
	t.mu.Unlock()
}

// Init prepares the table to receive a new streaming result. The caller
// (the query goroutine in tui.go) supplies the column descriptors pulled
// from the cursor and then follows up with Append()/Done(). Any prior
// result is discarded.
func (t *table) Init(cols []db.Column) {
	t.mu.Lock()
	t.cols = cols
	t.widths = make([]int, len(cols))
	for i, c := range cols {
		t.widths[i] = runeLen(c.Name)
	}
	t.rendered = nil
	t.streaming = true
	t.capped = false
	t.err = nil
	t.scrollRow = 0
	t.scrollCol = 0
	t.mu.Unlock()
}

// Append adds one streamed row. Returns false once the in-memory cap has
// been reached; the caller should stop pulling from the cursor and call
// Done(nil) for final bookkeeping. Column widths only grow from the first
// sampleRows to keep append cost bounded.
func (t *table) Append(row []any) bool {
	cells := make([]string, len(row))
	for i, v := range row {
		cells[i] = stringifyCell(v)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.rendered) >= maxBufferedRows {
		t.capped = true
		return false
	}
	if len(t.rendered) < sampleRows {
		for j, cell := range cells {
			if j >= len(t.widths) {
				break
			}
			if l := runeLen(cell); l > t.widths[j] {
				t.widths[j] = l
			}
		}
	}
	t.rendered = append(t.rendered, cells)
	return true
}

// Done marks the stream as finished and records any final error (nil on
// a clean end-of-result).
func (t *table) Done(err error) {
	t.mu.Lock()
	t.streaming = false
	t.err = err
	t.mu.Unlock()
}

// RowCount returns the number of rows currently in the buffer. This is a
// snapshot: during streaming, the next draw may see more rows.
func (t *table) RowCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.rendered)
}

// HasColumns reports whether Init has been called since the last Clear.
// Used by hint builders that want to distinguish "no results" from
// "results shown, 0 rows".
func (t *table) HasColumns() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.cols) > 0
}

// Streaming reports whether a cursor drain is currently in progress.
func (t *table) Streaming() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.streaming
}

// Capped reports whether appends were stopped because the buffer cap was
// reached. Surfaced in the status line so the user knows the result set
// was larger than the buffer.
func (t *table) Capped() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.capped
}

// Wrap reports whether the table is in wrap mode.
func (t *table) Wrap() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.wrap
}

// ToggleWrap flips wrap mode and resets scroll so the top of the result set
// stays visible when the layout changes.
func (t *table) ToggleWrap() {
	t.mu.Lock()
	t.wrap = !t.wrap
	t.scrollRow = 0
	t.scrollCol = 0
	t.mu.Unlock()
}

// ScrollBy adjusts the top row by delta, clamped to valid range.
func (t *table) ScrollBy(delta int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.rendered) == 0 {
		return
	}
	t.scrollRow += delta
	if t.scrollRow < 0 {
		t.scrollRow = 0
	}
	max := len(t.rendered) - 1
	if t.scrollRow > max {
		t.scrollRow = max
	}
}

// ScrollColBy adjusts the horizontal offset by delta runes, clamped so the
// viewport never scrolls past the end of the widest rendered line.
func (t *table) ScrollColBy(delta int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.cols) == 0 {
		return
	}
	t.scrollCol += delta
	if t.scrollCol < 0 {
		t.scrollCol = 0
	}
	max := t.totalWidthLocked() - 1
	if max < 0 {
		max = 0
	}
	if t.scrollCol > max {
		t.scrollCol = max
	}
}

// totalWidthLocked is the full rendered line width (sum of column widths
// plus the " | " separators between them). Caller must hold t.mu.
func (t *table) totalWidthLocked() int {
	n := 0
	for i, w := range t.widths {
		if i > 0 {
			n += 3 // " | "
		}
		n += w
	}
	return n
}

// draw renders the table inside r (caller has already drawn the border).
func (t *table) draw(s *cellbuf, r rect) {
	innerRow := r.row + 1
	innerCol := r.col + 1
	innerW := r.w - 2
	innerH := r.h - 2
	if innerW <= 0 || innerH <= 0 {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.cols) == 0 {
		s.writeAt(innerRow, innerCol, truncate("(no results)", innerW))
		return
	}

	// Clamp horizontal scroll to the current viewport width so shrinking
	// the panel after scrolling right doesn't leave dead space.
	total := t.totalWidthLocked()
	if t.scrollCol > total-1 {
		t.scrollCol = total - 1
	}
	if t.scrollCol < 0 {
		t.scrollCol = 0
	}

	// Header row.
	header := renderRow(t.headerCellsLocked(), t.widths)
	s.setFg(colorTitleFocused)
	s.writeAt(innerRow, innerCol, sliceRunes(header, t.scrollCol, innerW))
	s.resetStyle()

	// Separator (single dash line).
	sep := renderSeparator(t.widths)
	s.writeAt(innerRow+1, innerCol, sliceRunes(sep, t.scrollCol, innerW))

	// Body rows.
	bodyH := innerH - 2
	if bodyH <= 0 {
		return
	}

	// Clamp scrollRow against the current buffer (rendered grows as rows
	// stream in; we don't want to snap the cursor forward if the user had
	// scrolled manually).
	if t.scrollRow >= len(t.rendered) {
		if len(t.rendered) > 0 {
			t.scrollRow = len(t.rendered) - 1
		} else {
			t.scrollRow = 0
		}
	}

	if !t.wrap {
		for i := 0; i < bodyH; i++ {
			rowIdx := t.scrollRow + i
			if rowIdx >= len(t.rendered) {
				break
			}
			line := renderRow(t.rendered[rowIdx], t.widths)
			s.writeAt(innerRow+2+i, innerCol, sliceRunes(line, t.scrollCol, innerW))
		}
		return
	}

	// Wrap mode: each data row may occupy multiple terminal rows when a
	// cell's content is longer than its column width. The longest cell's
	// wrapped-line count sets the block height; other cells are padded
	// with blanks so column alignment survives across wrapped lines.
	y := 0
	for rowIdx := t.scrollRow; rowIdx < len(t.rendered) && y < bodyH; rowIdx++ {
		block := wrapRow(t.rendered[rowIdx], t.widths)
		for _, line := range block {
			if y >= bodyH {
				break
			}
			s.writeAt(innerRow+2+y, innerCol, sliceRunes(line, t.scrollCol, innerW))
			y++
		}
	}
}

// headerCellsLocked builds the header row from the current columns. Caller
// must hold t.mu.
func (t *table) headerCellsLocked() []string {
	out := make([]string, len(t.cols))
	for i, c := range t.cols {
		out[i] = c.Name
	}
	return out
}

// renderRow joins cells with " | " padded to their column widths. No
// horizontal clipping -- callers slice the result to fit the viewport so
// scrollCol can slide across the full row width.
func renderRow(cells []string, widths []int) string {
	var b strings.Builder
	for i, cell := range cells {
		if i > 0 {
			b.WriteString(" | ")
		}
		padRight(&b, cell, widths[i])
	}
	return b.String()
}

func renderSeparator(widths []int) string {
	var b strings.Builder
	for i, w := range widths {
		if i > 0 {
			b.WriteString("-+-")
		}
		b.WriteString(strings.Repeat("-", w))
	}
	return b.String()
}

// sliceRunes returns up to width runes of s starting at offset. Out-of-range
// offsets yield an empty string rather than panicking, so callers can scroll
// freely without bounds-checking first.
func sliceRunes(s string, offset, width int) string {
	if width <= 0 || offset < 0 {
		return ""
	}
	rs := []rune(s)
	if offset >= len(rs) {
		return ""
	}
	end := offset + width
	if end > len(rs) {
		end = len(rs)
	}
	return string(rs[offset:end])
}

func padRight(b *strings.Builder, s string, w int) {
	b.WriteString(s)
	for i := runeLen(s); i < w; i++ {
		b.WriteByte(' ')
	}
}

func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if runeLen(s) <= w {
		return s
	}
	rs := []rune(s)
	if w <= 3 {
		return string(rs[:w])
	}
	return string(rs[:w-3]) + "..."
}

func runeLen(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}

// stringifyCell converts a driver-returned value into a display string.
// Keeps it short: cell inspection for blobs comes later.
func stringifyCell(v any) string {
	return sanitizeCell(rawStringify(v))
}

func rawStringify(v any) string {
	if v == nil {
		return "NULL"
	}
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case []byte:
		return fmt.Sprintf("<%d bytes>", len(x))
	default:
		return fmt.Sprintf("%v", v)
	}
}

// wrapRow splits a single logical result row into as many aligned terminal
// lines as its widest cell requires. Each cell is chopped at its column
// width; columns that run out of content early are filled with spaces so
// the " | " separators stay aligned.
func wrapRow(cells []string, widths []int) []string {
	// Pre-chop every cell into width-sized slices of runes.
	chunks := make([][]string, len(cells))
	maxLines := 1
	for i, cell := range cells {
		w := widths[i]
		if w <= 0 {
			chunks[i] = []string{""}
			continue
		}
		chunks[i] = chopRunes(cell, w)
		if len(chunks[i]) > maxLines {
			maxLines = len(chunks[i])
		}
	}
	out := make([]string, maxLines)
	for line := 0; line < maxLines; line++ {
		var b strings.Builder
		for i, col := range chunks {
			if i > 0 {
				b.WriteString(" | ")
			}
			if line < len(col) {
				padRight(&b, col[line], widths[i])
			} else {
				padRight(&b, "", widths[i])
			}
		}
		out[line] = b.String()
	}
	return out
}

// chopRunes splits s into slices of at most w runes. Empty strings become a
// single empty slice so wrapRow produces at least one output row per cell.
func chopRunes(s string, w int) []string {
	if s == "" {
		return []string{""}
	}
	runes := []rune(s)
	var out []string
	for i := 0; i < len(runes); i += w {
		end := i + w
		if end > len(runes) {
			end = len(runes)
		}
		out = append(out, string(runes[i:end]))
	}
	return out
}

// sanitizeCell replaces newlines, tabs, and other control characters with
// spaces so a single result row always fits on one display line. The full
// original value will still be available via the cell inspection view
// once that widget exists.
func sanitizeCell(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n', r == '\r', r == '\t':
			b.WriteByte(' ')
		case r < 0x20, r == 0x7f:
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
