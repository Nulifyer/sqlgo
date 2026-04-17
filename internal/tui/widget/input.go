// Package widget provides reusable TUI components built on top of the
// term/ rendering primitives. Components are domain-agnostic -- they
// know about cells, keys, and mouse events, but not about sessions,
// SQL, or connection forms.
package widget

import "github.com/Nulifyer/sqlgo/internal/tui/term"

// Input is a single-line text field used by modal forms, the save/open
// dialogs, search bars, etc. It intentionally doesn't do horizontal
// scroll, history, or selection -- DrawInput handles on-draw scrolling
// so the cursor stays visible.
type Input struct {
	runes []rune
	cur   int
}

func NewInput(seed string) *Input {
	return &Input{runes: []rune(seed), cur: len([]rune(seed))}
}

func (i *Input) String() string { return string(i.runes) }

func (i *Input) SetString(s string) {
	i.runes = []rune(s)
	i.cur = len(i.runes)
}

func (i *Input) Cursor() int   { return i.cur }
func (i *Input) Len() int      { return len(i.runes) }
func (i *Input) Runes() []rune { return i.runes }

func (i *Input) Insert(r rune) {
	i.runes = append(i.runes, 0)
	copy(i.runes[i.cur+1:], i.runes[i.cur:])
	i.runes[i.cur] = r
	i.cur++
}

func (i *Input) Backspace() {
	if i.cur == 0 {
		return
	}
	copy(i.runes[i.cur-1:], i.runes[i.cur:])
	i.runes = i.runes[:len(i.runes)-1]
	i.cur--
}

func (i *Input) Delete() {
	if i.cur >= len(i.runes) {
		return
	}
	copy(i.runes[i.cur:], i.runes[i.cur+1:])
	i.runes = i.runes[:len(i.runes)-1]
}

func (i *Input) MoveLeft() {
	if i.cur > 0 {
		i.cur--
	}
}
func (i *Input) MoveRight() {
	if i.cur < len(i.runes) {
		i.cur++
	}
}
func (i *Input) MoveHome() { i.cur = 0 }
func (i *Input) MoveEnd()  { i.cur = len(i.runes) }

// Handle applies an ordinary typing keypress. Returns true if the key
// was consumed. Form-level keys (Tab, Enter, Esc) must be filtered by
// the caller first.
func (i *Input) Handle(k term.Key) bool {
	switch k.Kind {
	case term.KeyRune:
		if k.Ctrl {
			return false
		}
		i.Insert(k.Rune)
		return true
	case term.KeyBackspace:
		i.Backspace()
		return true
	case term.KeyDelete:
		i.Delete()
		return true
	case term.KeyLeft:
		i.MoveLeft()
		return true
	case term.KeyRight:
		i.MoveRight()
		return true
	case term.KeyHome:
		i.MoveHome()
		return true
	case term.KeyEnd:
		i.MoveEnd()
		return true
	}
	return false
}

// DrawInput renders in's value at (row, col) within maxW runes,
// scrolling so the cursor stays visible, and places the terminal
// cursor at the correct position.
func DrawInput(c *term.Cellbuf, in *Input, row, col, maxW int) {
	drawInputRunes(c, in.runes, in.cur, row, col, maxW, true)
}

// DrawInputNoCursor is DrawInput without placing the terminal cursor.
// Used by multi-field widgets that want only the active field to own
// cursor placement.
func DrawInputNoCursor(c *term.Cellbuf, in *Input, row, col, maxW int) {
	drawInputRunes(c, in.runes, in.cur, row, col, maxW, false)
}

// DrawInputMasked is DrawInput with every rune displayed as '*'.
// Used by password/secret fields so the cursor and scrolling still
// behave the same as an ordinary input.
func DrawInputMasked(c *term.Cellbuf, in *Input, row, col, maxW int) {
	masked := make([]rune, len(in.runes))
	for i := range masked {
		masked[i] = '*'
	}
	drawInputRunes(c, masked, in.cur, row, col, maxW, true)
}

// DrawInputMaskedNoCursor is DrawInputMasked without placing the
// terminal cursor.
func DrawInputMaskedNoCursor(c *term.Cellbuf, in *Input, row, col, maxW int) {
	masked := make([]rune, len(in.runes))
	for i := range masked {
		masked[i] = '*'
	}
	drawInputRunes(c, masked, in.cur, row, col, maxW, false)
}

func drawInputRunes(c *term.Cellbuf, rs []rune, cur, row, col, maxW int, placeCursor bool) {
	offset := 0
	if len(rs) > maxW {
		offset = len(rs) - maxW
		if cur < offset {
			offset = cur
		}
		if cur > offset+maxW {
			offset = cur - maxW
		}
	}
	visible := rs[offset:]
	if len(visible) > maxW {
		visible = visible[:maxW]
	}
	c.WriteAt(row, col, string(visible))
	if placeCursor {
		c.PlaceCursor(row, col+cur-offset)
	}
}
