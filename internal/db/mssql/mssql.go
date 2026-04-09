// Package mssql registers the MSSQL driver with internal/db. Import it for
// side effects:
//
//	import _ "github.com/Nulifyer/sqlgo/internal/db/mssql"
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

// capabilities is the MSSQL-specific capability set. MSSQL uses T-SQL's
// SELECT TOP N, bracket-quoted identifiers, and go-mssqldb honors context
// cancellation at the network layer (pure-Go, no cgo).
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
	})
	if err != nil {
		return nil, fmt.Errorf("mssql: %w", err)
	}
	return conn, nil
}

// schemaQuery lists tables and views from INFORMATION_SCHEMA, filtering out
// the built-in system schemas so the explorer only shows user objects. The
// third column is a 0/1 flag (1 = view) to match the shared schema scanner.
const schemaQuery = `
SELECT
	TABLE_SCHEMA AS [schema],
	TABLE_NAME   AS name,
	CASE WHEN TABLE_TYPE = 'VIEW' THEN 1 ELSE 0 END AS is_view
FROM INFORMATION_SCHEMA.TABLES
WHERE TABLE_SCHEMA NOT IN ('sys', 'INFORMATION_SCHEMA')
ORDER BY TABLE_SCHEMA, TABLE_NAME;
`

// buildDSN produces a sqlserver:// URL understood by go-mssqldb.
// Connection options from cfg.Options are passed as query parameters, so
// callers can set e.g. "encrypt=disable" or "TrustServerCertificate=true"
// without this package knowing about them.
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
