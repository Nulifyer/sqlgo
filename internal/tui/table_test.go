package tui

import (
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
		{"hello", 4, "h..."},
		{"hello", 3, "hel"},
		{"hello", 1, "h"},
		{"hello", 0, ""},
	}
	for _, tc := range cases {
		if got := truncate(tc.in, tc.w); got != tc.want {
			t.Errorf("truncate(%q,%d) = %q, want %q", tc.in, tc.w, got, tc.want)
		}
	}
}
