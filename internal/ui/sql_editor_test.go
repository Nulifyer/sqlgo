package ui

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/nulifyer/sqlgo/internal/editor"
)

func TestSQLEditorTabAcceptsHighlightedCompletion(t *testing.T) {
	t.Parallel()

	sqlEditor := NewSQLEditor().SetCompletionProvider(func(force bool, text string, cursor int) (editor.CompletionContext, []editor.CompletionItem, error) {
		return editor.CompletionContext{
				Start:  0,
				End:    len(text),
				Prefix: text,
			}, []editor.CompletionItem{
				{Label: "users", Insert: "users", Kind: "table"},
				{Label: "users_archive", Insert: "users_archive", Kind: "table"},
			}, nil
	})

	sqlEditor.SetText("us", true)
	if !sqlEditor.popupVisible {
		t.Fatalf("expected autocomplete popup to be visible after typing a prefix")
	}

	handled := sqlEditor.HandleAutocompleteKey(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
	if !handled {
		t.Fatalf("expected tab to be handled when autocomplete is visible")
	}
	if got := sqlEditor.GetText(); got != "users" {
		t.Fatalf("GetText() = %q, want users", got)
	}
	if sqlEditor.popupVisible {
		t.Fatalf("expected autocomplete popup to hide after accepting a completion")
	}
}
