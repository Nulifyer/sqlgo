// Package mysql registers go-sql-driver/mysql. Import for side effects.
package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"

	gomysql "github.com/go-sql-driver/mysql"

	"github.com/Nulifyer/sqlgo/internal/db"
)

const driverName = "mysql"

func init() {
	db.Register(driver{})
}

type driver struct{}

func (driver) Name() string { return driverName }

var capabilities = db.Capabilities{
	SchemaDepth:     db.SchemaDepthSchemas,
	LimitSyntax:     db.LimitSyntaxLimit,
	IdentifierQuote: '`',
	SupportsCancel:  true,
	SupportsTLS:     true,
	ExplainFormat:   db.ExplainFormatMySQLJSON,
}

func (driver) Capabilities() db.Capabilities { return capabilities }

func (driver) Open(ctx context.Context, cfg db.Config) (db.Conn, error) {
	dsn := buildDSN(cfg)
	sqlDB, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("mysql open: %w", err)
	}
	conn, err := db.OpenSQL(ctx, sqlDB, db.SQLOptions{
		DriverName:   driverName,
		Capabilities: capabilities,
		SchemaQuery:  schemaQuery,
		ColumnsQuery: columnsQuery,
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

// columnsQuery uses ? (positional mysql placeholders).
const columnsQuery = `
SELECT COLUMN_NAME, DATA_TYPE
FROM information_schema.columns
WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
ORDER BY ORDINAL_POSITION;
`

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
