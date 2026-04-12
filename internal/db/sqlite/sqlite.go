// Package sqlite registers the modernc.org/sqlite driver (pure-Go,
// CGO-free). Import for side effects.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/Nulifyer/sqlgo/internal/db"
)

// quoteSQLiteLiteral escapes s for PRAGMA table_info, which takes
// a literal not a bind value. Defensive: table names come from
// sqlite_master, not user input.
func quoteSQLiteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

const (
	driverName      = "sqlite"
	syntheticSchema = "main" // sqlite's implicit schema name
)

func init() {
	db.Register(driver{})
}

type driver struct{}

func (driver) Name() string { return driverName }

var capabilities = db.Capabilities{
	SchemaDepth:     db.SchemaDepthFlat,
	LimitSyntax:     db.LimitSyntaxLimit,
	IdentifierQuote: '"',
	SupportsCancel:  true,
	SupportsTLS:     false,
	ExplainFormat:   db.ExplainFormatSQLiteRows,
}

func (driver) Capabilities() db.Capabilities { return capabilities }

func (driver) Open(ctx context.Context, cfg db.Config) (db.Conn, error) {
	dsn := buildDSN(cfg)
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite open: %w", err)
	}
	conn, err := db.OpenSQL(ctx, sqlDB, db.SQLOptions{
		DriverName:   driverName,
		Capabilities: capabilities,
		SchemaQuery:  schemaQuery,
		ColumnsBuilder: func(t db.TableRef) (string, []any) {
			q := "SELECT name, type FROM pragma_table_info(" + quoteSQLiteLiteral(t.Name) + ");"
			return q, nil
		},
	})
	if err != nil {
		return nil, fmt.Errorf("sqlite: %w", err)
	}
	return conn, nil
}

// schemaQuery lists user tables/views under the synthetic "main"
// schema. sqlite_% (internal metadata, FTS shadow tables) filtered.
const schemaQuery = `
SELECT
    'main' AS schema_name,
    name,
    CASE WHEN type = 'view' THEN 1 ELSE 0 END AS is_view
FROM sqlite_master
WHERE type IN ('table','view')
  AND name NOT LIKE 'sqlite_%'
ORDER BY name;
`

// buildDSN converts cfg into a sqlite DSN. cfg.Database is the
// file path; empty or ":memory:" → in-memory. cfg.Options becomes
// URI query params (e.g. _pragma=journal_mode(wal)).
func buildDSN(cfg db.Config) string {
	path := strings.TrimSpace(cfg.Database)
	if path == "" || path == ":memory:" {
		return ":memory:"
	}
	if len(cfg.Options) == 0 {
		return path
	}
	q := url.Values{}
	for k, v := range cfg.Options {
		q.Set(k, v)
	}
	u := url.URL{
		Scheme:   "file",
		Opaque:   path,
		RawQuery: q.Encode(),
	}
	return u.String()
}
