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

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	rdsauth "github.com/aws/aws-sdk-go-v2/feature/rds/auth"
	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/sqltok"
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
	db.RegisterProfile(Profile)
	db.RegisterTransport(PgxTransport)
	db.Register(preset{})
}

var Profile = db.Profile{
	Name: driverName,
	Capabilities: db.Capabilities{
		SchemaDepth:          db.SchemaDepthSchemas,
		LimitSyntax:          db.LimitSyntaxLimit,
		IdentifierQuote:      '"',
		SupportsCancel:       true,
		SupportsTLS:          true,
		ExplainFormat:        db.ExplainFormatPostgresJSON,
		Dialect:              sqltok.DialectPostgres,
		SupportsTransactions: true,
	},
	SchemaQuery:        schemaQuery,
	ColumnsQuery:       columnsQuery,
	RoutinesQuery:      routinesQuery,
	TriggersQuery:      triggersQuery,
	IsPermissionDenied: isPermissionDenied,
	DefinitionFetcher:  fetchDefinition,
}

var PgxTransport = db.Transport{
	Name:          "pgx",
	SQLDriverName: "pgx",
	DefaultPort:   5432,
	SupportsTLS:   true,
	BuildDSN:      buildDSN,
}

type preset struct{}

func (preset) Name() string                  { return driverName }
func (preset) Capabilities() db.Capabilities { return Profile.Capabilities }
func (preset) Open(ctx context.Context, cfg db.Config) (db.Conn, error) {
	// AWS RDS IAM auth: swap cfg.Password for a freshly-generated IAM
	// auth token (15-min TTL) before the DSN is built. The flag and
	// region are stripped from Options so they don't leak into the
	// DSN query string -- pgx rejects unknown startup params.
	if rdsIAMEnabled(cfg.Options) {
		mutated, err := applyRDSIAMToken(ctx, cfg)
		if err != nil {
			return nil, fmt.Errorf("rds iam token: %w", err)
		}
		cfg = mutated
	}
	return db.OpenWith(ctx, Profile, PgxTransport, cfg)
}

// rdsIAMEnabled reports whether the aws_rds_iam option is set to a
// truthy value. Case-insensitive so form cyclers, env overrides, and
// hand-edited YAML all behave the same.
func rdsIAMEnabled(opts map[string]string) bool {
	v := strings.TrimSpace(opts["aws_rds_iam"])
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// applyRDSIAMToken loads AWS credentials (via SDK default chain:
// env vars, ~/.aws/credentials, EC2/ECS/EKS role), generates a signed
// auth token for host:port/user, and returns a copy of cfg with
// Password replaced by the token. The aws_rds_iam / aws_region keys
// are removed from Options so they don't reach the DSN. Region
// resolution: explicit aws_region wins; otherwise we fall back to
// whatever awsconfig.LoadDefaultConfig picks from env/profile.
func applyRDSIAMToken(ctx context.Context, cfg db.Config) (db.Config, error) {
	region := strings.TrimSpace(cfg.Options["aws_region"])
	loadOpts := []func(*awsconfig.LoadOptions) error{}
	if region != "" {
		loadOpts = append(loadOpts, awsconfig.WithRegion(region))
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return cfg, fmt.Errorf("load aws config: %w", err)
	}
	if awsCfg.Region == "" {
		return cfg, errors.New("aws region not set (populate aws_region option or AWS_REGION env)")
	}
	host := cfg.Host
	if host == "" {
		host = "localhost"
	}
	port := cfg.Port
	if port == 0 {
		port = 5432
	}
	endpoint := host + ":" + strconv.Itoa(port)
	token, err := rdsauth.BuildAuthToken(ctx, endpoint, awsCfg.Region, cfg.User, awsCfg.Credentials)
	if err != nil {
		return cfg, fmt.Errorf("build rds auth token: %w", err)
	}
	out := cfg
	out.Password = token
	out.Options = make(map[string]string, len(cfg.Options))
	for k, v := range cfg.Options {
		if k == "aws_rds_iam" || k == "aws_region" {
			continue
		}
		out.Options[k] = v
	}
	return out, nil
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
