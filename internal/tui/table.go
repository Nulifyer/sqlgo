package tui

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/Nulifyer/sqlgo/internal/db"
)

// table renders a streamed query result as a scrollable aligned grid. Rows
// arrive via Append() on a background goroutine (the query runner in tui.go)
// while the UI goroutine reads them in draw(); the mutex keeps both sides
// consistent.
//
// State split:
//
//   - rendered: the streaming in-memory buffer of stringified cells. Control
//     characters (\n \r \t) are preserved in this form so the draw path can
//     paint them as dim two-char escapes rather than swallowing them.
//   - view: an index into rendered after applying the current filter and
//     sort. When both are inactive, view is nil and the draw path iterates
//     rendered directly.
//   - cellRow / cellCol: the active cell cursor, used for copy, sort, and
//     cell inspection. Navigation keys in the results panel drive these.
//   - widths: per-column display widths in terminal cells (not raw runes --
//     escape chars count as two so the column stays wide enough).
//
// wrap=false (default): cells are padded to their measured width and the
// whole row is clipped at the panel's right edge. wrap=true: each row spans
// as many terminal rows as the widest cell's line count requires, so nothing
// is hidden but fewer rows fit.
type table struct {
	mu        sync.Mutex
	cols      []db.Column
	widths    []int
	rendered  [][]string
	streaming bool
	capped    bool
	err       error

	// view is an index into rendered. When nil, the natural order (0..N-1)
	// of rendered is used. Filter and sort produce a non-nil view.
	view []int

	filter   string // case-insensitive substring; empty means no filter
	sortCol  int    // column index; -1 means no sort
	sortDesc bool

	// Cell cursor position within the current view. cellRow is an index
	// into view (or rendered when view is nil); cellCol is an index into
	// cols. Both clamp to valid range on every access.
	cellRow int
	cellCol int

	// Top-left scroll anchor. scrollRow tracks the first visible row in
	// the current view; scrollCol is a rune offset into the rendered
	// line so wide tables can slide left/right.
	scrollRow int
	scrollCol int

	wrap bool
}

const (
	sampleRows      = 200
	maxBufferedRows = 100_000
)

func newTable() *table { return &table{sortCol: -1} }

// Clear wipes all result state. Called on disconnect and before starting a
// new query.
func (t *table) Clear() {
	t.mu.Lock()
	t.cols = nil
	t.widths = nil
	t.rendered = nil
	t.view = nil
	t.filter = ""
	t.sortCol = -1
	t.sortDesc = false
	t.cellRow = 0
	t.cellCol = 0
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
// result -- including filter/sort state -- is discarded.
func (t *table) Init(cols []db.Column) {
	t.mu.Lock()
	t.cols = cols
	t.widths = make([]int, len(cols))
	for i, c := range cols {
		t.widths[i] = displayWidth(c.Name)
	}
	t.rendered = nil
	t.view = nil
	t.filter = ""
	t.sortCol = -1
	t.sortDesc = false
	t.cellRow = 0
	t.cellCol = 0
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
		cells[i] = sanitizeCell(rawStringify(v))
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
			if l := displayWidth(cell); l > t.widths[j] {
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
// reached.
func (t *table) Capped() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.capped
}

// Snapshot returns a point-in-time copy of the current columns and the
// rows in the current view (filtered + sorted if those are active).
// Fresh allocations so callers can retain the result without fighting
// streaming mutations.
func (t *table) Snapshot() ([]db.Column, [][]string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	cols := make([]db.Column, len(t.cols))
	copy(cols, t.cols)

	view := t.viewIndicesLocked()
	rows := make([][]string, len(view))
	for i, srcIdx := range view {
		src := t.rendered[srcIdx]
		row := make([]string, len(src))
		copy(row, src)
		rows[i] = row
	}
	return cols, rows
}

// viewIndicesLocked returns the current view as an index slice. When view
// is nil (no filter, no sort) it fabricates a [0..N-1] slice so callers
// can iterate uniformly. Caller must hold t.mu.
func (t *table) viewIndicesLocked() []int {
	if t.view != nil {
		return t.view
	}
	out := make([]int, len(t.rendered))
	for i := range out {
		out[i] = i
	}
	return out
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

// --- cell cursor navigation -------------------------------------------------

// MoveCellBy shifts the cell cursor by (dRow, dCol), clamped to valid
// range. Used by the Up/Dn/Lt/Rt bindings in the results panel.
func (t *table) MoveCellBy(dRow, dCol int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cellRow += dRow
	t.cellCol += dCol
	t.clampCursorLocked()
}

// MoveCellPage shifts the row cursor by approximately one page (10 rows)
// without touching the column cursor.
func (t *table) MoveCellPage(dir int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cellRow += dir * 10
	t.clampCursorLocked()
}

// MoveCellHome jumps the cursor to the top-left of the view.
func (t *table) MoveCellHome() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cellRow = 0
	t.cellCol = 0
}

// MoveCellEnd jumps the cursor to the last row, keeping the current col.
func (t *table) MoveCellEnd() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cellRow = t.viewLenLocked() - 1
	t.clampCursorLocked()
}

// clampCursorLocked keeps cellRow/cellCol inside the valid range for the
// current view and column set. Caller must hold t.mu.
func (t *table) clampCursorLocked() {
	n := t.viewLenLocked()
	if t.cellRow < 0 {
		t.cellRow = 0
	}
	if t.cellRow > n-1 {
		t.cellRow = n - 1
	}
	if t.cellRow < 0 {
		t.cellRow = 0
	}
	if t.cellCol < 0 {
		t.cellCol = 0
	}
	if t.cellCol > len(t.cols)-1 {
		t.cellCol = len(t.cols) - 1
	}
	if t.cellCol < 0 {
		t.cellCol = 0
	}
}

func (t *table) viewLenLocked() int {
	if t.view != nil {
		return len(t.view)
	}
	return len(t.rendered)
}

// CursorCell returns the value under the cell cursor, or "" if the view
// is empty.
func (t *table) CursorCell() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	idx, ok := t.cursorSourceIndexLocked()
	if !ok {
		return ""
	}
	row := t.rendered[idx]
	if t.cellCol < 0 || t.cellCol >= len(row) {
		return ""
	}
	return row[t.cellCol]
}

// CursorRow returns a copy of the row under the cell cursor, or nil.
func (t *table) CursorRow() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	idx, ok := t.cursorSourceIndexLocked()
	if !ok {
		return nil
	}
	src := t.rendered[idx]
	out := make([]string, len(src))
	copy(out, src)
	return out
}

// CursorColumn returns the column descriptor at the cell cursor's column,
// or an empty Column when the view is empty.
func (t *table) CursorColumn() db.Column {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cellCol < 0 || t.cellCol >= len(t.cols) {
		return db.Column{}
	}
	return t.cols[t.cellCol]
}

func (t *table) cursorSourceIndexLocked() (int, bool) {
	n := t.viewLenLocked()
	if n == 0 {
		return 0, false
	}
	if t.cellRow < 0 || t.cellRow >= n {
		return 0, false
	}
	if t.view != nil {
		return t.view[t.cellRow], true
	}
	return t.cellRow, true
}

// --- filter / sort ---------------------------------------------------------

// SetFilter replaces the current filter substring (case-insensitive) and
// rebuilds the view. An empty string clears the filter. The cursor stays
// at row 0 so the user lands on the first match after a filter edit.
func (t *table) SetFilter(s string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.filter = s
	t.rebuildViewLocked()
	t.cellRow = 0
	t.scrollRow = 0
	t.clampCursorLocked()
}

// Filter returns the current filter substring (empty when inactive).
func (t *table) Filter() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.filter
}

// CycleSortAtCursor rotates sort state on the column under the cell
// cursor: asc -> desc -> none. Returns the new direction for UI feedback.
func (t *table) CycleSortAtCursor() (col int, desc bool, active bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	target := t.cellCol
	if target < 0 || target >= len(t.cols) {
		return -1, false, false
	}
	switch {
	case t.sortCol != target:
		t.sortCol = target
		t.sortDesc = false
	case !t.sortDesc:
		t.sortDesc = true
	default:
		t.sortCol = -1
		t.sortDesc = false
	}
	t.rebuildViewLocked()
	t.clampCursorLocked()
	return t.sortCol, t.sortDesc, t.sortCol >= 0
}

// SortState reports the current sort column and direction. col == -1
// means no sort is active.
func (t *table) SortState() (col int, desc bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sortCol, t.sortDesc
}

// rebuildViewLocked recomputes the view index from rendered using the
// current filter and sort. Caller must hold t.mu.
func (t *table) rebuildViewLocked() {
	if t.filter == "" && t.sortCol < 0 {
		t.view = nil
		return
	}
	needle := strings.ToLower(t.filter)
	view := make([]int, 0, len(t.rendered))
	for i, row := range t.rendered {
		if needle != "" && !rowMatchesFilter(row, needle) {
			continue
		}
		view = append(view, i)
	}
	if t.sortCol >= 0 {
		col := t.sortCol
		desc := t.sortDesc
		sort.SliceStable(view, func(a, b int) bool {
			av := cellAt(t.rendered, view[a], col)
			bv := cellAt(t.rendered, view[b], col)
			if desc {
				return av > bv
			}
			return av < bv
		})
	}
	t.view = view
}

func cellAt(rendered [][]string, row, col int) string {
	if row < 0 || row >= len(rendered) {
		return ""
	}
	r := rendered[row]
	if col < 0 || col >= len(r) {
		return ""
	}
	return r[col]
}

func rowMatchesFilter(row []string, needle string) bool {
	for _, cell := range row {
		if strings.Contains(strings.ToLower(cell), needle) {
			return true
		}
	}
	return false
}

// --- draw ------------------------------------------------------------------

// ensureCellVisibleLocked slides scrollRow/scrollCol so the cell cursor
// sits inside a viewport of the given inner size. Caller must hold t.mu.
func (t *table) ensureCellVisibleLocked(innerW, bodyH int) {
	if bodyH > 0 {
		if t.cellRow < t.scrollRow {
			t.scrollRow = t.cellRow
		}
		if t.cellRow >= t.scrollRow+bodyH {
			t.scrollRow = t.cellRow - bodyH + 1
		}
		if t.scrollRow < 0 {
			t.scrollRow = 0
		}
	}
	if innerW > 0 && len(t.cols) > 0 {
		left, right := t.cellSpanLocked(t.cellCol)
		if left < t.scrollCol {
			t.scrollCol = left
		}
		if right > t.scrollCol+innerW-1 {
			t.scrollCol = right - innerW + 1
		}
		if t.scrollCol < 0 {
			t.scrollCol = 0
		}
	}
}

// cellSpanLocked returns the [left, right] rune offsets occupied by the
// given column in a rendered line. Used to auto-scroll so the cursor's
// cell stays visible when navigating horizontally. Caller must hold t.mu.
func (t *table) cellSpanLocked(col int) (int, int) {
	if col < 0 || col >= len(t.widths) {
		return 0, 0
	}
	left := 0
	for i := 0; i < col; i++ {
		left += t.widths[i] + 3 // " | "
	}
	right := left + t.widths[col] - 1
	if right < left {
		right = left
	}
	return left, right
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

	bodyH := innerH - 2
	if bodyH < 0 {
		bodyH = 0
	}

	t.clampCursorLocked()
	t.ensureCellVisibleLocked(innerW, bodyH)

	// Header row, with sort marker on the sorted column.
	header := renderHeaderRow(t.cols, t.widths, t.sortCol, t.sortDesc)
	s.setFg(colorTitleFocused)
	s.writeAt(innerRow, innerCol, sliceRunes(header, t.scrollCol, innerW))
	s.resetStyle()

	// Separator.
	sep := renderSeparator(t.widths)
	s.writeAt(innerRow+1, innerCol, sliceRunes(sep, t.scrollCol, innerW))

	if bodyH == 0 {
		return
	}

	view := t.viewIndicesLocked()
	if len(view) == 0 {
		msg := "(0 rows)"
		if t.filter != "" {
			msg = "(no matches for /" + t.filter + "/)"
		}
		s.writeAt(innerRow+2, innerCol, truncate(msg, innerW))
		return
	}

	// Clamp scrollRow one more time now that the view is known.
	if t.scrollRow >= len(view) {
		t.scrollRow = len(view) - 1
		if t.scrollRow < 0 {
			t.scrollRow = 0
		}
	}

	if !t.wrap {
		for i := 0; i < bodyH; i++ {
			rowIdx := t.scrollRow + i
			if rowIdx >= len(view) {
				break
			}
			src := t.rendered[view[rowIdx]]
			highlightCol := -1
			if rowIdx == t.cellRow {
				highlightCol = t.cellCol
			}
			drawDataRow(s, innerRow+2+i, innerCol, innerW, src, t.widths, t.scrollCol, highlightCol)
		}
		return
	}

	// Wrap mode: each data row may occupy multiple terminal rows.
	y := 0
	for rowIdx := t.scrollRow; rowIdx < len(view) && y < bodyH; rowIdx++ {
		src := t.rendered[view[rowIdx]]
		highlightCol := -1
		if rowIdx == t.cellRow {
			highlightCol = t.cellCol
		}
		block := wrapRow(src, t.widths)
		for _, line := range block {
			if y >= bodyH {
				break
			}
			// Wrap mode doesn't currently paint escape chars because
			// wrap already chops the cell; add styling in a later pass
			// once the wrap path is also convert to run-based draw.
			s.writeAt(innerRow+2+y, innerCol, sliceRunes(line, t.scrollCol, innerW))
			y++
		}
		_ = highlightCol
	}
}

// drawDataRow writes a single non-wrapped data row, painting each cell
// with the normal style (escape chars dimmed) and applying a reverse-
// video highlight to the cursor cell. innerW bounds the total runes
// written so the row clips at the panel's right edge.
func drawDataRow(s *cellbuf, row, col, innerW int, cells []string, widths []int, scrollCol, highlightCol int) {
	// Build the full rendered line once so scrollCol math is trivial,
	// then paint rune-by-rune starting at (scrollCol, 0).
	runes := rowRuneRuns(cells, widths)

	// Highlight span in rune offsets [hi0, hi1) within the full row.
	hi0, hi1 := -1, -1
	if highlightCol >= 0 && highlightCol < len(cells) {
		hi0 = 0
		for i := 0; i < highlightCol; i++ {
			hi0 += widths[i] + 3
		}
		hi1 = hi0 + widths[highlightCol]
	}

	normal := defaultStyle()
	dim := Style{FG: ansiBrightBlack, BG: ansiDefaultBG}
	selected := Style{FG: ansiDefault, BG: ansiDefaultBG, Attrs: attrReverse}

	written := 0
	for i := scrollCol; i < len(runes); i++ {
		if written >= innerW {
			break
		}
		run := runes[i]
		st := normal
		if run.escape {
			st = dim
		}
		if i >= hi0 && i < hi1 {
			st = selected
			if run.escape {
				// Selected AND escape: keep reverse + underline so it
				// stays visible on dark themes.
				st.Attrs |= attrUnderline
			}
		}
		s.writeStyled(row, col+written, run.s, st)
		written++
	}
}

// runeRun is a single visible cell to paint: exactly one terminal cell
// wide, either a plain rune or one half of an expanded escape sequence.
type runeRun struct {
	s      string
	escape bool
}

// rowRuneRuns produces the exact sequence of one-cell-wide runs that
// compose a full data row, including column separators and padding.
// Escape chars (\n \r \t) are expanded into two runs each and marked
// so the draw loop can style them independently.
func rowRuneRuns(cells []string, widths []int) []runeRun {
	var out []runeRun
	for i, cell := range cells {
		if i > 0 {
			out = append(out, runeRun{s: " "}, runeRun{s: "|"}, runeRun{s: " "})
		}
		out = appendCellRuns(out, cell, widths[i])
	}
	return out
}

// appendCellRuns writes cell's runes to out, expanding escapes, and then
// pads with spaces so the total cell span equals widthN.
func appendCellRuns(out []runeRun, cell string, widthN int) []runeRun {
	written := 0
	for _, r := range cell {
		if written >= widthN {
			break
		}
		switch r {
		case '\n':
			if written+2 > widthN {
				for written < widthN {
					out = append(out, runeRun{s: " "})
					written++
				}
				return out
			}
			out = append(out, runeRun{s: "\\", escape: true}, runeRun{s: "n", escape: true})
			written += 2
		case '\r':
			if written+2 > widthN {
				for written < widthN {
					out = append(out, runeRun{s: " "})
					written++
				}
				return out
			}
			out = append(out, runeRun{s: "\\", escape: true}, runeRun{s: "r", escape: true})
			written += 2
		case '\t':
			if written+2 > widthN {
				for written < widthN {
					out = append(out, runeRun{s: " "})
					written++
				}
				return out
			}
			out = append(out, runeRun{s: "\\", escape: true}, runeRun{s: "t", escape: true})
			written += 2
		default:
			if r < 0x20 || r == 0x7f {
				// Drop other control chars; they'd wreck the grid.
				continue
			}
			out = append(out, runeRun{s: string(r)})
			written++
		}
	}
	for written < widthN {
		out = append(out, runeRun{s: " "})
		written++
	}
	return out
}

// renderHeaderRow formats the column headers including an ASCII sort
// marker (" ^" / " v") on the sorted column so the user can tell at a
// glance which column ordering is active.
func renderHeaderRow(cols []db.Column, widths []int, sortCol int, desc bool) string {
	labels := make([]string, len(cols))
	for i, c := range cols {
		label := c.Name
		if i == sortCol {
			if desc {
				label += " v"
			} else {
				label += " ^"
			}
			if displayWidth(label) > widths[i] {
				// Measure string may exceed col width after adding the
				// marker; the sliceRunes clip below will still hide any
				// overflow on the right edge.
			}
		}
		labels[i] = label
	}
	return renderRow(labels, widths)
}

// renderRow joins cells with " | " padded to their column widths using
// display widths (so escape chars still measure as two cells). No
// horizontal clipping -- callers slice the result to fit the viewport.
func renderRow(cells []string, widths []int) string {
	var b strings.Builder
	for i, cell := range cells {
		if i > 0 {
			b.WriteString(" | ")
		}
		padCellTo(&b, cell, widths[i])
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

// sliceRunes returns up to width runes of s starting at offset. Out-of-
// range offsets yield an empty string rather than panicking.
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

// padCellTo writes a cell's contents using expanded escapes (so \n -> `\n`)
// and pads to w display cells.
func padCellTo(b *strings.Builder, s string, w int) {
	written := 0
	for _, r := range s {
		if written >= w {
			break
		}
		switch r {
		case '\n':
			if written+2 > w {
				for written < w {
					b.WriteByte(' ')
					written++
				}
				return
			}
			b.WriteString(`\n`)
			written += 2
		case '\r':
			if written+2 > w {
				for written < w {
					b.WriteByte(' ')
					written++
				}
				return
			}
			b.WriteString(`\r`)
			written += 2
		case '\t':
			if written+2 > w {
				for written < w {
					b.WriteByte(' ')
					written++
				}
				return
			}
			b.WriteString(`\t`)
			written += 2
		default:
			if r < 0x20 || r == 0x7f {
				continue
			}
			b.WriteRune(r)
			written++
		}
	}
	for written < w {
		b.WriteByte(' ')
		written++
	}
}

// displayWidth counts the number of terminal cells a string occupies
// when drawn with the escape-expansion rules used by padCellTo.
func displayWidth(s string) int {
	n := 0
	for _, r := range s {
		switch r {
		case '\n', '\r', '\t':
			n += 2
		default:
			if r < 0x20 || r == 0x7f {
				continue
			}
			n++
		}
	}
	return n
}

func runeLen(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}

// stringifyCell converts a driver-returned value into a display string.
// Keeps escape chars intact for the draw path to style; control chars
// below space are stripped.
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

// sanitizeCell preserves \n \r \t so the draw path can render them as
// visible dim escapes. Other control chars (below space, DEL) are
// dropped outright because they'd wreck the grid alignment in ways a
// single-character stand-in can't recover from.
func sanitizeCell(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\n', '\r', '\t':
			b.WriteRune(r)
		default:
			if r < 0x20 || r == 0x7f {
				continue
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}

// wrapRow splits a single logical result row into as many aligned
// terminal lines as its widest cell requires. Each cell is chopped at
// its column width; columns that run out of content early are filled
// with spaces so the " | " separators stay aligned. Kept plain for the
// current wrap mode -- escape-char styling in wrap mode comes later.
func wrapRow(cells []string, widths []int) []string {
	chunks := make([][]string, len(cells))
	maxLines := 1
	for i, cell := range cells {
		w := widths[i]
		if w <= 0 {
			chunks[i] = []string{""}
			continue
		}
		chunks[i] = chopCellDisplay(cell, w)
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
				padDisplayTo(&b, col[line], widths[i])
			} else {
				padDisplayTo(&b, "", widths[i])
			}
		}
		out[line] = b.String()
	}
	return out
}

// chopCellDisplay splits a cell into width-sized slices, counting escape
// chars as two display cells so the wrapped segments don't overflow.
// Empty strings become a single empty slice so wrapRow produces at least
// one output row per cell.
func chopCellDisplay(s string, w int) []string {
	if s == "" {
		return []string{""}
	}
	var out []string
	var b strings.Builder
	written := 0
	flush := func() {
		out = append(out, b.String())
		b.Reset()
		written = 0
	}
	for _, r := range s {
		size := 1
		repr := string(r)
		switch r {
		case '\n':
			repr = `\n`
			size = 2
		case '\r':
			repr = `\r`
			size = 2
		case '\t':
			repr = `\t`
			size = 2
		default:
			if r < 0x20 || r == 0x7f {
				continue
			}
		}
		if written+size > w {
			flush()
		}
		b.WriteString(repr)
		written += size
	}
	if written > 0 {
		flush()
	}
	if len(out) == 0 {
		out = append(out, "")
	}
	return out
}

// padDisplayTo writes s's display form (escapes expanded) into b and
// pads to w display cells.
func padDisplayTo(b *strings.Builder, s string, w int) {
	written := 0
	for _, r := range s {
		if written >= w {
			break
		}
		size := 1
		repr := string(r)
		switch r {
		case '\n':
			repr = `\n`
			size = 2
		case '\r':
			repr = `\r`
			size = 2
		case '\t':
			repr = `\t`
			size = 2
		default:
			if r < 0x20 || r == 0x7f {
				continue
			}
		}
		if written+size > w {
			break
		}
		b.WriteString(repr)
		written += size
	}
	for written < w {
		b.WriteByte(' ')
		written++
	}
}

// padRight writes s then spaces up to width w. Used by legacy callers
// that don't care about escape expansion.
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
