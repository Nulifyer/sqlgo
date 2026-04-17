package tui

import "testing"

func TestSanitizeInspectorTextEscapesCR(t *testing.T) {
	t.Parallel()
	in := "line1\rline2"
	want := `line1\rline2`
	if got := sanitizeInspectorText(in); got != want {
		t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
	}
}

func TestSanitizeInspectorTextEscapesTab(t *testing.T) {
	t.Parallel()
	in := "a\tb"
	want := `a\tb`
	if got := sanitizeInspectorText(in); got != want {
		t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
	}
}

func TestSanitizeInspectorTextKeepsNewline(t *testing.T) {
	t.Parallel()
	in := "line1\nline2"
	if got := sanitizeInspectorText(in); got != in {
		t.Errorf("sanitize(%q) = %q, newline mangled", in, got)
	}
}

func TestSanitizeInspectorTextDropsControl(t *testing.T) {
	t.Parallel()
	in := "a\x00b\x07c\x1bd\x7fe"
	want := "abcde"
	if got := sanitizeInspectorText(in); got != want {
		t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
	}
}

// TestWrapTextCRDoesNotOverwriteLine is the regression guard for
// the reported bug: a value like "line1\rline2" used to pass \r
// through to writeStyled, which the terminal interpreted as a
// carriage return and overwrote line1 with line2.
func TestWrapTextCRDoesNotOverwriteLine(t *testing.T) {
	t.Parallel()
	lines := wrapText("line1\rline2", 40)
	if len(lines) != 1 {
		t.Fatalf("wrap len = %d, want 1 (cr escaped, not a line break)", len(lines))
	}
	want := `line1\rline2`
	if lines[0] != want {
		t.Errorf("line[0] = %q, want %q", lines[0], want)
	}
}

func TestWrapTextCRLFCollapsesCorrectly(t *testing.T) {
	t.Parallel()
	// "\r\n" collapses to a single newline; standalone "\r" stays literal.
	lines := wrapText("a\r\nb\rc", 40)
	if len(lines) != 2 {
		t.Fatalf("wrap len = %d, want 2", len(lines))
	}
	if lines[0] != "a" {
		t.Errorf("line[0] = %q, want %q", lines[0], "a")
	}
	if lines[1] != `b\rc` {
		t.Errorf("line[1] = %q, want %q", lines[1], `b\rc`)
	}
}
