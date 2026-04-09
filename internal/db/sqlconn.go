package db

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
)

// SQLOptions holds engine-specific knobs for the shared sqlConn wrapper.
// Engine adapters build one of these and hand it to OpenSQL alongside an
// already-opened *sql.DB.
//
// SchemaQuery runs against the *sql.DB to populate the explorer. The query
// MUST return three columns in this order: schema name, object name, and a
// 1=view / 0=table flag (any non-zero int = view). Engines without a
// native schema concept (SQLite) can pass a query that synthesizes a
// fixed placeholder schema like "main".
type SQLOptions struct {
	DriverName   string
	Capabilities Capabilities
	SchemaQuery  string
}

// OpenSQL wraps a stdlib *sql.DB as a db.Conn. Engine adapters build a DSN,
// call sql.Open with the registered database/sql driver name, and hand the
// result to OpenSQL. This keeps every adapter to a handful of lines.
//
// The Conn takes ownership of the *sql.DB and closes it in Close().
func OpenSQL(ctx context.Context, sqlDB *sql.DB, opts SQLOptions) (Conn, error) {
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return &sqlConn{db: sqlDB, opts: opts}, nil
}

type sqlConn struct {
	db   *sql.DB
	opts SQLOptions
}

func (c *sqlConn) Driver() string { return c.opts.DriverName }

func (c *sqlConn) Capabilities() Capabilities { return c.opts.Capabilities }

func (c *sqlConn) Schema(ctx context.Context) (*SchemaInfo, error) {
	if c.opts.SchemaQuery == "" {
		return &SchemaInfo{}, nil
	}
	rows, err := c.db.QueryContext(ctx, c.opts.SchemaQuery)
	if err != nil {
		return nil, fmt.Errorf("schema query: %w", err)
	}
	defer rows.Close()

	var out []TableRef
	for rows.Next() {
		var (
			schema, name string
			isView       int
		)
		if err := rows.Scan(&schema, &name, &isView); err != nil {
			return nil, fmt.Errorf("schema scan: %w", err)
		}
		kind := TableKindTable
		if isView != 0 {
			kind = TableKindView
		}
		out = append(out, TableRef{Schema: schema, Name: name, Kind: kind})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("schema rows: %w", err)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Schema != out[j].Schema {
			return out[i].Schema < out[j].Schema
		}
		return out[i].Name < out[j].Name
	})
	return &SchemaInfo{Tables: out}, nil
}

func (c *sqlConn) Close() error {
	return c.db.Close()
}

func (c *sqlConn) Ping(ctx context.Context) error {
	return c.db.PingContext(ctx)
}

func (c *sqlConn) Exec(ctx context.Context, query string, args ...any) error {
	if _, err := c.db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("exec: %w", err)
	}
	return nil
}

// Query starts a query and returns a streaming Rows. The returned cursor
// keeps the underlying *sql.Rows open until Close() is called; callers
// MUST call Close() (typically in a defer or on cancel) or the statement
// will hold the connection indefinitely.
//
// Column metadata is fetched up front so the table widget can render the
// header before any rows stream in. If column discovery fails, the cursor
// is torn down and Query returns the error.
func (c *sqlConn) Query(ctx context.Context, query string) (Rows, error) {
	rows, err := c.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("column types: %w", err)
	}
	cols := make([]Column, len(types))
	for i, t := range types {
		cols[i] = Column{
			Name:     t.Name(),
			TypeName: t.DatabaseTypeName(),
		}
	}
	return &sqlRows{rows: rows, cols: cols}, nil
}

// sqlRows adapts *sql.Rows to the streaming db.Rows interface. Every Scan()
// allocates a fresh []any so callers can retain rows in a buffer (the
// stdlib's Scan reuses destinations, which makes buffering unsafe). []byte
// values are converted to string here so the TUI never has to worry about
// RawBytes-like lifetime hazards.
type sqlRows struct {
	rows   *sql.Rows
	cols   []Column
	closed bool
	err    error
}

func (r *sqlRows) Columns() []Column { return r.cols }

func (r *sqlRows) Next() bool {
	if r.closed || r.err != nil {
		return false
	}
	return r.rows.Next()
}

func (r *sqlRows) Scan() ([]any, error) {
	if r.closed {
		return nil, fmt.Errorf("scan: rows closed")
	}
	dest := make([]any, len(r.cols))
	ptrs := make([]any, len(r.cols))
	for i := range dest {
		ptrs[i] = &dest[i]
	}
	if err := r.rows.Scan(ptrs...); err != nil {
		r.err = fmt.Errorf("scan: %w", err)
		return nil, r.err
	}
	// []byte -> string for display. Drivers hand back bytes for text types
	// (varchar, text, etc); keeping them as bytes makes rendering awkward
	// and hides values in the TUI.
	for i, v := range dest {
		if b, ok := v.([]byte); ok {
			dest[i] = string(b)
		}
	}
	return dest, nil
}

func (r *sqlRows) Err() error {
	if r.err != nil {
		return r.err
	}
	if r.rows == nil {
		return nil
	}
	return r.rows.Err()
}

// Close tears down the cursor. Safe to call multiple times and safe to call
// before any Next(). The second call is a no-op so defer + explicit cancel
// paths both work.
func (r *sqlRows) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	return r.rows.Close()
}
