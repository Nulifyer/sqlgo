// Package sqlite registers the mattn/go-sqlite3 driver. Requires cgo.
// Import for side effects.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"

	_ "github.com/mattn/go-sqlite3"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/sqltok"
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
	ExplainFormat:        db.ExplainFormatSQLiteRows,
	Dialect:              sqltok.DialectSQLite,
	SupportsTransactions: true,
}

func (driver) Capabilities() db.Capabilities { return capabilities }

func (driver) Open(ctx context.Context, cfg db.Config) (db.Conn, error) {
	dsn := buildDSN(cfg)
	sqlDB, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite open: %w", err)
	}
	conn, err := db.OpenSQL(ctx, sqlDB, db.SQLOptions{
		DriverName:    driverName,
		Capabilities:  capabilities,
		SchemaQuery:   schemaQuery,
		TriggersQuery: triggersQuery,
		ColumnsBuilder: func(t db.TableRef) (string, []any) {
			q := "SELECT name, type FROM pragma_table_info(" + quoteSQLiteLiteral(t.Name) + ");"
			return q, nil
		},
		DefinitionFetcher: fetchDefinition,
	})
	if err != nil {
		return nil, fmt.Errorf("sqlite: %w", err)
	}
	return conn, nil
}

// schemaQuery lists user + system tables/views under the synthetic
// "main" schema. sqlite_% objects are flagged is_system=1 so the
// explorer groups them under Sys.
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

// triggersQuery: user triggers via sqlite_master. SQL body is parsed
// loosely for timing (BEFORE/AFTER/INSTEAD OF) and event (INSERT/UPDATE/DELETE).
const triggersQuery = `
SELECT
    'main' AS schema_name,
    IFNULL(tbl_name, '') AS table_name,
    name   AS name,
    CASE
        WHEN UPPER(sql) LIKE '%INSTEAD OF%' THEN 'INSTEAD OF'
        WHEN UPPER(sql) LIKE '%BEFORE%'     THEN 'BEFORE'
        ELSE 'AFTER'
    END AS timing,
    CASE
        WHEN UPPER(sql) LIKE '%INSERT%' THEN 'INSERT'
        WHEN UPPER(sql) LIKE '%UPDATE%' THEN 'UPDATE'
        WHEN UPPER(sql) LIKE '%DELETE%' THEN 'DELETE'
        ELSE ''
    END AS event,
    CASE WHEN name LIKE 'sqlite_%' THEN 1 ELSE 0 END AS is_system
FROM sqlite_master
WHERE type = 'trigger'
ORDER BY name;
`

// fetchDefinition retrieves the stored CREATE text from sqlite_master for
// views and triggers and prepends a DROP IF EXISTS. sqlite has no stored
// procedures or functions, so those kinds return ErrDefinitionUnsupported.
func fetchDefinition(ctx context.Context, sqlDB *sql.DB, kind, schema, name string) (string, error) {
	var masterType, dropKw string
	switch kind {
	case "view":
		masterType, dropKw = "view", "VIEW"
	case "trigger":
		masterType, dropKw = "trigger", "TRIGGER"
	default:
		return "", db.ErrDefinitionUnsupported
	}
	_ = schema // sqlite is flat; schema ignored beyond the synthetic "main"
	var body sql.NullString
	err := sqlDB.QueryRowContext(ctx,
		"SELECT sql FROM sqlite_master WHERE type = ? AND name = ?",
		masterType, name).Scan(&body)
	if err != nil {
		return "", fmt.Errorf("sqlite_master: %w", err)
	}
	if !body.Valid || strings.TrimSpace(body.String) == "" {
		return "", fmt.Errorf("no definition for %s %s", kind, name)
	}
	drop := fmt.Sprintf("DROP %s IF EXISTS \"%s\";\n", dropKw, strings.ReplaceAll(name, `"`, `""`))
	return drop + strings.TrimRight(body.String, "\r\n\t ;") + ";", nil
}

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
