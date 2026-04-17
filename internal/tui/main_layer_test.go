package tui

import "testing"

func TestNewTabInheritsActiveCatalog(t *testing.T) {
	t.Parallel()
	m := newMainLayer()
	m.session.activeCatalog = "analytics"

	m.newTab()

	if got := m.session.activeCatalog; got != "analytics" {
		t.Fatalf("new tab activeCatalog = %q, want %q", got, "analytics")
	}
	if len(m.sessions) != 2 {
		t.Fatalf("sessions len = %d, want 2", len(m.sessions))
	}
	if got := m.sessions[1].activeCatalog; got != "analytics" {
		t.Fatalf("sessions[1].activeCatalog = %q, want %q", got, "analytics")
	}
}
