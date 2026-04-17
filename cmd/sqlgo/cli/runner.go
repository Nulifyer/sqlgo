package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Nulifyer/sqlgo/internal/config"
	"github.com/Nulifyer/sqlgo/internal/connectutil"
	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/output"
	"github.com/Nulifyer/sqlgo/internal/sqltok"
	"github.com/Nulifyer/sqlgo/internal/store"
)

// runOptions is the resolved, ready-to-execute shape of a CLI
// invocation. exec and export both build one of these from their flag
// set and hand it to run().
type runOptions struct {
	driver string
	cfg    db.Config
	saved  *config.Connection // nil when --dsn was used
	sql    string
	format output.Format

	allowUnsafe     bool
	continueOnError bool
	recordHistory   bool
	timeout         time.Duration
	maxRows         int

	openConn func(ctx context.Context) (db.Conn, io.Closer, error)
	out      io.Writer
	outClose func() error // non-nil when out is a file we opened
	stderr   io.Writer
}

// buildRunOptions centralises the flag → runOptions translation shared
// by every verb that actually runs SQL. It resolves the connection
// (saved vs inline), reads the password (flag/env/keyring/stdin), picks
// a default format when one wasn't given, and opens the output file.
func buildRunOptions(flags *commonFlags, stdin io.Reader, stdout, stderr io.Writer, defaultFormat output.Format) (*runOptions, ExitCode, error) {
	if err := flags.validateSources(); err != nil {
		return nil, ExitUsage, err
	}

	opts := &runOptions{
		allowUnsafe:     flags.AllowUnsafe,
		continueOnError: flags.ContinueOnError,
		recordHistory:   flags.RecordHistory,
		timeout:         flags.Timeout,
		maxRows:         flags.MaxRows,
		stderr:          stderr,
	}

	runtime := runtimeDepsFactory()

	// Connection resolution.
	if flags.DSN != "" {
		drv, cfg, err := parseDSN(flags.DSN)
		if err != nil {
			return nil, ExitUsage, err
		}
		opts.driver = drv
		opts.cfg = cfg
		opts.openConn = func(ctx context.Context) (db.Conn, io.Closer, error) {
			driver, err := runtime.GetDriver(drv)
			if err != nil {
				return nil, nil, err
			}
			conn, err := driver.Open(ctx, cfg)
			return conn, nil, err
		}
	} else {
		st, err := openStore(context.Background())
		if err != nil {
			return nil, ExitConn, fmt.Errorf("open store: %w", err)
		}
		defer st.Close()
		c, err := st.GetConnection(context.Background(), flags.Conn)
		if err != nil {
			if errors.Is(err, store.ErrConnectionNotFound) {
				return nil, ExitConn, fmt.Errorf("no saved connection %q (try: sqlgo conns list)", flags.Conn)
			}
			return nil, ExitConn, err
		}
		resolved, err := connectutil.ResolveSavedConnection(c, runtime)
		if err != nil {
			return nil, ExitConn, err
		}
		opts.saved = &c
		opts.driver = c.Driver
		opts.cfg = resolved.Config
		opts.openConn = func(ctx context.Context) (db.Conn, io.Closer, error) {
			conn, tunnel, err := connectutil.OpenResolvedConnection(ctx, resolved, runtime)
			if err != nil {
				return nil, nil, err
			}
			if tunnel == nil {
				return conn, nil, nil
			}
			return conn, tunnel, nil
		}
	}

	// Password overrides, in precedence order: --password-stdin > env.
	// Neither is mandatory; if they are empty we keep whatever came from
	// the DSN or keyring.
	if flags.PasswordStdin {
		pw, err := readPasswordLine(stdin)
		if err != nil {
			return nil, ExitUsage, fmt.Errorf("read password: %w", err)
		}
		opts.cfg.Password = pw
	} else if v, ok := os.LookupEnv("SQLGO_PASSWORD"); ok {
		opts.cfg.Password = v
	}
	if opts.openConn == nil {
		return nil, ExitConn, errors.New("connection open path not configured")
	}
	cfg := opts.cfg
	opts.openConn = func(ctx context.Context) (db.Conn, io.Closer, error) {
		if flags.DSN == "" && opts.saved != nil {
			resolved, err := connectutil.ResolveSavedConnection(*opts.saved, runtime)
			if err != nil {
				return nil, nil, err
			}
			resolved.Config.Password = cfg.Password
			conn, tunnel, err := connectutil.OpenResolvedConnection(ctx, resolved, runtime)
			if err != nil {
				return nil, nil, err
			}
			if tunnel == nil {
				return conn, nil, nil
			}
			return conn, tunnel, nil
		}
		driver, err := runtime.GetDriver(opts.driver)
		if err != nil {
			return nil, nil, err
		}
		conn, err := driver.Open(ctx, cfg)
		return conn, nil, err
	}

	// SQL source.
	sql, err := flags.resolveQuery(stdin)
	if err != nil {
		return nil, ExitUsage, err
	}
	if strings.TrimSpace(sql) == "" {
		return nil, ExitUsage, errors.New("no SQL provided (use -q, -f, or pipe stdin)")
	}
	opts.sql = sql

	// Output format + destination.
	format := defaultFormat
	if flags.Format != "" {
		f, err := output.FormatFromName(flags.Format)
		if err != nil {
			return nil, ExitUsage, err
		}
		format = f
	} else if flags.Output != "" {
		if f, ok := output.FormatFromPath(flags.Output); ok {
			format = f
		}
	} else if !terminalDetector(stdout) {
		format = output.TSV
	}
	opts.format = format

	if flags.Output != "" {
		fp, err := os.Create(flags.Output)
		if err != nil {
			return nil, ExitUsage, fmt.Errorf("create %s: %w", flags.Output, err)
		}
		opts.out = fp
		opts.outClose = fp.Close
	} else {
		opts.out = stdout
	}

	return opts, ExitOK, nil
}

// run opens the connection, executes the full SQL batch through Query,
// and streams each result set to the selected writer. It honors the
// unsafe-mutation gate before any work starts so a destructive batch
// aborts early.
func run(ctx context.Context, opts *runOptions) ExitCode {
	started := time.Now()
	defer func() {
		if opts.outClose != nil {
			_ = opts.outClose()
		}
	}()

	// Unsafe-mutation gate. We run the safety classifier on the whole
	// batch so a mixed script (UPDATE + DROP in one file) surfaces every
	// flagged statement at once.
	if flagged := sqltok.UnsafeMutations(opts.sql); len(flagged) > 0 && !opts.allowUnsafe {
		stderrf(opts.stderr, "refusing to run %d unsafe statement(s) without --allow-unsafe:", len(flagged))
		for _, m := range flagged {
			fmt.Fprintf(opts.stderr, "  - %s: %s\n", m.Reason, m.Statement)
		}
		return ExitUnsafeRefused
	}

	dialCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	conn, connClose, err := opts.openConn(dialCtx)
	if err != nil {
		stderrf(opts.stderr, "connect: %v", err)
		return ExitConn
	}
	defer conn.Close()
	if connClose != nil {
		defer connClose.Close()
	}

	// Feed the driver the whole batch via Query; NextResultSet handles
	// multi-statement. This matches what the TUI does, so the CLI's
	// behaviour around `;` boundaries and driver quirks is identical.
	stmtCtx := ctx
	if opts.timeout > 0 {
		var c context.CancelFunc
		stmtCtx, c = context.WithTimeout(ctx, opts.timeout)
		defer c()
	}

	rows, err := conn.Query(stmtCtx, opts.sql)
	if err != nil {
		if opts.recordHistory {
			recordHistory(ctx, opts, err, 0, time.Since(started))
		}
		stderrf(opts.stderr, "query: %v", err)
		return ExitQuery
	}
	defer rows.Close()

	exit := ExitOK
	first := true
	totalRows := int64(0)
	for {
		if !first {
			if !rows.NextResultSet() {
				break
			}
		}
		first = false

		n, ec := streamResultSet(opts, rows)
		totalRows += n
		if ec != ExitOK {
			if !opts.continueOnError {
				exit = ec
				break
			}
			exit = ec
		}
	}
	if opts.recordHistory {
		recordHistory(ctx, opts, nil, totalRows, time.Since(started))
	}
	return exit
}

// streamResultSet drains a single result set into the output writer
// using the selected format. For formats that buffer (table, json,
// markdown) it collects rows first; delimited/jsonl stream directly.
func streamResultSet(opts *runOptions, rows db.Rows) (int64, ExitCode) {
	cols := rows.Columns()
	buf := make([][]string, 0, 128)
	count := 0
	for rows.Next() {
		vals, err := rows.Scan()
		if err != nil {
			stderrf(opts.stderr, "scan: %v", err)
			return int64(count), ExitQuery
		}
		buf = append(buf, stringifyRow(vals))
		count++
		if opts.maxRows > 0 && count >= opts.maxRows {
			break
		}
	}
	if err := rows.Err(); err != nil {
		// Still try to flush what we have so the user sees partial results.
		_ = output.Write(opts.out, cols, buf, opts.format)
		stderrf(opts.stderr, "rows: %v", err)
		if errors.Is(err, context.DeadlineExceeded) {
			return int64(count), ExitQuery
		}
		return int64(count), ExitQuery
	}
	if opts.maxRows > 0 && count >= opts.maxRows {
		stderrf(opts.stderr, "row cap %d reached; truncating output", opts.maxRows)
	}
	if err := output.Write(opts.out, cols, buf, opts.format); err != nil {
		stderrf(opts.stderr, "write: %v", err)
		return int64(count), ExitQuery
	}
	if opts.maxRows > 0 && count >= opts.maxRows {
		return int64(count), ExitRowCap
	}
	return int64(count), ExitOK
}

// stringifyRow coerces driver-supplied values to the string row shape
// the output package expects. Matches the TUI's formatting choices so
// CSV/TSV exports from CLI and TUI look identical.
func stringifyRow(vals []any) []string {
	out := make([]string, len(vals))
	for i, v := range vals {
		out[i] = stringifyValue(v)
	}
	return out
}

func stringifyValue(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(x)
	case time.Time:
		return x.Format(time.RFC3339Nano)
	case string:
		return x
	}
	return fmt.Sprintf("%v", v)
}

// readPasswordLine consumes a single line from r, trims the trailing
// newline, and returns the rest. Used by --password-stdin so callers
// can `echo $SECRET | sqlgo exec --password-stdin …` without having
// the secret appear in argv.
func readPasswordLine(r io.Reader) (string, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	s := strings.TrimRight(string(b), "\r\n")
	return s, nil
}

// openStore opens the connection/history store. Split out so the conns
// and history verbs can share the open path once they land.
func openStore(ctx context.Context) (cliStore, error) {
	return openStoreFn(ctx)
}

// recordHistory writes a single batch entry to the history store. Best
// effort: failures print a warning but don't change the verb's exit
// code, because a missing history DB shouldn't block a successful
// query from returning rows.
func recordHistory(ctx context.Context, opts *runOptions, runErr error, rowCount int64, elapsed time.Duration) {
	st, err := openStore(ctx)
	if err != nil {
		stderrf(opts.stderr, "history: open store: %v", err)
		return
	}
	defer st.Close()
	connName := ""
	if opts.saved != nil {
		connName = opts.saved.Name
	}
	entry := store.HistoryEntry{
		ConnectionName: connName,
		SQL:            opts.sql,
		ExecutedAt:     time.Now().UTC(),
		Elapsed:        elapsed,
		RowCount:       rowCount,
	}
	if runErr != nil {
		entry.Error = runErr.Error()
	}
	if err := st.RecordHistory(ctx, entry); err != nil {
		stderrf(opts.stderr, "history: %v", err)
	}
}
