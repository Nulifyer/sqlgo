package term

// Basic ANSI SGR foreground/background codes. Using these (instead of
// 256-color indices) lets the user's terminal theme pick the actual RGB
// values, so sqlgo blends into whichever color scheme the terminal is
// configured with.
const (
	// Foreground.
	AnsiDefault       = 39 // reset fg to terminal default
	AnsiBrightBlack   = 90 // usually rendered as grey
	AnsiBrightRed     = 91
	AnsiBrightGreen   = 92
	AnsiBrightYellow  = 93
	AnsiBrightBlue    = 94
	AnsiBrightMagenta = 95
	AnsiBrightCyan    = 96

	// Background. Parallel to the fg codes but offset by 10.
	AnsiDefaultBG = 49 // reset bg to terminal default
)

// Theme is the indirection seam for colors. Each field is a Style -- the
// fg/bg/attrs bundle layers draw with -- so re-skinning is a matter of
// swapping one struct. The concrete default theme is DefaultTheme below;
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
	SQLTable    Style // ident in table position (after FROM/JOIN/etc.)
	SQLColumn   Style // ident that isn't a function or table -- column refs, aliases
}

// DefaultTheme is the out-of-the-box color scheme. Foregrounds for the
// non-SQL roles track the pre-Phase-1.9 constants so the visual output
// of panels / borders / status bar is unchanged; SQL syntax roles are
// new in Phase 3 and pick bright ANSI colors that track the user's
// terminal palette. Backgrounds stay at terminal default so we don't
// clobber the user's background.
var DefaultTheme = Theme{
	BorderFocused:   Style{FG: AnsiBrightCyan, BG: AnsiDefaultBG},
	BorderUnfocused: Style{FG: AnsiBrightBlack, BG: AnsiDefaultBG},
	TitleFocused:    Style{FG: AnsiBrightCyan, BG: AnsiDefaultBG},
	TitleUnfocused:  Style{FG: AnsiDefault, BG: AnsiDefaultBG},
	StatusBar:       Style{FG: AnsiBrightBlack, BG: AnsiDefaultBG},
	Selection:       Style{FG: AnsiBrightCyan, BG: AnsiDefaultBG},

	SQLKeyword:  Style{FG: AnsiBrightBlue, BG: AnsiDefaultBG, Attrs: AttrBold},
	SQLString:   Style{FG: AnsiBrightGreen, BG: AnsiDefaultBG},
	SQLNumber:   Style{FG: AnsiBrightMagenta, BG: AnsiDefaultBG},
	SQLComment:  Style{FG: AnsiBrightBlack, BG: AnsiDefaultBG},
	SQLOperator: Style{FG: AnsiBrightYellow, BG: AnsiDefaultBG},
	SQLPunct:    Style{FG: AnsiDefault, BG: AnsiDefaultBG},
	SQLFunction: Style{FG: AnsiBrightYellow, BG: AnsiDefaultBG, Attrs: AttrBold},
	SQLTable:    Style{FG: AnsiBrightCyan, BG: AnsiDefaultBG},
	SQLColumn:   Style{FG: AnsiDefault, BG: AnsiDefaultBG},
}

// CurrentTheme is the theme consulted by widgets. Swapping this is how a
// Phase-2 "theme picker" will re-skin the UI without touching widget
// code.
var CurrentTheme = DefaultTheme

// Legacy semantic role constants. New code should pull Styles from
// CurrentTheme; these are kept so existing setFg(colorX) call sites
// continue to work while the widgets migrate.
const (
	ColorBorderFocused   = AnsiBrightCyan
	ColorBorderUnfocused = AnsiBrightBlack
	ColorTitleFocused    = AnsiBrightCyan
	ColorTitleUnfocused  = AnsiDefault
	ColorStatusBar       = AnsiBrightBlack
	ColorError           = AnsiBrightRed
	ColorOK              = AnsiBrightGreen
)
