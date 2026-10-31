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

// openImportExts mirrors the extensions the file driver's importer
// dispatches on. SQLite databases with other extensions are also accepted
// when their file header identifies them as SQLite.
var openImportExts = map[string]struct{}{
	".csv":    {},
	".tsv":    {},
	".jsonl":  {},
	".ndjson": {},
}

var openSQLiteExts = map[string]struct{}{
	".db":      {},
	".db3":     {},
	".s3db":    {},
	".sqlite":  {},
	".sqlite3": {},
}

const sqliteFileHeader = "SQLite format 3\x00"

// runOpen handles `sqlgo open FILE [FILE...]`. It opens a SQLite database
// directly or loads CSV/TSV/JSONL files into an ephemeral in-memory SQLite
// database via the file driver.
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
		io.WriteString(stderr, "usage: sqlgo open [flags] FILE [FILE...]\n")
		io.WriteString(stderr, "\nOpens one SQLite database directly, or loads CSV/TSV/JSONL files into\n")
		io.WriteString(stderr, "an ephemeral in-memory SQLite database (one table per file, named after\n")
		io.WriteString(stderr, "the filename). With no query flags the TUI launches pre-connected. Passing\n")
		io.WriteString(stderr, "-q/-f/stdin runs headlessly and writes results to stdout or -o.\n")
		io.WriteString(stderr, "No saved connection is created.\n\n")
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
	driver, err := openDriverForPaths(paths)
	if err != nil {
		stderrf(stderr, "open: %v", err)
		return ExitUsage
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

	database := paths[0]
	if driver == "file" {
		database = strings.Join(paths, ";")
	}
	connCfg := db.Config{Database: database}
	headless := strings.TrimSpace(sql) != "" || outPath != ""

	if !headless {
		// Interactive: launch the TUI pre-connected to the file driver.
		// The ephemeral connection never touches the store.
		name := "open:" + strings.Join(paths, ",")
		ic := &config.Connection{
			Name:     name,
			Driver:   driver,
			Database: connCfg.Database,
		}
		if err := tui.Run(tui.Options{InitialConnection: ic, Version: Version}); err != nil {
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
	var outFinish func(bool) error
	if outPath != "" {
		atomic, err := createAtomicOutput(outPath)
		if err != nil {
			stderrf(stderr, "create %s: %v", outPath, err)
			return ExitUsage
		}
		out = atomic.Writer()
		outClose = func() error { return atomic.Finish(false) }
		outFinish = atomic.Finish
	}

	runtime := runtimeDepsFactory()
	opts := &runOptions{
		driver:          driver,
		cfg:             connCfg,
		sql:             sql,
		format:          fmtSel,
		allowUnsafe:     allowUnsafe,
		continueOnError: continueOnError,
		timeout:         timeout,
		maxRows:         maxRows,
		out:             out,
		outClose:        outClose,
		outFinish:       outFinish,
		stderr:          stderr,
	}
	opts.openConn = func(ctx context.Context) (db.Conn, io.Closer, error) {
		d, err := runtime.GetDriver(driver)
		if err != nil {
			return nil, nil, err
		}
		conn, err := d.Open(ctx, connCfg)
		return conn, nil, err
	}
	return run(context.Background(), opts)
}

func openDriverForPaths(paths []string) (string, error) {
	foundSQLite := false
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			return "", err
		}

		ext := strings.ToLower(filepath.Ext(p))
		if _, ok := openImportExts[ext]; ok {
			continue
		}
		if _, ok := openSQLiteExts[ext]; ok {
			foundSQLite = true
			continue
		}

		isSQLite, err := hasSQLiteHeader(p)
		if err != nil {
			return "", err
		}
		if !isSQLite {
			return "", errors.New("unsupported file " + p + " (expected CSV, TSV, JSONL, or SQLite database)")
		}
		foundSQLite = true
	}

	if foundSQLite {
		if len(paths) != 1 {
			return "", errors.New("a SQLite database must be opened by itself")
		}
		return "sqlite", nil
	}
	return "file", nil
}

func hasSQLiteHeader(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()

	header := make([]byte, len(sqliteFileHeader))
	n, err := io.ReadFull(f, header)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return false, err
	}
	return n == len(header) && string(header) == sqliteFileHeader, nil
}
