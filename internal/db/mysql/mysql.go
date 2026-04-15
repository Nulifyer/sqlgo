// Package mysql registers go-sql-driver/mysql. Import for side effects.
package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	gomysql "github.com/go-sql-driver/mysql"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/sqltok"
)

// isPermissionDenied returns true for MySQL access errors:
// 1044 (access denied for db), 1142 (command denied on table),
// 1143 (column denied), 1227 (specific privilege required).
func isPermissionDenied(err error) bool {
	var me *gomysql.MySQLError
	if !errors.As(err, &me) {
		return false
	}
	switch me.Number {
	case 1044, 1142, 1143, 1227:
		return true
	}
	return false
}

const driverName = "mysql"

func init() {
	db.Register(driver{})
}

type driver struct{}

func (driver) Name() string { return driverName }

var capabilities = db.Capabilities{
	SchemaDepth:           db.SchemaDepthSchemas,
	LimitSyntax:           db.LimitSyntaxLimit,
	IdentifierQuote:       '`',
	SupportsCancel:        true,
	SupportsTLS:           true,
	ExplainFormat:         db.ExplainFormatMySQLJSON,
	Dialect:               sqltok.DialectMySQL,
	SupportsTransactions:  true,
	SupportsCrossDatabase: true,
}

func (driver) Capabilities() db.Capabilities { return capabilities }

func (driver) Open(ctx context.Context, cfg db.Config) (db.Conn, error) {
	dsn := buildDSN(cfg)
	sqlDB, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("mysql open: %w", err)
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
		DatabaseListQuery:  databaseListQuery,
		UseDatabaseStmt:    useDatabaseStmt,
	})
	if err != nil {
		return nil, fmt.Errorf("mysql: %w", err)
	}
	return conn, nil
}

// schemaQuery: user + system tables/views. MySQL system DBs
// (mysql, information_schema, performance_schema, sys) are flagged
// is_system=1 so the explorer groups them under Sys.
const schemaQuery = `
SELECT
    TABLE_SCHEMA AS schema_name,
    TABLE_NAME   AS name,
    CASE WHEN TABLE_TYPE = 'VIEW' THEN 1 ELSE 0 END AS is_view,
    CASE WHEN TABLE_SCHEMA IN ('mysql', 'information_schema', 'performance_schema', 'sys') THEN 1 ELSE 0 END AS is_system
FROM information_schema.tables
ORDER BY TABLE_SCHEMA, TABLE_NAME;
`

// routinesQuery: procedures and functions via information_schema.ROUTINES.
const routinesQuery = `
SELECT
    ROUTINE_SCHEMA AS schema_name,
    ROUTINE_NAME   AS name,
    CASE ROUTINE_TYPE WHEN 'PROCEDURE' THEN 'P' ELSE 'F' END AS kind,
    'SQL'          AS language,
    CASE WHEN ROUTINE_SCHEMA IN ('mysql','information_schema','performance_schema','sys') THEN 1 ELSE 0 END AS is_system
FROM information_schema.ROUTINES
ORDER BY ROUTINE_SCHEMA, ROUTINE_NAME;
`

// triggersQuery: user triggers via information_schema.TRIGGERS.
const triggersQuery = `
SELECT
    TRIGGER_SCHEMA         AS schema_name,
    EVENT_OBJECT_TABLE     AS table_name,
    TRIGGER_NAME           AS name,
    ACTION_TIMING          AS timing,
    EVENT_MANIPULATION     AS event,
    CASE WHEN TRIGGER_SCHEMA IN ('mysql','information_schema','performance_schema','sys') THEN 1 ELSE 0 END AS is_system
FROM information_schema.TRIGGERS
ORDER BY TRIGGER_SCHEMA, EVENT_OBJECT_TABLE, TRIGGER_NAME;
`

// databaseListQuery returns user-visible schemas. information_schema,
// mysql, performance_schema, sys are filtered so the explorer's DB
// tier stays on user-owned databases.
const databaseListQuery = `
SELECT SCHEMA_NAME
FROM information_schema.SCHEMATA
WHERE SCHEMA_NAME NOT IN ('information_schema','mysql','performance_schema','sys')
ORDER BY SCHEMA_NAME;
`

// useDatabaseStmt quotes the DB name with backticks. Embedded backticks
// must be doubled per mysql identifier rules.
func useDatabaseStmt(name string) string {
	return "USE `" + strings.ReplaceAll(name, "`", "``") + "`"
}

// columnsQuery uses ? (positional mysql placeholders).
const columnsQuery = `
SELECT COLUMN_NAME, DATA_TYPE
FROM information_schema.columns
WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
ORDER BY ORDINAL_POSITION;
`

// fetchDefinition runs SHOW CREATE {VIEW|PROCEDURE|FUNCTION|TRIGGER} and
// prepends a DROP ... IF EXISTS so the result is runnable as an edit. MySQL
// has no CREATE OR REPLACE for these kinds.
func fetchDefinition(ctx context.Context, sqlDB *sql.DB, kind, schema, name string) (string, error) {
	var kw, createCol, dropKw, dropTarget string
	qualified := mysqlQuoteIdent(schema) + "." + mysqlQuoteIdent(name)
	switch kind {
	case "view":
		kw, createCol, dropKw, dropTarget = "VIEW", "Create View", "VIEW", qualified
	case "procedure":
		kw, createCol, dropKw, dropTarget = "PROCEDURE", "Create Procedure", "PROCEDURE", qualified
	case "function":
		kw, createCol, dropKw, dropTarget = "FUNCTION", "Create Function", "FUNCTION", qualified
	case "trigger":
		kw, createCol, dropKw, dropTarget = "TRIGGER", "SQL Original Statement", "TRIGGER", qualified
	default:
		return "", db.ErrDefinitionUnsupported
	}
	rows, err := sqlDB.QueryContext(ctx, "SHOW CREATE "+kw+" "+qualified)
	if err != nil {
		return "", fmt.Errorf("show create %s: %w", kw, err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return "", fmt.Errorf("show create columns: %w", err)
	}
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return "", fmt.Errorf("show create next: %w", err)
		}
		return "", fmt.Errorf("no definition for %s %s.%s", kind, schema, name)
	}
	vals := make([]sql.NullString, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return "", fmt.Errorf("show create scan: %w", err)
	}
	var body string
	for i, c := range cols {
		if c == createCol {
			body = vals[i].String
			break
		}
	}
	if strings.TrimSpace(body) == "" {
		return "", fmt.Errorf("empty definition for %s %s.%s", kind, schema, name)
	}
	drop := fmt.Sprintf("DROP %s IF EXISTS %s;\n", dropKw, dropTarget)
	return drop + strings.TrimRight(body, "\r\n\t ;") + ";", nil
}

func mysqlQuoteIdent(s string) string {
	return "`" + strings.ReplaceAll(s, "`", "``") + "`"
}

// buildDSN uses gomysql.Config for escaping + parseTime handling.
// Known knobs (tls, parseTime, allowNativePasswords) get lifted
// into Config fields; the rest become raw Params.
func buildDSN(cfg db.Config) string {
	mc := gomysql.NewConfig()
	mc.User = cfg.User
	mc.Passwd = cfg.Password
	mc.Net = "tcp"
	host := cfg.Host
	if host == "" {
		host = "localhost"
	}
	port := cfg.Port
	if port == 0 {
		port = 3306
	}
	mc.Addr = host + ":" + strconv.Itoa(port)
	mc.DBName = cfg.Database
	// parseTime=true so DATETIME comes back as time.Time, not []byte.
	mc.ParseTime = true
	extraKeys := make([]string, 0, len(cfg.Options))
	for k := range cfg.Options {
		extraKeys = append(extraKeys, k)
	}
	sort.Strings(extraKeys)
	for _, k := range extraKeys {
		v := cfg.Options[k]
		switch strings.ToLower(k) {
		case "tls":
			mc.TLSConfig = v
		case "parsetime":
			if strings.EqualFold(v, "false") || v == "0" {
				mc.ParseTime = false
			}
		case "allownativepasswords":
			mc.AllowNativePasswords = !(strings.EqualFold(v, "false") || v == "0")
		default:
			if mc.Params == nil {
				mc.Params = map[string]string{}
			}
			mc.Params[k] = v
		}
	}
	return mc.FormatDSN()
}
