// Package sqlite registers the pure-Go SQLite driver with internal/db. Import
// it for side effects:
//
//	import _ "github.com/Nulifyer/sqlgo/internal/db/sqlite"
//
// The underlying driver is modernc.org/sqlite, a pure-Go transpilation of
// SQLite's C sources -- so sqlgo stays a single native binary with CGO off.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/Nulifyer/sqlgo/internal/db"
)

const (
	driverName = "sqlite"
	// syntheticSchema is the name used in the explorer tree for SQLite's
	// (schema-less) objects. Matches the name sqlite uses internally for
	// the main database attachment.
	syntheticSchema = "main"
)

func init() {
	db.Register(driver{})
}

type driver struct{}

func (driver) Name() string { return driverName }

// capabilities is the SQLite-specific capability set. SQLite has no schema
// layer (everything sits in a single database), uses ANSI double quotes for
// identifiers, supports LIMIT, and -- via modernc.org/sqlite -- honors ctx
// cancellation by calling sqlite3_interrupt between statements. No TLS
// knobs: it's a local file.
var capabilities = db.Capabilities{
	SchemaDepth:     db.SchemaDepthFlat,
	LimitSyntax:     db.LimitSyntaxLimit,
	IdentifierQuote: '"',
	SupportsCancel:  true,
	SupportsTLS:     false,
}

func (driver) Capabilities() db.Capabilities { return capabilities }

func (driver) Open(ctx context.Context, cfg db.Config) (db.Conn, error) {
	dsn := buildDSN(cfg)
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite open: %w", err)
	}
	conn, err := db.OpenSQL(ctx, sqlDB, db.SQLOptions{
		DriverName:   driverName,
		Capabilities: capabilities,
		SchemaQuery:  schemaQuery,
	})
	if err != nil {
		return nil, fmt.Errorf("sqlite: %w", err)
	}
	return conn, nil
}

// schemaQuery lists user tables and views from sqlite_master under a
// synthetic "main" schema so the shared Schema scanner produces the same
// three-column shape as the other engines. sqlite_% objects (internal
// metadata, FTS shadow tables) are filtered out so the explorer only shows
// user objects.
const schemaQuery = `
SELECT
    'main' AS schema_name,
    name,
    CASE WHEN type = 'view' THEN 1 ELSE 0 END AS is_view
FROM sqlite_master
WHERE type IN ('table','view')
  AND name NOT LIKE 'sqlite_%'
ORDER BY name;
`

// buildDSN produces a modernc.org/sqlite DSN from cfg. SQLite has no
// host/port/user/password; cfg.Database is the file path (empty or ":memory:"
// means an in-memory database). cfg.Options is passed through as URI query
// parameters, so callers can set pragmas via the driver's _pragma knob:
//
//	Options["_pragma"] = "journal_mode(wal)"
//
// Multiple pragmas are supported by repeating the key in the caller's flow;
// since Options is a map we can't round-trip repeats here, so pass a single
// semicolon-separated _pragma value for now (modernc.org/sqlite accepts it).
func buildDSN(cfg db.Config) string {
	path := strings.TrimSpace(cfg.Database)
	if path == "" || path == ":memory:" {
		return ":memory:"
	}
	if len(cfg.Options) == 0 {
		return path
	}
	// URI form lets us attach query params. Forward slashes are fine on
	// Windows because sqlite accepts them.
	q := url.Values{}
	for k, v := range cfg.Options {
		q.Set(k, v)
	}
	u := url.URL{
		Scheme:   "file",
		Opaque:   path,
		RawQuery: q.Encode(),
	}
	return u.String()
}
