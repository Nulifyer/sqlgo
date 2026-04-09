package db

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
)

// SQLOptions holds engine-specific knobs for the shared sqlConn wrapper.
// SchemaQuery runs against the open *sql.DB to populate the explorer. The
// query MUST return three columns in this order: schema name, object name,
// and a 1=view/0=table flag (any non-zero int = view).
type SQLOptions struct {
	DriverName  string
	SchemaQuery string
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

func (c *sqlConn) Query(ctx context.Context, query string) (*Result, error) {
	rows, err := c.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, fmt.Errorf("column types: %w", err)
	}

	cols := make([]Column, len(types))
	for i, t := range types {
		cols[i] = Column{
			Name:     t.Name(),
			TypeName: t.DatabaseTypeName(),
		}
	}

	var out [][]any
	for rows.Next() {
		// Scan into a fresh []any each row; sql.RawBytes would be faster but
		// the bytes become invalid after the next Scan, which makes the
		// result buffer unusable. Plain any is fine for materialized results.
		dest := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range dest {
			ptrs[i] = &dest[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		// []byte -> string for display. Drivers hand back bytes for text
		// types (varchar, text, etc); keeping them as bytes makes rendering
		// awkward and hides values in the TUI.
		for i, v := range dest {
			if b, ok := v.([]byte); ok {
				dest[i] = string(b)
			}
		}
		out = append(out, dest)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows: %w", err)
	}

	return &Result{Columns: cols, Rows: out}, nil
}
