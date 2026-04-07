package editor

import (
	"strings"
	"testing"
)

func TestFormatSQLNormalizesClausesAndKeywords(t *testing.T) {
	t.Parallel()

	input := "select id,name from users where status = 'active' order by created_at desc;"
	got := FormatSQL(input)

	wantParts := []string{
		"SELECT id,",
		"    name",
		"FROM users",
		"WHERE status = 'active'",
		"ORDER BY created_at DESC;",
	}
	for _, want := range wantParts {
		if !strings.Contains(got, want) {
			t.Fatalf("formatted SQL missing %q\n%s", want, got)
		}
	}
}

func TestFormatSQLPreservesCommentsAndStrings(t *testing.T) {
	t.Parallel()

	input := "-- keep me\nselect 'a,b', note from logs /* trailing */ where id = 42;"
	got := FormatSQL(input)

	if !strings.Contains(got, "-- keep me") {
		t.Fatalf("formatted SQL lost line comment:\n%s", got)
	}
	if !strings.Contains(got, "'a,b'") {
		t.Fatalf("formatted SQL lost string literal:\n%s", got)
	}
	if !strings.Contains(got, "/* trailing */") {
		t.Fatalf("formatted SQL lost block comment:\n%s", got)
	}
}

func TestHighlightSQLColorsMajorTokenTypes(t *testing.T) {
	t.Parallel()

	input := "-- note\nselect id, 'ok', 42 from users"
	got := HighlightSQL(input)

	wantParts := []string{
		"[gray]-- note[-]",
		"[yellow::b]SELECT[-:-:-]",
		"[green]'ok'[-]",
		"[blue]42[-]",
	}
	for _, want := range wantParts {
		if !strings.Contains(got, want) {
			t.Fatalf("highlighted SQL missing %q\n%s", want, got)
		}
	}
}
