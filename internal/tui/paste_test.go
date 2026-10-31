package tui

import (
	"strings"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/clipboard"
	"github.com/Nulifyer/sqlgo/internal/tui/term"
)

func TestEditorPasteRoundTrip(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, text, want string
	}{
		{"CRLF", "select 1;\r\n\r\nselect 2;\r\n", "select 1;\n\nselect 2;\n"},
		{"CR", "select 1;\rselect 2;\r", "select 1;\nselect 2;\n"},
		{"LF", "select 1;\nselect 2;\n", "select 1;\nselect 2;\n"},
		{"mixed", "a\r\nb\rc\nd", "a\nb\nc\nd"},
		{"controls", "é\t\x00\x1f\x7f\u0085\r\n漢字 ^M", "é\t\n漢字 ^M"},
	}
	for _, route := range []string{"terminal", "clipboard"} {
		t.Run(route, func(t *testing.T) {
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					m := newMainLayer()
					m.focus = FocusQuery
					clip := clipboard.NewMemory()
					a := &app{layers: []Layer{m}, clipboard: clip}
					b := m.editor.buf
					b.SetText("before OLD after")
					b.SetAnchor(0, 7)
					b.SetCursor(0, 10)
					if route == "terminal" {
						reader := term.NewKeyReader(strings.NewReader("\x1b[200~" + tt.text + "\x1b[201~"))
						msg, err := reader.Read()
						if err != nil {
							t.Fatal(err)
						}
						if !m.HandleInput(a, msg) {
							t.Fatal("terminal paste was not consumed")
						}
					} else {
						if err := clip.Copy(tt.text); err != nil {
							t.Fatal(err)
						}
						m.editor.handleInsert(a, Key{Kind: KeyRune, Rune: 'v', Ctrl: true})
					}
					want := "before " + tt.want + " after"
					if got := b.Text(); got != want {
						t.Fatalf("pasted text = %q, want %q", got, want)
					}
					if b.HasSelection() {
						t.Fatal("paste should replace selection")
					}
					lines := strings.Split("before "+tt.want, "\n")
					if row, col := b.Cursor(); row != len(lines)-1 || col != len([]rune(lines[len(lines)-1])) {
						t.Fatalf("cursor = %d/%d, want end of inserted text", row, col)
					}
					if !b.Undo() || b.Text() != "before OLD after" {
						t.Fatalf("one undo should restore replaced text, got %q", b.Text())
					}
					if !b.Redo() || b.Text() != want {
						t.Fatalf("redo should restore paste, got %q", b.Text())
					}
					b.SelectAll()
					m.editor.handleInsert(a, Key{Kind: KeyRune, Rune: 'c', Ctrl: true})
					copied, err := clip.Paste()
					if err != nil || copied != want {
						t.Fatalf("copy = %q, %v, want %q", copied, err, want)
					}
				})
			}
		})
	}
}
