// Package mssql registers the go-mssqldb driver. Import for
// side effects.
package mssql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	mssqldb "github.com/microsoft/go-mssqldb"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/sqltok"
)

// isPermissionDenied returns true for MSSQL permission error numbers:
// 229 (SELECT/EXECUTE denied), 230 (column denied), 297 (user lacks rights),
// 300 (VIEW SERVER STATE), 916 (cross-DB denied).
func isPermissionDenied(err error) bool {
	var me mssqldb.Error
	if !errors.As(err, &me) {
		return false
	}
	switch me.Number {
	case 229, 230, 297, 300, 916:
		return true
	}
	for _, e := range me.All {
		switch e.Number {
		case 229, 230, 297, 300, 916:
			return true
		}
	}
	return false
}

const driverName = "mssql"

func init() {
	db.Register(driver{})
}

type driver struct{}

func (driver) Name() string { return driverName }

var capabilities = db.Capabilities{
	SchemaDepth:     db.SchemaDepthSchemas,
	LimitSyntax:     db.LimitSyntaxSelectTop,
	IdentifierQuote: '[',
	SupportsCancel:  true,
	SupportsTLS:     true,
	ExplainFormat:        db.ExplainFormatMSSQLXML,
	Dialect:              sqltok.DialectMSSQL,
	SupportsTransactions: true,
}

func (driver) Capabilities() db.Capabilities { return capabilities }

func (driver) Open(ctx context.Context, cfg db.Config) (db.Conn, error) {
	dsn := buildDSN(cfg)
	sqlDB, err := sql.Open("sqlserver", dsn)
	if err != nil {
		return nil, fmt.Errorf("mssql open: %w", err)
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
		ExplainRunner:      runExplain,
	})
	if err != nil {
		return nil, fmt.Errorf("mssql: %w", err)
	}
	return conn, nil
}

// schemaQuery: user + system tables/views. sys/INFORMATION_SCHEMA
// are flagged is_system=1 for the explorer Sys group. Union the two
// because INFORMATION_SCHEMA.TABLES itself does not list objects in
// the sys schema.
// Drive off sys.objects so is_ms_shipped routes dbo-schema system
// tables (spt_*, MSreplication_options) into Sys correctly.
// INFORMATION_SCHEMA views are accessible via the sys schema too;
// flagged via o.is_ms_shipped. Filter on o.type for base tables and
// views only.
const schemaQuery = `
SELECT
	s.name AS [schema],
	o.name AS name,
	CASE WHEN o.type = 'V' THEN 1 ELSE 0 END AS is_view,
	CASE WHEN o.is_ms_shipped = 1 OR s.name IN ('sys', 'INFORMATION_SCHEMA') THEN 1 ELSE 0 END AS is_system
FROM sys.objects o
JOIN sys.schemas s ON s.schema_id = o.schema_id
WHERE o.type IN ('U','V')
ORDER BY s.name, o.name;
`

// routinesQuery: procedures, scalar/inline/table-valued functions via sys.objects.
// type codes: P=procedure, FN=scalar fn, IF=inline TVF, TF=multi-stmt TVF, AF=aggregate.
const routinesQuery = `
SELECT
    s.name AS [schema],
    o.name AS name,
    CASE o.type
        WHEN 'P'  THEN 'P'
        WHEN 'AF' THEN 'A'
        ELSE 'F'
    END AS kind,
    CASE WHEN o.type = 'AF' THEN 'CLR' ELSE 'SQL' END AS language,
    CASE WHEN o.is_ms_shipped = 1 OR s.name IN ('sys','INFORMATION_SCHEMA') THEN 1 ELSE 0 END AS is_system
FROM sys.objects o
JOIN sys.schemas s ON s.schema_id = o.schema_id
WHERE o.type IN ('P','FN','IF','TF','AF')
ORDER BY s.name, o.name;
`

// triggersQuery: DML triggers via sys.triggers joined to parent table.
// type_desc values combine timing + events; normalize to AFTER/INSTEAD OF.
const triggersQuery = `
SELECT
    s.name AS [schema],
    t.name AS table_name,
    tr.name AS name,
    CASE WHEN tr.is_instead_of_trigger = 1 THEN 'INSTEAD OF' ELSE 'AFTER' END AS timing,
    STUFF((
        SELECT ',' + te.type_desc
        FROM sys.trigger_events te
        WHERE te.object_id = tr.object_id
        FOR XML PATH('')
    ), 1, 1, '') AS event,
    CASE WHEN tr.is_ms_shipped = 1 OR s.name IN ('sys','INFORMATION_SCHEMA') THEN 1 ELSE 0 END AS is_system
FROM sys.triggers tr
JOIN sys.tables t ON t.object_id = tr.parent_id
JOIN sys.schemas s ON s.schema_id = t.schema_id
WHERE tr.parent_class = 1
ORDER BY s.name, t.name, tr.name;
`

// columnsQuery uses @p1/@p2 (go-mssqldb named placeholders).
const columnsQuery = `
SELECT COLUMN_NAME, DATA_TYPE
FROM INFORMATION_SCHEMA.COLUMNS
WHERE TABLE_SCHEMA = @p1 AND TABLE_NAME = @p2
ORDER BY ORDINAL_POSITION;
`

// fetchDefinition returns runnable DDL for a view/procedure/function/trigger.
// Uses OBJECT_DEFINITION(OBJECT_ID(...)) to retrieve the original CREATE text,
// then rewrites the leading `CREATE` keyword to `CREATE OR ALTER` so the text
// is directly runnable as an edit.
func fetchDefinition(ctx context.Context, sqlDB *sql.DB, kind, schema, name string) (string, error) {
	switch kind {
	case "view", "procedure", "function", "trigger":
	default:
		return "", db.ErrDefinitionUnsupported
	}
	qualified := "[" + strings.ReplaceAll(schema, "]", "]]") + "].[" + strings.ReplaceAll(name, "]", "]]") + "]"
	row := sqlDB.QueryRowContext(ctx, "SELECT OBJECT_DEFINITION(OBJECT_ID(@p1))", qualified)
	var def sql.NullString
	if err := row.Scan(&def); err != nil {
		return "", fmt.Errorf("object_definition: %w", err)
	}
	if !def.Valid || strings.TrimSpace(def.String) == "" {
		return "", fmt.Errorf("no definition available for %s %s.%s (may be encrypted or not found)", kind, schema, name)
	}
	return rewriteCreateOrAlter(def.String), nil
}

// runExplain pins a single connection, toggles SET SHOWPLAN_XML ON,
// runs the target SQL (which returns the plan XML as one row instead
// of executing), then turns SHOWPLAN off. The pin is required because
// SHOWPLAN_XML is session state; a *sql.DB may hand the next call a
// different pooled connection. Returns the single XML row as rows[0][0].
func runExplain(ctx context.Context, sqlDB *sql.DB, query string) ([][]any, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(query), "; \t\r\n")
	if trimmed == "" {
		return nil, fmt.Errorf("explain: empty query")
	}
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("explain pin conn: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "SET SHOWPLAN_XML ON"); err != nil {
		return nil, fmt.Errorf("set showplan_xml on: %w", err)
	}
	// Always try to turn SHOWPLAN off before releasing the conn back to
	// the pool, even if the target query failed. Uses a background ctx
	// so a cancelled parent doesn't leave the session in SHOWPLAN mode.
	defer func() {
		_, _ = conn.ExecContext(context.Background(), "SET SHOWPLAN_XML OFF")
	}()
	rows, err := conn.QueryContext(ctx, trimmed)
	if err != nil {
		return nil, fmt.Errorf("explain query: %w", err)
	}
	defer rows.Close()
	var out [][]any
	for rows.Next() {
		var xml sql.NullString
		if err := rows.Scan(&xml); err != nil {
			return nil, fmt.Errorf("explain scan: %w", err)
		}
		if xml.Valid {
			out = append(out, []any{xml.String})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("explain rows: %w", err)
	}
	return out, nil
}

// rewriteCreateOrAlter finds the first CREATE token (case-insensitive, after any
// leading whitespace/comments) and replaces it with CREATE OR ALTER unless the
// text already contains "OR ALTER" after CREATE.
func rewriteCreateOrAlter(src string) string {
	upper := strings.ToUpper(src)
	idx := strings.Index(upper, "CREATE")
	if idx < 0 {
		return src
	}
	after := strings.TrimLeft(upper[idx+len("CREATE"):], " \t\r\n")
	if strings.HasPrefix(after, "OR ALTER") || strings.HasPrefix(after, "OR REPLACE") {
		return src
	}
	return src[:idx] + "CREATE OR ALTER" + src[idx+len("CREATE"):]
}

// buildDSN produces a sqlserver:// URL. cfg.Options → query params.
func buildDSN(cfg db.Config) string {
	host := cfg.Host
	if host == "" {
		host = "localhost"
	}
	port := cfg.Port
	if port == 0 {
		port = 1433
	}

	u := url.URL{
		Scheme: "sqlserver",
		User:   url.UserPassword(cfg.User, cfg.Password),
		Host:   host + ":" + strconv.Itoa(port),
	}
	q := u.Query()
	if cfg.Database != "" {
		q.Set("database", cfg.Database)
	}
	for k, v := range cfg.Options {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	return u.String()
}
