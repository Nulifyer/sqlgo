package tui

import (
	"testing"
)

// seedEditor builds an editor with text pre-inserted. Each element
// of lines becomes one line in the buffer.
func seedEditor(lines ...string) *editor {
	e := newEditor()
	e.buf.Clear()
	for i, ln := range lines {
		if i > 0 {
			e.buf.InsertNewline()
		}
		for _, r := range ln {
			e.buf.Insert(r)
		}
	}
	return e
}

func TestComputeMatchesCaseInsensitive(t *testing.T) {
	t.Parallel()
	e := seedEditor(
		"SELECT * FROM users",
		"WHERE users.id = 1",
		"  OR USERS.id = 2",
	)
	got := e.computeMatches("users")
	if len(got) != 3 {
		t.Fatalf("len(matches) = %d, want 3 (%+v)", len(got), got)
	}
	// Row 0, col 14 ("users" starts after "SELECT * FROM ").
	if got[0].row != 0 || got[0].col != 14 || got[0].length != 5 {
		t.Errorf("match[0] = %+v, want row 0 col 14 len 5", got[0])
	}
	if got[1].row != 1 || got[1].col != 6 {
		t.Errorf("match[1] = %+v, want row 1 col 6", got[1])
	}
	if got[2].row != 2 || got[2].col != 5 {
		t.Errorf("match[2] = %+v, want row 2 col 5", got[2])
	}
}

func TestComputeMatchesEmptyNeedleReturnsNil(t *testing.T) {
	t.Parallel()
	e := seedEditor("SELECT 1")
	if got := e.computeMatches(""); got != nil {
		t.Errorf("empty needle matches = %+v, want nil", got)
	}
}

func TestComputeMatchesNonOverlapping(t *testing.T) {
	t.Parallel()
	// "aaaa" searching for "aa" should produce 2 non-overlapping
	// matches (col 0 and col 2), not 3.
	e := seedEditor("aaaa")
	got := e.computeMatches("aa")
	if len(got) != 2 {
		t.Fatalf("len(matches) = %d, want 2 (%+v)", len(got), got)
	}
	if got[0].col != 0 || got[1].col != 2 {
		t.Errorf("matches = %+v, want cols [0 2]", got)
	}
}

func TestSetSearchSnapsToFirstMatchAtOrAfterCursor(t *testing.T) {
	t.Parallel()
	e := seedEditor(
		"foo bar foo",
		"foo baz foo",
	)
	// Park cursor at row 1 col 0 so the first "foo at or after"
	// should be row 1 col 0, not row 0 col 0.
	e.buf.MoveDown()
	e.buf.MoveHome()
	e.SetSearch("foo")
	if e.CurrentMatchIndex() != 3 {
		t.Errorf("CurrentMatchIndex = %d, want 3 (matches: %+v)", e.CurrentMatchIndex(), e.matches)
	}
}

func TestNextMatchWrapsAround(t *testing.T) {
	t.Parallel()
	e := seedEditor("a b a b a")
	e.SetSearch("a")
	if e.MatchCount() != 3 {
		t.Fatalf("MatchCount = %d, want 3", e.MatchCount())
	}
	// Cursor starts at (0,0), first match is match 1.
	if got := e.CurrentMatchIndex(); got != 1 {
		t.Errorf("initial CurrentMatchIndex = %d, want 1", got)
	}
	e.NextMatch()
	if got := e.CurrentMatchIndex(); got != 2 {
		t.Errorf("after NextMatch #1 = %d, want 2", got)
	}
	e.NextMatch()
	if got := e.CurrentMatchIndex(); got != 3 {
		t.Errorf("after NextMatch #2 = %d, want 3", got)
	}
	e.NextMatch() // wrap
	if got := e.CurrentMatchIndex(); got != 1 {
		t.Errorf("after wrap = %d, want 1", got)
	}
}

func TestPrevMatchWrapsAround(t *testing.T) {
	t.Parallel()
	e := seedEditor("x x x")
	e.SetSearch("x")
	if e.MatchCount() != 3 {
		t.Fatalf("MatchCount = %d", e.MatchCount())
	}
	// currentMatch = 0. PrevMatch wraps to the last entry.
	e.PrevMatch()
	if got := e.CurrentMatchIndex(); got != 3 {
		t.Errorf("prev wrap = %d, want 3", got)
	}
	e.PrevMatch()
	if got := e.CurrentMatchIndex(); got != 2 {
		t.Errorf("prev = %d, want 2", got)
	}
}

func TestReplaceCurrentAdvances(t *testing.T) {
	t.Parallel()
	e := seedEditor("foo bar foo baz")
	e.SetSearch("foo")
	if !e.ReplaceCurrent("XX") {
		t.Fatal("ReplaceCurrent returned false")
	}
	if got := e.buf.Text(); got != "XX bar foo baz" {
		t.Errorf("buffer = %q, want %q", got, "XX bar foo baz")
	}
	// Match count should still be 1 (just the trailing "foo").
	if e.MatchCount() != 1 {
		t.Errorf("MatchCount after first replace = %d, want 1", e.MatchCount())
	}
}

func TestReplaceAllCountsSubstitutions(t *testing.T) {
	t.Parallel()
	e := seedEditor(
		"foo foo foo",
		"  foo",
	)
	e.SetSearch("foo")
	n := e.ReplaceAll("bar")
	if n != 4 {
		t.Errorf("ReplaceAll = %d, want 4", n)
	}
	if got := e.buf.Text(); got != "bar bar bar\n  bar" {
		t.Errorf("buffer = %q, want %q", got, "bar bar bar\n  bar")
	}
}

func TestReplaceAllHandlesShorterReplacement(t *testing.T) {
	t.Parallel()
	// Replacement is shorter than the needle; walking matches in
	// reverse keeps earlier positions valid during mutation.
	e := seedEditor("abcdef abcdef")
	e.SetSearch("abcdef")
	n := e.ReplaceAll("x")
	if n != 2 {
		t.Errorf("ReplaceAll = %d, want 2", n)
	}
	if got := e.buf.Text(); got != "x x" {
		t.Errorf("buffer = %q, want %q", got, "x x")
	}
}

func TestClearSearchResets(t *testing.T) {
	t.Parallel()
	e := seedEditor("foo foo")
	e.SetSearch("foo")
	if !e.HasSearch() {
		t.Fatal("HasSearch false after SetSearch")
	}
	e.ClearSearch()
	if e.HasSearch() {
		t.Error("HasSearch true after ClearSearch")
	}
	if e.MatchCount() != 0 {
		t.Errorf("MatchCount = %d after ClearSearch", e.MatchCount())
	}
	if e.currentMatch != -1 {
		t.Errorf("currentMatch = %d, want -1", e.currentMatch)
	}
}

func TestMatchStyleAtIdentifiesCurrent(t *testing.T) {
	t.Parallel()
	e := seedEditor("foo foo foo")
	e.SetSearch("foo")
	// First match is at col 0..3 on row 0. Middle rune of match 0
	// should be "current".
	isCurrent, inMatch := e.matchStyleAt(0, 1)
	if !inMatch {
		t.Fatal("col 1 should be in a match")
	}
	if !isCurrent {
		t.Error("col 1 should be the current match")
	}
	// Middle of match 1 (col 4..7): in match but not current.
	_, inMatch = e.matchStyleAt(0, 5)
	if !inMatch {
		t.Fatal("col 5 should be in a match")
	}
	isCurrent, _ = e.matchStyleAt(0, 5)
	if isCurrent {
		t.Error("col 5 should NOT be the current match")
	}
	// Outside any match.
	_, inMatch = e.matchStyleAt(0, 3)
	if inMatch {
		t.Error("col 3 should not be in a match (space between foos)")
	}
}

// TestCtrlFOpensFindLayer drives main_layer's HandleKey with a
// Ctrl+F key while focused on the query editor and verifies the
// overlay lands on the stack.
func TestCtrlFOpensFindLayer(t *testing.T) {
	t.Parallel()
	a := &app{}
	a.layers = []Layer{newMainLayer()}
	m := a.mainLayerPtr()
	// Fake a terminal so the overlay's Draw path has something to
	// read (HandleKey itself doesn't touch term, but tests that
	// expand in the future might).
	a.term = &terminal{width: 80, height: 24}
	m.focus = FocusQuery

	m.HandleKey(a, Key{Kind: KeyRune, Rune: 'f', Ctrl: true})

	if len(a.layers) != 2 {
		t.Fatalf("layers = %d, want 2 (find overlay pushed)", len(a.layers))
	}
	if _, ok := a.layers[1].(*findLayer); !ok {
		t.Errorf("top layer = %T, want *findLayer", a.layers[1])
	}
}

// TestFindLayerEscClosesAndClears exercises the layer's own
// HandleKey path: Esc should clear search state on the editor and
// pop the layer.
func TestFindLayerEscClosesAndClears(t *testing.T) {
	t.Parallel()
	a := &app{}
	a.layers = []Layer{newMainLayer()}
	a.term = &terminal{width: 80, height: 24}
	m := a.mainLayerPtr()
	m.editor = seedEditor("foo foo")
	m.editor.SetSearch("foo")

	fl := newFindLayer("foo")
	a.pushLayer(fl)

	fl.HandleKey(a, Key{Kind: KeyEsc})

	if len(a.layers) != 1 {
		t.Errorf("layers = %d, want 1 (overlay popped)", len(a.layers))
	}
	if m.editor.HasSearch() {
		t.Error("editor search should be cleared after Esc")
	}
}

// TestFindLayerEnterReplaceActsOnReplaceField drives the two-field
// Enter-semantics: Enter in the Replace field calls ReplaceCurrent.
func TestFindLayerEnterReplaceActsOnReplaceField(t *testing.T) {
	t.Parallel()
	a := &app{}
	a.layers = []Layer{newMainLayer()}
	a.term = &terminal{width: 80, height: 24}
	m := a.mainLayerPtr()
	m.editor = seedEditor("foo bar foo")

	fl := newFindLayer("")
	a.pushLayer(fl)
	// Type "foo" into the Find field.
	for _, r := range "foo" {
		fl.HandleKey(a, Key{Kind: KeyRune, Rune: r})
	}
	if m.editor.MatchCount() != 2 {
		t.Fatalf("MatchCount after typing = %d, want 2", m.editor.MatchCount())
	}
	// Tab over to Replace, type "XX", press Enter.
	fl.HandleKey(a, Key{Kind: KeyTab})
	for _, r := range "XX" {
		fl.HandleKey(a, Key{Kind: KeyRune, Rune: r})
	}
	fl.HandleKey(a, Key{Kind: KeyEnter})

	if got := m.editor.buf.Text(); got != "XX bar foo" {
		t.Errorf("buffer = %q, want %q", got, "XX bar foo")
	}
}

// TestFindLayerCtrlRReplacesAll verifies the Ctrl+R replace-all
// shortcut lands its full substitution in one shot.
func TestFindLayerCtrlRReplacesAll(t *testing.T) {
	t.Parallel()
	a := &app{}
	a.layers = []Layer{newMainLayer()}
	a.term = &terminal{width: 80, height: 24}
	m := a.mainLayerPtr()
	m.editor = seedEditor("foo foo foo")

	fl := newFindLayer("foo")
	a.pushLayer(fl)
	// Seed matches via the same path the real UI takes on open.
	m.editor.SetSearch("foo")
	// Tab to Replace, type "X", press Ctrl+R.
	fl.HandleKey(a, Key{Kind: KeyTab})
	fl.HandleKey(a, Key{Kind: KeyRune, Rune: 'X'})
	fl.HandleKey(a, Key{Kind: KeyRune, Rune: 'r', Ctrl: true})

	if got := m.editor.buf.Text(); got != "X X X" {
		t.Errorf("buffer = %q, want %q", got, "X X X")
	}
}
