package tui

import (
	"fmt"
	"strings"

	"github.com/Nulifyer/sqlgo/internal/db"
)

// table renders a db.Result as a scrollable aligned grid. Column widths are
// computed once per SetResult call from the headers and up to sampleRows of
// data so very tall result sets don't stall rendering.
type table struct {
	res       *db.Result
	widths    []int
	scrollRow int
	rendered  [][]string // stringified cells (lazy, same shape as res.Rows)
}

const sampleRows = 200

func newTable() *table { return &table{} }

// SetResult replaces the displayed result set and resets scroll.
func (t *table) SetResult(r *db.Result) {
	t.res = r
	t.scrollRow = 0
	if r == nil {
		t.widths = nil
		t.rendered = nil
		return
	}
	t.rendered = make([][]string, len(r.Rows))
	for i, row := range r.Rows {
		cells := make([]string, len(row))
		for j, v := range row {
			cells[j] = stringifyCell(v)
		}
		t.rendered[i] = cells
	}
	t.widths = make([]int, len(r.Columns))
	for i, c := range r.Columns {
		t.widths[i] = runeLen(c.Name)
	}
	limit := len(t.rendered)
	if limit > sampleRows {
		limit = sampleRows
	}
	for i := 0; i < limit; i++ {
		for j, cell := range t.rendered[i] {
			if l := runeLen(cell); l > t.widths[j] {
				t.widths[j] = l
			}
		}
	}
}

// Result returns the current result set (may be nil).
func (t *table) Result() *db.Result { return t.res }

// ScrollBy adjusts the top row by delta, clamped to valid range.
func (t *table) ScrollBy(delta int) {
	if t.res == nil {
		return
	}
	t.scrollRow += delta
	if t.scrollRow < 0 {
		t.scrollRow = 0
	}
	max := len(t.res.Rows) - 1
	if max < 0 {
		max = 0
	}
	if t.scrollRow > max {
		t.scrollRow = max
	}
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

	if t.res == nil {
		s.writeAt(innerRow, innerCol, truncate("(no results)", innerW))
		return
	}
	if len(t.res.Columns) == 0 {
		s.writeAt(innerRow, innerCol, truncate("(query returned no columns)", innerW))
		return
	}

	// Header row.
	header := renderRow(t.headerCells(), t.widths, innerW)
	s.setFg(colorTitleFocused)
	s.writeAt(innerRow, innerCol, header)
	s.resetStyle()

	// Separator (single dash line).
	sep := renderSeparator(t.widths, innerW)
	s.writeAt(innerRow+1, innerCol, sep)

	// Body rows.
	bodyH := innerH - 2
	if bodyH <= 0 {
		return
	}
	for i := 0; i < bodyH; i++ {
		rowIdx := t.scrollRow + i
		if rowIdx >= len(t.rendered) {
			break
		}
		line := renderRow(t.rendered[rowIdx], t.widths, innerW)
		s.writeAt(innerRow+2+i, innerCol, line)
	}
}

func (t *table) headerCells() []string {
	out := make([]string, len(t.res.Columns))
	for i, c := range t.res.Columns {
		out[i] = c.Name
	}
	return out
}

// renderRow joins cells with " | " padded to their column widths, clipped
// to maxW runes.
func renderRow(cells []string, widths []int, maxW int) string {
	var b strings.Builder
	for i, cell := range cells {
		if i > 0 {
			b.WriteString(" | ")
		}
		padRight(&b, cell, widths[i])
	}
	return truncate(b.String(), maxW)
}

func renderSeparator(widths []int, maxW int) string {
	var b strings.Builder
	for i, w := range widths {
		if i > 0 {
			b.WriteString("-+-")
		}
		b.WriteString(strings.Repeat("-", w))
	}
	return truncate(b.String(), maxW)
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
