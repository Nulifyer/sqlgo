package widget

import (
	"testing"

	"github.com/Nulifyer/sqlgo/internal/tui/term"
)

func TestFormDrawPlacesCursorOnActiveInputOnly(t *testing.T) {
	t.Parallel()

	form := Form{
		Fields: []*FormField{
			{Label: "First", Input: NewInput("one")},
			{Label: "Second", Input: NewInput("two")},
		},
		Active: 0,
	}
	buf := term.NewCellbuf(40, 6)
	form.Draw(buf, term.Rect{Row: 1, Col: 1, W: 38, H: 4}, FormDrawOpts{LabelW: 10})
	if !buf.CursorWanted {
		t.Fatal("expected active input to place cursor")
	}
	firstRow, firstCol := buf.CursorRow, buf.CursorCol
	if firstRow != 1 {
		t.Fatalf("cursor row for first active field = %d, want 1", firstRow)
	}

	form.Active = 1
	buf.Reset()
	form.Draw(buf, term.Rect{Row: 1, Col: 1, W: 38, H: 4}, FormDrawOpts{LabelW: 10})
	if !buf.CursorWanted {
		t.Fatal("expected active input to place cursor")
	}
	if buf.CursorRow != 2 {
		t.Fatalf("cursor row for second active field = %d, want 2", buf.CursorRow)
	}
	if buf.CursorRow == firstRow && buf.CursorCol == firstCol {
		t.Fatalf("cursor stayed on first field at (%d,%d)", buf.CursorRow, buf.CursorCol)
	}
}
