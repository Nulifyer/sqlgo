package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Nulifyer/sqlgo/internal/atomicfile"
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

	openConn  func(ctx context.Context) (db.Conn, io.Closer, error)
	out       io.Writer
	outClose  func() error // cleanup hook for callers that build without running
	outFinish func(commit bool) error
	stderr    io.Writer
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
		atomic, err := atomicfile.Create(flags.Output)
		if err != nil {
			return nil, ExitUsage, fmt.Errorf("create %s: %w", flags.Output, err)
		}
		opts.out = atomic.Writer()
		opts.outClose = func() error { return atomic.Finish(false) }
		opts.outFinish = atomic.Finish
	} else {
		opts.out = stdout
	}

	return opts, ExitOK, nil
}

// run opens the connection, executes the full SQL batch through Query,
// and streams each result set to the selected writer. It honors the
// unsafe-mutation gate before any work starts so a destructive batch
// aborts early.
func run(ctx context.Context, opts *runOptions) (exit ExitCode) {
	started := time.Now()
	defer func() {
		if opts.outFinish != nil {
			commit := exit == ExitOK || exit == ExitRowCap
			if err := opts.outFinish(commit); err != nil {
				stderrf(opts.stderr, "output: %v", err)
				exit = ExitQuery
			}
			opts.outClose = nil
		} else if opts.outClose != nil {
			if err := opts.outClose(); err != nil {
				stderrf(opts.stderr, "output: %v", err)
				exit = ExitQuery
			}
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
	exit = ExitOK
	totalRows := int64(0)
	var runErr error
	renderer := invocationOutput{writer: opts.out, format: opts.format}
	queries := []string{opts.sql}
	if opts.continueOnError {
		queries = sqltok.SplitStatements(opts.sql)
		if len(queries) == 0 {
			queries = []string{opts.sql}
		}
	}

	markFailure := func(code ExitCode, err error) {
		if err != nil && runErr == nil {
			runErr = err
		}
		if exit == ExitOK || code == ExitQuery {
			exit = code
		}
	}
	stop := false
	for _, query := range queries {
		if opts.maxRows > 0 && totalRows >= int64(opts.maxRows) {
			break
		}
		queryCtx := ctx
		cancelQuery := func() {}
		if opts.timeout > 0 {
			queryCtx, cancelQuery = context.WithTimeout(ctx, opts.timeout)
		}
		rows, err := conn.Query(queryCtx, query)
		if err != nil {
			cancelQuery()
			stderrf(opts.stderr, "query: %v", err)
			markFailure(ExitQuery, err)
			if !opts.continueOnError {
				break
			}
			continue
		}

		first := true
		for {
			if !first && !rows.NextResultSet() {
				break
			}
			first = false
			remaining := 0
			if opts.maxRows > 0 {
				remaining = opts.maxRows - int(totalRows)
			}
			n, code, resultErr := streamResultSet(opts, &renderer, rows, remaining)
			totalRows += n
			if code == ExitOK {
				continue
			}
			markFailure(code, resultErr)
			if code == ExitRowCap || !opts.continueOnError {
				stop = true
				break
			}
		}
		if err := rows.Close(); err != nil {
			stderrf(opts.stderr, "rows close: %v", err)
			markFailure(ExitQuery, fmt.Errorf("rows close: %w", err))
			if !opts.continueOnError {
				stop = true
			}
		}
		cancelQuery()
		if stop {
			break
		}
	}
	if err := renderer.Finish(); err != nil {
		stderrf(opts.stderr, "write: %v", err)
		markFailure(ExitQuery, err)
	}
	if opts.recordHistory {
		recordHistory(ctx, opts, runErr, totalRows, time.Since(started))
	}
	return exit
}

// streamResultSet drains a single result set into the output writer
// using the selected format. For formats that buffer (table, json,
// markdown) it collects rows first; delimited/jsonl stream directly.
func streamResultSet(opts *runOptions, renderer *invocationOutput, rows db.Rows, rowLimit int) (int64, ExitCode, error) {
	cols := rows.Columns()
	var buf [][]string
	var stream *output.RowStream
	if output.StreamsRows(opts.format) {
		var err error
		stream, err = output.NewRowStream(opts.out, cols, opts.format)
		if err != nil {
			stderrf(opts.stderr, "write: %v", err)
			return 0, ExitQuery, err
		}
	} else {
		buf = make([][]string, 0, 128)
	}
	count := 0
	for rows.Next() {
		vals, err := rows.Scan()
		if err != nil {
			if stream != nil {
				_ = stream.Close()
			}
			stderrf(opts.stderr, "scan: %v", err)
			return int64(count), ExitQuery, fmt.Errorf("scan: %w", err)
		}
		row := stringifyRow(vals)
		if stream != nil {
			if err := stream.WriteRow(row); err != nil {
				_ = stream.Close()
				stderrf(opts.stderr, "write: %v", err)
				return int64(count), ExitQuery, err
			}
		} else {
			buf = append(buf, row)
		}
		count++
		if rowLimit > 0 && count >= rowLimit {
			break
		}
	}
	flush := func() error {
		if stream != nil {
			return stream.Close()
		}
		return renderer.WriteResult(cols, buf)
	}
	if err := rows.Err(); err != nil {
		// Still try to flush what we have so the user sees partial results.
		_ = flush()
		stderrf(opts.stderr, "rows: %v", err)
		return int64(count), ExitQuery, err
	}
	if err := flush(); err != nil {
		stderrf(opts.stderr, "write: %v", err)
		return int64(count), ExitQuery, err
	}
	if rowLimit > 0 && count >= rowLimit {
		stderrf(opts.stderr, "row cap %d reached; truncating output", opts.maxRows)
		return int64(count), ExitRowCap, fmt.Errorf("row cap %d reached", opts.maxRows)
	}
	return int64(count), ExitOK, nil
}

type invocationOutput struct {
	writer  io.Writer
	format  output.Format
	results []output.Result
}

func (o *invocationOutput) WriteResult(cols []db.Column, rows [][]string) error {
	if o.format == output.JSON || o.format == output.HTML {
		o.results = append(o.results, output.Result{
			Columns: append([]db.Column(nil), cols...),
			Rows:    rows,
		})
		return nil
	}
	return output.Write(o.writer, cols, rows, o.format)
}

func (o *invocationOutput) Finish() error {
	if len(o.results) == 0 {
		return nil
	}
	return output.WriteResults(o.writer, o.results, o.format)
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
		entry.RowCount = -1
	}
	if err := st.RecordHistory(ctx, entry); err != nil {
		stderrf(opts.stderr, "history: %v", err)
	}
}
