//go:build !windows

package term

import (
	"io"
	"os"
	"time"
)

// StdinReader returns the byte source for key input. On non-Windows
// platforms os.Stdin works directly; Windows has a separate
// implementation that bypasses Go's internal/poll console wrapper
// (which translates ^Z to EOF and would otherwise close the app on
// Ctrl+Z).
func StdinReader() io.Reader { return os.Stdin }

// StdinPeekReadable reports whether os.Stdin has at least one byte
// available to read within d. Implemented with a short-lived goroutine
// doing a 1-byte Read into a throwaway buffer; the byte would be lost,
// so callers must only invoke this when the bufio.Reader is known to be
// empty AND callers understand the tradeoff. On non-Windows this path
// is used rarely (mostly for distinguishing bare Esc from CSI), and the
// platforms we target here don't have a no-consume peek syscall that
// plays nicely with raw-mode terminals. For now, fall back to a
// conservative always-false so we never race the main reader. Bare Esc
// still works because the main reader's buffered check is always tried
// first.
func StdinPeekReadable(d time.Duration) bool {
	_ = d
	return false
}
