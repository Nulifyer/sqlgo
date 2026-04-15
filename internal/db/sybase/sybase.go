// Package sybase registers the Nulifyer/go-tds driver for Sybase ASE
// (TDS 5.0). Import for side effects.
package sybase

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	_ "github.com/Nulifyer/go-tds"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/sqltok"
)

const driverName = "sybase"

func init() {
	db.Register(driver{})
}

type driver struct{}

func (driver) Name() string { return driverName }

var capabilities = db.Capabilities{
	SchemaDepth:           db.SchemaDepthSchemas,
	LimitSyntax:           db.LimitSyntaxSelectTop,
	IdentifierQuote:       '"',
	SupportsCancel:        true,
	SupportsTLS:           true,
	ExplainFormat:         db.ExplainFormatNone,
	Dialect:               sqltok.DialectSybase,
	SupportsTransactions:  true,
	SupportsCrossDatabase: true,
}

func (driver) Capabilities() db.Capabilities { return capabilities }

func (driver) Open(ctx context.Context, cfg db.Config) (db.Conn, error) {
	dsn := buildDSN(cfg)
	sqlDB, err := sql.Open("tds", dsn)
	if err != nil {
		return nil, fmt.Errorf("sybase open: %w", err)
	}
	conn, err := db.OpenSQL(ctx, sqlDB, db.SQLOptions{
		DriverName:        driverName,
		Capabilities:      capabilities,
		SchemaQuery:       schemaQuery,
		ColumnsQuery:      columnsQuery,
		RoutinesQuery:     routinesQuery,
		TriggersQuery:     triggersQuery,
		DefinitionFetcher: fetchDefinition,
		DatabaseListQuery: databaseListQuery,
		UseDatabaseStmt:   useDatabaseStmt,
	})
	if err != nil {
		return nil, fmt.Errorf("sybase: %w", err)
	}
	return conn, nil
}

// Sybase ASE sysobjects type codes:
//   U = user table, S = system table, V = view
//   P = stored procedure, XP = extended proc
//   TR = trigger, SF/RF = functions (ASE 15+)
// Owners live in sysusers keyed by sysobjects.uid.

const schemaQuery = `
SELECT
    u.name AS schema_name,
    o.name AS name,
    CASE WHEN o.type = 'V' THEN 1 ELSE 0 END AS is_view,
    CASE WHEN o.type = 'S' OR u.name IN ('dbo') AND o.name LIKE 'sys%' THEN 1 ELSE 0 END AS is_system
FROM sysobjects o
JOIN sysusers u ON u.uid = o.uid
WHERE o.type IN ('U','V','S')
ORDER BY u.name, o.name
`

const routinesQuery = `
SELECT
    u.name AS schema_name,
    o.name AS name,
    CASE o.type
        WHEN 'P'  THEN 'P'
        WHEN 'XP' THEN 'P'
        ELSE 'F'
    END AS kind,
    'SQL' AS language,
    CASE WHEN o.type = 'XP' OR (u.name = 'dbo' AND o.name LIKE 'sp_%') THEN 1 ELSE 0 END AS is_system
FROM sysobjects o
JOIN sysusers u ON u.uid = o.uid
WHERE o.type IN ('P','XP','SF','RF')
ORDER BY u.name, o.name
`

const triggersQuery = `
SELECT
    u.name AS schema_name,
    p.name AS table_name,
    o.name AS name,
    'AFTER' AS timing,
    'INSERT/UPDATE/DELETE' AS event,
    0 AS is_system
FROM sysobjects o
JOIN sysusers u ON u.uid = o.uid
JOIN sysobjects p ON p.id = o.deltrig OR p.id = o.instrig OR p.id = o.updtrig
WHERE o.type = 'TR'
ORDER BY u.name, p.name, o.name
`

const columnsQuery = `
SELECT c.name, t.name
FROM syscolumns c
JOIN systypes t ON t.usertype = c.usertype
JOIN sysobjects o ON o.id = c.id
JOIN sysusers u ON u.uid = o.uid
WHERE u.name = ? AND o.name = ?
ORDER BY c.colid
`

// fetchDefinition rebuilds DDL from syscomments.text fragments (ordered
// by colid). ASE stores CREATE text in 255-char chunks that must be
// concatenated. No CREATE OR REPLACE on ASE: caller must DROP first.
func fetchDefinition(ctx context.Context, sqlDB *sql.DB, kind, schema, name string) (string, error) {
	var dropKw string
	switch kind {
	case "view":
		dropKw = "VIEW"
	case "procedure":
		dropKw = "PROCEDURE"
	case "function":
		dropKw = "FUNCTION"
	case "trigger":
		dropKw = "TRIGGER"
	default:
		return "", db.ErrDefinitionUnsupported
	}
	const q = `
SELECT c.text
FROM syscomments c
JOIN sysobjects o ON o.id = c.id
JOIN sysusers  u ON u.uid = o.uid
WHERE u.name = ? AND o.name = ?
ORDER BY c.colid, c.number
`
	rows, err := sqlDB.QueryContext(ctx, q, schema, name)
	if err != nil {
		return "", fmt.Errorf("syscomments: %w", err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var chunk sql.NullString
		if err := rows.Scan(&chunk); err != nil {
			return "", fmt.Errorf("syscomments scan: %w", err)
		}
		if chunk.Valid {
			b.WriteString(chunk.String)
		}
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("syscomments rows: %w", err)
	}
	body := strings.TrimSpace(b.String())
	if body == "" {
		return "", fmt.Errorf("no definition for %s %s.%s (may be encrypted or hidden)", kind, schema, name)
	}
	qualified := quoteIdent(schema) + "." + quoteIdent(name)
	drop := fmt.Sprintf("DROP %s %s\ngo\n", dropKw, qualified)
	return drop + strings.TrimRight(body, "\r\n\t ;") + "\ngo\n", nil
}

// databaseListQuery lists user databases from master..sysdatabases,
// filtering out the standard ASE system databases. status & 320 masks
// offline (32) and suspect (256) states so the explorer never tries to
// USE an unusable database.
const databaseListQuery = `
SELECT name
FROM master..sysdatabases
WHERE name NOT IN ('master','model','tempdb','sybsystemprocs','sybsystemdb','dbccdb')
  AND (status & 320) = 0
ORDER BY name
`

// useDatabaseStmt emits `use [dbname]`. ASE 15+ accepts bracketed
// identifiers without requiring `set quoted_identifier on`. `]` inside
// a name is doubled defensively; ASE disallows it in practice.
func useDatabaseStmt(name string) string {
	return "use [" + strings.ReplaceAll(name, "]", "]]") + "]"
}

func quoteIdent(s string) string {
	return "\"" + strings.ReplaceAll(s, "\"", "\"\"") + "\""
}

// buildDSN produces a tds:// URL accepted by github.com/Nulifyer/go-tds.
// cfg.Options → query params (e.g. encryptPassword=yes, ssl=on, sslCA=path).
func buildDSN(cfg db.Config) string {
	host := cfg.Host
	if host == "" {
		host = "localhost"
	}
	port := cfg.Port
	if port == 0 {
		port = 5000
	}

	u := url.URL{
		Scheme: "tds",
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
