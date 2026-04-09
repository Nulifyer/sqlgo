// Package db defines the abstract database layer used by sqlgo. Each
// supported engine (mssql, mysql, postgres, sqlite) implements a Driver that
// returns a Conn for running queries and inspecting schema.
//
// The interface is intentionally narrow: the TUI consumes Driver/Conn and
// never touches engine-specific types. Engine adapters live in subpackages
// (internal/db/mssql, etc) and register themselves via Register().
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
	// Name returns a stable identifier ("mssql", "mysql", ...).
	Name() string
	// Open establishes a new connection. The returned Conn is owned by the
	// caller and must be closed.
	Open(ctx context.Context, cfg Config) (Conn, error)
}

// Conn is a live connection to a database. It is NOT required to be safe
// for concurrent use; the TUI serializes queries per connection.
type Conn interface {
	io.Closer
	// Ping verifies the connection is alive.
	Ping(ctx context.Context) error
	// Query runs sql and materializes the full result set. Streaming will be
	// added when the results panel needs it.
	Query(ctx context.Context, sql string) (*Result, error)
	// Schema returns the list of user-visible tables and views, grouped by
	// schema, so the explorer can render a tree. Engines without schemas
	// (sqlite, mysql) return everything under a single synthetic schema.
	Schema(ctx context.Context) (*SchemaInfo, error)
	// Driver returns the engine name this connection was opened with.
	// The explorer uses this to pick the right SELECT top/limit syntax.
	Driver() string
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

// Result is a materialized query result set. Rows[i][j] is the j-th column
// of the i-th row, as returned by the driver's Scan (typically string,
// int64, float64, bool, []byte, time.Time, or nil).
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
