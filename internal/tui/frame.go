package tui

// Single box-drawing glyph set. The focused/unfocused distinction is carried
// by color, not geometry.
type borderSet struct {
	tl, tr, bl, br, h, v rune
}

var borderSingle = borderSet{tl: '┌', tr: '┐', bl: '└', br: '┘', h: '─', v: '│'}

// drawFrame renders a bordered panel with an optional title. Focused panels
// use colorBorderFocused / colorTitleFocused; others use the dim variants.
func drawFrame(s *cellbuf, r rect, title string, focused bool) {
	drawFrameInfo(s, r, title, "", focused)
}

// drawFrameInfo is drawFrame with an optional right-aligned info label on
// the top border. Used by the results panel to surface "100 rows / 12ms"
// without waiting for the user to look at the footer. Right labels that
// don't fit alongside the left title are silently dropped.
func drawFrameInfo(s *cellbuf, r rect, title, rightInfo string, focused bool) {
	if r.w < 2 || r.h < 2 {
		return
	}
	top := r.row
	bot := r.row + r.h - 1
	left := r.col
	right := r.col + r.w - 1

	b := borderSingle

	borderColor := colorBorderUnfocused
	titleColor := colorTitleUnfocused
	if focused {
		borderColor = colorBorderFocused
		titleColor = colorTitleFocused
	}

	s.setFg(borderColor)
	s.writeAt(top, left, string(b.tl))
	s.writeAt(top, right, string(b.tr))
	s.writeAt(bot, left, string(b.bl))
	s.writeAt(bot, right, string(b.br))
	s.hLine(top, left+1, right-1, b.h)
	s.hLine(bot, left+1, right-1, b.h)
	s.vLine(left, top+1, bot-1, b.v)
	s.vLine(right, top+1, bot-1, b.v)
	s.resetStyle()

	leftLen := 0
	if title != "" {
		label := " " + title + " "
		maxLen := r.w - 4
		if maxLen < 1 {
			return
		}
		if len(label) > maxLen {
			label = label[:maxLen]
		}
		s.setFg(titleColor)
		s.writeAt(top, left+2, label)
		s.resetStyle()
		leftLen = len(label)
	}

	if rightInfo != "" {
		label := " " + rightInfo + " "
		// Reserve two cells on the right corner side and a 1-cell gap
		// from the left title so they never touch.
		maxLen := r.w - 4 - leftLen - 1
		if maxLen < 1 {
			return
		}
		if len(label) > maxLen {
			label = label[:maxLen]
		}
		col := right - 1 - len(label) + 1
		s.setFg(titleColor)
		s.writeAt(top, col, label)
		s.resetStyle()
	}
}
