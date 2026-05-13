package tui

import (
	"strings"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/clipboard"
	"github.com/Nulifyer/sqlgo/internal/config"
	"github.com/Nulifyer/sqlgo/internal/db"
)

func TestNewMainLayerStartsWithoutVisibleTabs(t *testing.T) {
	t.Parallel()
	m := newMainLayer()

	if len(m.sessions) != 0 {
		t.Fatalf("sessions len = %d, want 0", len(m.sessions))
	}
	if m.activeTab != -1 {
		t.Fatalf("activeTab = %d, want -1", m.activeTab)
	}
	if m.session == nil || m.editor == nil {
		t.Fatal("detached query frame was not initialized")
	}
	if got := m.queryRightInfo(); got != "" {
		t.Fatalf("queryRightInfo() = %q, want empty", got)
	}
}

func TestNewTabInheritsActiveCatalog(t *testing.T) {
	t.Parallel()
	m := newMainLayer()
	m.session.activeCatalog = "analytics"

	m.newTab()

	if got := m.session.activeCatalog; got != "analytics" {
		t.Fatalf("new tab activeCatalog = %q, want %q", got, "analytics")
	}
	if len(m.sessions) != 1 {
		t.Fatalf("sessions len = %d, want 1", len(m.sessions))
	}
	if got := m.sessions[0].activeCatalog; got != "analytics" {
		t.Fatalf("sessions[0].activeCatalog = %q, want %q", got, "analytics")
	}
}

func TestCloseLastTabLeavesDetachedFrame(t *testing.T) {
	t.Parallel()
	m := newMainLayer()
	m.session.activeCatalog = "analytics"
	m.newTab()

	m.closeTab(0)

	if len(m.sessions) != 0 {
		t.Fatalf("sessions len = %d, want 0", len(m.sessions))
	}
	if m.activeTab != -1 {
		t.Fatalf("activeTab = %d, want -1", m.activeTab)
	}
	if m.session == nil {
		t.Fatal("detached frame missing after closing last tab")
	}
	if got := m.session.activeCatalog; got != "analytics" {
		t.Fatalf("detached activeCatalog = %q, want %q", got, "analytics")
	}
}

func TestEnsureActiveTabPromotesDetachedFrame(t *testing.T) {
	t.Parallel()
	m := newMainLayer()
	m.session.activeCatalog = "warehouse"
	m.editor.buf.SetText("select 1")

	sess := m.ensureActiveTab()

	if len(m.sessions) != 1 {
		t.Fatalf("sessions len = %d, want 1", len(m.sessions))
	}
	if m.activeTab != 0 {
		t.Fatalf("activeTab = %d, want 0", m.activeTab)
	}
	if sess != m.session {
		t.Fatal("active session mismatch after promotion")
	}
	if got := sess.title; got != "Query 1" {
		t.Fatalf("title = %q, want %q", got, "Query 1")
	}
	if got := sess.activeCatalog; got != "warehouse" {
		t.Fatalf("activeCatalog = %q, want %q", got, "warehouse")
	}
	if got := sess.editor.buf.Text(); got != "select 1" {
		t.Fatalf("buffer = %q, want %q", got, "select 1")
	}
}

func TestEditorPasteSanitizesControlsAndPreservesTabs(t *testing.T) {
	t.Parallel()

	m := newMainLayer()
	a := &app{layers: []Layer{m}}
	m.focus = FocusQuery
	m.HandleInput(a, PasteMsg{Text: "a\tb\x00\x1f\x7f\u0085\nc"})

	if got, want := m.editor.buf.Text(), "a\tb\nc"; got != want {
		t.Fatalf("buffer = %q, want %q", got, want)
	}
}

func TestEditorTabDisplayUsesTabStops(t *testing.T) {
	t.Parallel()

	if text, width := editorDisplayRuneAt('\t', 0); text != "    " || width != 4 {
		t.Fatalf("tab at col 0 = %q/%d, want four spaces/4", text, width)
	}
	if text, width := editorDisplayRuneAt('\t', 3); text != " " || width != 1 {
		t.Fatalf("tab at col 3 = %q/%d, want one space/1", text, width)
	}

	line := []rune("a\tb")
	if got, ok := screenColForRune(line, 0, 2); !ok || got != 4 {
		t.Fatalf("screenColForRune after tab = %d/%v, want 4/true", got, ok)
	}
}

func TestEditorCaretFromScreenUsesTabStops(t *testing.T) {
	t.Parallel()

	e := newEditor()
	e.buf.SetText("a\tb")
	r := rect{Row: 1, Col: 1, W: 20, H: 5}
	bodyCol := r.Col + 1 + e.gutterWidth()

	row, col, ok := e.caretFromScreen(r, 2, bodyCol+1)
	if !ok || row != 0 || col != 1 {
		t.Fatalf("caret in tab span = row %d col %d ok %v, want 0/1/true", row, col, ok)
	}
	row, col, ok = e.caretFromScreen(r, 2, bodyCol+4)
	if !ok || row != 0 || col != 2 {
		t.Fatalf("caret after tab span = row %d col %d ok %v, want 0/2/true", row, col, ok)
	}
}

func TestEditorEnsureCursorVisibleUsesTabStops(t *testing.T) {
	t.Parallel()

	e := newEditor()
	text := strings.Repeat("a\t", 40) + "tail"
	e.buf.SetText(text)
	e.buf.SetCursor(0, len([]rune(text)))
	e.ensureCursorVisible(12, 5)

	line := e.buf.Line(0)
	cursorOut, ok := screenColForRune(line, e.scrollCol, len(line))
	if !ok || cursorOut >= 12 {
		t.Fatalf("cursor screen col = %d/%v, want visible within width 12; scrollCol=%d", cursorOut, ok, e.scrollCol)
	}
	if e.scrollCol == 0 {
		t.Fatal("scrollCol = 0, want horizontal scroll for long tabbed line")
	}
}

func TestExplorerSearchPasteRoutesThroughMainLayer(t *testing.T) {
	t.Parallel()

	m := newMainLayer()
	a := &app{layers: []Layer{m}}
	m.explorer.SetSchema(fixtureSchema(), db.SchemaDepthSchemas)
	m.focus = FocusExplorer
	m.explorer.ActivateSearch()
	m.explorer.cursor = 1
	m.explorer.scroll = 1

	if !m.HandleInput(a, PasteMsg{Text: "hr\t\n\x00\x1f\x7f\u0085"}) {
		t.Fatal("HandleInput returned false, want paste consumed by explorer search")
	}
	if got, want := m.explorer.searchInput.String(), "hr "; got != want {
		t.Fatalf("search input = %q, want %q", got, want)
	}
	if m.explorer.cursor != 0 || m.explorer.scroll != 0 {
		t.Fatalf("cursor/scroll = %d/%d, want 0/0", m.explorer.cursor, m.explorer.scroll)
	}
	gotLabels := make([]string, len(m.explorer.items))
	for i, it := range m.explorer.items {
		gotLabels[i] = it.label
	}
	wantLabels := []string{"hr", "Tables", "employees"}
	if strings.Join(gotLabels, ",") != strings.Join(wantLabels, ",") {
		t.Fatalf("filtered labels = %v, want %v", gotLabels, wantLabels)
	}
}

func TestQueryIndicatorsPromptForDatabaseWhenUnpinned(t *testing.T) {
	t.Parallel()

	m := newMainLayer()
	m.explorer.SetDatabases([]string{"SqlgoA", "SqlgoB"})
	sess := m.ensureActiveTab()

	if !m.sessionNeedsDatabase(sess) {
		t.Fatal("sessionNeedsDatabase = false, want true")
	}
	if got := queryTabLabel(sess, true, m.sessionNeedsDatabase(sess)); !strings.Contains(got, "(!)") || strings.Contains(got, "select DB") {
		t.Fatalf("queryTabLabel() = %q, want compact DB indicator", got)
	}
	if got := m.queryRightInfo(); strings.Contains(got, "Select DB") || !strings.Contains(got, "Ln 1, Col 1") {
		t.Fatalf("queryRightInfo() = %q, want plain cursor position", got)
	}
	m.focus = FocusQuery
	a := &app{activeConn: &config.Connection{Name: "local"}, layers: []Layer{m}}
	if got := m.Hints(a); !strings.Contains(got, "Alt+D=select DB") {
		t.Fatalf("Hints() = %q, want select DB hint", got)
	}
	if got := m.explorerTitle(a); !strings.Contains(got, "[select DB]") {
		t.Fatalf("explorerTitle() = %q, want select DB indicator", got)
	}
}

func TestQueryIndicatorsDoNotPromptWhenDatabasePinned(t *testing.T) {
	t.Parallel()

	m := newMainLayer()
	m.explorer.SetDatabases([]string{"SqlgoA", "SqlgoB"})
	sess := m.ensureActiveTab()
	sess.activeCatalog = "SqlgoA"

	if m.sessionNeedsDatabase(sess) {
		t.Fatal("sessionNeedsDatabase = true, want false")
	}
	if got := queryTabLabel(sess, true, m.sessionNeedsDatabase(sess)); !strings.Contains(got, "(SqlgoA)") || strings.Contains(got, "(!)") {
		t.Fatalf("queryTabLabel() = %q, want pinned DB without select prompt", got)
	}
	m.focus = FocusQuery
	a := &app{layers: []Layer{m}}
	if got := m.Hints(a); strings.Contains(got, "select DB") {
		t.Fatalf("Hints() = %q, want no select DB hint", got)
	}
}

func TestQueryIndicatorsDoNotPromptForSingleDatabase(t *testing.T) {
	t.Parallel()

	m := newMainLayer()
	m.explorer.SetDatabases([]string{"SqlgoA"})
	sess := m.ensureActiveTab()

	if m.sessionNeedsDatabase(sess) {
		t.Fatal("sessionNeedsDatabase = true, want false")
	}
}

func TestCtrlCCancelsOnlyFromResultsFocus(t *testing.T) {
	t.Parallel()
	m := newMainLayer()
	a := &app{layers: []Layer{m}}
	sess := m.ensureActiveTab()
	sess.running = true
	cancelled := false
	sess.cancel = func() { cancelled = true }
	m.focus = FocusResults

	m.HandleKey(a, Key{Kind: KeyRune, Rune: 'c', Ctrl: true})

	if !cancelled {
		t.Fatal("Ctrl+C should cancel when Results is focused")
	}
	if got := sess.status; got != "cancelling…" {
		t.Fatalf("status = %q, want %q", got, "cancelling…")
	}
}

func TestCtrlCDoesNotCancelFromQueryFocus(t *testing.T) {
	t.Parallel()
	m := newMainLayer()
	a := &app{layers: []Layer{m}}
	sess := m.ensureActiveTab()
	sess.running = true
	cancelled := false
	sess.cancel = func() { cancelled = true }
	m.focus = FocusQuery
	m.editor.buf.SetText("select 1")
	m.editor.buf.SetCursor(0, 0)
	m.editor.buf.SetAnchor(0, 0)
	m.editor.buf.SetCursor(0, len(m.editor.buf.Line(0)))

	m.HandleKey(a, Key{Kind: KeyRune, Rune: 'c', Ctrl: true})

	if cancelled {
		t.Fatal("Ctrl+C should not cancel when Query is focused")
	}
	if got := sess.status; got != "" {
		t.Fatalf("status = %q, want empty", got)
	}
}

func TestCopyAllResultsIncludesHeaders(t *testing.T) {
	t.Parallel()
	m := newMainLayer()
	clip := clipboard.NewMemory()
	a := &app{clipboard: clip}
	feedRows(m.table,
		[]db.Column{{Name: "id"}, {Name: "name"}},
		[][]any{{int64(1), "alice"}},
	)

	m.copyAllResultsTSV(a)

	got, err := clip.Paste()
	if err != nil {
		t.Fatalf("clipboard paste: %v", err)
	}
	const want = "id\tname\n1\talice\n"
	if got != want {
		t.Fatalf("copied TSV = %q, want %q", got, want)
	}
}

func TestCopyAllResultsTSVFlattensUnsafeCellText(t *testing.T) {
	t.Parallel()
	m := newMainLayer()
	clip := clipboard.NewMemory()
	a := &app{clipboard: clip}
	feedRows(m.table,
		[]db.Column{{Name: "id"}, {Name: "notes\nraw"}},
		[][]any{{int64(1), "alpha\tbeta\r\ngamma"}},
	)

	m.copyAllResultsTSV(a)

	got, err := clip.Paste()
	if err != nil {
		t.Fatalf("clipboard paste: %v", err)
	}
	const want = "id\tnotes raw\n1\talpha beta gamma\n"
	if got != want {
		t.Fatalf("copied TSV = %q, want %q", got, want)
	}
}

func TestAltShiftACopiesMarkdownWithoutQuery(t *testing.T) {
	t.Parallel()
	m := newMainLayer()
	clip := clipboard.NewMemory()
	a := &app{clipboard: clip}
	feedRows(m.table,
		[]db.Column{{Name: "id"}, {Name: "notes"}},
		[][]any{{int64(1), "alpha\nbeta | gamma"}},
	)

	m.handleResultsKey(a, Key{Kind: KeyRune, Rune: 'A', Alt: true})

	got, err := clip.Paste()
	if err != nil {
		t.Fatalf("clipboard paste: %v", err)
	}
	if strings.Contains(got, "```sql") {
		t.Fatalf("markdown copy included query block: %q", got)
	}
	if !strings.Contains(got, "| id  | notes") || !strings.Contains(got, `alpha<br>beta \| gamma`) {
		t.Fatalf("markdown copy missing escaped table content: %q", got)
	}
}
