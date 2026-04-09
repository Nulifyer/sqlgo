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
	}
}
