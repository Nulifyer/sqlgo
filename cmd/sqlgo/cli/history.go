package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/output"
	"github.com/Nulifyer/sqlgo/internal/store"
)

// runHistory dispatches `sqlgo history <subcommand>`. Shares the same
// flat-subcommand shape as conns -- none of these execute SQL so
// runner.go wouldn't carry its weight here.
func runHistory(argv []string, stdout, stderr io.Writer) ExitCode {
	if len(argv) == 0 {
		historyUsage(stderr)
		return ExitUsage
	}
	sub, rest := argv[0], argv[1:]
	switch sub {
	case "list", "ls":
		return historyList(rest, stdout, stderr, "")
	case "search":
		return historySearch(rest, stdout, stderr)
	case "clear":
		return historyClear(rest, stderr)
	case "help", "-h", "--help":
		historyUsage(stdout)
		return ExitOK
	}
	fmt.Fprintf(stderr, "sqlgo: history: unknown subcommand %q\n", sub)
	historyUsage(stderr)
	return ExitUsage
}

func historyUsage(w io.Writer) {
	io.WriteString(w, `usage: sqlgo history <subcommand> [flags]

subcommands:
  list    [-c NAME] [--limit N] [--format FMT]    list recent entries
  search  [-c NAME] [--limit N] [--format FMT] Q  full-text search
  clear   [-c NAME] [--force]                     delete history (scoped or all)
`)
}

func historyList(argv []string, stdout, stderr io.Writer, defaultQuery string) ExitCode {
	fs := flag.NewFlagSet("history list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		conn   string
		limit  int
		format string
	)
	fs.StringVar(&conn, "c", "", "filter by connection name")
	fs.StringVar(&conn, "conn", "", "filter by connection name")
	fs.IntVar(&limit, "limit", 50, "maximum rows to return (0 = default 50)")
	fs.StringVar(&format, "format", "", "output format: csv|tsv|json|jsonl|markdown|table")
	if err := fs.Parse(argv); err != nil {
		return ExitUsage
	}
	ctx := context.Background()
	st, err := openStore(ctx)
	if err != nil {
		stderrf(stderr, "open store: %v", err)
		return ExitConn
	}
	defer st.Close()

	entries, err := st.ListRecentHistory(ctx, conn, limit)
	if err != nil {
		stderrf(stderr, "%v", err)
		return ExitConn
	}
	return writeHistory(entries, format, stdout, stderr)
}

func historySearch(argv []string, stdout, stderr io.Writer) ExitCode {
	fs := flag.NewFlagSet("history search", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		conn   string
		limit  int
		format string
	)
	fs.StringVar(&conn, "c", "", "filter by connection name")
	fs.StringVar(&conn, "conn", "", "filter by connection name")
	fs.IntVar(&limit, "limit", 50, "maximum rows to return")
	fs.StringVar(&format, "format", "", "output format: csv|tsv|json|jsonl|markdown|table")
	positional, err := parseInterleaved(fs, argv)
	if err != nil {
		return ExitUsage
	}
	if len(positional) == 0 {
		stderrf(stderr, "history search: query required")
		return ExitUsage
	}
	q := strings.Join(positional, " ")
	ctx := context.Background()
	st, err := openStore(ctx)
	if err != nil {
		stderrf(stderr, "open store: %v", err)
		return ExitConn
	}
	defer st.Close()
	entries, err := st.SearchHistory(ctx, conn, q, limit)
	if err != nil {
		stderrf(stderr, "%v", err)
		return ExitConn
	}
	return writeHistory(entries, format, stdout, stderr)
}

// historyClear deletes all history rows, optionally scoped to one
// connection. Wants --force because it is irreversible and a typo
// ("clear" vs "list") would otherwise wipe the whole ring silently.
func historyClear(argv []string, stderr io.Writer) ExitCode {
	fs := flag.NewFlagSet("history clear", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		conn  string
		force bool
	)
	fs.StringVar(&conn, "c", "", "only clear entries for this connection")
	fs.StringVar(&conn, "conn", "", "only clear entries for this connection")
	fs.BoolVar(&force, "force", false, "required -- confirms the deletion")
	if err := fs.Parse(argv); err != nil {
		return ExitUsage
	}
	if !force {
		stderrf(stderr, "history clear: refusing without --force")
		return ExitUsage
	}
	ctx := context.Background()
	st, err := openStore(ctx)
	if err != nil {
		stderrf(stderr, "open store: %v", err)
		return ExitConn
	}
	defer st.Close()
	n, err := st.ClearHistory(ctx, conn)
	if err != nil {
		stderrf(stderr, "%v", err)
		return ExitConn
	}
	fmt.Fprintf(stderr, "sqlgo: cleared %d entries\n", n)
	return ExitOK
}

// writeHistory renders a []HistoryEntry through the shared output
// package. Format defaults mirror the rest of the CLI: table on a tty,
// tsv on a pipe.
func writeHistory(entries []store.HistoryEntry, format string, stdout, stderr io.Writer) ExitCode {
	fmtSel := output.Table
	if format != "" {
		f, err := output.FormatFromName(format)
		if err != nil {
			stderrf(stderr, "%v", err)
			return ExitUsage
		}
		fmtSel = f
	} else if !isTerminal(stdout) {
		fmtSel = output.TSV
	}
	cols := []db.Column{
		{Name: "id"}, {Name: "conn"}, {Name: "executed_at"},
		{Name: "elapsed_ms"}, {Name: "rows"}, {Name: "error"}, {Name: "sql"},
	}
	rows := make([][]string, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, []string{
			strconv.FormatInt(e.ID, 10),
			e.ConnectionName,
			e.ExecutedAt.Format("2006-01-02 15:04:05"),
			strconv.FormatInt(e.Elapsed.Milliseconds(), 10),
			strconv.FormatInt(e.RowCount, 10),
			e.Error,
			e.SQL,
		})
	}
	if err := output.Write(stdout, cols, rows, fmtSel); err != nil {
		stderrf(stderr, "write: %v", err)
		return ExitQuery
	}
	return ExitOK
}
