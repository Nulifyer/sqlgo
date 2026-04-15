// Package cli implements sqlgo's non-TUI verb surface: scriptable
// connection management, query exec, result export, and history access.
// It is intentionally separate from internal/tui so headless invocations
// pull in nothing that touches the terminal renderer.
//
// Dispatch is the single entry point; main.go routes here when the
// first positional argument matches a known verb. Unknown verbs and the
// no-arg case fall through to the TUI so the default user experience is
// unchanged.
package cli

import (
	"fmt"
	"io"
)

// ExitCode is the numeric exit status the CLI returns. Stable across
// releases so scripts can branch on the value.
type ExitCode int

const (
	ExitOK            ExitCode = 0
	ExitUsage         ExitCode = 1
	ExitConn          ExitCode = 2
	ExitQuery         ExitCode = 3
	ExitUnsafeRefused ExitCode = 4
	ExitRowCap        ExitCode = 5
)

// IsVerb reports whether name is a CLI verb. main.go calls this before
// stripping the verb off os.Args; anything else falls through to the
// TUI's existing flag parsing (which still treats a bare path as an
// initial-query seed).
func IsVerb(name string) bool {
	switch name {
	case "exec", "export", "conns", "history", "version":
		return true
	}
	return false
}

// Dispatch runs the verb in argv[0] with the remainder as its flags.
// stdin/stdout/stderr are passed in so tests can drive the CLI without
// touching real OS handles. argv must be non-empty; callers should
// guard with IsVerb first.
func Dispatch(argv []string, stdin io.Reader, stdout, stderr io.Writer) ExitCode {
	if len(argv) == 0 {
		fmt.Fprintln(stderr, "sqlgo: no verb")
		return ExitUsage
	}
	verb := argv[0]
	switch verb {
	case "exec":
		return runExec(argv[1:], stdin, stdout, stderr)
	case "export":
		return runExport(argv[1:], stdin, stdout, stderr)
	case "conns":
		return runConns(argv[1:], stdin, stdout, stderr)
	case "history":
		return runHistory(argv[1:], stdout, stderr)
	case "version":
		return runVersion(argv[1:], stdout)
	}
	fmt.Fprintf(stderr, "sqlgo: unknown verb %q\n", verb)
	return ExitUsage
}
