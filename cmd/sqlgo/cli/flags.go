package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// commonFlags is the shared set of connection + query-source + output
// options used by both `exec` and `export`. Two verbs have slightly
// different defaults and validation rules (see validate), but the
// underlying flag surface is identical so scripts can reuse argument
// lists between them.
type commonFlags struct {
	// Connection selection: exactly one of Conn or DSN must be set.
	Conn string
	DSN  string

	// Query source: exactly one of Query, File, or stdin (via File == "-").
	// When all three are empty and stdin is not a tty, stdin is used
	// automatically so `cat q.sql | sqlgo exec -c foo` works without
	// the caller specifying `-f -`.
	Query string
	File  string

	// Output. Format is parsed lazily in validate so we can default based
	// on whether stdout is a tty and whether an Output path was given.
	Format string
	Output string

	// Execution tuning.
	AllowUnsafe     bool
	ContinueOnError bool
	RecordHistory   bool
	Timeout         time.Duration
	MaxRows         int
	PasswordStdin   bool
}

// registerCommon adds the shared flag set to fs. exec and export each
// own their own FlagSet so usage lines stay accurate per-verb.
func (f *commonFlags) registerCommon(fs *flag.FlagSet) {
	fs.StringVar(&f.Conn, "c", "", "saved connection name")
	fs.StringVar(&f.Conn, "conn", "", "saved connection name")
	fs.StringVar(&f.DSN, "dsn", "", "inline connection DSN (driver://user:pass@host:port/db?opt=v)")

	fs.StringVar(&f.Query, "q", "", "SQL query text")
	fs.StringVar(&f.Query, "query", "", "SQL query text")
	fs.StringVar(&f.File, "f", "", "path to .sql file (- for stdin)")
	fs.StringVar(&f.File, "file", "", "path to .sql file (- for stdin)")

	fs.StringVar(&f.Format, "format", "", "output format: csv|tsv|json|jsonl|markdown|table")
	fs.StringVar(&f.Output, "o", "", "output path (default stdout)")
	fs.StringVar(&f.Output, "output", "", "output path (default stdout)")

	fs.BoolVar(&f.AllowUnsafe, "allow-unsafe", false, "permit destructive statements (UPDATE/DELETE without WHERE, TRUNCATE, DROP)")
	fs.BoolVar(&f.ContinueOnError, "continue-on-error", false, "keep running subsequent statements after a failure")
	fs.BoolVar(&f.RecordHistory, "record-history", false, "append executed statements to the history store")
	fs.DurationVar(&f.Timeout, "timeout", 0, "abort each statement after this duration (0 = no timeout)")
	fs.IntVar(&f.MaxRows, "max-rows", 0, "stop reading after this many rows (0 = unlimited)")
	fs.BoolVar(&f.PasswordStdin, "password-stdin", false, "read the connection password from stdin (before any query)")
}

// resolveQuery loads the SQL text from whichever source the flags point
// at. An explicit -q beats -f beats piped stdin. Returns an empty string
// when no source is available; callers decide whether that's an error.
func (f *commonFlags) resolveQuery(stdin io.Reader) (string, error) {
	if f.Query != "" {
		return f.Query, nil
	}
	if f.File == "-" {
		b, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("read stdin: %w", err)
		}
		return string(b), nil
	}
	if f.File != "" {
		b, err := os.ReadFile(f.File)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", f.File, err)
		}
		return string(b), nil
	}
	// Auto-pick stdin when no explicit source was given and stdin is
	// not a tty. Callers that want to force-error on missing source
	// should check the empty-string return.
	if !isTerminal(stdin) {
		b, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("read stdin: %w", err)
		}
		return string(b), nil
	}
	return "", nil
}

// validateSources enforces the "exactly one connection" and "at most
// one explicit query source" invariants.
func (f *commonFlags) validateSources() error {
	if f.Conn == "" && f.DSN == "" {
		return errors.New("one of -c/--conn or --dsn is required")
	}
	if f.Conn != "" && f.DSN != "" {
		return errors.New("-c/--conn and --dsn are mutually exclusive")
	}
	if f.Query != "" && f.File != "" {
		return errors.New("-q/--query and -f/--file are mutually exclusive")
	}
	return nil
}

// isTerminal reports whether v is a tty-backed *os.File. Used to
// auto-pick human-friendly defaults (table format, stdin auto-consume).
// Accepts any (io.Reader or io.Writer) so it serves both ends; a
// non-*os.File reports false so test buffers are treated as pipes.
func isTerminal(v any) bool {
	f, ok := v.(*os.File)
	if !ok {
		return false
	}
	return isTerminalFD(f.Fd())
}

// stderrf wraps fmt.Fprintln for the common "sqlgo: <msg>" prefix so
// every verb formats errors the same way.
func stderrf(stderr io.Writer, format string, args ...any) {
	fmt.Fprintf(stderr, "sqlgo: "+strings.TrimRight(format, "\n")+"\n", args...)
}
