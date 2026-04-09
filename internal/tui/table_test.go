package tui

import (
	"testing"

	"github.com/Nulifyer/sqlgo/internal/db"
)

func TestTableSetResultComputesWidths(t *testing.T) {
	t.Parallel()
	res := &db.Result{
		Columns: []db.Column{{Name: "id"}, {Name: "name"}},
		Rows: [][]any{
			{int64(1), "alice"},
			{int64(200), "bob"},
			{int64(3), "charlotte"},
		},
	}
	tbl := newTable()
	tbl.SetResult(res)

	// widths: id max("id",1,200,3)=3 ; name max("name",alice,bob,charlotte)=9
	want := []int{3, 9}
	for i, w := range want {
		if tbl.widths[i] != w {
			t.Errorf("widths[%d] = %d, want %d", i, tbl.widths[i], w)
		}
	}
}

func TestTableScrollClamping(t *testing.T) {
	t.Parallel()
	rows := make([][]any, 5)
	for i := range rows {
		rows[i] = []any{int64(i)}
	}
	res := &db.Result{
		Columns: []db.Column{{Name: "n"}},
		Rows:    rows,
	}
	tbl := newTable()
	tbl.SetResult(res)

	tbl.ScrollBy(-10)
	if tbl.scrollRow != 0 {
		t.Errorf("scrollRow after negative = %d, want 0", tbl.scrollRow)
	}
	tbl.ScrollBy(100)
	if tbl.scrollRow != len(rows)-1 {
		t.Errorf("scrollRow after overscroll = %d, want %d", tbl.scrollRow, len(rows)-1)
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
