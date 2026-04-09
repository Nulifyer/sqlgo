package clipboard

import (
	"errors"
	"testing"
)

func TestMemoryRoundTrip(t *testing.T) {
	t.Parallel()
	c := NewMemory()
	if err := c.Copy("hello"); err != nil {
		t.Fatalf("copy: %v", err)
	}
	got, err := c.Paste()
	if err != nil {
		t.Fatalf("paste: %v", err)
	}
	if got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

func TestMemoryOverwrites(t *testing.T) {
	t.Parallel()
	c := NewMemory()
	_ = c.Copy("first")
	_ = c.Copy("second")
	got, _ := c.Paste()
	if got != "second" {
		t.Errorf("got %q, want %q", got, "second")
	}
}

func TestSystemIsClipboardInterface(t *testing.T) {
	// Sanity: the concrete systemClipboard satisfies Clipboard.
	var _ Clipboard = System()
}

// TestSystemCopyHeadless covers the CI case where the system clipboard
// isn't wired up: either Copy succeeds (dev machine) or returns
// ErrUnsupported. Any other error is a bug in mapErr.
func TestSystemCopyHeadless(t *testing.T) {
	err := System().Copy("probe")
	if err != nil && !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unexpected error from Copy: %v", err)
	}
}
