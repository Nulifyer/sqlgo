package tui

import "time"

// session owns the per-tab state that will eventually be swapped when the
// user switches between query tabs: the editor buffer and the result table,
// plus the last-query summary surfaced on the results panel border.
//
// In Phase 0 there is exactly one session, embedded into mainLayer so the
// existing m.editor / m.table / m.lastErr field accesses keep working via
// Go's promoted-field rules. Later phases introduce []*session on mainLayer
// and swap the embedded pointer on tab change.
type session struct {
	editor *editor
	table  *table

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

func newSession() *session {
	return &session{
		editor: newEditor(),
		table:  newTable(),
	}
}
