package ui

import "testing"

func TestTextBufferSetGetTextRoundTrip(t *testing.T) {
	t.Parallel()

	cases := []string{
		"",
		"single",
		"two\nlines",
		"three\nlines\nhere",
		"trailing newline\n",
		"\nleading newline",
		"unicode 你好\nемоji 🙂",
	}
	for _, in := range cases {
		in := in
		t.Run(in, func(t *testing.T) {
			t.Parallel()
			b := NewTextBuffer()
			b.SetText(in)
			if got := b.GetText(); got != in {
				t.Fatalf("round trip mismatch:\nin:  %q\nout: %q", in, got)
			}
		})
	}
}

func TestTextBufferInsertSingleLine(t *testing.T) {
	t.Parallel()

	b := NewTextBuffer()
	b.SetText("hello world")
	row, col := b.Insert(0, 5, ",")
	if row != 0 || col != 6 {
		t.Fatalf("Insert returned (%d,%d), want (0,6)", row, col)
	}
	if got := b.GetText(); got != "hello, world" {
		t.Fatalf("GetText = %q", got)
	}
}

func TestTextBufferInsertMultiLineSplitsRow(t *testing.T) {
	t.Parallel()

	b := NewTextBuffer()
	b.SetText("SELECT name FROM users")
	row, col := b.Insert(0, 7, "id,\n    ")
	if row != 1 || col != 4 {
		t.Fatalf("Insert returned (%d,%d), want (1,4)", row, col)
	}
	want := "SELECT id,\n    name FROM users"
	if got := b.GetText(); got != want {
		t.Fatalf("GetText = %q\nwant   = %q", got, want)
	}
}

func TestTextBufferInsertNewlineSplitsCurrentRow(t *testing.T) {
	t.Parallel()

	b := NewTextBuffer()
	b.SetText("SELECT a, b")
	// col 9 sits immediately after the comma, before the space.
	row, col := b.Insert(0, 9, "\n    ")
	if row != 1 || col != 4 {
		t.Fatalf("Insert returned (%d,%d), want (1,4)", row, col)
	}
	if got := b.GetText(); got != "SELECT a,\n     b" {
		t.Fatalf("GetText = %q", got)
	}
}

func TestTextBufferInsertAtEndOfLastLine(t *testing.T) {
	t.Parallel()

	// Regression: inserting at end of buffer must not lose tail content of
	// other lines or jump cursor.
	b := NewTextBuffer()
	b.SetText("first\nsecond")
	row, col := b.Insert(1, 6, "    ")
	if row != 1 || col != 10 {
		t.Fatalf("Insert returned (%d,%d), want (1,10)", row, col)
	}
	if got := b.GetText(); got != "first\nsecond    " {
		t.Fatalf("GetText = %q", got)
	}
}

func TestTextBufferDeleteRangeSameLine(t *testing.T) {
	t.Parallel()

	b := NewTextBuffer()
	b.SetText("SELECT 'a'")
	row, col := b.DeleteRange(0, 7, 0, 10)
	if row != 0 || col != 7 {
		t.Fatalf("DeleteRange returned (%d,%d), want (0,7)", row, col)
	}
	if got := b.GetText(); got != "SELECT " {
		t.Fatalf("GetText = %q", got)
	}
}

func TestTextBufferDeleteRangeAcrossLines(t *testing.T) {
	t.Parallel()

	b := NewTextBuffer()
	b.SetText("SELECT a,\n    b,\n    c\nFROM t")
	row, col := b.DeleteRange(0, 9, 2, 5)
	if row != 0 || col != 9 {
		t.Fatalf("DeleteRange returned (%d,%d), want (0,9)", row, col)
	}
	want := "SELECT a,\nFROM t"
	if got := b.GetText(); got != want {
		t.Fatalf("GetText = %q\nwant   = %q", got, want)
	}
}

func TestTextBufferByteOffsetMatchesGetText(t *testing.T) {
	t.Parallel()

	b := NewTextBuffer()
	b.SetText("hello\nworld\n你好")
	text := b.GetText()

	type pos struct{ row, col int }
	checks := []pos{
		{0, 0}, {0, 5}, {1, 0}, {1, 3}, {1, 5}, {2, 0}, {2, 1}, {2, 2},
	}
	for _, p := range checks {
		offset := b.ByteOffset(p.row, p.col)
		if offset > len(text) {
			t.Fatalf("ByteOffset(%d,%d) = %d, exceeds text length %d", p.row, p.col, offset, len(text))
		}
		gotRow, gotCol := b.PositionFromByteOffset(offset)
		if gotRow != p.row || gotCol != p.col {
			t.Fatalf("round trip (%d,%d) -> %d -> (%d,%d)", p.row, p.col, offset, gotRow, gotCol)
		}
	}
}

func TestTextBufferTotalByteLenMatchesGetText(t *testing.T) {
	t.Parallel()

	cases := []string{"", "abc", "two\nlines", "你好\nworld", "trailing\n"}
	for _, c := range cases {
		c := c
		t.Run(c, func(t *testing.T) {
			t.Parallel()
			b := NewTextBuffer()
			b.SetText(c)
			if got, want := b.TotalByteLen(), len(b.GetText()); got != want {
				t.Fatalf("TotalByteLen = %d, want %d", got, want)
			}
		})
	}
}

func TestTextBufferClampPositionHandlesOutOfRange(t *testing.T) {
	t.Parallel()

	b := NewTextBuffer()
	b.SetText("ab\ncd")
	row, col := b.ClampPosition(99, 99)
	if row != 1 || col != 2 {
		t.Fatalf("ClampPosition(99,99) = (%d,%d), want (1,2)", row, col)
	}
	row, col = b.ClampPosition(-1, -1)
	if row != 0 || col != 0 {
		t.Fatalf("ClampPosition(-1,-1) = (%d,%d), want (0,0)", row, col)
	}
}
