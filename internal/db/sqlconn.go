package db

import (
	"context"
	"database/sql"
	"fmt"
)

// OpenSQL wraps a stdlib *sql.DB as a db.Conn. Engine adapters build a DSN,
// call sql.Open with the registered database/sql driver name, and hand the
// result to OpenSQL. This keeps every adapter to a handful of lines.
//
// The Conn takes ownership of the *sql.DB and closes it in Close().
func OpenSQL(ctx context.Context, sqlDB *sql.DB) (Conn, error) {
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return &sqlConn{db: sqlDB}, nil
}

type sqlConn struct {
	db *sql.DB
}

func (c *sqlConn) Close() error {
	return c.db.Close()
}

func (c *sqlConn) Ping(ctx context.Context) error {
	return c.db.PingContext(ctx)
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
