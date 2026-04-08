package editor

import (
	"strings"
	"testing"
)

func TestStatementRangeAtSelectsStatementUnderCursor(t *testing.T) {
	t.Parallel()

	text := "SELECT 1;\nSELECT 2;\nSELECT 3;"
	tests := []struct {
		name   string
		cursor int
		want   string
	}{
		{name: "first statement", cursor: 3, want: "SELECT 1;"},
		{name: "boundary semicolon", cursor: 8, want: "SELECT 1;"},
		{name: "second statement after newline", cursor: 12, want: "SELECT 2;"},
		{name: "third statement", cursor: 22, want: "SELECT 3;"},
		{name: "cursor at end", cursor: len(text), want: "SELECT 3;"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			start, end := StatementRangeAt(text, tt.cursor)
			got := text[start:end]
			if got != tt.want {
				t.Fatalf("StatementRangeAt(%d) = %q, want %q", tt.cursor, got, tt.want)
			}
		})
	}
}

func TestStatementRangeAtIgnoresSemicolonsInsideStringsAndComments(t *testing.T) {
	t.Parallel()

	text := "SELECT 'a;b';\n-- comment;\nSELECT 2;"
	start, end := StatementRangeAt(text, 5)
	if got := text[start:end]; got != "SELECT 'a;b';" {
		t.Fatalf("first statement = %q, want %q", got, "SELECT 'a;b';")
	}

	start, end = StatementRangeAt(text, len(text)-3)
	got := text[start:end]
	if !strings.Contains(got, "SELECT 2;") {
		t.Fatalf("second statement = %q, want to contain %q", got, "SELECT 2;")
	}
	if strings.Contains(got, "'a;b'") {
		t.Fatalf("second statement = %q, must not include first-statement string", got)
	}
}

func TestStatementRangeAtSingleStatementNoSemicolon(t *testing.T) {
	t.Parallel()

	text := "SELECT 1\nFROM users"
	start, end := StatementRangeAt(text, 4)
	if got := text[start:end]; got != text {
		t.Fatalf("StatementRangeAt = %q, want full buffer", got)
	}
}

func TestStatementRangeAtEmptyBufferReturnsZero(t *testing.T) {
	t.Parallel()

	start, end := StatementRangeAt("", 0)
	if start != 0 || end != 0 {
		t.Fatalf("StatementRangeAt(\"\") = (%d, %d), want (0, 0)", start, end)
	}
}
