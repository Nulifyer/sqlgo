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

func TestBufferSelectionSingleLine(t *testing.T) {
	t.Parallel()
	b := newBuffer()
	b.SetText("hello world")
	b.MoveHome()
	// Select "hello"
	for i := 0; i < 5; i++ {
		b.SelectRight()
	}
	if !b.HasSelection() {
		t.Fatal("HasSelection = false, want true")
	}
	if got := b.Selection(); got != "hello" {
		t.Errorf("Selection = %q, want %q", got, "hello")
	}
}

func TestBufferSelectionAcrossLines(t *testing.T) {
	t.Parallel()
	b := newBuffer()
	b.SetText("one\ntwo\nthree")
	// Cursor starts at end of "three". Anchor there, select upward by
	// two lines to land at end of "one".
	b.SelectUp()
	b.SelectUp()
	b.SelectEnd()
	got := b.Selection()
	want := "\ntwo\nthree"
	if got != want {
		t.Errorf("Selection = %q, want %q", got, want)
	}
}

func TestBufferDeleteSelectionCollapses(t *testing.T) {
	t.Parallel()
	b := newBuffer()
	b.SetText("abc123xyz")
	b.MoveHome()
	for i := 0; i < 3; i++ {
		b.MoveRight()
	}
	// Select "123".
	for i := 0; i < 3; i++ {
		b.SelectRight()
	}
	b.DeleteSelection()
	if got := b.Text(); got != "abcxyz" {
		t.Errorf("after delete sel: %q, want %q", got, "abcxyz")
	}
	if b.HasSelection() {
		t.Errorf("selection still active after DeleteSelection")
	}
}

func TestBufferInsertTextMultiline(t *testing.T) {
	t.Parallel()
	b := newBuffer()
	b.SetText("start")
	b.MoveEnd()
	b.InsertText("\nmiddle\nend")
	if got := b.Text(); got != "start\nmiddle\nend" {
		t.Errorf("InsertText: %q", got)
	}
}

func TestBufferUndoRedo(t *testing.T) {
	t.Parallel()
	b := newBuffer()
	for _, r := range "hello" {
		b.Insert(r)
	}
	if !b.Undo() {
		t.Fatal("Undo returned false on non-empty history")
	}
	// Undo pops the last Insert; after one undo the buffer should
	// contain "hell".
	if got := b.Text(); got != "hell" {
		t.Errorf("after one undo: %q, want %q", got, "hell")
	}
	if !b.Redo() {
		t.Fatal("Redo returned false after undo")
	}
	if got := b.Text(); got != "hello" {
		t.Errorf("after redo: %q", got)
	}
}

func TestBufferSelectAll(t *testing.T) {
	t.Parallel()
	b := newBuffer()
	b.SetText("line1\nline2")
	b.SelectAll()
	if got := b.Selection(); got != "line1\nline2" {
		t.Errorf("SelectAll: %q", got)
	}
}

func TestBufferMoveWordLeft(t *testing.T) {
	t.Parallel()
	b := newBuffer()
	b.SetText("SELECT id, name FROM users")
	// Cursor at end.
	b.MoveWordLeft()
	if r, c := b.Cursor(); r != 0 || c != 21 {
		t.Errorf("after 1 MoveWordLeft: (%d,%d), want (0,21)", r, c)
	}
	b.MoveWordLeft()
	if r, c := b.Cursor(); r != 0 || c != 16 {
		t.Errorf("after 2 MoveWordLeft: (%d,%d), want (0,16)", r, c)
	}
	b.MoveWordLeft()
	if r, c := b.Cursor(); r != 0 || c != 11 {
		t.Errorf("after 3 MoveWordLeft: (%d,%d), want (0,11)", r, c)
	}
}

func TestBufferMoveWordRight(t *testing.T) {
	t.Parallel()
	b := newBuffer()
	b.SetText("SELECT id, name FROM users")
	// 0         1         2
	// 012345678901234567890123456
	// SELECT id, name FROM users
	// Word starts: 0, 7, 11, 16, 21
	b.MoveHome()
	b.MoveWordRight()
	if r, c := b.Cursor(); r != 0 || c != 7 {
		t.Errorf("after 1 MoveWordRight: (%d,%d), want (0,7)", r, c)
	}
	b.MoveWordRight()
	if r, c := b.Cursor(); r != 0 || c != 11 {
		t.Errorf("after 2 MoveWordRight: (%d,%d), want (0,11)", r, c)
	}
	b.MoveWordRight()
	if r, c := b.Cursor(); r != 0 || c != 16 {
		t.Errorf("after 3 MoveWordRight: (%d,%d), want (0,16)", r, c)
	}
}

func TestBufferMoveWordLeftCrossesLines(t *testing.T) {
	t.Parallel()
	b := newBuffer()
	b.SetText("first\nsecond")
	b.row, b.col = 1, 0
	b.MoveWordLeft()
	if r, c := b.Cursor(); r != 0 || c != 5 {
		t.Errorf("cross-line MoveWordLeft: (%d,%d), want (0,5)", r, c)
	}
}

func TestBufferDeleteWordLeft(t *testing.T) {
	t.Parallel()
	b := newBuffer()
	b.SetText("SELECT users FROM")
	// Cursor after "users" (position 12).
	b.row, b.col = 0, 12
	b.DeleteWordLeft()
	if got := b.Text(); got != "SELECT  FROM" {
		t.Errorf("after DeleteWordLeft: %q, want %q", got, "SELECT  FROM")
	}
}

func TestBufferDeleteWordRight(t *testing.T) {
	t.Parallel()
	b := newBuffer()
	b.SetText("SELECT users FROM")
	b.row, b.col = 0, 7
	b.DeleteWordRight()
	if got := b.Text(); got != "SELECT FROM" {
		t.Errorf("after DeleteWordRight: %q, want %q", got, "SELECT FROM")
	}
}

func TestBufferDeleteWordLeftUndoes(t *testing.T) {
	t.Parallel()
	b := newBuffer()
	b.SetText("SELECT users FROM")
	b.row, b.col = 0, 12
	b.DeleteWordLeft()
	if !b.Undo() {
		t.Fatal("Undo returned false")
	}
	if got := b.Text(); got != "SELECT users FROM" {
		t.Errorf("after undo: %q", got)
	}
}
