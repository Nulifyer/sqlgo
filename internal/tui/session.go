package tui

import (
	"context"
	"fmt"
	"time"
)

// resultTab is one tab in the Results pane. A simple single-result query
// produces one tab; a multi-statement batch whose driver supports
// NextResultSet() produces one tab per result set. Each tab owns the row
// buffer (*table) and the summary fields surfaced on the Results border.
type resultTab struct {
	title string
	table *table

	// Last-query summary. lastHasResult is the gate: zero on startup /
	// after a disconnect so no stale "0 rows / 0ms" shows up before any
	// query.
	lastRowCount  int
	lastColCount  int
	lastElapsed   time.Duration
	lastHasResult bool
	lastCapped    bool
	lastCapReason string
	lastErr       string
	lastErrLine   int

	// resultsErrScroll is the top-line offset into the wrapped error text
	// when lastErr is rendered in place of the table. Reset when a new
	// query starts.
	resultsErrScroll int
}

func newResultTab(title string) *resultTab {
	return &resultTab{title: title, table: newTable()}
}

// session owns the per-query-tab state that will eventually be swapped when
// the user switches between query tabs: the editor buffer and the list of
// result tabs produced by the last run.
//
// The active *resultTab is embedded so m.table / m.lastErr / etc. resolve
// via promoted fields without touching the ~136 call sites. Tab switching
// swaps this pointer. results is the ordered list of tabs for the current
// query; activeResult indexes into it.
type session struct {
	// title is the label shown on the query tab strip ("Query 1",
	// "Query 2", ...). Auto-generated on new tab; not user-editable yet.
	title string

	editor *editor
	*resultTab

	results      []*resultTab
	activeResult int

	// Per-session query runner state. Moved off the app so a long
	// query in one tab does not block the Run action in another. The
	// *sql.DB pool underlying every adapter is already goroutine-safe,
	// so parallel queries just need independent cancel handles.
	running        bool
	cancel         context.CancelFunc
	lastQuerySQL   string
	lastQueryStart time.Time

	// status is the transient feedback line shown in the Results
	// border ("running…", "3 row(s) in 12ms"). Per-session so each
	// tab remembers its own last message when the user switches.
	status string

	// preview marks a tab opened by an Explorer activation (SELECT-
	// from-table scaffold). Preview tabs are reused when another
	// table is previewed and are promoted to a permanent tab on the
	// first real edit. Mirrors VSCode's single-click preview pane.
	preview bool
}

func newSession() *session {
	tab := newResultTab("Result 1")
	return &session{
		title:        "Query 1",
		editor:       newEditor(),
		resultTab:    tab,
		results:      []*resultTab{tab},
		activeResult: 0,
	}
}

// resetResults replaces the tab list with a single fresh "Result 1" tab
// and activates it. Called at the start of a query run and on disconnect.
func (s *session) resetResults() {
	tab := newResultTab("Result 1")
	s.results = []*resultTab{tab}
	s.activeResult = 0
	s.resultTab = tab
}

// appendResultTab adds a new tab to the list and activates it. The goroutine
// uses this via an evtResultSetStart event so tab creation stays on the
// main loop.
func (s *session) appendResultTab(tab *resultTab) {
	s.results = append(s.results, tab)
	s.activeResult = len(s.results) - 1
	s.resultTab = tab
}

// switchResult activates the tab at idx (clamped). No-op if the index is
// out of range.
func (s *session) switchResult(idx int) {
	if idx < 0 || idx >= len(s.results) {
		return
	}
	s.activeResult = idx
	s.resultTab = s.results[idx]
}

// resultTabTitle formats a tab's bar label. Prefixes with "*" if the tab
// holds an error so the user sees failed sets at a glance.
func resultTabTitle(t *resultTab) string {
	if t.lastErr != "" && t.lastErr != "cancelled" {
		return "! " + t.title
	}
	return t.title
}

// nextResultTitle returns the title to use for the Nth new result tab in
// a multi-statement run. The 1-based index keeps the "Result 1" / "Result 2"
// naming stable across drivers.
func nextResultTitle(n int) string {
	return fmt.Sprintf("Result %d", n)
}
