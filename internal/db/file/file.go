// Package file registers a "file" driver that loads CSV, TSV, and
// JSONL files into an in-memory SQLite database and returns a normal
// db.Conn pointed at it. Each file becomes one table named after the
// filename. Import for side effects.
//
// cfg.Database is a list of file paths separated by ';' (or ','). Any
// sqlite URI params in cfg.Options are appended to the backing DSN.
package file

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"

	_ "github.com/mattn/go-sqlite3"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/db/fileimport"
	"github.com/Nulifyer/sqlgo/internal/sqltok"
)

const driverName = "file"

func init() { db.Register(driver{}) }

type driver struct{}

func (driver) Name() string { return driverName }

// Capabilities mirror sqlite. File-backed connections can't run
// EXPLAIN usefully, so the format is SQLiteRows and the TUI handles it.
var capabilities = db.Capabilities{
	SchemaDepth:          db.SchemaDepthFlat,
	LimitSyntax:          db.LimitSyntaxLimit,
	IdentifierQuote:      '"',
	SupportsCancel:       true,
	SupportsTLS:          false,
	ExplainFormat:        db.ExplainFormatSQLiteRows,
	Dialect:              sqltok.DialectSQLite,
	SupportsTransactions: true,
}

func (driver) Capabilities() db.Capabilities { return capabilities }

func (driver) Open(ctx context.Context, cfg db.Config) (db.Conn, error) {
	paths := splitPaths(cfg.Database)
	if len(paths) == 0 {
		return nil, fmt.Errorf("file: no paths in Database")
	}
	sqlDB, err := sql.Open("sqlite3", sharedMemoryDSN(cfg))
	if err != nil {
		return nil, fmt.Errorf("file: open sqlite: %w", err)
	}
	// cache=shared lets multiple connections see the same in-memory
	// db, but pinning to a single conn is simpler and avoids any
	// driver-specific sharing semantics.
	sqlDB.SetMaxOpenConns(1)

	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("file: ping: %w", err)
	}
	for _, p := range paths {
		if _, err := fileimport.Load(ctx, sqlDB, p); err != nil {
			_ = sqlDB.Close()
			return nil, fmt.Errorf("file: load %q: %w", p, err)
		}
	}
	return db.OpenSQL(ctx, sqlDB, db.SQLOptions{
		DriverName:   driverName,
		Capabilities: capabilities,
		SchemaQuery:  schemaQuery,
		ColumnsBuilder: func(t db.TableRef) (string, []any) {
			q := "SELECT name, type FROM pragma_table_info('" +
				strings.ReplaceAll(t.Name, "'", "''") + "');"
			return q, nil
		},
	})
}

const schemaQuery = `
SELECT
    'main' AS schema_name,
    name,
    CASE WHEN type = 'view' THEN 1 ELSE 0 END AS is_view,
    CASE WHEN name LIKE 'sqlite_%' THEN 1 ELSE 0 END AS is_system
FROM sqlite_master
WHERE type IN ('table','view')
ORDER BY name;
`

// splitPaths accepts ';' or ',' separators. Trims whitespace and
// drops empties.
func splitPaths(in string) []string {
	in = strings.TrimSpace(in)
	if in == "" {
		return nil
	}
	raw := strings.FieldsFunc(in, func(r rune) bool { return r == ';' || r == ',' })
	out := make([]string, 0, len(raw))
	for _, p := range raw {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// sharedMemoryDSN builds a per-connection in-memory SQLite DSN. Any
// cfg.Options are passed through as URI query params so advanced
// users can set PRAGMAs via mattn/go-sqlite3's _foreign_keys, _journal_mode,
// _busy_timeout, etc. query params.
func sharedMemoryDSN(cfg db.Config) string {
	if len(cfg.Options) == 0 {
		return ":memory:"
	}
	q := url.Values{}
	for k, v := range cfg.Options {
		q.Set(k, v)
	}
	u := url.URL{Scheme: "file", Opaque: ":memory:", RawQuery: q.Encode()}
	return u.String()
}
