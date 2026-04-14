package db

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
)

// SQLOptions holds engine-specific knobs for the shared sqlConn
// wrapper. Adapters build one and pass it to OpenSQL.
//
// SchemaQuery must return (schema, name, is_view int, is_system int).
// Flat-schema engines synthesize a placeholder schema like "main".
// is_system flags engine-internal catalogs (pg_catalog, sys, etc.)
// so the explorer can group them under a Sys header.
//
// ColumnsQuery takes (schema, table) positional args and returns
// (col_name, type_name). Placeholder style varies per driver.
//
// ColumnsBuilder is the escape hatch for engines that can't take
// bind values for the column lookup (sqlite PRAGMA). Takes
// precedence over ColumnsQuery.
type SQLOptions struct {
	DriverName     string
	Capabilities   Capabilities
	SchemaQuery    string
	ColumnsQuery   string
	ColumnsBuilder func(t TableRef) (string, []any)
}

// OpenSQL wraps a *sql.DB as a db.Conn. Takes ownership of sqlDB.
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
			schema, name     string
			isView, isSystem int
		)
		if err := rows.Scan(&schema, &name, &isView, &isSystem); err != nil {
			return nil, fmt.Errorf("schema scan: %w", err)
		}
		kind := TableKindTable
		if isView != 0 {
			kind = TableKindView
		}
		out = append(out, TableRef{Schema: schema, Name: name, Kind: kind, System: isSystem != 0})
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

// Columns runs ColumnsQuery or ColumnsBuilder. Returns nil when
// neither is configured (no error).
func (c *sqlConn) Columns(ctx context.Context, t TableRef) ([]Column, error) {
	var (
		query string
		args  []any
	)
	if c.opts.ColumnsBuilder != nil {
		query, args = c.opts.ColumnsBuilder(t)
	} else if c.opts.ColumnsQuery != "" {
		query = c.opts.ColumnsQuery
		args = []any{t.Schema, t.Name}
	} else {
		return nil, nil
	}
	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("columns query %s.%s: %w", t.Schema, t.Name, err)
	}
	defer rows.Close()
	var out []Column
	for rows.Next() {
		var (
			name    string
			typeSQL sql.NullString
		)
		if err := rows.Scan(&name, &typeSQL); err != nil {
			return nil, fmt.Errorf("columns scan: %w", err)
		}
		out = append(out, Column{Name: name, TypeName: typeSQL.String})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("columns rows: %w", err)
	}
	return out, nil
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

// Query returns a streaming Rows. Caller MUST Close() or the
// statement holds the connection. Column metadata is fetched up
// front so headers render before rows stream.
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

// sqlRows adapts *sql.Rows to db.Rows. Each Scan allocates a
// fresh []any so callers can buffer rows safely. []byte -> string
// to avoid RawBytes lifetime traps.
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
	// []byte -> string so text columns display cleanly.
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

// Close tears down the cursor. Idempotent.
func (r *sqlRows) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	return r.rows.Close()
}

// NextResultSet advances *sql.Rows to the next result set and refreshes
// the cached column descriptors. Drivers that don't produce multiple
// result sets return false immediately.
func (r *sqlRows) NextResultSet() bool {
	if r.closed || r.err != nil {
		return false
	}
	if !r.rows.NextResultSet() {
		return false
	}
	types, err := r.rows.ColumnTypes()
	if err != nil {
		r.err = fmt.Errorf("column types: %w", err)
		return false
	}
	cols := make([]Column, len(types))
	for i, t := range types {
		cols[i] = Column{
			Name:     t.Name(),
			TypeName: t.DatabaseTypeName(),
		}
	}
	r.cols = cols
	return true
}
