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

	clearScreen = csi + "2J"
	clearLine   = csi + "2K"
	cursorHome  = csi + "H"
	cursorHide  = csi + "?25l"
	cursorShow  = csi + "?25h"
	altScreenOn = csi + "?1049h"
	altScreenOff = csi + "?1049l"
	resetStyle  = csi + "0m"
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

// reverse toggles reverse video.
func reverse(w io.Writer) {
	io.WriteString(w, csi+"7m")
}

// reset clears all SGR attributes.
func reset(w io.Writer) {
	io.WriteString(w, resetStyle)
}
