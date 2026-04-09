// Package postgres registers the pure-Go Postgres driver with internal/db.
// Import it for side effects:
//
//	import _ "github.com/Nulifyer/sqlgo/internal/db/postgres"
//
// The underlying driver is github.com/jackc/pgx/v5/stdlib, pgx's
// database/sql wrapper. pgx is pure Go, honors context cancellation at
// the network layer, and is the modern successor to lib/pq.
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

// capabilities is the Postgres-specific capability set. Postgres groups
// tables under schemas, uses ANSI double-quoted identifiers, LIMIT/OFFSET
// for row caps, honors ctx cancellation via pgx's network layer, and
// accepts TLS knobs through the DSN (sslmode=require, etc).
var capabilities = db.Capabilities{
	SchemaDepth:     db.SchemaDepthSchemas,
	LimitSyntax:     db.LimitSyntaxLimit,
	IdentifierQuote: '"',
	SupportsCancel:  true,
	SupportsTLS:     true,
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
	})
	if err != nil {
		return nil, fmt.Errorf("postgres: %w", err)
	}
	return conn, nil
}

// schemaQuery lists user tables and views grouped under their schema.
// pg_catalog and information_schema are excluded because they're not
// something users want to browse in the explorer by default. The is_view
// column is a 0/1 int to match the shared schema scanner's expected
// three-column shape.
const schemaQuery = `
SELECT
    table_schema AS schema_name,
    table_name   AS name,
    CASE WHEN table_type = 'VIEW' THEN 1 ELSE 0 END AS is_view
FROM information_schema.tables
WHERE table_schema NOT IN ('pg_catalog', 'information_schema')
  AND table_schema NOT LIKE 'pg_toast%'
  AND table_schema NOT LIKE 'pg_temp_%'
ORDER BY table_schema, table_name;
`

// buildDSN produces a postgres:// URL understood by pgx/stdlib. Options
// from cfg.Options are passed as query parameters, so callers can set
// sslmode=disable, application_name=sqlgo, etc without this package
// knowing about them.
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
