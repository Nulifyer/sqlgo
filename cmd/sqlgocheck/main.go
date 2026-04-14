// sqlgocheck is a development smoke-test binary. It opens a connection using
// the db.Driver interface, runs a query, and prints the result set. Used
// while building new engine adapters before the TUI is wired up.
//
// Usage:
//
//	go run ./cmd/sqlgocheck -driver mssql \
//	    -host localhost -port 11433 \
//	    -user sa -pass 'SqlGo_dev_Pass1!' \
//	    -query 'SELECT @@VERSION'
//
// The -user/-pass defaults match the compose.yaml dev fixtures. Override
// them for any non-local-compose target — they are not production creds.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Nulifyer/sqlgo/internal/db"
	_ "github.com/Nulifyer/sqlgo/internal/db/mssql"
	_ "github.com/Nulifyer/sqlgo/internal/db/mysql"
	_ "github.com/Nulifyer/sqlgo/internal/db/postgres"
	_ "github.com/Nulifyer/sqlgo/internal/db/sqlite"
)

func main() {
	var (
		driverName = flag.String("driver", "mssql", "registered driver name")
		host       = flag.String("host", "localhost", "database host")
		port       = flag.Int("port", 11433, "database port")
		user       = flag.String("user", "sa", "username")
		pass       = flag.String("pass", "SqlGo_dev_Pass1!", "password")
		database   = flag.String("db", "", "initial database")
		query      = flag.String("query", "SELECT @@VERSION AS version", "SQL to execute")
		opts       = flag.String("opts", "encrypt=disable", "comma-separated key=value driver options")
		timeout    = flag.Duration("timeout", 10*time.Second, "connect/query timeout")
	)
	flag.Parse()

	cfg := db.Config{
		Host:     *host,
		Port:     *port,
		User:     *user,
		Password: *pass,
		Database: *database,
		Options:  parseOpts(*opts),
	}

	d, err := db.Get(*driverName)
	if err != nil {
		die(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	conn, err := d.Open(ctx, cfg)
	if err != nil {
		die(fmt.Errorf("open: %w", err))
	}
	defer conn.Close()

	rows, err := conn.Query(ctx, *query)
	if err != nil {
		die(fmt.Errorf("query: %w", err))
	}
	defer rows.Close()

	res, err := drainRows(rows)
	if err != nil {
		die(fmt.Errorf("scan: %w", err))
	}
	printResult(res)
}

// drainRows pulls a Rows cursor into a materialized Result. sqlgocheck is
// a development smoke test, so fully buffering the result set is fine —
// the TUI uses a streaming consumer instead.
func drainRows(r db.Rows) (*db.Result, error) {
	out := &db.Result{Columns: r.Columns()}
	for r.Next() {
		row, err := r.Scan()
		if err != nil {
			return nil, err
		}
		out.Rows = append(out.Rows, row)
	}
	if err := r.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func parseOpts(s string) map[string]string {
	if s == "" {
		return nil
	}
	out := map[string]string{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out
}

// printResult writes a rudimentary aligned table to stdout. Purely a
// debug helper; the real TUI has its own renderer.
func printResult(r *db.Result) {
	widths := make([]int, len(r.Columns))
	for i, c := range r.Columns {
		widths[i] = len(c.Name)
	}
	cells := make([][]string, len(r.Rows))
	for i, row := range r.Rows {
		cells[i] = make([]string, len(row))
		for j, v := range row {
			s := fmt.Sprintf("%v", v)
			cells[i][j] = s
			if len(s) > widths[j] {
				widths[j] = len(s)
			}
		}
	}

	var b strings.Builder
	for i, c := range r.Columns {
		if i > 0 {
			b.WriteString(" | ")
		}
		fmt.Fprintf(&b, "%-*s", widths[i], c.Name)
	}
	b.WriteByte('\n')
	for i := range r.Columns {
		if i > 0 {
			b.WriteString("-+-")
		}
		b.WriteString(strings.Repeat("-", widths[i]))
	}
	b.WriteByte('\n')
	for _, row := range cells {
		for i, v := range row {
			if i > 0 {
				b.WriteString(" | ")
			}
			fmt.Fprintf(&b, "%-*s", widths[i], v)
		}
		b.WriteByte('\n')
	}
	fmt.Printf("(%d row(s))\n%s", len(r.Rows), b.String())
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "sqlgocheck:", err)
	os.Exit(1)
}
