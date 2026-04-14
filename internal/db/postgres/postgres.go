// Package postgres registers pgx/v5/stdlib. Import for side effects.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

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
		DefinitionFetcher:  fetchDefinition,
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

// fetchDefinition returns runnable DDL for a view/procedure/function/trigger.
// Views use CREATE OR REPLACE VIEW. Procedures/functions use pg_get_functiondef
// which already emits CREATE OR REPLACE. Triggers emit DROP TRIGGER IF EXISTS
// followed by pg_get_triggerdef (postgres has no CREATE OR REPLACE TRIGGER).
func fetchDefinition(ctx context.Context, sqlDB *sql.DB, kind, schema, name string) (string, error) {
	switch kind {
	case "view":
		var body sql.NullString
		q := `SELECT pg_get_viewdef(format('%I.%I', $1::text, $2::text)::regclass, true)`
		if err := sqlDB.QueryRowContext(ctx, q, schema, name).Scan(&body); err != nil {
			return "", fmt.Errorf("pg_get_viewdef: %w", err)
		}
		if !body.Valid {
			return "", fmt.Errorf("no definition for view %s.%s", schema, name)
		}
		return fmt.Sprintf("CREATE OR REPLACE VIEW %s.%s AS\n%s",
			pgQuoteIdent(schema), pgQuoteIdent(name), strings.TrimRight(body.String, "\r\n\t ;")+";"), nil

	case "procedure", "function":
		var body sql.NullString
		q := `
SELECT pg_get_functiondef(p.oid)
FROM pg_proc p
JOIN pg_namespace n ON n.oid = p.pronamespace
WHERE n.nspname = $1 AND p.proname = $2
LIMIT 1`
		if err := sqlDB.QueryRowContext(ctx, q, schema, name).Scan(&body); err != nil {
			return "", fmt.Errorf("pg_get_functiondef: %w", err)
		}
		if !body.Valid {
			return "", fmt.Errorf("no definition for %s %s.%s", kind, schema, name)
		}
		return body.String, nil

	case "trigger":
		var (
			body  sql.NullString
			table string
		)
		q := `
SELECT pg_get_triggerdef(t.oid, true), c.relname
FROM pg_trigger t
JOIN pg_class c ON c.oid = t.tgrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND t.tgname = $2 AND NOT t.tgisinternal
LIMIT 1`
		if err := sqlDB.QueryRowContext(ctx, q, schema, name).Scan(&body, &table); err != nil {
			return "", fmt.Errorf("pg_get_triggerdef: %w", err)
		}
		if !body.Valid {
			return "", fmt.Errorf("no definition for trigger %s.%s", schema, name)
		}
		drop := fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON %s.%s;\n",
			pgQuoteIdent(name), pgQuoteIdent(schema), pgQuoteIdent(table))
		return drop + strings.TrimRight(body.String, "\r\n\t ;") + ";", nil
	}
	return "", db.ErrDefinitionUnsupported
}

func pgQuoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

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
