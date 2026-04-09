package tui

import (
	"fmt"
	"os"

	"golang.org/x/term"
)

// terminal wraps the controlling terminal in raw mode and exposes its size.
// Restore() must be called before exit (defer it).
//
// On Windows the console has separate input and output handles and each one
// supports a different subset of APIs: MakeRaw wants the input handle (stdin),
// while GetConsoleScreenBufferInfo (used by term.GetSize) wants the output
// handle (stdout). Passing the wrong one yields "The handle is invalid." So
// we store both fds and use the right one per call.
type terminal struct {
	inFd     int
	outFd    int
	oldState *term.State
	width    int
	height   int
}

func openTerminal() (*terminal, error) {
	inFd := int(os.Stdin.Fd())
	outFd := int(os.Stdout.Fd())
	if !term.IsTerminal(inFd) {
		return nil, fmt.Errorf("stdin is not a terminal")
	}
	if !term.IsTerminal(outFd) {
		return nil, fmt.Errorf("stdout is not a terminal")
	}
	st, err := term.MakeRaw(inFd)
	if err != nil {
		return nil, fmt.Errorf("make raw: %w", err)
	}
	w, h, err := term.GetSize(outFd)
	if err != nil {
		_ = term.Restore(inFd, st)
		return nil, fmt.Errorf("get size: %w", err)
	}
	return &terminal{inFd: inFd, outFd: outFd, oldState: st, width: w, height: h}, nil
}

func (t *terminal) Restore() {
	if t == nil || t.oldState == nil {
		return
	}
	_ = term.Restore(t.inFd, t.oldState)
	t.oldState = nil
}

// refreshSize re-reads the terminal size. Returns true if it changed.
func (t *terminal) refreshSize() bool {
	w, h, err := term.GetSize(t.outFd)
	if err != nil {
		return false
	}
	if w == t.width && h == t.height {
		return false
	}
	t.width, t.height = w, h
	return true
}
