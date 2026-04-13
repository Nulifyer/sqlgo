package tui

// Basic ANSI SGR foreground/background codes. Using these (instead of
// 256-color indices) lets the user's terminal theme pick the actual RGB
// values, so sqlgo blends into whichever color scheme the terminal is
// configured with.
const (
	// Foreground.
	ansiDefault       = 39 // reset fg to terminal default
	ansiBrightBlack   = 90 // usually rendered as grey
	ansiBrightGreen   = 92
	ansiBrightYellow  = 93
	ansiBrightBlue    = 94
	ansiBrightMagenta = 95
	ansiBrightCyan    = 96

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

	// SQL syntax highlight roles used by the query editor. Each maps
	// directly to a sqltok.Kind; Text/Ident both render with the
	// default style to preserve contrast against the terminal bg.
	SQLKeyword  Style
	SQLString   Style
	SQLNumber   Style
	SQLComment  Style
	SQLOperator Style
	SQLPunct    Style
	SQLFunction Style // ident immediately followed by '(' and its matching parens
}

// defaultTheme is the out-of-the-box color scheme. Foregrounds for the
// non-SQL roles track the pre-Phase-1.9 constants so the visual output
// of panels / borders / status bar is unchanged; SQL syntax roles are
// new in Phase 3 and pick bright ANSI colors that track the user's
// terminal palette. Backgrounds stay at terminal default so we don't
// clobber the user's background.
var defaultTheme = Theme{
	BorderFocused:   Style{FG: ansiBrightCyan, BG: ansiDefaultBG},
	BorderUnfocused: Style{FG: ansiBrightBlack, BG: ansiDefaultBG},
	TitleFocused:    Style{FG: ansiBrightCyan, BG: ansiDefaultBG},
	TitleUnfocused:  Style{FG: ansiDefault, BG: ansiDefaultBG},
	StatusBar:       Style{FG: ansiBrightBlack, BG: ansiDefaultBG},
	Selection:       Style{FG: ansiBrightCyan, BG: ansiDefaultBG},

	SQLKeyword:  Style{FG: ansiBrightBlue, BG: ansiDefaultBG, Attrs: attrBold},
	SQLString:   Style{FG: ansiBrightGreen, BG: ansiDefaultBG},
	SQLNumber:   Style{FG: ansiBrightMagenta, BG: ansiDefaultBG},
	SQLComment:  Style{FG: ansiBrightBlack, BG: ansiDefaultBG},
	SQLOperator: Style{FG: ansiBrightYellow, BG: ansiDefaultBG},
	SQLPunct:    Style{FG: ansiDefault, BG: ansiDefaultBG},
	SQLFunction: Style{FG: ansiBrightYellow, BG: ansiDefaultBG, Attrs: attrBold},
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
