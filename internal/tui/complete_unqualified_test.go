package tui

import (
	"context"
	"errors"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/db"
)

type schemaCompletionConn struct {
	db.Conn
	requests      chan db.TableRef
	defaultSchema bool
	fail          bool
}

func (*schemaCompletionConn) Capabilities() db.Capabilities { return db.Capabilities{} }

func (c *schemaCompletionConn) Columns(_ context.Context, ref db.TableRef) ([]db.Column, error) {
	c.requests <- ref
	if c.fail {
		return nil, errors.New("metadata unavailable")
	}
	if ref.Schema != "dbo" && !(ref.Schema == "" && c.defaultSchema) {
		return nil, nil
	}
	return []db.Column{{Name: "_id", TypeName: "int"}}, nil
}

func TestUnqualifiedCompletionUsesServerForAmbiguousSchema(t *testing.T) {
	m := newMainLayer()
	e := m.ensureActiveTab().editor
	m.focus = FocusQuery
	m.explorer.info = &db.SchemaInfo{Tables: []db.TableRef{{Schema: "sales", Name: "mytable"}, {Schema: "dbo", Name: "mytable"}}}
	conn := &schemaCompletionConn{requests: make(chan db.TableRef, 2), defaultSchema: true}
	a := &app{layers: []Layer{m}, conn: conn, asyncCh: make(chan func(*app), 4)}
	e.buf.ResetText("select * from mytable order by _")
	e.openCompletion(a)
	documentCallback(t, a)(a)
	if ref := <-conn.requests; ref.Schema != "" {
		t.Fatalf("ambiguous metadata guessed a schema: %+v", ref)
	}
	found := false
	if e.complete != nil {
		for _, item := range e.complete.items {
			found = found || item.text == "_id"
		}
	}
	if !found {
		t.Fatalf("server-resolved columns missing: %+v", e.complete)
	}
}

func TestColumnCompletionCallbackPreservesNewerNavigationAndClosesFailedLoad(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "stale navigation", true: "failed load"}[fail], func(t *testing.T) {
			m := newMainLayer()
			e := m.ensureActiveTab().editor
			m.focus = FocusQuery
			m.explorer.info = &db.SchemaInfo{Tables: []db.TableRef{{Schema: "dbo", Name: "mytable"}}}
			conn := &schemaCompletionConn{requests: make(chan db.TableRef, 2), fail: fail}
			a := &app{layers: []Layer{m}, conn: conn, asyncCh: make(chan func(*app), 4)}
			e.buf.ResetText("select * from mytable order by _")
			e.openCompletion(a)
			if !fail {
				e.handleInsert(a, Key{Kind: KeyLeft})
			}
			documentCallback(t, a)(a)
			if !fail && e.complete != nil {
				t.Fatal("stale metadata reopened completion")
			}
			if fail && e.complete != nil {
				for _, item := range e.complete.items {
					if item.loading {
						t.Fatal("failed metadata kept a loading placeholder")
					}
				}
			}
		})
	}
}

func TestTableResolutionAcceptsScopedMetadataWithoutCatalog(t *testing.T) {
	m := newMainLayer()
	m.ensureActiveTab().activeCatalog = "appdb"
	m.explorer.info = &db.SchemaInfo{Tables: []db.TableRef{{Schema: "dbo", Name: "MyTable"}}}
	a := &app{layers: []Layer{m}}
	ref := a.resolveTableRef(tableScope{catalog: "appdb", name: "mytable"})
	if ref.Catalog != "appdb" || ref.Schema != "dbo" || ref.Name != "MyTable" {
		t.Fatalf("scoped metadata lost: %+v", ref)
	}
}

func TestUnqualifiedColumnsResolveCatalogMetadataAndRefreshAsync(t *testing.T) {
	for _, table := range []string{"mytable", "dbo.mytable", "mytable AS m"} {
		t.Run(table, func(t *testing.T) {
			m := newMainLayer()
			e := m.ensureActiveTab().editor
			m.focus = FocusQuery
			m.explorer.info = &db.SchemaInfo{Tables: []db.TableRef{{Catalog: "appdb", Schema: "dbo", Name: "mytable"}}}
			conn := &schemaCompletionConn{requests: make(chan db.TableRef, 2)}
			a := &app{layers: []Layer{m}, conn: conn, asyncCh: make(chan func(*app), 4)}
			e.buf.ResetText("select *\nfrom " + table + "\norder by _")
			e.openCompletion(a)
			if e.complete == nil {
				t.Fatal("pending column fetch lost its completion request")
			}
			documentCallback(t, a)(a)
			ref := <-conn.requests
			if ref.Schema != "dbo" || ref.Catalog != "appdb" {
				t.Fatalf("wrong metadata target: %+v", ref)
			}
			if e.complete == nil {
				t.Fatal("loaded columns did not reopen completion")
			}
			found := false
			for _, item := range e.complete.items {
				found = found || item.kind == completeColumn && item.text == "_id"
			}
			if !found {
				t.Fatalf("missing ORDER BY column: %+v", e.complete.items)
			}
		})
	}
}

func TestBareTableResolutionDoesNotGuessAmongSchemas(t *testing.T) {
	m := newMainLayer()
	m.explorer.info = &db.SchemaInfo{Tables: []db.TableRef{{Schema: "sales", Name: "mytable"}, {Schema: "dbo", Name: "mytable"}}}
	a := &app{layers: []Layer{m}}
	if ref := a.resolveTableRef(tableScope{name: "mytable"}); ref.Schema != "" {
		t.Fatalf("guessed ambiguous schema: %+v", ref)
	}
}
