package term

// Single box-drawing glyph set. The focused/unfocused distinction is carried
// by color, not geometry.
type BorderSet struct {
	TL, TR, BL, BR, H, V rune
}

var BorderSingle = BorderSet{TL: '\u256d', TR: '\u256e', BL: '\u2570', BR: '\u256f', H: '\u2500', V: '\u2502'}

// DrawFrame renders a bordered panel with an optional title. Focused panels
// use ColorBorderFocused / ColorTitleFocused; others use the dim variants.
func DrawFrame(s *Cellbuf, r Rect, title string, focused bool) {
	DrawFrameInfo(s, r, title, "", focused)
}

// DrawFrameInfo is DrawFrame with an optional right-aligned info label on
// the top border. Used by the results panel to surface "100 rows / 12ms"
// without waiting for the user to look at the footer. Right labels that
// don't fit alongside the left title are silently dropped.
func DrawFrameInfo(s *Cellbuf, r Rect, title, rightInfo string, focused bool) {
	if r.W < 2 || r.H < 2 {
		return
	}
	top := r.Row
	bot := r.Row + r.H - 1
	left := r.Col
	right := r.Col + r.W - 1

	b := BorderSingle

	borderColor := ColorBorderUnfocused
	titleColor := ColorTitleUnfocused
	if focused {
		borderColor = ColorBorderFocused
		titleColor = ColorTitleFocused
	}

	s.SetFg(borderColor)
	s.WriteAt(top, left, string(b.TL))
	s.WriteAt(top, right, string(b.TR))
	s.WriteAt(bot, left, string(b.BL))
	s.WriteAt(bot, right, string(b.BR))
	s.HLine(top, left+1, right-1, b.H)
	s.HLine(bot, left+1, right-1, b.H)
	s.VLine(left, top+1, bot-1, b.V)
	s.VLine(right, top+1, bot-1, b.V)
	s.ResetStyle()

	leftLen := 0
	if title != "" {
		label := " " + title + " "
		maxLen := r.W - 4
		if maxLen < 1 {
			return
		}
		if len(label) > maxLen {
			label = label[:maxLen]
		}
		s.SetFg(titleColor)
		s.WriteAt(top, left+2, label)
		s.ResetStyle()
		leftLen = len(label)
	}

	if rightInfo != "" {
		label := " " + rightInfo + " "
		// Reserve two cells on the right corner side and a 1-cell gap
		// from the left title so they never touch.
		maxLen := r.W - 4 - leftLen - 1
		if maxLen < 1 {
			return
		}
		if len(label) > maxLen {
			label = label[:maxLen]
		}
		col := right - 1 - len(label) + 1
		s.SetFg(titleColor)
		s.WriteAt(top, col, label)
		s.ResetStyle()
	}
}
