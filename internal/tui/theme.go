package tui

// Basic ANSI SGR foreground codes. Using these (instead of 256-color indices)
// lets the user's terminal theme pick the actual RGB values, so sqlgo blends
// into whichever color scheme the terminal is configured with.
const (
	ansiDefault     = 39 // reset fg to terminal default
	ansiBrightBlack = 90 // usually rendered as grey
	ansiBrightCyan  = 96
)

// Semantic theme roles. Swap the right-hand side to re-skin the UI.
const (
	colorBorderFocused   = ansiBrightCyan
	colorBorderUnfocused = ansiBrightBlack
	colorTitleFocused    = ansiBrightCyan
	colorTitleUnfocused  = ansiDefault
	colorStatusBar       = ansiBrightBlack
)
