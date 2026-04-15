package tui

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/limits"
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
	bytes     int64
	streaming bool
	capped    bool
	capReason string
	err       error

	// view is an index into rendered. When nil, the natural order (0..N-1)
	// of rendered is used. Filter and sort produce a non-nil view.
	view []int

	// filter is the raw filter string as the user typed it; kept so
	// the filter overlay can re-seed its input field. The compiled
	// matcher lives in filterMatch and is rebuilt whenever filter
	// changes. filterNote carries any parse warning (e.g. "bad
	// regex, using substring") for the filter overlay to show.
	filter      string
	filterMatch func(row []string) bool
	filterNote  string

	sortCol  int // column index; -1 means no sort
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

const sampleRows = 200

// maxBufferedBytes is the result-set byte cap, sourced from the
// shared limits package (env: SQLGO_BYTE_CAP, default 2 GiB).
var maxBufferedBytes = limits.ByteCap()

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
	t.filterMatch = nil
	t.filterNote = ""
	t.sortCol = -1
	t.sortDesc = false
	t.cellRow = 0
	t.cellCol = 0
	t.streaming = false
	t.capped = false
	t.capReason = ""
	t.bytes = 0
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
	t.filterMatch = nil
	t.filterNote = ""
	t.sortCol = -1
	t.sortDesc = false
	t.cellRow = 0
	t.cellCol = 0
	t.streaming = true
	t.capped = false
	t.capReason = ""
	t.bytes = 0
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
	var rowBytes int64
	for i, v := range row {
		cells[i] = sanitizeCell(rawStringify(v))
		rowBytes += int64(len(cells[i]))
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.bytes+rowBytes > maxBufferedBytes {
		t.capped = true
		t.capReason = formatByteSize(maxBufferedBytes)
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
	t.bytes += rowBytes
	return true
}

// formatByteSize renders n as a compact human-readable size for
// status messages ("256MB", "1GB"). Binary (power-of-1024) units.
func formatByteSize(n int64) string {
	const (
		kib = 1 << 10
		mib = 1 << 20
		gib = 1 << 30
	)
	switch {
	case n >= gib:
		return fmt.Sprintf("%dGB", n/gib)
	case n >= mib:
		return fmt.Sprintf("%dMB", n/mib)
	case n >= kib:
		return fmt.Sprintf("%dKB", n/kib)
	}
	return fmt.Sprintf("%dB", n)
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

// ColCount returns the number of columns in the current result set, or 0
// before Init / after Clear.
func (t *table) ColCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.cols)
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

// CapReason returns a short label describing which cap tripped
// ("100000 rows", "256MB"). Empty when Capped() is false.
func (t *table) CapReason() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.capReason
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

// MoveCellPage shifts the row cursor by approximately one page
// (tablePageStep rows) without touching the column cursor.
func (t *table) MoveCellPage(dir int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cellRow += dir * tablePageStep
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

// SetFilter replaces the current filter expression and rebuilds the
// view. Three flavors are recognized:
//
//   - empty string: clears the filter.
//   - "/pattern/"  : pattern is compiled as a Go regexp (case
//     insensitive). A bad regex falls back to substring match and
//     the filterNote carries a warning the overlay can surface.
//   - "col:text"   : match only the column whose header (case
//     insensitive) equals "col". Unknown column falls back to a
//     row-wide substring match with a note.
//   - anything else: case-insensitive substring match across every
//     cell in a row, the original v1 behavior.
//
// The cursor stays at row 0 so the user lands on the first match
// after a filter edit.
func (t *table) SetFilter(s string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.filter = s
	t.filterMatch, t.filterNote = compileFilter(s, t.cols)
	t.rebuildViewLocked()
	t.cellRow = 0
	t.scrollRow = 0
	t.clampCursorLocked()
}

// Filter returns the current filter string (empty when inactive).
func (t *table) Filter() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.filter
}

// FilterNote returns any parse warning from the last SetFilter call.
// "" means the filter parsed cleanly. The overlay reads this to show
// a hint like "bad regex, using substring".
func (t *table) FilterNote() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.filterNote
}

// compileFilter parses a user-supplied filter string into a matcher
// closure over the table's current columns. The returned note is a
// human-readable warning for malformed input; empty means the filter
// parsed exactly as typed. A nil matcher means "no filter".
func compileFilter(s string, cols []db.Column) (func(row []string) bool, string) {
	if s == "" {
		return nil, ""
	}
	// Regex form: "/.../".
	if len(s) >= 2 && s[0] == '/' && s[len(s)-1] == '/' {
		pattern := s[1 : len(s)-1]
		if pattern == "" {
			return nil, "empty regex"
		}
		re, err := regexp.Compile("(?i)" + pattern)
		if err != nil {
			// Fall back to substring match on the literal text, with
			// a note so the user knows their regex was ignored.
			needle := strings.ToLower(pattern)
			return func(row []string) bool {
				for _, cell := range row {
					if strings.Contains(strings.ToLower(cell), needle) {
						return true
					}
				}
				return false
			}, "bad regex: " + err.Error()
		}
		return func(row []string) bool {
			for _, cell := range row {
				if re.MatchString(cell) {
					return true
				}
			}
			return false
		}, ""
	}
	// Column scope: "col:text". We look for the first ':' and try to
	// resolve its left side as a column header. If the column is
	// unknown we fall back to the substring path with a note.
	if idx := strings.Index(s, ":"); idx > 0 {
		colName := strings.TrimSpace(s[:idx])
		needle := strings.ToLower(s[idx+1:])
		colIdx := findColumnIndex(cols, colName)
		if colIdx >= 0 {
			return func(row []string) bool {
				if colIdx >= len(row) {
					return false
				}
				return strings.Contains(strings.ToLower(row[colIdx]), needle)
			}, ""
		}
		// Fall through to substring with a warning.
		return substringMatcher(s), "unknown column: " + colName
	}
	return substringMatcher(s), ""
}

// substringMatcher is the default filter behavior: case-insensitive
// substring against every cell in the row. Factored out so the
// fallback paths in compileFilter can reuse it without duplicating
// the loop.
func substringMatcher(s string) func(row []string) bool {
	needle := strings.ToLower(s)
	return func(row []string) bool {
		for _, cell := range row {
			if strings.Contains(strings.ToLower(cell), needle) {
				return true
			}
		}
		return false
	}
}

// findColumnIndex returns the index of the first column whose header
// matches name case-insensitively, or -1 if no column matches.
func findColumnIndex(cols []db.Column, name string) int {
	target := strings.ToLower(name)
	for i, c := range cols {
		if strings.ToLower(c.Name) == target {
			return i
		}
	}
	return -1
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
	if t.filterMatch == nil && t.sortCol < 0 {
		t.view = nil
		return
	}
	view := make([]int, 0, len(t.rendered))
	for i, row := range t.rendered {
		if t.filterMatch != nil && !t.filterMatch(row) {
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

// --- draw ------------------------------------------------------------------

// ensureCellVisibleLocked slides scrollRow/scrollCol so the cell cursor
// sits inside a viewport of the given inner size. Caller must hold t.mu.
//
// Horizontal scroll priority: when the cursor cell is wider than the
// viewport, we snap to its LEFT edge so the beginning of a long value
// is visible. The previous behavior was to snap the right edge into
// view (via the standard "scroll until right < viewport right" rule),
// which meant long cells always showed their tail, not their head --
// surprising when tab-navigating across wide columns.
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
		cellWidth := right - left + 1
		if cellWidth >= innerW {
			// Cell is at least as wide as the viewport; aligning
			// its left edge is the most useful anchor.
			t.scrollCol = left
		} else {
			// Cell fits: scroll the minimal amount to keep it
			// visible, preferring the left edge when the cursor
			// moved left and the right edge only when it had to.
			if left < t.scrollCol {
				t.scrollCol = left
			} else if right > t.scrollCol+innerW-1 {
				t.scrollCol = right - innerW + 1
			}
		}
		if t.scrollCol < 0 {
			t.scrollCol = 0
		}
	}
}

// CellAt hit-tests a mouse coordinate against the rendered table and
// moves the cell cursor to the clicked cell. Returns true if the click
// landed on a data row/column. Wrap mode is not supported because rows
// have variable heights there; callers should no-op in that case.
func (t *table) CellAt(r rect, screenRow, screenCol int) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.wrap || len(t.cols) == 0 {
		return false
	}
	innerRow := r.row + 1
	innerCol := r.col + 1
	innerW := r.w - 2
	innerH := r.h - 2
	if innerW <= 0 || innerH <= 0 {
		return false
	}
	bodyTop := innerRow + 2
	bodyH := innerH - 2
	if bodyH <= 0 {
		return false
	}
	dr := screenRow - bodyTop
	if dr < 0 || dr >= bodyH {
		return false
	}
	rowIdx := t.scrollRow + dr
	if rowIdx >= t.viewLenLocked() {
		return false
	}
	target := screenCol - innerCol + t.scrollCol
	if target < 0 {
		return false
	}
	col := -1
	for i := 0; i < len(t.widths); i++ {
		left, right := t.cellSpanLocked(i)
		if target >= left && target <= right {
			col = i
			break
		}
	}
	if col < 0 {
		return false
	}
	t.cellRow = rowIdx
	t.cellCol = col
	t.clampCursorLocked()
	return true
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

	// Wrap mode: each data row may occupy multiple terminal visual
	// rows. We produce a styled runs grid for the row (one inner slice
	// per wrapped line), then paint each line via drawRunsWithHighlight
	// so escape-char dim styling and cursor highlight work the same
	// way they do in non-wrap mode. The highlight span is computed
	// once per logical row and applied to every wrapped line of that
	// row so the cursor cell stays visible even when its value spans
	// multiple terminal lines.
	y := 0
	for rowIdx := t.scrollRow; rowIdx < len(view) && y < bodyH; rowIdx++ {
		src := t.rendered[view[rowIdx]]
		highlightCol := -1
		if rowIdx == t.cellRow {
			highlightCol = t.cellCol
		}
		hi0, hi1 := highlightSpan(t.widths, highlightCol)
		block := wrapRowRuns(src, t.widths)
		for _, line := range block {
			if y >= bodyH {
				break
			}
			drawRunsWithHighlight(s, innerRow+2+y, innerCol, innerW, line, t.scrollCol, hi0, hi1)
			y++
		}
	}
}

// drawDataRow writes a single non-wrapped data row, painting each
// cell with the normal style (escape chars dimmed) and applying a
// reverse-video highlight to the cursor cell. innerW bounds the total
// runes written so the row clips at the panel's right edge.
func drawDataRow(s *cellbuf, row, col, innerW int, cells []string, widths []int, scrollCol, highlightCol int) {
	runes := rowRuneRuns(cells, widths)
	hi0, hi1 := highlightSpan(widths, highlightCol)
	drawRunsWithHighlight(s, row, col, innerW, runes, scrollCol, hi0, hi1)
}

// drawRunsWithHighlight paints one horizontal line of runeRun values
// starting at (row, col), clipped to innerW. Runs flagged as escape
// (expanded \n/\r/\t) are drawn in the dim style; runs whose full-row
// offset falls inside [hi0, hi1) are drawn in the reverse-video
// cursor style. Used by both non-wrap and wrap modes so styling stays
// consistent across both.
//
// Wide-rune handling: each runeRun slot is 1 visual column, so the
// `written` counter advances by 1 per iteration regardless of the
// slot's contents. A continuation slot (cont=true) is the right half
// of a preceding wide glyph; we don't call writeStyled for it
// because the head already marked col+1 as wideCont. Skipping the
// continuation is correct AND necessary -- if we called writeStyled
// with s="" it would no-op, but any non-empty replacement write
// there would silently stomp the wide head's continuation slot.
//
// A wide head's right half that would spill past innerW gets
// replaced with a space so the column boundary stays clean.
func drawRunsWithHighlight(s *cellbuf, row, col, innerW int, runes []runeRun, scrollCol, hi0, hi1 int) {
	normal := defaultStyle()
	dim := Style{FG: ansiBrightBlack, BG: ansiDefaultBG}
	selected := Style{FG: ansiDefault, BG: ansiDefaultBG, Attrs: attrReverse}

	written := 0
	for i := scrollCol; i < len(runes); i++ {
		if written >= innerW {
			break
		}
		run := runes[i]
		// Continuation slots are painted by the head. Just advance.
		if run.cont {
			written++
			continue
		}
		// A wide head whose continuation would fall outside the
		// viewport must be replaced with a space, otherwise the
		// terminal would draw half a glyph and leak the right side
		// past the panel edge.
		if run.wide && written+2 > innerW {
			st := normal
			if i >= hi0 && i < hi1 {
				st = selected
			}
			s.writeStyled(row, col+written, " ", st)
			written++
			continue
		}
		st := normal
		if run.escape {
			st = dim
		}
		if i >= hi0 && i < hi1 {
			st = selected
			if run.escape {
				// Selected AND escape: keep reverse + underline so
				// it stays visible on dark themes.
				st.Attrs |= attrUnderline
			}
		}
		s.writeStyled(row, col+written, run.s, st)
		written++
	}
}

// highlightSpan returns the [lo, hi) rune-offset range a given column
// occupies in a full rendered row. col == -1 (no highlight) returns
// (-1, -1) so the main draw loop can skip the range check entirely.
func highlightSpan(widths []int, col int) (int, int) {
	if col < 0 || col >= len(widths) {
		return -1, -1
	}
	lo := 0
	for i := 0; i < col; i++ {
		lo += widths[i] + 3 // " | "
	}
	return lo, lo + widths[col]
}

// runeRun is a single visible slot in a rendered row: exactly one
// terminal cell wide. The invariant "one entry = one visual column"
// is what lets the draw loop use plain index arithmetic for
// scrollCol and cursor-highlight spans.
//
// For ordinary and escape-expanded runes, runeRun{s: "a"} or
// runeRun{s: "n", escape: true}. For a wide rune ('你', '🎉', etc)
// we emit two consecutive slots: a head (s = "你", wide = true) and
// a continuation (s = "", cont = true). Both slots occupy their own
// visual column but only the head carries the rune; the continuation
// is skipped in writes because the head's wide glyph covers it.
type runeRun struct {
	s      string
	escape bool
	wide   bool // head of a 2-cell wide glyph
	cont   bool // continuation slot (right half of a wide glyph)
}

// rowRuneRuns produces the exact sequence of one-cell-wide runs that
// compose a full data row, including column separators and padding.
// Escape chars (\n \r \t) are expanded into two runs each and marked
// so the draw loop can style them independently.
func rowRuneRuns(cells []string, widths []int) []runeRun {
	var out []runeRun
	for i, cell := range cells {
		if i > 0 {
			out = append(out, runeRun{s: " "}, runeRun{s: "│"}, runeRun{s: " "})
		}
		out = appendCellRuns(out, cell, widths[i])
	}
	return out
}

// wrapRowRuns is the wrap-mode parallel of rowRuneRuns. Each cell is
// chopped into as many visual lines as its content requires (at its
// column width, counting expanded escapes); lines are then zipped
// side-by-side with the separator runs so every visual line covers the
// full row width. Short cells are padded with blank runs so column
// alignment is preserved across all wrapped lines, which lets
// highlightSpan math from the non-wrap path apply unchanged.
func wrapRowRuns(cells []string, widths []int) [][]runeRun {
	if len(cells) == 0 {
		return nil
	}
	chopped := make([][][]runeRun, len(cells))
	maxLines := 1
	for i, cell := range cells {
		chopped[i] = chopCellRuns(cell, widths[i])
		if len(chopped[i]) > maxLines {
			maxLines = len(chopped[i])
		}
	}
	out := make([][]runeRun, maxLines)
	for line := 0; line < maxLines; line++ {
		var row []runeRun
		for i, col := range chopped {
			if i > 0 {
				row = append(row, runeRun{s: " "}, runeRun{s: "│"}, runeRun{s: " "})
			}
			if line < len(col) {
				row = append(row, col[line]...)
			} else {
				// Pad the empty slot to the column's full width so
				// separators line up with the non-wrap layout.
				for j := 0; j < widths[i]; j++ {
					row = append(row, runeRun{s: " "})
				}
			}
		}
		out[line] = row
	}
	return out
}

// chopCellRuns splits a single cell into runeRun slices, each exactly
// `width` visual cells wide. Handles the same escape expansion and
// wide-rune head/continuation emission as appendCellRuns and pads the
// trailing slice with blanks so every returned slice has identical
// length. An empty string yields a single all-blank slice so callers
// always see at least one visual line.
func chopCellRuns(cell string, width int) [][]runeRun {
	if width <= 0 {
		return [][]runeRun{{}}
	}
	var out [][]runeRun
	cur := make([]runeRun, 0, width)
	written := 0
	flush := func() {
		for written < width {
			cur = append(cur, runeRun{s: " "})
			written++
		}
		out = append(out, cur)
		cur = make([]runeRun, 0, width)
		written = 0
	}
	emitEscape := func(ch string) {
		if written+2 > width {
			flush()
		}
		cur = append(cur,
			runeRun{s: "\\", escape: true},
			runeRun{s: ch, escape: true},
		)
		written += 2
	}
	for _, r := range cell {
		switch r {
		case '\n':
			emitEscape("n")
		case '\r':
			emitEscape("r")
		case '\t':
			emitEscape("t")
		default:
			if r < 0x20 || r == 0x7f {
				continue
			}
			w := runeDisplayWidth(r)
			if w == 0 {
				continue
			}
			if written+w > width {
				flush()
			}
			if w == 2 {
				cur = append(cur,
					runeRun{s: string(r), wide: true},
					runeRun{cont: true},
				)
				written += 2
			} else {
				cur = append(cur, runeRun{s: string(r)})
				written++
			}
		}
	}
	if written > 0 || len(out) == 0 {
		flush()
	}
	return out
}

// appendCellRuns writes cell's runes to out, expanding escapes,
// emitting wide runes as head+continuation slot pairs, and padding
// with spaces so the total cell span equals widthN visual columns.
//
// Invariant: the number of slots appended for this cell is exactly
// widthN (or fewer if widthN was zero). One slot == one visual
// terminal column.
func appendCellRuns(out []runeRun, cell string, widthN int) []runeRun {
	written := 0
	// padSpaces fills the remainder of the cell with blank slots.
	padSpaces := func() []runeRun {
		for written < widthN {
			out = append(out, runeRun{s: " "})
			written++
		}
		return out
	}
	for _, r := range cell {
		if written >= widthN {
			break
		}
		switch r {
		case '\n', '\r', '\t':
			if written+2 > widthN {
				return padSpaces()
			}
			ch := "n"
			switch r {
			case '\r':
				ch = "r"
			case '\t':
				ch = "t"
			}
			out = append(out,
				runeRun{s: "\\", escape: true},
				runeRun{s: ch, escape: true},
			)
			written += 2
		default:
			if r < 0x20 || r == 0x7f {
				// Drop control chars; they'd wreck the grid.
				continue
			}
			w := runeDisplayWidth(r)
			if w == 0 {
				// Combining marks / zero-width joiners: dropped in
				// v1 so the grid stays in sync with the terminal.
				continue
			}
			if written+w > widthN {
				// Not enough room for a wide rune's second half.
				// Pad with a space so the column still aligns.
				return padSpaces()
			}
			if w == 2 {
				out = append(out,
					runeRun{s: string(r), wide: true},
					runeRun{cont: true},
				)
				written += 2
			} else {
				out = append(out, runeRun{s: string(r)})
				written++
			}
		}
	}
	return padSpaces()
}

// renderHeaderRow formats the column headers including a sort
// direction marker (" ▲" / " ▼") on the sorted column so the user can
// tell at a glance which column ordering is active.
func renderHeaderRow(cols []db.Column, widths []int, sortCol int, desc bool) string {
	labels := make([]string, len(cols))
	for i, c := range cols {
		label := c.Name
		if i == sortCol {
			if desc {
				label += " ▼"
			} else {
				label += " ▲"
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
			b.WriteString(" │ ")
		}
		padCellTo(&b, cell, widths[i])
	}
	return b.String()
}

func renderSeparator(widths []int) string {
	var b strings.Builder
	for i, w := range widths {
		if i > 0 {
			b.WriteString("─┼─")
		}
		b.WriteString(strings.Repeat("─", w))
	}
	return b.String()
}

// sliceRunes returns the slice of s occupying visual columns
// [offset, offset+width). Wide runes are treated atomically: if a
// wide glyph straddles either edge it's replaced with a space so the
// visible content stays aligned. Out-of-range offsets yield an empty
// string rather than panicking.
func sliceRunes(s string, offset, width int) string {
	if width <= 0 || offset < 0 {
		return ""
	}
	var b strings.Builder
	col := 0
	written := 0
	for _, r := range s {
		rw := runeDisplayWidth(r)
		if rw == 0 {
			continue
		}
		start := col
		end := col + rw
		col = end
		// Entirely before the window.
		if end <= offset {
			continue
		}
		// Entirely after the window.
		if start >= offset+width {
			break
		}
		// Partially clipped on the left (wide rune straddles the
		// offset): replace with spaces for the visible half.
		if start < offset {
			for i := start; i < offset; i++ {
				// nothing; left half not included
			}
			for i := offset; i < end && written < width; i++ {
				b.WriteByte(' ')
				written++
			}
			continue
		}
		// Partially clipped on the right.
		if end > offset+width {
			for i := start; i < offset+width; i++ {
				b.WriteByte(' ')
				written++
			}
			break
		}
		// Fully inside the window.
		b.WriteRune(r)
		written += rw
	}
	return b.String()
}

// padCellTo writes a cell's contents using expanded escapes (so \n ->
// `\n`) and pads to w visual cells. Wide runes count as 2 columns.
func padCellTo(b *strings.Builder, s string, w int) {
	written := 0
	padTail := func() {
		for written < w {
			b.WriteByte(' ')
			written++
		}
	}
	for _, r := range s {
		if written >= w {
			break
		}
		switch r {
		case '\n', '\r', '\t':
			if written+2 > w {
				padTail()
				return
			}
			switch r {
			case '\n':
				b.WriteString(`\n`)
			case '\r':
				b.WriteString(`\r`)
			case '\t':
				b.WriteString(`\t`)
			}
			written += 2
		default:
			if r < 0x20 || r == 0x7f {
				continue
			}
			rw := runeDisplayWidth(r)
			if rw == 0 {
				// Combining mark / ZWJ: drop for grid alignment.
				continue
			}
			if written+rw > w {
				padTail()
				return
			}
			b.WriteRune(r)
			written += rw
		}
	}
	padTail()
}

// displayWidth counts the number of terminal cells a string occupies
// when drawn with the escape-expansion rules used by padCellTo.
// Wide runes (CJK, fullwidth, emoji) count as 2 via runewidth.
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
			n += runeDisplayWidth(r)
		}
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

// truncate clips s to at most w visual columns, appending "..." when
// the input exceeds the width. Counts wide runes as 2 columns via
// runewidth so CJK / emoji content doesn't visually overflow when it
// measures under the raw rune count limit.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if stringDisplayWidth(s) <= w {
		return s
	}
	if w <= 1 {
		// Too narrow for an ellipsis; just clip by visual width.
		return clipToWidth(s, w)
	}
	return clipToWidth(s, w-1) + "…"
}

// clipToWidth returns the longest prefix of s whose visual width is
// at most w. Wide runes are treated atomically -- if the next rune
// would push past w, it's omitted entirely rather than leaving a
// half-glyph.
func clipToWidth(s string, w int) string {
	if w <= 0 {
		return ""
	}
	written := 0
	var b strings.Builder
	for _, r := range s {
		rw := runeDisplayWidth(r)
		if rw == 0 {
			continue
		}
		if written+rw > w {
			break
		}
		b.WriteRune(r)
		written += rw
	}
	return b.String()
}
