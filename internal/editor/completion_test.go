package editor

import (
	"testing"

	"github.com/nulifyer/sqlgo/internal/db"
)

func TestDetectCompletionContextQualifier(t *testing.T) {
	t.Parallel()

	text := `SELECT u.na FROM users u`
	cursor := len(`SELECT u.na`)
	ctx := DetectCompletionContext(text, cursor)
	if ctx.Prefix != "na" {
		t.Fatalf("Prefix = %q, want %q", ctx.Prefix, "na")
	}
	if ctx.Qualifier != "u" {
		t.Fatalf("Qualifier = %q, want %q", ctx.Qualifier, "u")
	}
}

func TestBuildCompletionItemsIncludesAliasColumns(t *testing.T) {
	t.Parallel()

	meta := db.CompletionMetadata{
		Objects: []db.ObjectMetadata{
			{Name: "users", Qualified: `"users"`, Type: db.ExplorerTable, Columns: []string{"id", "name", "email"}},
		},
	}
	ctx := CompletionContext{Prefix: "na", Qualifier: "u"}
	items := BuildCompletionItems(meta, `SELECT u.na FROM users u`, ctx)
	if len(items) == 0 {
		t.Fatalf("expected completion items")
	}
	if items[0].Insert != "name" {
		t.Fatalf("top completion = %q, want %q", items[0].Insert, "name")
	}
}
