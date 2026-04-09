// Package db defines the abstract database layer used by sqlgo. Each
// supported engine (mssql, sqlite, postgres, mysql, ...) implements a
// Driver that returns a Conn for running queries and inspecting schema.
//
// The interface is intentionally narrow: the TUI consumes Driver/Conn/Rows
// and never touches engine-specific types. Engine adapters live in
// subpackages (internal/db/mssql, internal/db/sqlite, ...) and register
// themselves via Register() in their init().
//
// Queries stream. Conn.Query returns a Rows cursor that the caller drains
// via Next()/Scan(). The caller is responsible for Close(), which releases
// the underlying driver cursor and is safe to call at any time, including
// before any Next(). Combined with context cancellation this gives the TUI
// a clean "stop the in-flight query and throw away the buffer" path even
// on multi-million-row SELECTs.
package db

import (
	"context"
	"fmt"
	"io"
	"sort"
	"sync"
)

// Config is an engine-agnostic connection description. Options carries
// driver-specific knobs (e.g. "encrypt", "trustServerCertificate" for MSSQL)
// so adding a new engine doesn't require changing this struct.
type Config struct {
	Host     string
	Port     int
	User     string
	Password string
	Database string
	Options  map[string]string
}

// Driver opens connections for a single database engine. Implementations
// must be safe to call from multiple goroutines.
type Driver interface {
	// Name returns a stable identifier ("mssql", "sqlite", ...).
	Name() string
	// Capabilities describes what the engine supports. The TUI reads this
	// instead of branching on Name() strings.
	Capabilities() Capabilities
	// Open establishes a new connection. The returned Conn is owned by the
	// caller and must be closed.
	Open(ctx context.Context, cfg Config) (Conn, error)
}

// Capabilities describes the features and syntax of a database engine. The
// TUI uses this to avoid hard-coded `if driver == "mssql"` branches:
// explorer tree depth, SELECT generation, TLS toggles in the connection
// form, and cancel wiring all read from here.
type Capabilities struct {
	// SchemaDepth describes whether tables are grouped under a schema
	// level. SQLite is Flat (everything in one bucket); Postgres, MSSQL,
	// MySQL are Schemas.
	SchemaDepth SchemaDepth

	// LimitSyntax selects between "SELECT TOP N ..." (MSSQL) and
	// "SELECT ... LIMIT N" (everything else).
	LimitSyntax LimitSyntax

	// IdentifierQuote is the opening character used to quote identifiers:
	// '[' for MSSQL, '`' for MySQL, '"' for ANSI SQL (Postgres, SQLite).
	// The closing character is derived: '[' -> ']', else same as opening.
	IdentifierQuote rune

	// SupportsCancel reports whether the driver honors context cancellation
	// on in-flight queries at the network layer. True for pure-Go MSSQL,
	// pgx, and go-sql-driver/mysql; false for SQLite (cancel only takes
	// effect between rows on a local DB, which is still fine for us).
	SupportsCancel bool

	// SupportsTLS reports whether the DSN accepts TLS/SSL knobs in
	// Config.Options. Drives the TLS field group in the connection form.
	SupportsTLS bool
}

// SchemaDepth describes the object hierarchy the explorer should render
// for an engine.
type SchemaDepth int

const (
	// SchemaDepthFlat is a single bucket of tables+views with no schema
	// layer above them (e.g. SQLite). The explorer hides the schema node
	// and shows Tables/Views subgroups at the root.
	SchemaDepthFlat SchemaDepth = iota
	// SchemaDepthSchemas groups tables+views under a schema node
	// (Postgres, MSSQL, MySQL, ...).
	SchemaDepthSchemas
)

// LimitSyntax selects between the two "first N rows" forms.
type LimitSyntax int

const (
	// LimitSyntaxLimit is the ANSI-ish "SELECT ... LIMIT N" tail used by
	// Postgres, MySQL, SQLite.
	LimitSyntaxLimit LimitSyntax = iota
	// LimitSyntaxSelectTop is the "SELECT TOP N ..." prefix used by MSSQL.
	LimitSyntaxSelectTop
)

// Conn is a live connection to a database. It is NOT required to be safe
// for concurrent use; the TUI serializes queries per connection.
type Conn interface {
	io.Closer
	// Ping verifies the connection is alive.
	Ping(ctx context.Context) error
	// Query starts a query and returns a streaming cursor over its rows.
	// The query is still executing when Query returns; the caller pulls
	// results via Rows.Next()/Scan() and must call Rows.Close() when done
	// (deferred, or on cancel).
	Query(ctx context.Context, sql string) (Rows, error)
	// Exec runs a statement that does not return rows (DDL, INSERT/UPDATE/
	// DELETE). args are positional bind values using whatever placeholder
	// syntax the driver expects. The seed package is the primary consumer.
	Exec(ctx context.Context, sql string, args ...any) error
	// Schema returns the list of user-visible tables and views, grouped by
	// schema, so the explorer can render a tree. Engines without schemas
	// (sqlite) return everything under a single synthetic schema.
	Schema(ctx context.Context) (*SchemaInfo, error)
	// Driver returns the engine name this connection was opened with.
	Driver() string
	// Capabilities returns the driver's capability set. Shortcut for
	// looking up the Driver from the registry and calling its
	// Capabilities() — kept on Conn so widgets with a Conn don't need a
	// registry lookup.
	Capabilities() Capabilities
}

// Rows is a forward-only cursor over a running query. The driver only
// pulls from the network when Next() is called, so an 8M-row SELECT never
// materializes in memory unless the caller keeps draining. Close() aborts
// the in-flight query (via the caller's context and the underlying
// driver's row cursor) and is idempotent.
type Rows interface {
	// Columns returns the column descriptors. Available as soon as Query
	// returns without error; does not block on row delivery.
	Columns() []Column
	// Next advances to the next row. Returns false on end-of-result or
	// error; check Err() to distinguish.
	Next() bool
	// Scan returns the current row as a freshly allocated slice. The slice
	// is owned by the caller and is safe to retain across further Next()
	// calls, unlike stdlib sql.Rows.Scan which reuses buffers.
	Scan() ([]any, error)
	// Err returns any error that stopped Next() from returning true. nil
	// on clean end-of-result.
	Err() error
	// Close releases the underlying cursor and any associated resources.
	// Safe to call at any time, including before the first Next(); safe
	// to call multiple times.
	Close() error
}

// TableKind distinguishes tables from views in the schema tree.
type TableKind int

const (
	TableKindTable TableKind = iota
	TableKindView
)

// TableRef is a single table or view in the schema tree.
type TableRef struct {
	Schema string
	Name   string
	Kind   TableKind
}

// SchemaInfo is a flat list of tables+views. The explorer groups by Schema
// at render time; keeping the storage flat makes it trivial to sort and to
// compute counts.
type SchemaInfo struct {
	Tables []TableRef
}

// Column describes a single column in a query result.
type Column struct {
	Name     string
	TypeName string // driver-reported SQL type name, for display
}

// Result is a materialized view of a query's rows. Conn.Query does NOT
// return this — it returns a streaming Rows cursor instead. The TUI's
// table widget keeps a Result as its in-memory buffer of rows pulled from
// the cursor so far; tests use it as a convenient fixture shape.
type Result struct {
	Columns []Column
	Rows    [][]any
}

// --- registry ---------------------------------------------------------------

var (
	regMu   sync.RWMutex
	drivers = map[string]Driver{}
)

// Register adds a Driver to the global registry. Engine adapters call this
// from their init() so importing the package is enough to enable the engine.
// Panics if the same name is registered twice.
func Register(d Driver) {
	if d == nil {
		panic("db.Register: nil driver")
	}
	regMu.Lock()
	defer regMu.Unlock()
	name := d.Name()
	if _, dup := drivers[name]; dup {
		panic(fmt.Sprintf("db.Register: duplicate driver %q", name))
	}
	drivers[name] = d
}

// Get returns a registered driver by name.
func Get(name string) (Driver, error) {
	regMu.RLock()
	defer regMu.RUnlock()
	d, ok := drivers[name]
	if !ok {
		return nil, fmt.Errorf("db: driver %q not registered", name)
	}
	return d, nil
}

// Registered returns the sorted names of all registered drivers.
func Registered() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	names := make([]string, 0, len(drivers))
	for n := range drivers {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
