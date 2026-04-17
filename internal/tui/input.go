package tui

// input is a single-line text field used by modal forms (connection form,
// later the command line, etc). Kept intentionally small — it doesn't do
// horizontal scroll, history, or selection.
type input struct {
	runes []rune
	cur   int
}

func newInput(seed string) *input {
	return &input{runes: []rune(seed), cur: len([]rune(seed))}
}

func (i *input) String() string { return string(i.runes) }

func (i *input) SetString(s string) {
	i.runes = []rune(s)
	i.cur = len(i.runes)
}

func (i *input) Insert(r rune) {
	i.runes = append(i.runes, 0)
	copy(i.runes[i.cur+1:], i.runes[i.cur:])
	i.runes[i.cur] = r
	i.cur++
}

func (i *input) Backspace() {
	if i.cur == 0 {
		return
	}
	copy(i.runes[i.cur-1:], i.runes[i.cur:])
	i.runes = i.runes[:len(i.runes)-1]
	i.cur--
}

func (i *input) Delete() {
	if i.cur >= len(i.runes) {
		return
	}
	copy(i.runes[i.cur:], i.runes[i.cur+1:])
	i.runes = i.runes[:len(i.runes)-1]
}

func (i *input) MoveLeft() {
	if i.cur > 0 {
		i.cur--
	}
}
func (i *input) MoveRight() {
	if i.cur < len(i.runes) {
		i.cur++
	}
}
func (i *input) MoveHome() { i.cur = 0 }
func (i *input) MoveEnd()  { i.cur = len(i.runes) }

// drawInput renders the input value at (row, col) within maxW runes,
// scrolling so the cursor stays visible, and places the terminal cursor
// at the correct position. Callers that previously did the tail-clip +
// placeCursor dance inline should use this instead.
func drawInput(c *cellbuf, in *input, row, col, maxW int) {
	rs := in.runes
	cur := in.cur
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
	c.PlaceCursor(row, col+cur-offset)
}

// handle applies an ordinary typing keypress to the input. Returns true if
// the key was consumed. Form-level keys (Tab, Enter, Esc) must be filtered
// by the caller first.
func (i *input) handle(k Key) bool {
	switch k.Kind {
	case KeyRune:
		if k.Ctrl {
			return false
		}
		i.Insert(k.Rune)
		return true
	case KeyBackspace:
		i.Backspace()
		return true
	case KeyDelete:
		i.Delete()
		return true
	case KeyLeft:
		i.MoveLeft()
		return true
	case KeyRight:
		i.MoveRight()
		return true
	case KeyHome:
		i.MoveHome()
		return true
	case KeyEnd:
		i.MoveEnd()
		return true
	}
	return false
}
