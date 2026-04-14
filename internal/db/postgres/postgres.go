// Package postgres registers pgx/v5/stdlib. Import for side effects.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strconv"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/Nulifyer/sqlgo/internal/db"
)

// isPermissionDenied returns true for SQLSTATE 42501 (insufficient_privilege).
func isPermissionDenied(err error) bool {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Code == "42501"
	}
	return false
}

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
		DriverName:         driverName,
		Capabilities:       capabilities,
		SchemaQuery:        schemaQuery,
		ColumnsQuery:       columnsQuery,
		RoutinesQuery:      routinesQuery,
		TriggersQuery:      triggersQuery,
		IsPermissionDenied: isPermissionDenied,
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

// routinesQuery: functions, procedures, aggregates from pg_proc.
// prokind: f=function, p=procedure, a=aggregate, w=window.
const routinesQuery = `
SELECT
    n.nspname AS schema_name,
    p.proname AS name,
    CASE p.prokind
        WHEN 'p' THEN 'P'
        WHEN 'a' THEN 'A'
        ELSE 'F'
    END AS kind,
    l.lanname AS language,
    CASE WHEN n.nspname IN ('pg_catalog', 'information_schema') THEN 1 ELSE 0 END AS is_system
FROM pg_proc p
JOIN pg_namespace n ON n.oid = p.pronamespace
JOIN pg_language l ON l.oid = p.prolang
WHERE p.prokind IN ('f', 'p', 'a')
ORDER BY n.nspname, p.proname;
`

// triggersQuery: user triggers from pg_trigger (skip internal).
const triggersQuery = `
SELECT
    n.nspname AS schema_name,
    c.relname AS table_name,
    t.tgname  AS name,
    CASE WHEN (t.tgtype & 2) = 2 THEN 'BEFORE' ELSE 'AFTER' END AS timing,
    trim(
        both ' ' FROM
        CASE WHEN (t.tgtype & 4)  = 4  THEN ' INSERT' ELSE '' END ||
        CASE WHEN (t.tgtype & 8)  = 8  THEN ' DELETE' ELSE '' END ||
        CASE WHEN (t.tgtype & 16) = 16 THEN ' UPDATE' ELSE '' END ||
        CASE WHEN (t.tgtype & 32) = 32 THEN ' TRUNCATE' ELSE '' END
    ) AS event,
    CASE WHEN n.nspname IN ('pg_catalog', 'information_schema') THEN 1 ELSE 0 END AS is_system
FROM pg_trigger t
JOIN pg_class c ON c.oid = t.tgrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE NOT t.tgisinternal
ORDER BY n.nspname, c.relname, t.tgname;
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
