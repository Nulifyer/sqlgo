package term

import (
	"fmt"
	"io"
)

// Minimal ANSI/VT escape primitives. Everything draws by writing strings to
// an io.Writer; no global state. Coordinates are 1-based to match VT.

const (
	esc = "\x1b"
	csi = esc + "["

	CursorHide   = csi + "?25l"
	CursorShow   = csi + "?25h"
	AltScreenOn  = csi + "?1049h"
	AltScreenOff = csi + "?1049l"
	ResetStyle   = csi + "0m"

	// SGR mouse reporting: ?1002 = button-event tracking (press, release,
	// and motion only while a button is held -- no idle-motion flood),
	// ?1006 = SGR extended encoding (decimal coordinates, avoids the
	// 223-column cap of the legacy X10 encoding). Button-event mode is
	// required for drag-to-select in the editor; the decoder already
	// preserves the button on motion events.
	MouseOn  = csi + "?1002;1006h"
	MouseOff = csi + "?1002;1006l"

	// Bracketed paste: terminal brackets pasted text with ESC[200~ and
	// ESC[201~ so the input parser can deliver it as one PasteMsg
	// instead of a flood of key events.
	PasteOn  = csi + "?2004h"
	PasteOff = csi + "?2004l"
)

// MoveTo positions the cursor (1-based row, col).
func MoveTo(w io.Writer, row, col int) {
	fmt.Fprintf(w, "%s%d;%dH", csi, row, col)
}

// fgColor sets the foreground via a raw SGR code. Use the Ansi* constants
// in theme.go (e.g. AnsiBrightCyan). This emits basic/bright ANSI colors
// (30-37, 90-97) so the terminal's configured palette is used, not a fixed
// 256-color index.
func fgColor(w io.Writer, code int) {
	fmt.Fprintf(w, "%s%dm", csi, code)
}

// bgColor sets the background via a raw SGR code (40-47 / 100-107 / 49
// for default). Kept parallel to fgColor so the emit path can track fg
// and bg independently in the flush diff loop.
func bgColor(w io.Writer, code int) {
	fmt.Fprintf(w, "%s%dm", csi, code)
}

// setAttrs emits the SGR codes for the given attribute bitmask. Callers
// must first emit ResetStyle (and re-emit any fg/bg they still want) if
// transitioning from a cell with different attrs: SGR has no "turn off
// just bold" code that's portable across terminals, so we reset and
// rebuild. The flush loop already does that when attrs changes.
func setAttrs(w io.Writer, a CellAttrs) {
	if a&AttrBold != 0 {
		fmt.Fprintf(w, "%s1m", csi)
	}
	if a&AttrUnderline != 0 {
		fmt.Fprintf(w, "%s4m", csi)
	}
	if a&AttrReverse != 0 {
		fmt.Fprintf(w, "%s7m", csi)
	}
}

// moveTo is the internal alias used by screen.go within the package.
func moveTo(w io.Writer, row, col int) { MoveTo(w, row, col) }
