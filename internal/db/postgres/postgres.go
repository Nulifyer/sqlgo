// Package postgres registers pgx/v5/stdlib. Import for side effects.
package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strconv"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/Nulifyer/sqlgo/internal/db"
)

const driverName = "postgres"

func init() {
	db.Register(driver{})
}

type driver struct{}

func (driver) Name() string { return driverName }

var capabilities = db.Capabilities{
	SchemaDepth:     db.SchemaDepthSchemas,
	LimitSyntax:     db.LimitSyntaxLimit,
	IdentifierQuote: '"',
	SupportsCancel:  true,
	SupportsTLS:     true,
	ExplainFormat:   db.ExplainFormatPostgresJSON,
}

func (driver) Capabilities() db.Capabilities { return capabilities }

func (driver) Open(ctx context.Context, cfg db.Config) (db.Conn, error) {
	dsn := buildDSN(cfg)
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres open: %w", err)
	}
	conn, err := db.OpenSQL(ctx, sqlDB, db.SQLOptions{
		DriverName:   driverName,
		Capabilities: capabilities,
		SchemaQuery:  schemaQuery,
		ColumnsQuery: columnsQuery,
	})
	if err != nil {
		return nil, fmt.Errorf("postgres: %w", err)
	}
	return conn, nil
}

// schemaQuery: user + system tables/views. pg_catalog and
// information_schema are flagged is_system=1 so the explorer groups
// them under Sys. pg_toast% / pg_temp_% are still excluded — they're
// implementation noise, not useful catalog views.
const schemaQuery = `
SELECT
    table_schema AS schema_name,
    table_name   AS name,
    CASE WHEN table_type = 'VIEW' THEN 1 ELSE 0 END AS is_view,
    CASE WHEN table_schema IN ('pg_catalog', 'information_schema') THEN 1 ELSE 0 END AS is_system
FROM information_schema.tables
WHERE table_schema NOT LIKE 'pg_toast%'
  AND table_schema NOT LIKE 'pg_temp_%'
ORDER BY table_schema, table_name;
`

// columnsQuery uses $1/$2 (pgx bind placeholders).
const columnsQuery = `
SELECT column_name, data_type
FROM information_schema.columns
WHERE table_schema = $1 AND table_name = $2
ORDER BY ordinal_position;
`

// buildDSN produces a postgres:// URL. cfg.Options → query params.
func buildDSN(cfg db.Config) string {
	host := cfg.Host
	if host == "" {
		host = "localhost"
	}
	port := cfg.Port
	if port == 0 {
		port = 5432
	}

	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(cfg.User, cfg.Password),
		Host:   host + ":" + strconv.Itoa(port),
	}
	if cfg.Database != "" {
		u.Path = "/" + cfg.Database
	}
	q := u.Query()
	for k, v := range cfg.Options {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	return u.String()
}
