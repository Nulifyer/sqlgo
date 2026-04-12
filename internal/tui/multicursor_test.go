package tui

import (
	"strings"
	"testing"
)

// seedMultilineEditor builds an editor with multiple lines and
// positions the cursor at (row, col).
func seedMultilineEditor(text string, row, col int) *editor {
	e := newEditor()
	e.buf.Clear()
	e.buf.InsertText(text)
	e.buf.SetCursor(row, col)
	return e
}

func TestMultiCursorAddBelowInsertsRune(t *testing.T) {
	t.Parallel()
	e := seedMultilineEditor("foo\nbar\nbaz", 0, 3)
	// Add cursor on row 1, col 3.
	e.addCursorRelative(1)
	if len(e.extraCursors) != 1 {
		t.Fatalf("extras = %d, want 1", len(e.extraCursors))
	}
	// Type 'X' — should land at col 3 on both rows.
	e.handleInsert(nil, Key{Kind: KeyRune, Rune: 'X'})
	got := e.buf.Text()
	want := "fooX\nbarX\nbaz"
	if got != want {
		t.Errorf("buffer = %q, want %q", got, want)
	}
}

func TestMultiCursorAddAboveAndType(t *testing.T) {
	t.Parallel()
	e := seedMultilineEditor("foo\nbar\nbaz", 2, 3)
	e.addCursorRelative(-1) // row 1, col 3
	e.addCursorRelative(-2) // from primary row 2: row 0 col 3
	// Wait — addCursorRelative is from primary. Primary is
	// still row 2 (not moved by add). So -2 goes to row 0.
	if len(e.extraCursors) != 2 {
		t.Fatalf("extras = %d, want 2", len(e.extraCursors))
	}
	e.handleInsert(nil, Key{Kind: KeyRune, Rune: 'Y'})
	got := e.buf.Text()
	want := "fooY\nbarY\nbazY"
	if got != want {
		t.Errorf("buffer = %q, want %q", got, want)
	}
}

func TestMultiCursorBackspace(t *testing.T) {
	t.Parallel()
	e := seedMultilineEditor("foo1\nbar1\nbaz1", 0, 4)
	e.addCursorRelative(1)
	e.addCursorRelative(2)
	e.handleInsert(nil, Key{Kind: KeyBackspace})
	got := e.buf.Text()
	want := "foo\nbar\nbaz"
	if got != want {
		t.Errorf("buffer = %q, want %q", got, want)
	}
}

func TestMultiCursorBackspaceAtCol0Skipped(t *testing.T) {
	t.Parallel()
	e := seedMultilineEditor("foo\n\nbaz", 0, 0)
	e.addCursorRelative(1) // row 1 col 0 (empty line)
	e.addCursorRelative(2) // row 2 col 0
	// Backspace at col 0 on all cursors should be a no-op
	// (joining rows would break the row-uniqueness invariant).
	e.handleInsert(nil, Key{Kind: KeyBackspace})
	got := e.buf.Text()
	if got != "foo\n\nbaz" {
		t.Errorf("buffer mutated unexpectedly: %q", got)
	}
}

func TestMultiCursorDelete(t *testing.T) {
	t.Parallel()
	e := seedMultilineEditor("xfoo\nxbar\nxbaz", 0, 0)
	e.addCursorRelative(1)
	e.addCursorRelative(2)
	e.handleInsert(nil, Key{Kind: KeyDelete})
	got := e.buf.Text()
	if got != "foo\nbar\nbaz" {
		t.Errorf("buffer = %q, want strip leading x", got)
	}
}

func TestMultiCursorMoveLeftRight(t *testing.T) {
	t.Parallel()
	e := seedMultilineEditor("abcd\nefgh\nijkl", 0, 2)
	e.addCursorRelative(1)
	e.handleInsert(nil, Key{Kind: KeyRight})
	// After Right, primary is at (0, 3), extra at (1, 3).
	row, col := e.buf.Cursor()
	if row != 0 || col != 3 {
		t.Errorf("primary = (%d,%d), want (0,3)", row, col)
	}
	if len(e.extraCursors) != 1 {
		t.Fatalf("extras len = %d", len(e.extraCursors))
	}
	if e.extraCursors[0].row != 1 || e.extraCursors[0].col != 3 {
		t.Errorf("extra = %+v, want row 1 col 3", e.extraCursors[0])
	}
}

func TestMultiCursorEscCollapses(t *testing.T) {
	t.Parallel()
	e := seedMultilineEditor("a\nb\nc", 0, 1)
	e.addCursorRelative(1)
	e.addCursorRelative(2)
	e.handleInsert(nil, Key{Kind: KeyEsc})
	if len(e.extraCursors) != 0 {
		t.Errorf("extras should be empty after Esc, got %d", len(e.extraCursors))
	}
}

func TestMultiCursorEnterCollapses(t *testing.T) {
	t.Parallel()
	e := seedMultilineEditor("foo\nbar\nbaz", 0, 3)
	e.addCursorRelative(1)
	e.handleInsert(nil, Key{Kind: KeyEnter})
	// Extras should be gone and primary should have inserted
	// a newline at its single position.
	if e.hasMultiCursor() {
		t.Errorf("extras should be cleared after Enter")
	}
	// Buffer should have an extra line now.
	if !strings.Contains(e.buf.Text(), "foo\n") {
		t.Errorf("buffer = %q", e.buf.Text())
	}
}

func TestMultiCursorClipboardCollapses(t *testing.T) {
	t.Parallel()
	e := seedMultilineEditor("foo\nbar", 0, 0)
	e.addCursorRelative(1)
	e.handleInsert(nil, Key{Kind: KeyRune, Rune: 'c', Ctrl: true})
	if e.hasMultiCursor() {
		t.Errorf("extras should be cleared after Ctrl+C")
	}
}

func TestMultiCursorDuplicateRowDropped(t *testing.T) {
	t.Parallel()
	e := seedMultilineEditor("a\nb\nc", 0, 0)
	e.addCursorRelative(1)
	// Adding again at the same delta should not duplicate.
	e.addCursorRelative(1)
	if len(e.extraCursors) != 1 {
		t.Errorf("extras = %d, want 1 (row dedupe)", len(e.extraCursors))
	}
}

func TestMultiCursorOutOfBoundsNoOp(t *testing.T) {
	t.Parallel()
	e := seedMultilineEditor("only", 0, 0)
	e.addCursorRelative(1)  // no row 1 exists
	e.addCursorRelative(-1) // no row -1 exists
	if len(e.extraCursors) != 0 {
		t.Errorf("out-of-range adds should no-op")
	}
}

func TestMultiCursorColumnClampOnShortLine(t *testing.T) {
	t.Parallel()
	e := seedMultilineEditor("aaaaa\nbb\nccccc", 0, 5)
	e.addCursorRelative(1) // row 1 only has 2 chars; clamp to 2
	if len(e.extraCursors) != 1 {
		t.Fatalf("extras = %d, want 1", len(e.extraCursors))
	}
	if e.extraCursors[0].col != 2 {
		t.Errorf("col = %d, want 2 (clamped)", e.extraCursors[0].col)
	}
}
