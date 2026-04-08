package ui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

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

func TestSQLEditorInputHandlerTabAcceptsHighlightedCompletion(t *testing.T) {
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
	handler := sqlEditor.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyTAB, 0, tcell.ModNone), func(p tview.Primitive) {})

	if got := sqlEditor.GetText(); got != "users" {
		t.Fatalf("GetText() = %q, want users", got)
	}
}

func TestSQLEditorAutoTriggersOnDotForQualifiedColumns(t *testing.T) {
	t.Parallel()

	sqlEditor := NewSQLEditor().SetCompletionProvider(func(force bool, text string, cursor int) (editor.CompletionContext, []editor.CompletionItem, error) {
		return editor.CompletionContext{
				Start:     len(text),
				End:       len(text),
				Prefix:    "",
				Qualifier: "users",
			}, []editor.CompletionItem{
				{Label: "id", Insert: "id", Kind: "column"},
				{Label: "name", Insert: "name", Kind: "column"},
			}, nil
	})

	sqlEditor.SetText("users.", true)
	if !sqlEditor.popupVisible {
		t.Fatalf("expected autocomplete popup to auto-open after typing the qualifier dot")
	}

	// Ctrl+Space should still be a no-op accept (already visible).
	handled := sqlEditor.HandleAutocompleteKey(tcell.NewEventKey(tcell.KeyCtrlSpace, 0, tcell.ModNone))
	if !handled {
		t.Fatalf("expected ctrl+space to be handled")
	}
	if !sqlEditor.popupVisible {
		t.Fatalf("expected autocomplete popup to remain visible after explicit trigger")
	}
}

func TestSQLEditorEnterAcceptsAutocompleteWhenPopupVisible(t *testing.T) {
	t.Parallel()

	sqlEditor := NewSQLEditor().SetCompletionProvider(func(force bool, text string, cursor int) (editor.CompletionContext, []editor.CompletionItem, error) {
		return editor.CompletionContext{
				Start:  0,
				End:    len(text),
				Prefix: text,
			}, []editor.CompletionItem{
				{Label: "users", Insert: "users", Kind: "table"},
			}, nil
	})

	sqlEditor.SetText("us", true)
	if !sqlEditor.popupVisible {
		t.Fatalf("expected popup to be visible")
	}

	handler := sqlEditor.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(p tview.Primitive) {})

	if got := sqlEditor.GetText(); got != "users" {
		t.Fatalf("GetText() = %q, want users", got)
	}
	if sqlEditor.popupVisible {
		t.Fatalf("expected popup to close after accepting via enter")
	}
}

func TestSQLEditorShiftTabOutdentsCurrentLine(t *testing.T) {
	t.Parallel()

	sqlEditor := NewSQLEditor()
	sqlEditor.SetText("    SELECT\n        name", false)
	sqlEditor.Select(len("    SELECT\n        name"), len("    SELECT\n        name"))

	handled := sqlEditor.HandleAutocompleteKey(tcell.NewEventKey(tcell.KeyBacktab, 0, tcell.ModShift))
	if !handled {
		t.Fatalf("expected shift+tab to be handled")
	}
	if got := sqlEditor.GetText(); got != "    SELECT\n    name" {
		t.Fatalf("GetText() = %q, want outdented current line", got)
	}
}

func TestSQLEditorInputHandlerShiftTabOutdentsCurrentLine(t *testing.T) {
	t.Parallel()

	sqlEditor := NewSQLEditor()
	sqlEditor.SetText("    SELECT\n        name", false)
	sqlEditor.Select(len("    SELECT\n        name"), len("    SELECT\n        name"))

	handler := sqlEditor.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyBacktab, 0, tcell.ModShift), func(p tview.Primitive) {})

	if got := sqlEditor.GetText(); got != "    SELECT\n    name" {
		t.Fatalf("GetText() = %q, want outdented current line", got)
	}
}

func TestSQLEditorInputHandlerShiftTabAsTabWithModifierOutdentsCurrentLine(t *testing.T) {
	t.Parallel()

	sqlEditor := NewSQLEditor()
	sqlEditor.SetText("SELECT \n    *\n    ", false)
	sqlEditor.Select(len("SELECT \n    *\n    "), len("SELECT \n    *\n    "))

	handler := sqlEditor.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyTAB, 0, tcell.ModShift), func(p tview.Primitive) {})

	if got := sqlEditor.GetText(); got != "SELECT \n    *\n" {
		t.Fatalf("GetText() = %q, want trailing indent removed from current line", got)
	}
}

func TestSQLEditorInputHandlerTabIndentsCurrentLineWhenAutocompleteHidden(t *testing.T) {
	t.Parallel()

	sqlEditor := NewSQLEditor()
	sqlEditor.SetText("SELECT\n*", false)
	sqlEditor.Select(len("SELECT\n*"), len("SELECT\n*"))

	handler := sqlEditor.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyTAB, 0, tcell.ModNone), func(p tview.Primitive) {})

	if got := sqlEditor.GetText(); got != "SELECT\n    *" {
		t.Fatalf("GetText() = %q, want indented current line", got)
	}
}

func TestSQLEditorInputHandlerTabIndentPreservesViewportOffset(t *testing.T) {
	t.Parallel()

	sqlEditor := NewSQLEditor()
	sqlEditor.SetText("SELECT\nFROM users\nWHERE active = 1\nORDER BY created_at\nLIMIT 10", false)
	sqlEditor.Select(len(sqlEditor.GetText()), len(sqlEditor.GetText()))
	sqlEditor.textArea.SetOffset(3, 0)

	handler := sqlEditor.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyTAB, 0, tcell.ModNone), func(p tview.Primitive) {})

	rowOffset, columnOffset := sqlEditor.textArea.GetOffset()
	if rowOffset != 3 || columnOffset != 0 {
		t.Fatalf("offset = (%d, %d), want (3, 0)", rowOffset, columnOffset)
	}
}

func TestSQLEditorAltBracketIndentAndOutdentCurrentLine(t *testing.T) {
	t.Parallel()

	sqlEditor := NewSQLEditor()
	sqlEditor.SetText("SELECT\n*", false)
	sqlEditor.Select(len("SELECT\n*"), len("SELECT\n*"))

	handler := sqlEditor.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyRune, ']', tcell.ModAlt), func(p tview.Primitive) {})
	if got := sqlEditor.GetText(); got != "SELECT\n    *" {
		t.Fatalf("after indent GetText() = %q, want indented current line", got)
	}

	handler(tcell.NewEventKey(tcell.KeyRune, '[', tcell.ModAlt), func(p tview.Primitive) {})
	if got := sqlEditor.GetText(); got != "SELECT\n*" {
		t.Fatalf("after outdent GetText() = %q, want original current line", got)
	}
}

func TestIndentTextDoesNotBleedPastTrailingNewlineSelection(t *testing.T) {
	t.Parallel()

	// Selection covers "a,\nb,\n" (lines 0..1 plus trailing newline). The
	// caret sits at the start of line 2, so line 2 ("c") must NOT be
	// indented.
	text := "a,\nb,\nc"
	start := 0
	end := len("a,\nb,\n")

	updated, nextStart, nextEnd := indentText(text, start, end, true)
	want := "    a,\n    b,\nc"
	if updated != want {
		t.Fatalf("indentText() = %q, want %q", updated, want)
	}
	if nextStart != start+4 {
		t.Fatalf("nextStart = %d, want %d", nextStart, start+4)
	}
	// 2 lines indented * 4 spaces = 8 added before nextEnd
	if nextEnd != end+8 {
		t.Fatalf("nextEnd = %d, want %d", nextEnd, end+8)
	}
}

func TestOutdentTextDoesNotBleedPastTrailingNewlineSelection(t *testing.T) {
	t.Parallel()

	text := "    a,\n    b,\n    c"
	start := 0
	end := len("    a,\n    b,\n")

	updated, _, _ := outdentText(text, start, end, true)
	want := "a,\nb,\n    c"
	if updated != want {
		t.Fatalf("outdentText() = %q, want %q", updated, want)
	}
}

func TestSQLEditorSharedBufferSynchronizesTextAndSelection(t *testing.T) {
	t.Parallel()

	buffer := NewSQLEditorBuffer()
	mainEditor := NewSQLEditor().SetBuffer(buffer)
	studioEditor := NewSQLEditor().SetBuffer(buffer).SetRenderModeHighlighted(true)

	mainEditor.SetText("SELECT\n    name\nFROM users", false)
	mainEditor.Select(len("SELECT\n    name\n"), len("SELECT\n    name\nFROM"))

	if got := studioEditor.GetText(); got != "SELECT\n    name\nFROM users" {
		t.Fatalf("studio GetText() = %q", got)
	}

	_, start, end := studioEditor.GetSelection()
	if start != len("SELECT\n    name\n") || end != len("SELECT\n    name\nFROM") {
		t.Fatalf("studio selection = (%d, %d), want synchronized selection", start, end)
	}

	studioEditor.Replace(0, len("SELECT"), "select")
	if got := mainEditor.GetText(); got != "select\n    name\nFROM users" {
		t.Fatalf("main GetText() = %q, want shared buffer update", got)
	}
}
