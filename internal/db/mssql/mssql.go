// Package mssql registers the go-mssqldb driver. Import for
// side effects.
package mssql

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strconv"

	_ "github.com/microsoft/go-mssqldb"

	"github.com/Nulifyer/sqlgo/internal/db"
)

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
}

func (driver) Capabilities() db.Capabilities { return capabilities }

func (driver) Open(ctx context.Context, cfg db.Config) (db.Conn, error) {
	dsn := buildDSN(cfg)
	sqlDB, err := sql.Open("sqlserver", dsn)
	if err != nil {
		return nil, fmt.Errorf("mssql open: %w", err)
	}
	conn, err := db.OpenSQL(ctx, sqlDB, db.SQLOptions{
		DriverName:   driverName,
		Capabilities: capabilities,
		SchemaQuery:  schemaQuery,
		ColumnsQuery: columnsQuery,
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

// columnsQuery uses @p1/@p2 (go-mssqldb named placeholders).
const columnsQuery = `
SELECT COLUMN_NAME, DATA_TYPE
FROM INFORMATION_SCHEMA.COLUMNS
WHERE TABLE_SCHEMA = @p1 AND TABLE_NAME = @p2
ORDER BY ORDINAL_POSITION;
`

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
