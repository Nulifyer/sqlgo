package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/clipboard"
	"github.com/Nulifyer/sqlgo/internal/db"
)

type explorerColumnConn struct {
	cols           []db.Column
	columnsCalls   int
	columnsInCalls int
	lastDatabase   string
}

func (c *explorerColumnConn) Close() error { return nil }
func (c *explorerColumnConn) Ping(context.Context) error {
	return nil
}
func (c *explorerColumnConn) Query(context.Context, string) (db.Rows, error) {
	return nil, errors.New("not implemented")
}
func (c *explorerColumnConn) Exec(context.Context, string, ...any) error {
	return errors.New("not implemented")
}
func (c *explorerColumnConn) Schema(context.Context) (*db.SchemaInfo, error) {
	return fixtureSchema(), nil
}
func (c *explorerColumnConn) Columns(_ context.Context, _ db.TableRef) ([]db.Column, error) {
	c.columnsCalls++
	return c.cols, nil
}
func (c *explorerColumnConn) ColumnsIn(_ context.Context, database string, _ db.TableRef) ([]db.Column, error) {
	c.columnsInCalls++
	c.lastDatabase = database
	return c.cols, nil
}
func (c *explorerColumnConn) Definition(context.Context, string, string, string) (string, error) {
	return "", db.ErrDefinitionUnsupported
}
func (c *explorerColumnConn) Explain(context.Context, string) ([][]any, error) {
	return nil, db.ErrExplainUnsupported
}
func (c *explorerColumnConn) Driver() string { return "test" }
func (c *explorerColumnConn) Capabilities() db.Capabilities {
	return db.Capabilities{SchemaDepth: db.SchemaDepthSchemas, LimitSyntax: db.LimitSyntaxSelectTop, IdentifierQuote: '['}
}

func expandExplorerToOrders(e *explorer) {
	e.SetSchema(fixtureSchema(), db.SchemaDepthSchemas)
	e.cursor = 0
	e.Toggle()
	e.cursor = 1
	e.Toggle()
	e.cursor = 2
}

func TestExplorerSpaceLoadsColumns(t *testing.T) {
	t.Parallel()

	conn := &explorerColumnConn{cols: []db.Column{{Name: "id", TypeName: "int"}}}
	m := newMainLayer()
	a := &app{conn: conn, layers: []Layer{m}}
	m.focus = FocusExplorer
	expandExplorerToOrders(m.explorer)

	m.HandleKey(a, Key{Kind: KeyRune, Rune: ' '})

	if conn.columnsCalls != 1 {
		t.Fatalf("Columns calls = %d, want 1", conn.columnsCalls)
	}
	foundColumn := false
	for _, it := range m.explorer.items {
		if it.kind == itemColumn && it.label == "id" && it.suffix == "int" {
			foundColumn = true
			break
		}
	}
	if !foundColumn {
		t.Fatalf("loaded column missing from explorer items: %+v", m.explorer.items)
	}
}

func TestExplorerColumnLoadUsesColumnsInForCatalog(t *testing.T) {
	t.Parallel()

	conn := &explorerColumnConn{cols: []db.Column{{Name: "id"}}}
	m := newMainLayer()
	a := &app{conn: conn, layers: []Layer{m}}

	a.loadExplorerColumns(db.TableRef{Catalog: "SqlgoA", Schema: "dbo", Name: "orders", Kind: db.TableKindTable})

	if conn.columnsInCalls != 1 || conn.lastDatabase != "SqlgoA" {
		t.Fatalf("ColumnsIn calls/database = %d/%q, want 1/SqlgoA", conn.columnsInCalls, conn.lastDatabase)
	}
}

func TestExplorerEnterOnTableStillPrefillsSelect(t *testing.T) {
	t.Parallel()

	conn := &explorerColumnConn{}
	m := newMainLayer()
	a := &app{conn: conn, layers: []Layer{m}}
	m.focus = FocusExplorer
	expandExplorerToOrders(m.explorer)

	m.HandleKey(a, Key{Kind: KeyEnter})

	if m.focus != FocusQuery {
		t.Fatalf("focus = %v, want Query", m.focus)
	}
	got := m.editor.buf.Text()
	if !strings.Contains(got, "SELECT TOP 100") || !strings.Contains(got, "[dbo].[orders]") {
		t.Fatalf("editor SQL = %q, want SELECT preview", got)
	}
}

func TestExplorerCopiesQualifiedName(t *testing.T) {
	t.Parallel()

	conn := &explorerColumnConn{}
	clip := clipboard.NewMemory()
	m := newMainLayer()
	a := &app{conn: conn, clipboard: clip, layers: []Layer{m}}
	m.focus = FocusExplorer
	expandExplorerToOrders(m.explorer)

	m.HandleKey(a, Key{Kind: KeyRune, Rune: 'y'})

	got, err := clip.Paste()
	if err != nil {
		t.Fatalf("clipboard paste: %v", err)
	}
	if got != "[dbo].[orders]" {
		t.Fatalf("copied name = %q, want [dbo].[orders]", got)
	}
}
