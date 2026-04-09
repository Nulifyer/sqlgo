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

func TestTableScrollClamping(t *testing.T) {
	t.Parallel()
	rows := make([][]any, 5)
	for i := range rows {
		rows[i] = []any{int64(i)}
	}
	tbl := newTable()
	feedRows(tbl, []db.Column{{Name: "n"}}, rows)

	tbl.ScrollBy(-10)
	if tbl.scrollRow != 0 {
		t.Errorf("scrollRow after negative = %d, want 0", tbl.scrollRow)
	}
	tbl.ScrollBy(100)
	if tbl.scrollRow != len(rows)-1 {
		t.Errorf("scrollRow after overscroll = %d, want %d", tbl.scrollRow, len(rows)-1)
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
		{"line1\nline2\tend", "line1 line2 end"},
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
