// Package firebird registers nakagami/firebirdsql, a pure-Go client
// for Firebird 2.5/3.x/4.x. Import for side effects.
//
// cfg.Host/Port/User/Password are standard. cfg.Database is the server
// path or alias to the .fdb file (e.g. "/var/lib/firebird/data/acme.fdb"
// or a pre-configured alias). Firebird has no schemas; the explorer
// gets a synthesized "main" bucket.
package firebird

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"

	_ "github.com/nakagami/firebirdsql"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/sqltok"
)

const driverName = "firebird"

func init() {
	db.Register(driver{})
}

type driver struct{}

func (driver) Name() string { return driverName }

var capabilities = db.Capabilities{
	SchemaDepth:          db.SchemaDepthFlat,
	LimitSyntax:          db.LimitSyntaxFetchFirst,
	IdentifierQuote:      '"',
	SupportsCancel:       true,
	SupportsTLS:          false,
	ExplainFormat:        db.ExplainFormatNone,
	Dialect:              sqltok.DialectFirebird,
	SupportsTransactions: true,
}

func (driver) Capabilities() db.Capabilities { return capabilities }

func (driver) Open(ctx context.Context, cfg db.Config) (db.Conn, error) {
	dsn := buildDSN(cfg)
	sqlDB, err := sql.Open("firebirdsql", dsn)
	if err != nil {
		return nil, fmt.Errorf("firebird open: %w", err)
	}
	conn, err := db.OpenSQL(ctx, sqlDB, db.SQLOptions{
		DriverName:         driverName,
		Capabilities:       capabilities,
		SchemaQuery:        schemaQuery,
		ColumnsBuilder:     buildColumnsQuery,
		RoutinesQuery:      routinesQuery,
		TriggersQuery:      triggersQuery,
		IsPermissionDenied: isPermissionDenied,
		DefinitionFetcher:  fetchDefinition,
	})
	if err != nil {
		return nil, fmt.Errorf("firebird: %w", err)
	}
	return conn, nil
}

// isPermissionDenied matches Firebird "no permission" SQLCODEs surfaced
// in the error text. firebirdsql formats them as "... SQLCODE: -551 ..."
// plus human message.
func isPermissionDenied(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "-551"): // no permission for access to
		return true
	case strings.Contains(msg, "no permission"):
		return true
	}
	return false
}

// Firebird stores identifiers uppercase in RDB$ system tables and pads
// CHAR columns with trailing spaces, so we TRIM everything we read back.
// RDB$SYSTEM_FLAG = 1 marks engine-owned objects.
const schemaQuery = `
SELECT
    CAST('main' AS VARCHAR(32))                     AS schema_name,
    TRIM(RDB$RELATION_NAME)                         AS name,
    CASE WHEN RDB$VIEW_SOURCE IS NULL THEN 0 ELSE 1 END AS is_view,
    CASE WHEN COALESCE(RDB$SYSTEM_FLAG, 0) = 0 THEN 0 ELSE 1 END AS is_system
FROM RDB$RELATIONS
ORDER BY 2
`

const routinesQuery = `
SELECT
    CAST('main' AS VARCHAR(32))                     AS schema_name,
    TRIM(RDB$PROCEDURE_NAME)                        AS name,
    CAST('P' AS CHAR(1))                            AS kind,
    CAST('PSQL' AS VARCHAR(16))                     AS language,
    CASE WHEN COALESCE(RDB$SYSTEM_FLAG, 0) = 0 THEN 0 ELSE 1 END AS is_system
FROM RDB$PROCEDURES
UNION ALL
SELECT
    CAST('main' AS VARCHAR(32)),
    TRIM(RDB$FUNCTION_NAME),
    CAST('F' AS CHAR(1)),
    CAST('PSQL' AS VARCHAR(16)),
    CASE WHEN COALESCE(RDB$SYSTEM_FLAG, 0) = 0 THEN 0 ELSE 1 END
FROM RDB$FUNCTIONS
ORDER BY 2
`

// Firebird encodes trigger timing+event as an integer:
//
//	1 BEFORE INSERT   2 AFTER INSERT
//	3 BEFORE UPDATE   4 AFTER UPDATE
//	5 BEFORE DELETE   6 AFTER DELETE
//
// Values >= 8192 are multi-event DB-level triggers; we label those as
// DATABASE so the explorer at least shows something useful.
const triggersQuery = `
SELECT
    CAST('main' AS VARCHAR(32))                     AS schema_name,
    COALESCE(TRIM(RDB$RELATION_NAME), '')           AS table_name,
    TRIM(RDB$TRIGGER_NAME)                          AS name,
    CASE MOD(RDB$TRIGGER_TYPE - 1, 2)
         WHEN 0 THEN 'BEFORE'
         ELSE 'AFTER'
    END                                             AS timing,
    CASE
         WHEN RDB$TRIGGER_TYPE IN (1,2) THEN 'INSERT'
         WHEN RDB$TRIGGER_TYPE IN (3,4) THEN 'UPDATE'
         WHEN RDB$TRIGGER_TYPE IN (5,6) THEN 'DELETE'
         ELSE 'DATABASE'
    END                                             AS event,
    CASE WHEN COALESCE(RDB$SYSTEM_FLAG, 0) = 0 THEN 0 ELSE 1 END AS is_system
FROM RDB$TRIGGERS
ORDER BY 2, 3
`

// buildColumnsQuery inlines the table name as a literal because the
// firebirdsql driver's ? binds sometimes refuse CHAR(31) comparisons
// against parameters. Names are identifiers, not user input.
func buildColumnsQuery(t db.TableRef) (string, []any) {
	q := fmt.Sprintf(`
SELECT
    TRIM(rf.RDB$FIELD_NAME) AS column_name,
    TRIM(
        CASE f.RDB$FIELD_TYPE
            WHEN  7 THEN 'SMALLINT'
            WHEN  8 THEN 'INTEGER'
            WHEN 10 THEN 'FLOAT'
            WHEN 12 THEN 'DATE'
            WHEN 13 THEN 'TIME'
            WHEN 14 THEN 'CHAR'
            WHEN 16 THEN 'BIGINT'
            WHEN 27 THEN 'DOUBLE PRECISION'
            WHEN 35 THEN 'TIMESTAMP'
            WHEN 37 THEN 'VARCHAR'
            WHEN 261 THEN 'BLOB'
            ELSE 'UNKNOWN'
        END
    ) AS type_name
FROM RDB$RELATION_FIELDS rf
JOIN RDB$FIELDS f ON rf.RDB$FIELD_SOURCE = f.RDB$FIELD_NAME
WHERE rf.RDB$RELATION_NAME = %s
ORDER BY rf.RDB$FIELD_POSITION`, fbQuoteString(t.Name))
	return q, nil
}

// fetchDefinition reconstructs DDL from RDB$ source columns. Firebird
// doesn't ship a GET_DDL equivalent; we return the stored body prefixed
// with a rough CREATE header so the result is runnable after DROP.
func fetchDefinition(ctx context.Context, sqlDB *sql.DB, kind, schema, name string) (string, error) {
	var body sql.NullString
	switch kind {
	case "view":
		err := sqlDB.QueryRowContext(ctx, `
SELECT RDB$VIEW_SOURCE FROM RDB$RELATIONS
WHERE RDB$RELATION_NAME = ? AND RDB$VIEW_SOURCE IS NOT NULL`,
			padFBName(name)).Scan(&body)
		if err != nil {
			return wrapNotFound(err, kind, name)
		}
		return fmt.Sprintf("DROP VIEW %s;\nCREATE VIEW %s AS\n%s;",
			fbQuoteIdent(name), fbQuoteIdent(name), strings.TrimRight(body.String, " \r\n\t;")), nil
	case "procedure":
		err := sqlDB.QueryRowContext(ctx, `
SELECT RDB$PROCEDURE_SOURCE FROM RDB$PROCEDURES
WHERE RDB$PROCEDURE_NAME = ?`, padFBName(name)).Scan(&body)
		if err != nil {
			return wrapNotFound(err, kind, name)
		}
		return fmt.Sprintf("DROP PROCEDURE %s;\nCREATE PROCEDURE %s AS\n%s;",
			fbQuoteIdent(name), fbQuoteIdent(name), strings.TrimRight(body.String, " \r\n\t;")), nil
	case "trigger":
		var table sql.NullString
		err := sqlDB.QueryRowContext(ctx, `
SELECT RDB$RELATION_NAME, RDB$TRIGGER_SOURCE FROM RDB$TRIGGERS
WHERE RDB$TRIGGER_NAME = ?`, padFBName(name)).Scan(&table, &body)
		if err != nil {
			return wrapNotFound(err, kind, name)
		}
		tbl := strings.TrimSpace(table.String)
		return fmt.Sprintf("DROP TRIGGER %s;\nCREATE TRIGGER %s FOR %s AS\n%s;",
			fbQuoteIdent(name), fbQuoteIdent(name), fbQuoteIdent(tbl),
			strings.TrimRight(body.String, " \r\n\t;")), nil
	default:
		return "", db.ErrDefinitionUnsupported
	}
}

func wrapNotFound(err error, kind, name string) (string, error) {
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("no definition for %s %s", kind, name)
	}
	return "", fmt.Errorf("firebird definition: %w", err)
}

func fbQuoteIdent(s string) string {
	return `"` + strings.ReplaceAll(strings.TrimSpace(s), `"`, `""`) + `"`
}

func fbQuoteString(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// padFBName right-pads to 31 chars when comparing against RDB$ CHAR(31)
// columns — drivers that don't auto-pad fail these lookups silently.
// firebirdsql does pad, but we trim-then-repad defensively.
func padFBName(s string) string { return strings.TrimSpace(s) }

// buildDSN formats the firebirdsql DSN:
//
//	user:password@host[:port]/path
//
// cfg.Options round-trip as query-string params (role, charset, ...).
func buildDSN(cfg db.Config) string {
	host := cfg.Host
	if host == "" {
		host = "localhost"
	}
	port := cfg.Port
	hostPart := host
	if port != 0 {
		hostPart = fmt.Sprintf("%s:%d", host, port)
	}
	user := url.QueryEscape(cfg.User)
	pass := url.QueryEscape(cfg.Password)
	dsn := fmt.Sprintf("%s:%s@%s/%s", user, pass, hostPart, cfg.Database)
	if len(cfg.Options) > 0 {
		q := url.Values{}
		for k, v := range cfg.Options {
			q.Set(k, v)
		}
		dsn += "?" + q.Encode()
	}
	return dsn
}
