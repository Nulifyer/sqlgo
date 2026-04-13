package tui

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"time"
)

// restoreTerminalOnPanic is deferred by Run so a panic in the main loop
// doesn't leave the user stuck in a raw-mode alt-screen with the cursor
// hidden. It emits the un-setup sequences directly to stdout, restores
// cooked mode via term.Restore, writes the stack trace with \r\n line
// endings (raw mode eats bare \n), and re-panics so the program still
// exits non-zero and the runtime prints its usual diagnostics.
//
// When SQLGO_DEBUG=1 the stack is additionally dumped to
// sqlgo-panic-<unix>.log in the working directory so the user can
// recover it after the terminal scrolls past.
func restoreTerminalOnPanic(t *terminal) {
	r := recover()
	if r == nil {
		return
	}
	// Emit un-setup sequences in the order that leaves the least-broken
	// terminal if any single write fails: reset style first (so nothing
	// we print below inherits a leftover bg color), then show cursor,
	// then switch back to the main screen.
	_, _ = io.WriteString(os.Stdout, resetStyle)
	_, _ = io.WriteString(os.Stdout, cursorShow)
	// Unconditionally disable mouse/paste reporting: off-sequences on
	// a terminal that never had them enabled are no-ops, so we don't
	// need to plumb the screen's View state in here.
	_, _ = io.WriteString(os.Stdout, pasteOff)
	_, _ = io.WriteString(os.Stdout, mouseOff)
	_, _ = io.WriteString(os.Stdout, altScreenOff)
	t.Restore()

	stack := debug.Stack()
	// Raw mode strips the usual \n -> \r\n translation on output. We've
	// already left raw mode via t.Restore, but be defensive: some panics
	// happen before Restore succeeds, and the stderr write still goes
	// through a terminal in an unknown state.
	msg := fmt.Sprintf("sqlgo: panic: %v\r\n%s", r, stackCRLF(stack))
	_, _ = io.WriteString(os.Stderr, msg)

	if os.Getenv("SQLGO_DEBUG") == "1" {
		name := fmt.Sprintf("sqlgo-panic-%d.log", time.Now().Unix())
		if f, err := os.Create(name); err == nil {
			_, _ = fmt.Fprintf(f, "panic: %v\n%s", r, stack)
			_ = f.Close()
		}
	}

	// Re-panic so the exit code reflects the failure. The deferred
	// t.Restore() in Run is a no-op after our call above.
	panic(r)
}

// stackCRLF rewrites \n to \r\n for raw-mode-safe printing.
func stackCRLF(b []byte) []byte {
	out := make([]byte, 0, len(b)+len(b)/32)
	for _, c := range b {
		if c == '\n' {
			out = append(out, '\r', '\n')
			continue
		}
		out = append(out, c)
	}
	return out
}
