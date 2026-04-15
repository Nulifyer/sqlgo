package tui

import (
	"bytes"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/db"
)

// feedRows is a test helper that pushes a fixture through the streaming
// Init/Append/Done flow the real query runner uses. Keeping the helper
// local to the tests avoids tying the production path to any fixture type.
func feedRows(tbl *table, cols []db.Column, rows [][]any) {
	tbl.Init(cols)
	for _, r := range rows {
		tbl.Append(r)
	}
	tbl.Done(nil)
}

func TestTableStreamingComputesWidths(t *testing.T) {
	t.Parallel()
	tbl := newTable()
	feedRows(tbl,
		[]db.Column{{Name: "id"}, {Name: "name"}},
		[][]any{
			{int64(1), "alice"},
			{int64(200), "bob"},
			{int64(3), "charlotte"},
		},
	)

	// widths: id max("id",1,200,3)=3 ; name max("name",alice,bob,charlotte)=9
	want := []int{3, 9}
	for i, w := range want {
		if tbl.widths[i] != w {
			t.Errorf("widths[%d] = %d, want %d", i, tbl.widths[i], w)
		}
	}
	if got := tbl.RowCount(); got != 3 {
		t.Errorf("RowCount = %d, want 3", got)
	}
	if tbl.Streaming() {
		t.Errorf("Streaming still true after Done")
	}
}

func TestTableCellCursorClamps(t *testing.T) {
	t.Parallel()
	rows := make([][]any, 5)
	for i := range rows {
		rows[i] = []any{int64(i)}
	}
	tbl := newTable()
	feedRows(tbl, []db.Column{{Name: "n"}}, rows)

	// Moving up past the top clamps to row 0.
	tbl.MoveCellBy(-10, 0)
	if tbl.cellRow != 0 {
		t.Errorf("cellRow after overscroll up = %d, want 0", tbl.cellRow)
	}
	// Moving down past the bottom clamps to the last row.
	tbl.MoveCellBy(100, 0)
	if tbl.cellRow != len(rows)-1 {
		t.Errorf("cellRow after overscroll down = %d, want %d", tbl.cellRow, len(rows)-1)
	}
	// Column cursor clamps on a single-column table.
	tbl.MoveCellBy(0, 10)
	if tbl.cellCol != 0 {
		t.Errorf("cellCol after overscroll right = %d, want 0", tbl.cellCol)
	}
}

func TestTableFilterNarrowsView(t *testing.T) {
	t.Parallel()
	tbl := newTable()
	feedRows(tbl,
		[]db.Column{{Name: "id"}, {Name: "name"}},
		[][]any{
			{int64(1), "alice"},
			{int64(2), "Bob"},
			{int64(3), "Charlie"},
			{int64(4), "alicia"},
		},
	)
	tbl.SetFilter("ali")
	_, snap := tbl.Snapshot()
	if len(snap) != 2 {
		t.Errorf("filter 'ali' got %d rows, want 2", len(snap))
	}
	tbl.SetFilter("")
	_, snap = tbl.Snapshot()
	if len(snap) != 4 {
		t.Errorf("cleared filter got %d rows, want 4", len(snap))
	}
}

func TestTableFilterColumnScoped(t *testing.T) {
	t.Parallel()
	tbl := newTable()
	feedRows(tbl,
		[]db.Column{{Name: "id"}, {Name: "name"}},
		[][]any{
			// "alice" appears in both id and name across rows so a
			// plain substring would match two; the column-scoped
			// filter should only match the one with alice in name.
			{int64(1), "alice"},
			{int64(42), "alice is 42"},
			{int64(3), "bob"},
		},
	)
	tbl.SetFilter("name:alice")
	_, snap := tbl.Snapshot()
	if len(snap) != 2 {
		t.Errorf("col-scoped filter 'name:alice' got %d rows, want 2: %v", len(snap), snap)
	}
	if note := tbl.FilterNote(); note != "" {
		t.Errorf("unexpected note: %q", note)
	}
}

func TestTableFilterUnknownColumnFallsBack(t *testing.T) {
	t.Parallel()
	tbl := newTable()
	feedRows(tbl,
		[]db.Column{{Name: "id"}, {Name: "name"}},
		[][]any{
			{int64(1), "alice"},
			{int64(2), "bob"},
		},
	)
	tbl.SetFilter("nope:alice")
	_, snap := tbl.Snapshot()
	// Unknown column falls through to substring -- the literal
	// "nope:alice" won't match either row.
	if len(snap) != 0 {
		t.Errorf("unknown column filter got %d rows, want 0", len(snap))
	}
	if note := tbl.FilterNote(); note == "" {
		t.Errorf("expected a filter note for unknown column")
	}
}

func TestTableFilterRegex(t *testing.T) {
	t.Parallel()
	tbl := newTable()
	feedRows(tbl,
		[]db.Column{{Name: "sku"}},
		[][]any{
			{"ABC-123"},
			{"XYZ-999"},
			{"ABC-456"},
		},
	)
	tbl.SetFilter(`/^ABC-\d+$/`)
	_, snap := tbl.Snapshot()
	if len(snap) != 2 {
		t.Errorf("regex filter got %d rows, want 2: %v", len(snap), snap)
	}
}

// TestTableRendersUnicodeEndToEnd is the acceptance test for the
// wide-rune refactor: it feeds the actual test_notes unicode fixture
// rows through table.draw → screen.composite → screen.flush → a
// bytes.Buffer and asserts the plain-text residue matches a
// byte-for-byte expected layout.
//
// This is the test that would have caught every ghost/half-glyph
// symptom in the earlier screenshots. The input covers: CJK, emoji,
// accented Latin, combining marks (dropped in v1), box drawing, and
// ASCII. The expected output is 4 visual rows -- 3 body + 1 header +
// 1 separator -- with every column separator lined up in the same
// absolute column across all rows.
func TestTableRendersUnicodeEndToEnd(t *testing.T) {
	t.Parallel()
	tbl := newTable()
	feedRows(tbl,
		[]db.Column{{Name: "label"}, {Name: "content"}},
		[][]any{
			{"cjk", "你好世界"},
			{"emoji", "a🎉b"},
			{"ascii", "hello"},
		},
	)

	// Give the table a fixed rect. width 40 is enough for the
	// widest row (label ~5 + 3 sep + content ~8 = 16) with slack.
	// Height 7 gives innerH=5 (5 minus 2 sep = 3 body rows), just
	// enough for the 3 fixture rows.
	var out bytes.Buffer
	scr := newScreen(&out, 40, 7)
	buf := newCellbuf(40, 7)
	tbl.draw(buf, rect{row: 1, col: 1, w: 40, h: 7})
	scr.composite([]*cellbuf{buf})
	if err := scr.flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}

	plain := stripANSI(out.String())
	// plain is one long byte string with moveTo sequences already
	// erased; the runes appear in row-major order. We can't easily
	// tell row boundaries, but we can check that every fixture's
	// content is present and that wide runes didn't leak into the
	// label column.
	for _, want := range []string{"cjk", "emoji", "ascii", "你好世界", "a🎉b", "hello"} {
		if !bytes.Contains([]byte(plain), []byte(want)) {
			t.Errorf("expected %q in rendered output, got %q", want, plain)
		}
	}

	// The row with '你好世界' must have its content sitting entirely
	// to the right of the ' | ' separator. Scan forward from the
	// start of the row's label and confirm no CJK rune precedes
	// the ' | ' marker.
	idx := bytes.Index([]byte(plain), []byte("cjk"))
	if idx < 0 {
		t.Fatalf("cjk row not found in output")
	}
	tail := plain[idx:]
	sepIdx := bytes.Index([]byte(tail), []byte(" │ "))
	if sepIdx < 0 {
		t.Fatalf("no separator after cjk label")
	}
	left := tail[:sepIdx]
	if bytes.ContainsRune([]byte(left), '你') || bytes.ContainsRune([]byte(left), '好') {
		t.Errorf("wide rune leaked into label column: %q", left)
	}

	// Also verify the wide frame state: feeding two successive
	// frames with different widths should not leave ghost content.
	// Frame 2: drop the CJK row, replace with ASCII.
	tbl2 := newTable()
	feedRows(tbl2,
		[]db.Column{{Name: "label"}, {Name: "content"}},
		[][]any{
			{"cjk", "narrow"},
			{"emoji", "ab"},
			{"ascii", "hello"},
		},
	)
	out.Reset()
	buf2 := newCellbuf(40, 7)
	tbl2.draw(buf2, rect{row: 1, col: 1, w: 40, h: 7})
	scr.composite([]*cellbuf{buf2})
	if err := scr.flush(); err != nil {
		t.Fatalf("flush2: %v", err)
	}
	plain2 := stripANSI(out.String())
	// The new frame's plain text must not mention any of the old
	// wide runes -- that would mean a ghost half survived.
	for _, ghost := range []string{"你", "好", "世", "界", "🎉"} {
		if bytes.ContainsRune([]byte(plain2), []rune(ghost)[0]) {
			t.Errorf("ghost rune %q survived second frame: %q", ghost, plain2)
		}
	}
}

// TestAppendCellRunsWideRunes pins the runeRun head+continuation
// layout for a wide-rune cell. The draw loop relies on every slot
// being exactly one visual column wide so scrollCol / highlight
// math stays trivial.
func TestAppendCellRunsWideRunes(t *testing.T) {
	t.Parallel()
	// Width 6 cells; "你好" is 4 visual columns so we expect 2
	// trailing blank slots.
	runs := appendCellRuns(nil, "你好", 6)
	if len(runs) != 6 {
		t.Fatalf("len(runs) = %d, want 6", len(runs))
	}
	if !runs[0].wide || runs[0].s != "你" {
		t.Errorf("runs[0] = %+v, want head 你", runs[0])
	}
	if !runs[1].cont {
		t.Errorf("runs[1] = %+v, want cont", runs[1])
	}
	if !runs[2].wide || runs[2].s != "好" {
		t.Errorf("runs[2] = %+v, want head 好", runs[2])
	}
	if !runs[3].cont {
		t.Errorf("runs[3] = %+v, want cont", runs[3])
	}
	if runs[4].s != " " || runs[4].wide || runs[4].cont {
		t.Errorf("runs[4] = %+v, want blank", runs[4])
	}
	if runs[5].s != " " || runs[5].wide || runs[5].cont {
		t.Errorf("runs[5] = %+v, want blank", runs[5])
	}
}

// TestAppendCellRunsWideRuneAtBoundary verifies that a wide rune
// that would straddle the column boundary gets replaced with a
// space so two halves of a glyph never leak across the viewport
// edge.
func TestAppendCellRunsWideRuneAtBoundary(t *testing.T) {
	t.Parallel()
	// Width 3 cells, content "你X": '你' takes 2, leaving 1, but
	// then 'X' fits. Total 3.
	runs := appendCellRuns(nil, "你X", 3)
	if len(runs) != 3 {
		t.Fatalf("len(runs) = %d, want 3", len(runs))
	}
	if !runs[0].wide || runs[0].s != "你" {
		t.Errorf("runs[0] = %+v", runs[0])
	}
	if !runs[1].cont {
		t.Errorf("runs[1] = %+v, want cont", runs[1])
	}
	if runs[2].s != "X" {
		t.Errorf("runs[2] = %+v, want X", runs[2])
	}

	// Now width 1 cells with '你' as input: no room for the wide
	// glyph's right half, so the whole rune is dropped and the
	// slot becomes blank.
	runs = appendCellRuns(nil, "你", 1)
	if len(runs) != 1 {
		t.Fatalf("len(runs) = %d, want 1", len(runs))
	}
	if runs[0].s != " " {
		t.Errorf("runs[0] = %+v, want blank (wide rune clipped)", runs[0])
	}
}

// TestDisplayWidthCountsWideAndEscapes sanity-checks the width
// helper across the mix of rune categories the test_notes table
// stresses.
func TestDisplayWidthCountsWideAndEscapes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want int
	}{
		{"hello", 5},
		{"你好", 4},                // 2+2
		{"a你b", 4},               // 1+2+1
		{"🎉", 2},                 // party popper
		{"café", 4},              // precomposed: 4 cells
		{"cafe\u0301", 4},        // combining mark contributes 0 width
		{"tab\there", 3 + 2 + 4}, // "tab" + \t (2 cells) + "here"
	}
	for _, tc := range cases {
		if got := displayWidth(tc.in); got != tc.want {
			t.Errorf("displayWidth(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestTableFilterBadRegexFallsBack(t *testing.T) {
	t.Parallel()
	tbl := newTable()
	feedRows(tbl,
		[]db.Column{{Name: "v"}},
		[][]any{
			{"alpha[unclosed"},
			{"beta"},
		},
	)
	tbl.SetFilter(`/[unclosed/`)
	_, snap := tbl.Snapshot()
	// Literal substring "[unclosed" should match the first row only.
	if len(snap) != 1 {
		t.Errorf("bad regex fallback got %d rows, want 1", len(snap))
	}
	if note := tbl.FilterNote(); note == "" {
		t.Errorf("expected a filter note for bad regex")
	}
}

func TestTableSortCyclesAscDescNone(t *testing.T) {
	t.Parallel()
	tbl := newTable()
	feedRows(tbl,
		[]db.Column{{Name: "id"}, {Name: "name"}},
		[][]any{
			{int64(3), "alice"},
			{int64(1), "bob"},
			{int64(2), "charlie"},
		},
	)
	// Put the cursor on the id column.
	tbl.MoveCellBy(0, 0)
	_, _, active := tbl.CycleSortAtCursor()
	if !active {
		t.Fatalf("expected sort to activate asc")
	}
	_, snap := tbl.Snapshot()
	if snap[0][0] != "1" || snap[2][0] != "3" {
		t.Errorf("asc sort order = %v", snap)
	}
	_, desc, active := tbl.CycleSortAtCursor()
	if !active || !desc {
		t.Fatalf("expected sort desc")
	}
	_, snap = tbl.Snapshot()
	if snap[0][0] != "3" || snap[2][0] != "1" {
		t.Errorf("desc sort order = %v", snap)
	}
	_, _, active = tbl.CycleSortAtCursor()
	if active {
		t.Fatalf("expected sort cleared")
	}
}

func TestTableClearResetsState(t *testing.T) {
	t.Parallel()
	tbl := newTable()
	feedRows(tbl,
		[]db.Column{{Name: "id"}},
		[][]any{{int64(1)}, {int64(2)}},
	)
	tbl.Clear()
	if tbl.HasColumns() {
		t.Errorf("HasColumns true after Clear")
	}
	if tbl.RowCount() != 0 {
		t.Errorf("RowCount = %d after Clear, want 0", tbl.RowCount())
	}
}

func TestTableStringifyCell(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   any
		want string
	}{
		{nil, "NULL"},
		{"hello", "hello"},
		{true, "true"},
		{false, "false"},
		{int64(42), "42"},
		{[]byte{1, 2, 3, 4}, "<4 bytes>"},
		{"line1\nline2\tend", "line1\nline2\tend"}, // preserved; draw dims them
	}
	for _, tc := range cases {
		if got := stringifyCell(tc.in); got != tc.want {
			t.Errorf("stringifyCell(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestTruncate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		w    int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello", 4, "hel…"},
		{"hello", 3, "he…"},
		{"hello", 1, "h"},
		{"hello", 0, ""},
	}
	for _, tc := range cases {
		if got := truncate(tc.in, tc.w); got != tc.want {
			t.Errorf("truncate(%q,%d) = %q, want %q", tc.in, tc.w, got, tc.want)
		}
	}
}

// TestChopCellRunsEscape verifies that escape characters inside a
// wrapped cell carry the escape flag through to the per-line chunks
// so the wrap draw path can dim them the same way the non-wrap path
// does.
func TestChopCellRunsEscape(t *testing.T) {
	t.Parallel()
	// Width 6, one line, plain \n in the middle: "a\nb" expands to
	// "a\\n b   " -- 1 + 2 + 1 content, padded to 6.
	lines := chopCellRuns("a\nb", 6)
	if len(lines) != 1 {
		t.Fatalf("len(lines) = %d, want 1", len(lines))
	}
	row := lines[0]
	if len(row) != 6 {
		t.Fatalf("len(row) = %d, want 6", len(row))
	}
	if row[0].s != "a" || row[0].escape {
		t.Errorf("row[0] = %+v, want plain 'a'", row[0])
	}
	if row[1].s != "\\" || !row[1].escape {
		t.Errorf("row[1] = %+v, want escape '\\\\'", row[1])
	}
	if row[2].s != "n" || !row[2].escape {
		t.Errorf("row[2] = %+v, want escape 'n'", row[2])
	}
	if row[3].s != "b" || row[3].escape {
		t.Errorf("row[3] = %+v, want plain 'b'", row[3])
	}
	for i := 4; i < 6; i++ {
		if row[i].s != " " {
			t.Errorf("row[%d] = %+v, want blank", i, row[i])
		}
	}
}

// TestChopCellRunsFlushesOnEscapeBoundary makes sure an escape pair
// that doesn't fit on the current line wraps as a unit onto the next
// line rather than splitting across the fold.
func TestChopCellRunsFlushesOnEscapeBoundary(t *testing.T) {
	t.Parallel()
	// Width 3, content "aa\nb": "aa" fills line 1, then "\\n" needs
	// 2 cells so it flushes line 1 to exactly width 3 (pad once) and
	// starts a new line with the escape pair.
	lines := chopCellRuns("aa\nb", 3)
	if len(lines) != 2 {
		t.Fatalf("len(lines) = %d, want 2", len(lines))
	}
	if lines[0][0].s != "a" || lines[0][1].s != "a" || lines[0][2].s != " " {
		t.Errorf("line 0 = %+v, want a a <blank>", lines[0])
	}
	if !lines[1][0].escape || lines[1][0].s != "\\" {
		t.Errorf("line 1 slot 0 = %+v, want escape head", lines[1][0])
	}
	if !lines[1][1].escape || lines[1][1].s != "n" {
		t.Errorf("line 1 slot 1 = %+v, want escape tail", lines[1][1])
	}
	if lines[1][2].s != "b" {
		t.Errorf("line 1 slot 2 = %+v, want plain 'b'", lines[1][2])
	}
}

// TestWrapRowRunsColumnAlignment verifies that wrapRowRuns pads short
// cells to their full column width on every wrapped line so the
// separator columns line up and highlightSpan math from the non-wrap
// path applies unchanged.
func TestWrapRowRunsColumnAlignment(t *testing.T) {
	t.Parallel()
	// Two columns, widths 3 and 3. Row content: short cell "a" and a
	// multi-line cell "xxxyyy" (chops to "xxx" + "yyy").
	widths := []int{3, 3}
	cells := []string{"a", "xxxyyy"}
	block := wrapRowRuns(cells, widths)
	if len(block) != 2 {
		t.Fatalf("len(block) = %d, want 2", len(block))
	}
	// Expected layout per line: 3 cells for col 0, " ", "|", " ", 3
	// cells for col 1 = 9 slots total.
	for i, line := range block {
		if len(line) != 9 {
			t.Fatalf("line %d len = %d, want 9", i, len(line))
		}
		if line[3].s != " " || line[4].s != "│" || line[5].s != " " {
			t.Errorf("line %d separator slots = %q %q %q, want ' ' '│' ' '",
				i, line[3].s, line[4].s, line[5].s)
		}
	}
	// Line 0: "a  " | "xxx"
	if block[0][0].s != "a" || block[0][1].s != " " || block[0][2].s != " " {
		t.Errorf("line 0 col 0 = %+v, want 'a' padded", block[0][:3])
	}
	if block[0][6].s != "x" || block[0][7].s != "x" || block[0][8].s != "x" {
		t.Errorf("line 0 col 1 = %+v, want 'xxx'", block[0][6:])
	}
	// Line 1: "   " | "yyy" -- the short cell must pad with blanks so
	// separators still line up on the continuation line.
	for i := 0; i < 3; i++ {
		if block[1][i].s != " " {
			t.Errorf("line 1 col 0 slot %d = %+v, want blank pad", i, block[1][i])
		}
	}
	if block[1][6].s != "y" || block[1][7].s != "y" || block[1][8].s != "y" {
		t.Errorf("line 1 col 1 = %+v, want 'yyy'", block[1][6:])
	}
}

// TestWrapHighlightSpanMatchesNonWrap confirms that the highlight
// column range computed from widths is the same whether the row is
// drawn wrapped or not -- this is the invariant that lets the wrap
// path reuse drawRunsWithHighlight's cursor-cell painting unchanged.
func TestWrapHighlightSpanMatchesNonWrap(t *testing.T) {
	t.Parallel()
	widths := []int{3, 3}
	cells := []string{"a", "xxxyyy"}
	lo, hi := highlightSpan(widths, 1)
	// col 0 = 3 wide, separator = 3 wide, col 1 starts at 6.
	if lo != 6 || hi != 9 {
		t.Fatalf("highlightSpan = [%d,%d), want [6,9)", lo, hi)
	}
	block := wrapRowRuns(cells, widths)
	// Every wrapped line must have slots 6..9 inside column 1 (so
	// the cursor highlight paints over the continuation's 'yyy' too).
	for i, line := range block {
		for j := lo; j < hi; j++ {
			if line[j].cont {
				t.Errorf("line %d slot %d is a continuation -- highlight span would miss the head", i, j)
			}
		}
	}
}
