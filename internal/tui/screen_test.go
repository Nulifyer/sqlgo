package tui

import (
	"bytes"
	"testing"
)

func TestIdleFrameEmitsNothing(t *testing.T) {
	// First flush draws a non-trivial frame; second flush with the
	// same composited content should write zero bytes thanks to the
	// framesEqual fast-path.
	var buf bytes.Buffer
	s := newScreen(&buf, 10, 3)

	layer := newCellbuf(10, 3)
	layer.writeStyled(1, 1, "hello", defaultStyle())
	s.composite([]*cellbuf{layer})
	if err := s.flush(); err != nil {
		t.Fatalf("flush 1: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatalf("first flush wrote nothing")
	}

	buf.Reset()
	layer2 := newCellbuf(10, 3)
	layer2.writeStyled(1, 1, "hello", defaultStyle())
	s.composite([]*cellbuf{layer2})
	if err := s.flush(); err != nil {
		t.Fatalf("flush 2: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("idle frame wrote %d bytes (%q), want 0", buf.Len(), buf.String())
	}
}

func TestApplyViewEmitsDeltasOnly(t *testing.T) {
	var buf bytes.Buffer
	s := newScreen(&buf, 10, 3)

	if err := s.applyView(View{AltScreen: true}); err != nil {
		t.Fatalf("apply 1: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte(altScreenOn)) {
		t.Errorf("first applyView did not emit altScreenOn: %q", buf.String())
	}

	buf.Reset()
	if err := s.applyView(View{AltScreen: true}); err != nil {
		t.Fatalf("apply 2: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("identical applyView wrote %d bytes, want 0", buf.Len())
	}

	buf.Reset()
	if err := s.applyView(View{AltScreen: true, PasteEnabled: true}); err != nil {
		t.Fatalf("apply 3: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte(pasteOn)) {
		t.Errorf("paste flip did not emit pasteOn: %q", buf.String())
	}
	if bytes.Contains(buf.Bytes(), []byte(altScreenOn)) {
		t.Errorf("unchanged AltScreen flag re-emitted altScreenOn: %q", buf.String())
	}
}
