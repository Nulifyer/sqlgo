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
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"

	"github.com/Nulifyer/sqlgo/internal/sqltok"
)

// ErrPermissionDenied is returned (or wrapped) by an adapter when the
// current user lacks rights to list a particular object kind. The TUI
// treats this as "render the group greyed with a (denied) hint" rather
// than a hard error.
var ErrPermissionDenied = errors.New("permission denied")

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

	// ExplainFormat describes how this engine returns EXPLAIN output.
	// None means the feature is unsupported for this driver.
	ExplainFormat ExplainFormat

	// Dialect selects the keyword overlay used by autocomplete so each
	// engine only suggests syntax it actually accepts (TOP for MSSQL,
	// RETURNING for Postgres/SQLite, PRAGMA for SQLite, ...).
	Dialect sqltok.Dialect
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

// ExplainFormat selects the driver's EXPLAIN output shape. None
// means the driver has no supported form and the TUI should skip
// the feature for that engine.
type ExplainFormat int

const (
	ExplainFormatNone ExplainFormat = iota
	// ExplainFormatPostgresJSON: `EXPLAIN (FORMAT JSON) ...` returns
	// one row with a JSON array containing a single top-level node.
	ExplainFormatPostgresJSON
	// ExplainFormatMySQLJSON: `EXPLAIN FORMAT=JSON ...` returns one
	// row with a JSON object rooted at "query_block".
	ExplainFormatMySQLJSON
	// ExplainFormatSQLiteRows: `EXPLAIN QUERY PLAN ...` returns rows
	// (id, parent, notused, detail) the TUI reparents into a tree.
	ExplainFormatSQLiteRows
	// ExplainFormatMSSQLXML: `SET SHOWPLAN_XML ON` + target statement
	// returns one row with a ShowPlanXML document. Requires a pinned
	// *sql.Conn so the SET state persists to the target query; the
	// MSSQL adapter supplies a custom SQLOptions.ExplainRunner for that.
	ExplainFormatMSSQLXML
)

// Conn is a live database connection. NOT required to be
// concurrent-safe; the TUI serializes per connection.
type Conn interface {
	io.Closer
	Ping(ctx context.Context) error
	// Query returns a streaming cursor. Caller MUST Close().
	Query(ctx context.Context, sql string) (Rows, error)
	// Exec runs a non-row statement. Placeholder style is
	// driver-specific.
	Exec(ctx context.Context, sql string, args ...any) error
	// Schema returns user-visible tables/views. Flat engines
	// return everything under a synthetic schema.
	Schema(ctx context.Context) (*SchemaInfo, error)
	// Columns returns ordered columns for one table. Callers
	// should cache -- the editor hits this on every trigger.
	Columns(ctx context.Context, t TableRef) ([]Column, error)
	// Definition returns runnable DDL for a single object.
	// kind is one of "view", "procedure", "function", "trigger".
	// The returned text is an engine-appropriate re-creatable form:
	// mssql uses CREATE OR ALTER, postgres uses CREATE OR REPLACE
	// (or DROP + CREATE for triggers), mysql/sqlite use DROP + CREATE.
	// Returns an error for unsupported kinds or drivers.
	Definition(ctx context.Context, kind, schema, name string) (string, error)
	// Explain returns raw plan rows for sql. Shape is engine-specific:
	// callers dispatch on Capabilities().ExplainFormat to parse. Adapters
	// that set ExplainFormatNone return ErrExplainUnsupported so the TUI
	// can skip the feature cleanly.
	Explain(ctx context.Context, sql string) ([][]any, error)
	Driver() string
	Capabilities() Capabilities
}

// ErrExplainUnsupported is returned by Conn.Explain for drivers whose
// Capabilities report ExplainFormatNone.
var ErrExplainUnsupported = errors.New("explain unsupported")

// ErrDefinitionUnsupported is returned by Conn.Definition when the
// driver or object kind doesn't support fetching a runnable DDL body.
var ErrDefinitionUnsupported = errors.New("definition retrieval unsupported")

// Rows is a forward-only query cursor. Rows are pulled lazily
// on Next(); Close() is idempotent and aborts the query.
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
	// NextResultSet advances to the next result set produced by the
	// query (e.g. a batch of SELECTs separated by semicolons on drivers
	// that support it). Returns false when there are no more result
	// sets. After a true return, Columns() reflects the new set and the
	// caller drains it with Next()/Scan() as usual. Drivers without
	// multi-result support return false on the first call.
	NextResultSet() bool
}

// TableKind distinguishes tables from views in the schema tree.
type TableKind int

const (
	TableKindTable TableKind = iota
	TableKindView
)

// TableRef is a single table or view in the schema tree.
// System is true for engine-internal catalogs (pg_catalog, sys,
// information_schema, sqlite_* etc.) so the explorer can bucket
// them under a "Sys" group instead of mixing with user objects.
type TableRef struct {
	Schema string
	Name   string
	Kind   TableKind
	System bool
}

// RoutineKind distinguishes stored procedures, functions, and aggregates.
type RoutineKind int

const (
	RoutineKindProcedure RoutineKind = iota
	RoutineKindFunction
	RoutineKindAggregate
)

// RoutineRef is a stored procedure, function, or aggregate.
type RoutineRef struct {
	Schema   string
	Name     string
	Kind     RoutineKind
	Language string // optional (pg: "plpgsql"/"sql"; mssql: "SQL"/"CLR"; mysql: "SQL")
	System   bool
}

// TriggerRef is a table-bound trigger.
type TriggerRef struct {
	Schema string
	Table  string
	Name   string
	Timing string // BEFORE/AFTER/INSTEAD OF
	Event  string // INSERT/UPDATE/DELETE (engines may return a joined list)
	System bool
}

// ObjectKindStatus records whether a listing query for one object kind
// succeeded, was denied, or is unsupported by the engine. The explorer
// shows a (denied)/(unsupported) hint next to empty groups.
type ObjectKindStatus int

const (
	ObjectKindOK ObjectKindStatus = iota
	ObjectKindDenied
	ObjectKindUnsupported
)

// SchemaInfo is a flat, per-kind listing of visible objects. The explorer
// groups by Schema and kind at render time.
type SchemaInfo struct {
	Tables   []TableRef
	Routines []RoutineRef
	Triggers []TriggerRef

	// Status tracks per-kind listing results. Absent key == OK.
	// Keys: "tables", "routines", "triggers".
	Status map[string]ObjectKindStatus
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
