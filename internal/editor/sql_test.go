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

func TestFormatSQLIndentsJoinAndOnClauses(t *testing.T) {
	t.Parallel()

	input := "select u.name, p.name from users u join projects p on p.owner_id = u.id where u.active = 1;"
	got := FormatSQL(input)

	wantParts := []string{
		"FROM users u",
		"    JOIN projects p",
		"        ON p.owner_id = u.id",
		"WHERE u.active = 1;",
	}
	for _, want := range wantParts {
		if !strings.Contains(got, want) {
			t.Fatalf("formatted SQL missing %q\n%s", want, got)
		}
	}
}

func TestFormatSQLIndentsNestedSubquery(t *testing.T) {
	t.Parallel()

	input := "select id from (select id, owner_id from projects where owner_id in (select id from users where active = 1)) p;"
	got := FormatSQL(input)

	wantParts := []string{
		"SELECT id",
		"FROM (",
		"    SELECT id,",
		"        owner_id",
		"    FROM projects",
		"    WHERE owner_id IN (",
		"        SELECT id",
		"        FROM users",
		"        WHERE active = 1",
		"    )",
		") p;",
	}
	for _, want := range wantParts {
		if !strings.Contains(got, want) {
			t.Fatalf("formatted nested SQL missing %q\n%s", want, got)
		}
	}
}

func TestNextLineIndentPreservesAndExtendsIndentation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		text   string
		cursor int
		want   string
	}{
		{
			name:   "preserves current line indentation",
			text:   "SELECT id,\n    name",
			cursor: len("SELECT id,\n    name"),
			want:   "    ",
		},
		{
			name:   "indents after clause header",
			text:   "SELECT",
			cursor: len("SELECT"),
			want:   indentUnit,
		},
		{
			name:   "indents after opening paren",
			text:   "WHERE (",
			cursor: len("WHERE ("),
			want:   indentUnit,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := NextLineIndent(tt.text, tt.cursor); got != tt.want {
				t.Fatalf("NextLineIndent() = %q, want %q", got, tt.want)
			}
		})
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
