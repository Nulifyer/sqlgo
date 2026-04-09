package tui

// Basic ANSI SGR foreground/background codes. Using these (instead of
// 256-color indices) lets the user's terminal theme pick the actual RGB
// values, so sqlgo blends into whichever color scheme the terminal is
// configured with.
const (
	// Foreground.
	ansiDefault     = 39 // reset fg to terminal default
	ansiBrightBlack = 90 // usually rendered as grey
	ansiBrightCyan  = 96

	// Background. Parallel to the fg codes but offset by 10.
	ansiDefaultBG = 49 // reset bg to terminal default
)

// Theme is the indirection seam for colors. Each field is a Style -- the
// fg/bg/attrs bundle layers draw with -- so re-skinning is a matter of
// swapping one struct. The concrete default theme is defaultTheme below;
// Phase 2+ will load a user-authored one from disk.
//
// Only the roles the TUI currently consumes are listed. Add to this as
// widgets start wanting their own semantic styles (syntax highlight
// categories, selection, diff add/remove, etc).
type Theme struct {
	BorderFocused   Style
	BorderUnfocused Style
	TitleFocused    Style
	TitleUnfocused  Style
	StatusBar       Style
	Selection       Style // e.g. explorer cursor row, picker highlight
}

// defaultTheme is the out-of-the-box color scheme. Foregrounds track the
// pre-Phase-1.9 constants so the visual output is unchanged; backgrounds
// stay at terminal default so we don't clobber the user's background.
var defaultTheme = Theme{
	BorderFocused:   Style{FG: ansiBrightCyan, BG: ansiDefaultBG},
	BorderUnfocused: Style{FG: ansiBrightBlack, BG: ansiDefaultBG},
	TitleFocused:    Style{FG: ansiBrightCyan, BG: ansiDefaultBG},
	TitleUnfocused:  Style{FG: ansiDefault, BG: ansiDefaultBG},
	StatusBar:       Style{FG: ansiBrightBlack, BG: ansiDefaultBG},
	Selection:       Style{FG: ansiBrightCyan, BG: ansiDefaultBG},
}

// currentTheme is the theme consulted by widgets. Swapping this is how a
// Phase-2 "theme picker" will re-skin the UI without touching widget
// code.
var currentTheme = defaultTheme

// Legacy semantic role constants. New code should pull Styles from
// currentTheme; these are kept so existing setFg(colorX) call sites
// continue to work while the widgets migrate.
const (
	colorBorderFocused   = ansiBrightCyan
	colorBorderUnfocused = ansiBrightBlack
	colorTitleFocused    = ansiBrightCyan
	colorTitleUnfocused  = ansiDefault
	colorStatusBar       = ansiBrightBlack
)
