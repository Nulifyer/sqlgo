package tui

import (
	"fmt"

	"github.com/Nulifyer/sqlgo/internal/sqltok"
)

// confirmRunLayer is the safety-net overlay shown before executing a
// batch that contains destructive statements the user might have typed
// by accident: UPDATE/DELETE without WHERE, TRUNCATE, DROP TABLE/DATABASE.
// Defaults to No — the user must explicitly choose Yes (either by key
// 'y' or by arrowing/tabbing onto Yes and pressing Enter) to proceed.
//
// On confirm, it pops itself and calls runQueryUnsafe on the app so the
// original runQuery safety check doesn't fire again in a loop.
type confirmRunLayer struct {
	findings []sqltok.UnsafeMutation
	yesSel   bool // false = No highlighted (default), true = Yes
}

func newConfirmRunLayer(findings []sqltok.UnsafeMutation) *confirmRunLayer {
	return &confirmRunLayer{findings: findings}
}

func (cl *confirmRunLayer) Draw(a *app, c *cellbuf) {
	boxW := 78
	if boxW > a.term.width-dialogMargin {
		boxW = a.term.width - dialogMargin
	}
	if boxW < 48 {
		boxW = 48
	}

	// Header + per-finding line + spacer + prompt.
	const header = 2
	const footer = 5
	listH := len(cl.findings)
	if listH > 8 {
		listH = 8
	}
	boxH := header + listH + footer
	if boxH > a.term.height-dialogMargin {
		boxH = a.term.height - dialogMargin
	}
	if boxH < 8 {
		boxH = 8
	}
	row := (a.term.height - boxH) / 2
	col := (a.term.width - boxW) / 2
	if row < 1 {
		row = 1
	}
	if col < 1 {
		col = 1
	}
	r := rect{row: row, col: col, w: boxW, h: boxH}
	c.fillRect(r)
	drawFrame(c, r, "Confirm destructive query", true)

	innerCol := col + 2

	// Header with warning glyph in yellow, header text in red so the
	// user notices this isn't the normal run path.
	warnStyle := Style{FG: ansiBrightYellow, BG: ansiDefaultBG, Attrs: attrBold}
	headStyle := Style{FG: ansiBrightRed, BG: ansiDefaultBG, Attrs: attrBold}
	const warn = "⚠ "
	c.writeStyled(row+1, innerCol, warn, warnStyle)
	c.writeStyled(row+1, innerCol+runeWidth(warn),
		truncate(fmt.Sprintf("%d statement(s) look destructive:", len(cl.findings)),
			boxW-4-runeWidth(warn)),
		headStyle)

	reasonStyle := Style{FG: ansiBrightRed, BG: ansiDefaultBG}
	for i := 0; i < listH; i++ {
		f := cl.findings[i]
		lineRow := row + 2 + i
		// Reason in red, then the statement excerpt in default style.
		reason := f.Reason + " -- "
		c.writeStyled(lineRow, innerCol, truncate(reason, boxW-4), reasonStyle)
		rest := boxW - 4 - runeWidth(reason)
		if rest > 0 {
			c.writeAt(lineRow, innerCol+runeWidth(reason),
				truncate(f.Statement, rest))
		}
	}

	promptRow := row + boxH - 4
	noLabel := "  No  "
	yesLabel := "  Yes  "
	if cl.yesSel {
		yesLabel = "> Yes <"
	} else {
		noLabel = "> No <"
	}

	// Prompt prefix + warning glyph, then the selectable options styled
	// by role (No = safe green-ish default; Yes = red to emphasise that
	// the user is overriding the guard), then the hint tail.
	safeStyle := Style{FG: ansiDefault, BG: ansiDefaultBG}
	if !cl.yesSel {
		safeStyle.Attrs = attrBold
	}
	dangerStyle := Style{FG: ansiBrightRed, BG: ansiDefaultBG}
	if cl.yesSel {
		dangerStyle.Attrs = attrBold
	}

	x := innerCol
	avail := boxW - 4
	write := func(s string, st Style) {
		if avail <= 0 {
			return
		}
		t := truncate(s, avail)
		c.writeStyled(promptRow, x, t, st)
		w := runeWidth(t)
		x += w
		avail -= w
	}
	write(warn, warnStyle)
	write("Run anyway?  ", headStyle)
	write(noLabel, safeStyle)
	write("   ", Style{FG: ansiDefault, BG: ansiDefaultBG})
	write(yesLabel, dangerStyle)

	hintRow := promptRow + 2
	c.writeStyled(hintRow, innerCol,
		truncate("(y=yes, n/Esc=no, Tab/←→ switch, Enter=confirm)", boxW-4),
		Style{FG: ansiBrightBlack, BG: ansiDefaultBG})
}

// runeWidth returns the number of grid cells s occupies. The confirm
// layer uses plain ASCII plus the ⚠ glyph, all of which are single-cell
// in the terminals sqlgo targets, so a rune count is sufficient.
func runeWidth(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}

func (cl *confirmRunLayer) HandleKey(a *app, k Key) {
	if k.Kind == KeyEsc {
		a.popLayer()
		return
	}
	if k.Kind == KeyRune && !k.Ctrl && !k.Alt {
		switch k.Rune {
		case 'n', 'N':
			a.popLayer()
			return
		case 'y', 'Y':
			cl.commit(a)
			return
		}
	}
	switch k.Kind {
	case KeyLeft, KeyRight, KeyTab:
		cl.yesSel = !cl.yesSel
		return
	case KeyEnter:
		if cl.yesSel {
			cl.commit(a)
			return
		}
		a.popLayer()
		return
	}
}

func (cl *confirmRunLayer) commit(a *app) {
	a.popLayer()
	a.runQueryUnsafe()
}

func (cl *confirmRunLayer) Hints(a *app) string {
	_ = a
	return joinHints("y=run", "n/Esc=cancel", "Tab=switch", "Enter=confirm")
}
