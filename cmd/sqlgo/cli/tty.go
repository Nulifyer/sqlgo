package cli

import "golang.org/x/term"

// isTerminalFD is a thin wrapper so flags.go stays independent of the
// tty-detection implementation.
func isTerminalFD(fd uintptr) bool { return term.IsTerminal(int(fd)) }
