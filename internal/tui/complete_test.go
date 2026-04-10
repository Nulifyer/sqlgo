package tui

import (
	"testing"
)

func TestWordBeforeCursor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		line      string
		col       int
		wantWord  string
		wantStart int
	}{
		{"plain word", "SELECT", 6, "SELECT", 0},
		{"middle of word", "SELECT", 3, "SEL", 0},
		{"at whitespace", "SELECT ", 7, "", 7},
		{"after qualifier dot", "dbo.use", 7, "use", 4},
		{"at dot", "dbo.", 4, "", 4},
		{"empty line", "", 0, "", 0},
		{"underscore ident", "my_table", 8, "my_table", 0},
		{"ident after space", "FROM users", 10, "users", 5},
		{"col beyond length clamps", "abc", 10, "abc", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			word, start := wordBeforeCursor([]rune(tc.line), tc.col)
			if word != tc.wantWord {
				t.Errorf("word = %q, want %q", word, tc.wantWord)
			}
			if start != tc.wantStart {
				t.Errorf("start = %d, want %d", start, tc.wantStart)
			}
		})
	}
}

func TestFilterCompletionsCaseInsensitivePrefix(t *testing.T) {
	t.Parallel()
	items := []completionItem{
		{text: "SELECT", kind: completeKeyword},
		{text: "SET", kind: completeKeyword},
		{text: "FROM", kind: completeKeyword},
		{text: "sessions", kind: completeTable},
		{text: "settings", kind: completeTable},
		{text: "users", kind: completeTable},
	}
	got := filterCompletions(items, "se")
	// "se" should match SELECT, SET, sessions, settings. Tables
	// outrank keywords, so sessions + settings come first.
	wantOrder := []string{"sessions", "settings", "SELECT", "SET"}
	if len(got) != len(wantOrder) {
		t.Fatalf("len(got) = %d, want %d (%+v)", len(got), len(wantOrder), got)
	}
	for i, want := range wantOrder {
		if got[i].text != want {
			t.Errorf("[%d] = %q, want %q", i, got[i].text, want)
		}
	}
}

func TestFilterCompletionsEmptyPrefixReturnsAll(t *testing.T) {
	t.Parallel()
	items := []completionItem{
		{text: "SELECT", kind: completeKeyword},
		{text: "users", kind: completeTable},
	}
	got := filterCompletions(items, "")
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	// Table should still outrank keyword even with an empty prefix.
	if got[0].text != "users" {
		t.Errorf("got[0] = %q, want users (tables outrank keywords)", got[0].text)
	}
}

func TestFilterCompletionsNoMatch(t *testing.T) {
	t.Parallel()
	items := []completionItem{
		{text: "SELECT", kind: completeKeyword},
		{text: "users", kind: completeTable},
	}
	if got := filterCompletions(items, "zzz"); len(got) != 0 {
		t.Errorf("len(got) = %d, want 0 (%+v)", len(got), got)
	}
}

func TestCompletionStateMoveSelectionClamps(t *testing.T) {
	t.Parallel()
	cs := &completionState{
		items: []completionItem{
			{text: "a"}, {text: "b"}, {text: "c"},
		},
	}
	cs.moveSelection(-5)
	if cs.selected != 0 {
		t.Errorf("selected after overscroll up = %d, want 0", cs.selected)
	}
	cs.moveSelection(10)
	if cs.selected != 2 {
		t.Errorf("selected after overscroll down = %d, want 2", cs.selected)
	}
	cs.moveSelection(-1)
	if cs.selected != 1 {
		t.Errorf("selected after up 1 = %d, want 1", cs.selected)
	}
}

// TestEditorAcceptCompletionReplacesPrefix exercises the full popup
// flow end-to-end: seed the buffer, open a popup with a hand-built
// state, accept, and check the resulting buffer text.
func TestEditorAcceptCompletionReplacesPrefix(t *testing.T) {
	t.Parallel()
	e := newEditor()
	// Type "sel" directly into the buffer so cursor lands at col 3.
	for _, r := range "sel" {
		e.buf.Insert(r)
	}
	// Simulate the state an openCompletion call would have built:
	// prefix "sel" starting at col 0, filtered list with SELECT and
	// SESSIONS (just so we can verify selected[0] wins).
	e.complete = &completionState{
		startCol: 0,
		prefix:   "sel",
		items: []completionItem{
			{text: "SELECT", kind: completeKeyword},
		},
	}
	e.acceptCompletion()
	if got := e.buf.Text(); got != "SELECT" {
		t.Errorf("buffer = %q, want %q", got, "SELECT")
	}
	if e.complete != nil {
		t.Errorf("complete state should be cleared after accept")
	}
	// Cursor should be at end of "SELECT".
	row, col := e.buf.Cursor()
	if row != 0 || col != 6 {
		t.Errorf("cursor = (%d,%d), want (0,6)", row, col)
	}
}

// TestEditorAcceptCompletionAfterDot confirms that accepting a
// completion with a leading schema qualifier only replaces the
// identifier half. "dbo.use" with prefix "use" accepting "users"
// yields "dbo.users".
func TestEditorAcceptCompletionAfterDot(t *testing.T) {
	t.Parallel()
	e := newEditor()
	for _, r := range "dbo.use" {
		e.buf.Insert(r)
	}
	e.complete = &completionState{
		startCol: 4, // position of 'u' in "dbo.use"
		prefix:   "use",
		items: []completionItem{
			{text: "users", kind: completeTable},
		},
	}
	e.acceptCompletion()
	if got := e.buf.Text(); got != "dbo.users" {
		t.Errorf("buffer = %q, want %q", got, "dbo.users")
	}
}

// TestEditorCtrlSpaceOpensPopup drives the full handleInsert path
// with a Ctrl+Space key, verifying the popup opens with a filtered
// list that reflects the word under the cursor. We build a minimal
// app with only the fields gatherCompletions needs.
func TestEditorCtrlSpaceOpensPopup(t *testing.T) {
	t.Parallel()
	e := newEditor()
	for _, r := range "sel" {
		e.buf.Insert(r)
	}
	// gatherCompletions reads through a.mainLayerPtr().explorer.
	// newMainLayer already seeds a default buffer we don't care
	// about for this test; its explorer has no schema so only
	// keywords come back from the gather call.
	a := &app{}
	a.layers = []Layer{newMainLayer()}

	ok := e.handleInsert(a, Key{Kind: KeyRune, Rune: ' ', Ctrl: true})
	if !ok {
		t.Fatal("Ctrl+Space not consumed")
	}
	if e.complete == nil {
		t.Fatal("complete state not opened")
	}
	if e.complete.prefix != "sel" {
		t.Errorf("prefix = %q, want sel", e.complete.prefix)
	}
	// SELECT should be in the filtered list.
	found := false
	for _, it := range e.complete.items {
		if it.text == "SELECT" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("SELECT not in filtered items: %+v", e.complete.items)
	}
}

// TestEditorPopupEscClosesWithoutMutating verifies Esc dismisses
// the popup and does not touch the buffer.
func TestEditorPopupEscClosesWithoutMutating(t *testing.T) {
	t.Parallel()
	e := newEditor()
	for _, r := range "sel" {
		e.buf.Insert(r)
	}
	e.complete = &completionState{
		startCol: 0,
		prefix:   "sel",
		items:    []completionItem{{text: "SELECT"}},
	}
	ok := e.handleInsert(nil, Key{Kind: KeyEsc})
	if !ok {
		t.Fatal("Esc not consumed")
	}
	if e.complete != nil {
		t.Errorf("complete state should be cleared")
	}
	if got := e.buf.Text(); got != "sel" {
		t.Errorf("buffer = %q, want unchanged 'sel'", got)
	}
}

// TestEditorPopupAnyKeyDismissesAndFallsThrough verifies that any
// non-popup key closes the popup and still performs its normal
// editor action.
func TestEditorPopupAnyKeyDismissesAndFallsThrough(t *testing.T) {
	t.Parallel()
	e := newEditor()
	for _, r := range "sel" {
		e.buf.Insert(r)
	}
	e.complete = &completionState{
		startCol: 0,
		prefix:   "sel",
		items:    []completionItem{{text: "SELECT"}},
	}
	// Typing a plain letter should dismiss the popup AND insert.
	e.handleInsert(nil, Key{Kind: KeyRune, Rune: 'x'})
	if e.complete != nil {
		t.Errorf("complete state should be cleared after fall-through key")
	}
	if got := e.buf.Text(); got != "selx" {
		t.Errorf("buffer = %q, want 'selx'", got)
	}
}
