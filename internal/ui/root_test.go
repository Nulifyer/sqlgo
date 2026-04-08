package ui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/nulifyer/sqlgo/internal/db"
	"github.com/nulifyer/sqlgo/internal/editor"
)

func TestActiveProfileByNameFindsMatchingProfile(t *testing.T) {
	nodes := []*tview.TreeNode{
		tview.NewTreeNode("one").SetReference(db.ConnectionProfile{Name: "one"}),
		tview.NewTreeNode("two").SetReference(db.ConnectionProfile{Name: "two"}),
	}

	got := activeProfileByName("two", nodes)
	if got == nil {
		t.Fatalf("activeProfileByName() = nil, want profile")
	}
	if got.Name != "two" {
		t.Fatalf("activeProfileByName().Name = %q, want two", got.Name)
	}
}

func TestActiveProfileByNameReturnsNilForMissingProfile(t *testing.T) {
	nodes := []*tview.TreeNode{
		tview.NewTreeNode("one").SetReference(db.ConnectionProfile{Name: "one"}),
	}

	if got := activeProfileByName("missing", nodes); got != nil {
		t.Fatalf("activeProfileByName() = %#v, want nil", got)
	}
}

func TestIsAutocompleteTriggerRecognizesTerminalFallbacks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		event *tcell.EventKey
		want  bool
	}{
		{
			name:  "ctrl space key",
			event: tcell.NewEventKey(tcell.KeyCtrlSpace, 0, tcell.ModNone),
			want:  true,
		},
		{
			name:  "ctrl rune 2 fallback",
			event: tcell.NewEventKey(tcell.KeyRune, '2', tcell.ModCtrl),
			want:  true,
		},
		{
			name:  "ctrl rune at fallback",
			event: tcell.NewEventKey(tcell.KeyRune, '@', tcell.ModCtrl),
			want:  true,
		},
		{
			name:  "plain 2",
			event: tcell.NewEventKey(tcell.KeyRune, '2', tcell.ModNone),
			want:  false,
		},
		{
			name:  "alt 2",
			event: tcell.NewEventKey(tcell.KeyRune, '2', tcell.ModAlt),
			want:  false,
		},
		{
			name:  "plain space",
			event: tcell.NewEventKey(tcell.KeyRune, ' ', tcell.ModNone),
			want:  false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isAutocompleteTrigger(tt.event); got != tt.want {
				t.Fatalf("isAutocompleteTrigger(%s) = %v, want %v", tt.event.Name(), got, tt.want)
			}
		})
	}
}

func TestRenderDataRowAddsTrailingPadding(t *testing.T) {
	t.Parallel()

	row := renderDataRow([]string{"alice", "active"}, []int{5, 6}, false)
	if !strings.HasSuffix(row, " ") {
		t.Fatalf("renderDataRow() = %q, want trailing padding space", row)
	}
}

func TestShouldShowAutocompleteRequiresNonBlankPrefixUnlessForced(t *testing.T) {
	t.Parallel()

	if shouldShowAutocomplete(editor.CompletionContext{Prefix: ""}, false) {
		t.Fatalf("blank prefix should not auto-open autocomplete")
	}
	if !shouldShowAutocomplete(editor.CompletionContext{Prefix: "us"}, false) {
		t.Fatalf("non-blank prefix should auto-open autocomplete")
	}
	if !shouldShowAutocomplete(editor.CompletionContext{Prefix: ""}, true) {
		t.Fatalf("forced autocomplete should open even with blank prefix")
	}
	if !shouldShowAutocomplete(editor.CompletionContext{Prefix: "", Qualifier: "users"}, false) {
		t.Fatalf("blank prefix with qualifier should auto-open autocomplete after typing dot")
	}
}

func TestShouldHideAutocompleteForExactSingleMatch(t *testing.T) {
	t.Parallel()

	items := []editor.CompletionItem{{Label: "users", Insert: "users"}}
	if !shouldHideAutocomplete(editor.CompletionContext{Prefix: "users"}, items) {
		t.Fatalf("exact single match should hide autocomplete")
	}
	if shouldHideAutocomplete(editor.CompletionContext{Prefix: "us"}, items) {
		t.Fatalf("partial single match should keep autocomplete visible")
	}
	if shouldHideAutocomplete(editor.CompletionContext{Prefix: "users"}, []editor.CompletionItem{
		{Label: "users", Insert: "users"},
		{Label: "users_archive", Insert: "users_archive"},
	}) {
		t.Fatalf("multiple matches should keep autocomplete visible")
	}
}

func TestModifierNamesFormatsModifierMask(t *testing.T) {
	t.Parallel()

	if got := modifierNames(tcell.ModNone); got != "none" {
		t.Fatalf("modifierNames(ModNone) = %q, want none", got)
	}
	if got := modifierNames(tcell.ModShift | tcell.ModCtrl); got != "Shift+Ctrl" {
		t.Fatalf("modifierNames(Shift|Ctrl) = %q, want Shift+Ctrl", got)
	}
}

func TestFormatKeyDebugEventIncludesBacktabDetails(t *testing.T) {
	t.Parallel()

	event := tcell.NewEventKey(tcell.KeyBacktab, 0, tcell.ModNone)
	got := formatKeyDebugEvent(event)
	if !strings.Contains(got, "Backtab") {
		t.Fatalf("formatKeyDebugEvent() = %q, want Backtab details", got)
	}
}

func TestLineColumnOffsetRoundTrip(t *testing.T) {
	t.Parallel()

	text := "SELECT\n    audit_logs\nWHERE\n    status = 'ok'"
	line, column := lineColumnFromOffset(text, len("SELECT\n    aud"))
	if line != 1 || column != len("    aud") {
		t.Fatalf("lineColumnFromOffset() = (%d, %d), want (1, %d)", line, column, len("    aud"))
	}

	offset := offsetForLineColumn(text, line, column)
	if offset != len("SELECT\n    aud") {
		t.Fatalf("offsetForLineColumn() = %d, want %d", offset, len("SELECT\n    aud"))
	}
}
