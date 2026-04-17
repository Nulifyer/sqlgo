package cli

import (
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Nulifyer/sqlgo/internal/config"
	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/output"
	"github.com/Nulifyer/sqlgo/internal/tui"
)

// openAllowedExts mirrors the extensions the file driver's importer
// dispatches on. Validated up front so a typo fails loudly instead of
// landing on the driver's "no loader for …" error deep in connect.
var openAllowedExts = map[string]struct{}{
	".csv":    {},
	".tsv":    {},
	".jsonl":  {},
	".ndjson": {},
}

// runOpen handles `sqlgo open FILE [FILE...]`. It loads CSV/TSV/JSONL
// files into an ephemeral in-memory SQLite database via the file driver.
//
// Default shape is interactive: with no query source the TUI launches
// pre-connected to the file so the user can browse the schema and run
// ad-hoc SQL. Supplying -q/-f/stdin (or -o) switches to a headless run
// that writes results and exits, matching `exec` semantics.
func runOpen(argv []string, stdin io.Reader, stdout, stderr io.Writer) ExitCode {
	fs := flag.NewFlagSet("open", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		query           string
		queryFile       string
		format          string
		outPath         string
		allowUnsafe     bool
		continueOnError bool
		timeout         time.Duration
		maxRows         int
	)
	fs.StringVar(&query, "q", "", "SQL query text (switches to headless)")
	fs.StringVar(&query, "query", "", "SQL query text (switches to headless)")
	fs.StringVar(&queryFile, "f", "", "path to .sql file (- for stdin, switches to headless)")
	fs.StringVar(&queryFile, "file", "", "path to .sql file (- for stdin, switches to headless)")
	fs.StringVar(&format, "format", "", "headless output format: csv|tsv|json|jsonl|markdown|table")
	fs.StringVar(&outPath, "o", "", "headless output path (default stdout)")
	fs.StringVar(&outPath, "output", "", "headless output path (default stdout)")
	fs.BoolVar(&allowUnsafe, "allow-unsafe", false, "permit destructive statements (headless)")
	fs.BoolVar(&continueOnError, "continue-on-error", false, "keep running after a failure (headless)")
	fs.DurationVar(&timeout, "timeout", 0, "abort each statement after this duration (headless)")
	fs.IntVar(&maxRows, "max-rows", 0, "stop reading after this many rows (headless)")

	fs.Usage = func() {
		io.WriteString(stderr, "usage: sqlgo open FILE [FILE...] [flags]\n")
		io.WriteString(stderr, "\nLoads CSV/TSV/JSONL files into an ephemeral in-memory SQLite\n")
		io.WriteString(stderr, "database (one table per file, named after the filename). With no\n")
		io.WriteString(stderr, "query flags the TUI launches pre-connected to the file. Passing\n")
		io.WriteString(stderr, "-q/-f/stdin runs headlessly and writes results to stdout or -o.\n")
		io.WriteString(stderr, "Nothing is persisted; no saved connection is created.\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(argv); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitOK
		}
		return ExitUsage
	}

	paths := fs.Args()
	if len(paths) == 0 {
		stderrf(stderr, "open: need at least one FILE")
		fs.Usage()
		return ExitUsage
	}
	for _, p := range paths {
		ext := strings.ToLower(filepath.Ext(p))
		if _, ok := openAllowedExts[ext]; !ok {
			stderrf(stderr, "open: unsupported extension %q for %s (expected .csv, .tsv, .jsonl, .ndjson)", ext, p)
			return ExitUsage
		}
		if _, err := os.Stat(p); err != nil {
			stderrf(stderr, "open: %v", err)
			return ExitUsage
		}
	}

	// Resolve any user-supplied query. Unlike exec, an empty result here
	// is the *normal* path -- it means "launch the TUI". Only auto-read
	// stdin when it's piped, otherwise resolveQuery would hang waiting
	// for an EOF the user never intended to send.
	var sql string
	hasExplicitQuery := query != "" || queryFile != ""
	if hasExplicitQuery {
		tmp := &commonFlags{Query: query, File: queryFile}
		s, err := tmp.resolveQuery(stdin)
		if err != nil {
			stderrf(stderr, "%v", err)
			return ExitUsage
		}
		sql = s
	} else if !terminalDetector(stdin) {
		// Piped stdin without -q/-f still counts as headless so
		// `cat q.sql | sqlgo open data.csv` works.
		b, err := io.ReadAll(stdin)
		if err != nil {
			stderrf(stderr, "read stdin: %v", err)
			return ExitUsage
		}
		sql = string(b)
	}

	connCfg := db.Config{Database: strings.Join(paths, ";")}
	headless := strings.TrimSpace(sql) != "" || outPath != ""

	if !headless {
		// Interactive: launch the TUI pre-connected to the file driver.
		// The ephemeral connection never touches the store.
		name := "open:" + strings.Join(paths, ",")
		ic := &config.Connection{
			Name:     name,
			Driver:   "file",
			Database: connCfg.Database,
		}
		if err := tui.Run(tui.Options{InitialConnection: ic}); err != nil {
			stderrf(stderr, "%v", err)
			return 1
		}
		return ExitOK
	}

	if strings.TrimSpace(sql) == "" {
		stderrf(stderr, "open: -o without a query; supply -q, -f, or pipe stdin")
		return ExitUsage
	}

	defFmt := output.TSV
	if terminalDetector(stdout) && outPath == "" {
		defFmt = output.Table
	}
	fmtSel := defFmt
	if format != "" {
		f, err := output.FormatFromName(format)
		if err != nil {
			stderrf(stderr, "%v", err)
			return ExitUsage
		}
		fmtSel = f
	} else if outPath != "" {
		if f, ok := output.FormatFromPath(outPath); ok {
			fmtSel = f
		}
	}

	out := stdout
	var outClose func() error
	if outPath != "" {
		fp, err := os.Create(outPath)
		if err != nil {
			stderrf(stderr, "create %s: %v", outPath, err)
			return ExitUsage
		}
		out = fp
		outClose = fp.Close
	}

	opts := &runOptions{
		driver:          "file",
		cfg:             connCfg,
		sql:             sql,
		format:          fmtSel,
		allowUnsafe:     allowUnsafe,
		continueOnError: continueOnError,
		timeout:         timeout,
		maxRows:         maxRows,
		out:             out,
		outClose:        outClose,
		stderr:          stderr,
	}
	return run(context.Background(), opts)
}
