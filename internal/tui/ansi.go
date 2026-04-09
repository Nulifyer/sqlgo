package tui

import (
	"fmt"
	"io"
)

// Minimal ANSI/VT escape primitives. Everything draws by writing strings to
// an io.Writer; no global state. Coordinates are 1-based to match VT.

const (
	esc = "\x1b"
	csi = esc + "["

	cursorHide   = csi + "?25l"
	cursorShow   = csi + "?25h"
	altScreenOn  = csi + "?1049h"
	altScreenOff = csi + "?1049l"
	resetStyle   = csi + "0m"
)

// moveTo positions the cursor (1-based row, col).
func moveTo(w io.Writer, row, col int) {
	fmt.Fprintf(w, "%s%d;%dH", csi, row, col)
}

// fgColor sets the foreground via a raw SGR code. Use the ansi* constants
// in theme.go (e.g. ansiBrightCyan). This emits basic/bright ANSI colors
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
// must first emit resetStyle (and re-emit any fg/bg they still want) if
// transitioning from a cell with different attrs: SGR has no "turn off
// just bold" code that's portable across terminals, so we reset and
// rebuild. The flush loop already does that when attrs changes.
func setAttrs(w io.Writer, a cellAttrs) {
	if a&attrBold != 0 {
		fmt.Fprintf(w, "%s1m", csi)
	}
	if a&attrUnderline != 0 {
		fmt.Fprintf(w, "%s4m", csi)
	}
	if a&attrReverse != 0 {
		fmt.Fprintf(w, "%s7m", csi)
	}
}
