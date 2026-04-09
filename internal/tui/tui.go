package tui

import (
	"fmt"
	"os"
)

// FocusTarget identifies which panel currently owns keyboard input.
type FocusTarget int

const (
	FocusExplorer FocusTarget = iota
	FocusQuery
	FocusResults
)

func (f FocusTarget) String() string {
	switch f {
	case FocusExplorer:
		return "Explorer"
	case FocusQuery:
		return "Query"
	case FocusResults:
		return "Results"
	}
	return "?"
}

// app holds the live TUI state. Phase 1: just focus + a quit flag.
type app struct {
	term   *terminal
	scr    *screen
	keys   *keyReader
	focus  FocusTarget
	status string
	quit   bool
}

// Run takes over the terminal and runs the event loop until the user quits
// (Ctrl+Q) or an error occurs. The terminal is always restored before return.
func Run() (err error) {
	t, err := openTerminal()
	if err != nil {
		return err
	}
	defer t.Restore()

	// Switch to alt screen so we don't trample the scrollback.
	fmt.Fprint(os.Stdout, altScreenOn)
	fmt.Fprint(os.Stdout, cursorHide)
	defer func() {
		fmt.Fprint(os.Stdout, cursorShow)
		fmt.Fprint(os.Stdout, altScreenOff)
	}()

	a := &app{
		term:   t,
		scr:    newScreen(os.Stdout, t.width, t.height),
		keys:   newKeyReader(os.Stdin),
		focus:  FocusQuery,
		status: "Ctrl+Q to quit  |  e/q/r switch panels",
	}

	for !a.quit {
		if a.term.refreshSize() {
			a.scr.resize(a.term.width, a.term.height)
		}
		a.draw()
		if err := a.scr.flush(); err != nil {
			return fmt.Errorf("flush: %w", err)
		}
		k, err := a.keys.Read()
		if err != nil {
			return fmt.Errorf("read key: %w", err)
		}
		a.handleKey(k)
	}
	return nil
}

func (a *app) handleKey(k Key) {
	// Global keys first.
	if k.Ctrl && k.Rune == 'q' {
		a.quit = true
		return
	}
	// Panel switching (sqlit-style: e/q/r).
	if k.Kind == KeyRune && !k.Ctrl {
		switch k.Rune {
		case 'e':
			a.focus = FocusExplorer
			return
		case 'q':
			a.focus = FocusQuery
			return
		case 'r':
			a.focus = FocusResults
			return
		}
	}
}

func (a *app) draw() {
	a.scr.beginFrame()
	p := computeLayout(a.term.width, a.term.height)

	drawFrame(a.scr, p.explorer, "Explorer", a.focus == FocusExplorer)
	drawFrame(a.scr, p.query, "Query", a.focus == FocusQuery)
	drawFrame(a.scr, p.results, "Results", a.focus == FocusResults)

	// Status line at the very bottom (no border).
	status := fmt.Sprintf(" [%s]  %s", a.focus, a.status)
	if len(status) > p.status.w {
		status = status[:p.status.w]
	}
	a.scr.setFg(colorStatusBar)
	a.scr.writeAt(p.status.row, p.status.col, status)
	a.scr.resetStyle()
}
