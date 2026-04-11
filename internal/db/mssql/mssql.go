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

// schemaQuery: user tables/views only. Excludes sys/INFORMATION_SCHEMA.
const schemaQuery = `
SELECT
	TABLE_SCHEMA AS [schema],
	TABLE_NAME   AS name,
	CASE WHEN TABLE_TYPE = 'VIEW' THEN 1 ELSE 0 END AS is_view
FROM INFORMATION_SCHEMA.TABLES
WHERE TABLE_SCHEMA NOT IN ('sys', 'INFORMATION_SCHEMA')
ORDER BY TABLE_SCHEMA, TABLE_NAME;
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
