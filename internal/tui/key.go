package tui

import (
	"io"

	"github.com/Nulifyer/sqlgo/internal/tui/term"
)

// Type and constant aliases so existing tui/ code compiles unchanged.
type KeyKind = term.KeyKind
type Key = term.Key

const (
	KeyRune      = term.KeyRune
	KeyEnter     = term.KeyEnter
	KeyTab       = term.KeyTab
	KeyBackTab   = term.KeyBackTab
	KeyBackspace = term.KeyBackspace
	KeyEsc       = term.KeyEsc
	KeyUp        = term.KeyUp
	KeyDown      = term.KeyDown
	KeyLeft      = term.KeyLeft
	KeyRight     = term.KeyRight
	KeyHome      = term.KeyHome
	KeyEnd       = term.KeyEnd
	KeyPgUp      = term.KeyPgUp
	KeyPgDn      = term.KeyPgDn
	KeyDelete    = term.KeyDelete
	KeyF1        = term.KeyF1
	KeyF2        = term.KeyF2
	KeyF3        = term.KeyF3
	KeyF4        = term.KeyF4
	KeyF5        = term.KeyF5
	KeyF6        = term.KeyF6
	KeyF7        = term.KeyF7
	KeyF8        = term.KeyF8
	KeyF9        = term.KeyF9
	KeyF10       = term.KeyF10
	KeyF11       = term.KeyF11
	KeyF12       = term.KeyF12
)

type keyReader = term.KeyReader

func newKeyReader(r io.Reader) *keyReader { return term.NewKeyReader(r) }
