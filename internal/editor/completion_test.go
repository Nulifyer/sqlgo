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

func TestBuildCompletionItemsIncludesDirectTableColumns(t *testing.T) {
	t.Parallel()

	meta := db.CompletionMetadata{
		Objects: []db.ObjectMetadata{
			{Name: "users", Qualified: `"users"`, Type: db.ExplorerTable, Columns: []string{"id", "name", "email"}},
		},
	}
	ctx := CompletionContext{Prefix: "na", Qualifier: "users"}
	items := BuildCompletionItems(meta, `SELECT users.na FROM users`, ctx)
	if len(items) == 0 {
		t.Fatalf("expected completion items")
	}
	if items[0].Insert != "name" {
		t.Fatalf("top completion = %q, want %q", items[0].Insert, "name")
	}
}

func TestBuildCompletionItemsFuzzyMatchesAbbreviations(t *testing.T) {
	t.Parallel()

	meta := db.CompletionMetadata{
		Objects: []db.ObjectMetadata{
			{Name: "users", Qualified: `"users"`, Type: db.ExplorerTable, Columns: []string{"id", "name", "user_id", "email_address"}},
		},
	}
	ctx := CompletionContext{Prefix: "uid", Qualifier: "users"}
	items := BuildCompletionItems(meta, `SELECT users.uid FROM users`, ctx)
	if len(items) == 0 {
		t.Fatalf("expected fuzzy matches for 'uid' to include user_id")
	}
	found := false
	for _, item := range items {
		if item.Insert == "user_id" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected user_id in fuzzy matches, got %#v", items)
	}
}

func TestFuzzySubsequenceScoreRejectsNonMatch(t *testing.T) {
	t.Parallel()

	if got := fuzzySubsequenceScore("xyz", "user_id"); got >= 0 {
		t.Fatalf("fuzzySubsequenceScore('xyz', 'user_id') = %d, want negative", got)
	}
	if got := fuzzySubsequenceScore("uid", "user_id"); got < 0 {
		t.Fatalf("fuzzySubsequenceScore('uid', 'user_id') = %d, want non-negative", got)
	}
}
