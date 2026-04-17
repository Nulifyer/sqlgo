package tui

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/Nulifyer/sqlgo/internal/db"
)

type catalogTestConn struct {
	schemaCalls chan string
}

func (c *catalogTestConn) Close() error { return nil }

func (c *catalogTestConn) Ping(ctx context.Context) error { return nil }

func (c *catalogTestConn) Query(ctx context.Context, sql string) (db.Rows, error) {
	return nil, io.EOF
}

func (c *catalogTestConn) Exec(ctx context.Context, sql string, args ...any) error { return nil }

func (c *catalogTestConn) Schema(ctx context.Context) (*db.SchemaInfo, error) { return nil, nil }

func (c *catalogTestConn) Columns(ctx context.Context, t db.TableRef) ([]db.Column, error) {
	return nil, nil
}

func (c *catalogTestConn) Definition(ctx context.Context, kind, schema, name string) (string, error) {
	return "", nil
}

func (c *catalogTestConn) Explain(ctx context.Context, sql string) ([][]any, error) { return nil, nil }

func (c *catalogTestConn) Driver() string { return "test" }

func (c *catalogTestConn) Capabilities() db.Capabilities {
	return db.Capabilities{SupportsCrossDatabase: true}
}

func (c *catalogTestConn) ListDatabases(ctx context.Context) ([]string, error) {
	return []string{"SqlgoA", "SqlgoB"}, nil
}

func (c *catalogTestConn) SchemaForDatabase(ctx context.Context, database string) (*db.SchemaInfo, error) {
	select {
	case c.schemaCalls <- database:
	default:
	}
	return fixtureSchema(), nil
}

func (c *catalogTestConn) UseDatabaseStmt(name string) string { return "USE " + name }

func TestCatalogLayerApplyLoadsSelectedDatabaseSchema(t *testing.T) {
	t.Parallel()

	m := newMainLayer()
	m.explorer.SetDatabases([]string{"SqlgoA", "SqlgoB"})

	conn := &catalogTestConn{schemaCalls: make(chan string, 1)}
	cl := newCatalogLayer([]string{"SqlgoA", "SqlgoB"})
	cl.list.Selected = 1 // "(default)", "SqlgoA", "SqlgoB"

	a := &app{
		conn:    conn,
		asyncCh: make(chan func(*app), 8),
		layers:  []Layer{m, cl},
	}

	cl.apply(a)

	if got := m.session.activeCatalog; got != "SqlgoA" {
		t.Fatalf("activeCatalog = %q, want %q", got, "SqlgoA")
	}
	if got := m.status; got != "tab now uses SqlgoA" {
		t.Fatalf("status = %q, want %q", got, "tab now uses SqlgoA")
	}
	if len(a.layers) != 1 {
		t.Fatalf("layers len = %d, want 1", len(a.layers))
	}
	if got := m.explorer.dbLoading["SqlgoA"]; got == "" {
		t.Fatalf("dbLoading[SqlgoA] empty, want loading marker")
	}

	select {
	case got := <-conn.schemaCalls:
		if got != "SqlgoA" {
			t.Fatalf("SchemaForDatabase called for %q, want %q", got, "SqlgoA")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("SchemaForDatabase was not called")
	}
}

func TestCatalogLayerApplySkipsLoadWhenSchemaAlreadyLoaded(t *testing.T) {
	t.Parallel()

	m := newMainLayer()
	m.explorer.SetDatabases([]string{"SqlgoA"})
	m.explorer.SetDatabaseSchema("SqlgoA", fixtureSchema())

	conn := &catalogTestConn{schemaCalls: make(chan string, 1)}
	cl := newCatalogLayer([]string{"SqlgoA"})
	cl.list.Selected = 1 // "(default)", "SqlgoA"

	a := &app{
		conn:    conn,
		asyncCh: make(chan func(*app), 8),
		layers:  []Layer{m, cl},
	}

	cl.apply(a)

	select {
	case got := <-conn.schemaCalls:
		t.Fatalf("SchemaForDatabase unexpectedly called for %q", got)
	case <-time.After(100 * time.Millisecond):
	}
}
