package term

import (
	"bytes"
	"testing"
)

func TestIdleFrameEmitsNothing(t *testing.T) {
	// First flush draws a non-trivial frame; second flush with the
	// same composited content should write zero bytes thanks to the
	// framesEqual fast-path.
	var buf bytes.Buffer
	s := NewScreen(&buf, 10, 3)

	layer := NewCellbuf(10, 3)
	layer.WriteStyled(1, 1, "hello", DefaultStyle())
	s.Composite([]*Cellbuf{layer})
	if err := s.Flush(); err != nil {
		t.Fatalf("flush 1: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatalf("first flush wrote nothing")
	}

	buf.Reset()
	layer2 := NewCellbuf(10, 3)
	layer2.WriteStyled(1, 1, "hello", DefaultStyle())
	s.Composite([]*Cellbuf{layer2})
	if err := s.Flush(); err != nil {
		t.Fatalf("flush 2: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("idle frame wrote %d bytes (%q), want 0", buf.Len(), buf.String())
	}
}

func TestApplyViewEmitsDeltasOnly(t *testing.T) {
	var buf bytes.Buffer
	s := NewScreen(&buf, 10, 3)

	if err := s.ApplyView(View{AltScreen: true}); err != nil {
		t.Fatalf("apply 1: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte(AltScreenOn)) {
		t.Errorf("first ApplyView did not emit AltScreenOn: %q", buf.String())
	}

	buf.Reset()
	if err := s.ApplyView(View{AltScreen: true}); err != nil {
		t.Fatalf("apply 2: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("identical ApplyView wrote %d bytes, want 0", buf.Len())
	}

	buf.Reset()
	if err := s.ApplyView(View{AltScreen: true, PasteEnabled: true}); err != nil {
		t.Fatalf("apply 3: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte(PasteOn)) {
		t.Errorf("paste flip did not emit PasteOn: %q", buf.String())
	}
	if bytes.Contains(buf.Bytes(), []byte(AltScreenOn)) {
		t.Errorf("unchanged AltScreen flag re-emitted AltScreenOn: %q", buf.String())
	}
}

func TestSanitizeWindowTitle(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"plain", "plain"},
		{"with\x1bescape", "withescape"},
		{"bel\x07end", "belend"},
		{"st\u009cend", "stend"},
		{"tab\ttab", "tabtab"},
		{"newline\nhere", "newlinehere"},
		{"del\x7fhere", "delhere"},
		{"unicode-\u03c0ok", "unicode-\u03c0ok"},
	}
	for _, tc := range cases {
		if got := sanitizeWindowTitle(tc.in); got != tc.want {
			t.Errorf("sanitizeWindowTitle(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
