package tui

import "testing"

func TestBufferInsertAndText(t *testing.T) {
	t.Parallel()
	b := newBuffer()
	for _, r := range "SELECT 1" {
		b.Insert(r)
	}
	if got := b.Text(); got != "SELECT 1" {
		t.Errorf("text = %q, want %q", got, "SELECT 1")
	}
	if r, c := b.Cursor(); r != 0 || c != 8 {
		t.Errorf("cursor = (%d,%d), want (0,8)", r, c)
	}
}

func TestBufferNewlineAndBackspace(t *testing.T) {
	t.Parallel()
	b := newBuffer()
	for _, r := range "SELECT" {
		b.Insert(r)
	}
	b.InsertNewline()
	for _, r := range "FROM t" {
		b.Insert(r)
	}
	if got := b.Text(); got != "SELECT\nFROM t" {
		t.Errorf("text = %q", got)
	}

	// Backspace at column 0 joins with previous line.
	b.MoveHome()
	b.Backspace()
	if got := b.Text(); got != "SELECTFROM t" {
		t.Errorf("after join: text = %q", got)
	}
	if r, c := b.Cursor(); r != 0 || c != 6 {
		t.Errorf("after join: cursor = (%d,%d), want (0,6)", r, c)
	}
}

func TestBufferMoveAndClamp(t *testing.T) {
	t.Parallel()
	b := newBuffer()
	for _, r := range "abc" {
		b.Insert(r)
	}
	b.InsertNewline()
	for _, r := range "de" {
		b.Insert(r)
	}
	// cursor at (1,2), now move up — should clamp to (0,2) since "abc" has length 3
	b.MoveUp()
	if r, c := b.Cursor(); r != 0 || c != 2 {
		t.Errorf("after MoveUp: cursor = (%d,%d), want (0,2)", r, c)
	}
	// move to end, then down — should clamp to len("de") = 2
	b.MoveEnd()
	b.MoveDown()
	if r, c := b.Cursor(); r != 1 || c != 2 {
		t.Errorf("after MoveDown: cursor = (%d,%d), want (1,2)", r, c)
	}
}

func TestBufferDeleteJoin(t *testing.T) {
	t.Parallel()
	b := newBuffer()
	for _, r := range "ab" {
		b.Insert(r)
	}
	b.InsertNewline()
	for _, r := range "cd" {
		b.Insert(r)
	}
	// cursor at (1,2), go to end of line 0
	b.row, b.col = 0, 2
	b.Delete() // should join "ab" + "cd"
	if got := b.Text(); got != "abcd" {
		t.Errorf("text = %q, want %q", got, "abcd")
	}
}
