package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Nulifyer/sqlgo/internal/config"
)

// ErrConnectionNotFound is returned by GetConnection / DeleteConnection
// when the named connection does not exist.
var ErrConnectionNotFound = errors.New("connection not found")

// ListConnections returns every saved connection, sorted by name.
func (s *Store) ListConnections(ctx context.Context) ([]config.Connection, error) {
	rows, err := s.db.QueryContext(ctx, `
        SELECT name, driver, host, port, username, password, database, options
        FROM connections
        ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list connections: %w", err)
	}
	defer rows.Close()

	var out []config.Connection
	for rows.Next() {
		c, err := scanConnection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list connections: %w", err)
	}
	return out, nil
}

// GetConnection returns the connection with the given name.
func (s *Store) GetConnection(ctx context.Context, name string) (config.Connection, error) {
	row := s.db.QueryRowContext(ctx, `
        SELECT name, driver, host, port, username, password, database, options
        FROM connections
        WHERE name = ?`, name)
	c, err := scanConnection(row)
	if errors.Is(err, sql.ErrNoRows) {
		return config.Connection{}, ErrConnectionNotFound
	}
	if err != nil {
		return config.Connection{}, err
	}
	return c, nil
}

// SaveConnection upserts a connection. If oldName is non-empty and differs
// from c.Name, the row under oldName is deleted first -- this is how the
// form layer propagates a rename without leaving a stale entry behind. Both
// operations run in a single transaction so a crash mid-rename can't
// duplicate or drop the row.
func (s *Store) SaveConnection(ctx context.Context, oldName string, c config.Connection) error {
	optsJSON, err := marshalOptions(c.Options)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("save connection: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if oldName != "" && oldName != c.Name {
		if _, err := tx.ExecContext(ctx, `DELETE FROM connections WHERE name = ?`, oldName); err != nil {
			return fmt.Errorf("rename delete: %w", err)
		}
	}

	if _, err := tx.ExecContext(ctx, `
        INSERT INTO connections(name, driver, host, port, username, password, database, options)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(name) DO UPDATE SET
            driver     = excluded.driver,
            host       = excluded.host,
            port       = excluded.port,
            username   = excluded.username,
            password   = excluded.password,
            database   = excluded.database,
            options    = excluded.options,
            updated_at = datetime('now')`,
		c.Name, c.Driver, c.Host, c.Port, c.User, c.Password, c.Database, optsJSON,
	); err != nil {
		return fmt.Errorf("save connection: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("save connection commit: %w", err)
	}
	return nil
}

// DeleteConnection removes the connection with the given name. A missing
// name is an error (ErrConnectionNotFound) so UI callers can distinguish
// "nothing to delete" from "succeeded".
func (s *Store) DeleteConnection(ctx context.Context, name string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM connections WHERE name = ?`, name)
	if err != nil {
		return fmt.Errorf("delete connection: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete connection rows: %w", err)
	}
	if n == 0 {
		return ErrConnectionNotFound
	}
	return nil
}

// rowScanner abstracts *sql.Row and *sql.Rows so scanConnection can be
// used by both Get and List paths.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanConnection(r rowScanner) (config.Connection, error) {
	var (
		c       config.Connection
		optsStr string
	)
	if err := r.Scan(
		&c.Name, &c.Driver, &c.Host, &c.Port,
		&c.User, &c.Password, &c.Database, &optsStr,
	); err != nil {
		return config.Connection{}, err
	}
	opts, err := unmarshalOptions(optsStr)
	if err != nil {
		return config.Connection{}, fmt.Errorf("connection %q: %w", c.Name, err)
	}
	c.Options = opts
	return c, nil
}

func marshalOptions(opts map[string]string) (string, error) {
	if len(opts) == 0 {
		return "{}", nil
	}
	b, err := json.Marshal(opts)
	if err != nil {
		return "", fmt.Errorf("marshal options: %w", err)
	}
	return string(b), nil
}

func unmarshalOptions(s string) (map[string]string, error) {
	if s == "" || s == "{}" {
		return nil, nil
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, fmt.Errorf("unmarshal options: %w", err)
	}
	if len(m) == 0 {
		return nil, nil
	}
	return m, nil
}
