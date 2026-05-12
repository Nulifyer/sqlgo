//go:build windows

package tui

import "testing"

func TestWrapPasteBatchPassesThroughOnlyNativePasteStart(t *testing.T) {
	t.Parallel()

	native := bracketedPasteStart + "SELECT 1" + bracketedPasteEnd
	if got := string(wrapPasteBatch(native)); got != native {
		t.Fatalf("native paste batch = %q, want unchanged %q", got, native)
	}

	const csiPayload = "\x1b[A"
	want := bracketedPasteStart + csiPayload + bracketedPasteEnd
	if got := string(wrapPasteBatch(csiPayload)); got != want {
		t.Fatalf("non-paste CSI batch = %q, want wrapped %q", got, want)
	}
}
