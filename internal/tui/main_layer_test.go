package tui

import "testing"

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
