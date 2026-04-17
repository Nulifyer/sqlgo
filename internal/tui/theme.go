package tui

import (
	"github.com/Nulifyer/sqlgo/internal/tui/term"
)

// Color constant shims. New code should reference term.Ansi* directly.
const (
	ansiDefault       = term.AnsiDefault
	ansiBrightBlack   = term.AnsiBrightBlack
	ansiBrightRed     = term.AnsiBrightRed
	ansiBrightGreen   = term.AnsiBrightGreen
	ansiBrightYellow  = term.AnsiBrightYellow
	ansiBrightBlue    = term.AnsiBrightBlue
	ansiBrightMagenta = term.AnsiBrightMagenta
	ansiBrightCyan    = term.AnsiBrightCyan
	ansiDefaultBG     = term.AnsiDefaultBG

	colorBorderFocused   = term.ColorBorderFocused
	colorBorderUnfocused = term.ColorBorderUnfocused
	colorTitleFocused    = term.ColorTitleFocused
	colorTitleUnfocused  = term.ColorTitleUnfocused
	colorStatusBar       = term.ColorStatusBar
	colorError           = term.ColorError
	colorOK              = term.ColorOK
)

// Theme is a re-export of term.Theme so existing tui/ code compiles unchanged.
type Theme = term.Theme

var defaultTheme = term.Theme{
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
	SQLTable:    Style{FG: ansiBrightCyan, BG: ansiDefaultBG},
	SQLColumn:   Style{FG: ansiDefault, BG: ansiDefaultBG},
}

var currentTheme = defaultTheme
