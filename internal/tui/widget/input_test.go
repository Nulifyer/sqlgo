package widget

import "testing"

func TestInputPasteTextSanitizesSingleLineText(t *testing.T) {
	t.Parallel()

	in := NewInput("foo")
	in.MoveEnd()
	if !in.PasteText("\tbar\nbaz\r\nqux\x00\x1f\x7f\u0085!") {
		t.Fatal("PasteText returned false")
	}

	const want = "foo bar baz qux!"
	if got := in.String(); got != want {
		t.Fatalf("input = %q, want %q", got, want)
	}
	if got := in.Cursor(); got != len([]rune(want)) {
		t.Fatalf("cursor = %d, want %d", got, len([]rune(want)))
	}
}

func TestInputPasteTextInsertsAtCursor(t *testing.T) {
	t.Parallel()

	in := NewInput("foobaz")
	for i := 0; i < 3; i++ {
		in.MoveLeft()
	}
	in.PasteText("\nbar\t")

	if got, want := in.String(), "foo bar baz"; got != want {
		t.Fatalf("input = %q, want %q", got, want)
	}
}
