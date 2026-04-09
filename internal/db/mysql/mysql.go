// Package mysql registers the pure-Go MySQL driver with internal/db.
// Import it for side effects:
//
//	import _ "github.com/Nulifyer/sqlgo/internal/db/mysql"
//
// The underlying driver is github.com/go-sql-driver/mysql -- the
// canonical pure-Go MySQL client. It honors ctx cancellation at the
// network layer and supports TLS via DSN parameters.
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

// capabilities is the MySQL-specific capability set. MySQL groups tables
// under "schemas" (which it calls databases), uses backtick-quoted
// identifiers, LIMIT for row caps, honors ctx cancellation via the
// driver's network layer, and accepts TLS knobs through DSN params.
var capabilities = db.Capabilities{
	SchemaDepth:     db.SchemaDepthSchemas,
	LimitSyntax:     db.LimitSyntaxLimit,
	IdentifierQuote: '`',
	SupportsCancel:  true,
	SupportsTLS:     true,
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
	})
	if err != nil {
		return nil, fmt.Errorf("mysql: %w", err)
	}
	return conn, nil
}

// schemaQuery lists user tables and views from information_schema,
// excluding MySQL's system schemas so the explorer only shows user
// objects. is_view is a 0/1 int matching the shared schema scanner.
const schemaQuery = `
SELECT
    TABLE_SCHEMA AS schema_name,
    TABLE_NAME   AS name,
    CASE WHEN TABLE_TYPE = 'VIEW' THEN 1 ELSE 0 END AS is_view
FROM information_schema.tables
WHERE TABLE_SCHEMA NOT IN ('mysql', 'information_schema', 'performance_schema', 'sys')
ORDER BY TABLE_SCHEMA, TABLE_NAME;
`

// buildDSN produces a go-sql-driver/mysql DSN. We use the driver's own
// Config type for correct escaping of special characters in user/pass
// and for its parseTime + loc handling. Options from cfg.Options are
// applied as raw Params; recognized top-level knobs (tls, parseTime)
// are also lifted into the Config struct so they work even if the user
// spelled them differently in the map.
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
	// parseTime=true so DATETIME/TIMESTAMP come back as time.Time rather
	// than []byte -- matches what the TUI's stringifyCell already expects.
	mc.ParseTime = true
	// Collect any Params from cfg.Options that we don't want to lift
	// into dedicated Config fields.
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
			// Let the user force it off if they really want bytes.
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
