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
	columnsErr     error
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
	if c.columnsErr != nil {
		return nil, c.columnsErr
	}
	return c.cols, nil
}
func (c *explorerColumnConn) ColumnsIn(_ context.Context, database string, _ db.TableRef) ([]db.Column, error) {
	c.columnsInCalls++
	c.lastDatabase = database
	if c.columnsErr != nil {
		return nil, c.columnsErr
	}
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

type explorerDesignFallbackConn struct {
	explorerColumnConn
}

func (c *explorerDesignFallbackConn) TableDesign(context.Context, db.TableRef) (db.TableDesign, error) {
	return db.TableDesign{}, errors.New("rich metadata denied")
}

type explorerCrossDesignFallbackConn struct {
	explorerColumnConn
	tableDesignCalls   int
	tableDesignInCalls int
}

func (c *explorerCrossDesignFallbackConn) TableDesign(context.Context, db.TableRef) (db.TableDesign, error) {
	c.tableDesignCalls++
	return db.TableDesign{}, errors.New("default-db metadata denied")
}

func (c *explorerCrossDesignFallbackConn) TableDesignIn(context.Context, string, db.TableRef) (db.TableDesign, error) {
	c.tableDesignInCalls++
	return db.TableDesign{}, errors.New("catalog metadata denied")
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

	conn := &explorerColumnConn{cols: []db.Column{{Name: "order id", TypeName: "int"}, {Name: "name", TypeName: "text"}}}
	m := newMainLayer()
	a := &app{conn: conn, layers: []Layer{m}}
	m.focus = FocusExplorer
	expandExplorerToOrders(m.explorer)

	m.HandleKey(a, Key{Kind: KeyEnter})

	if m.focus != FocusQuery {
		t.Fatalf("focus = %v, want Query", m.focus)
	}
	got := m.editor.buf.Text()
	if !strings.Contains(got, "SELECT TOP 100") || !strings.Contains(got, "[order id]") || !strings.Contains(got, "[name]") || !strings.Contains(got, "[dbo].[orders]") {
		t.Fatalf("editor SQL = %q, want SELECT preview with quoted columns", got)
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

func TestExplorerSelectFallbackStatusWhenColumnsFail(t *testing.T) {
	t.Parallel()

	conn := &explorerColumnConn{columnsErr: errors.New("permission denied")}
	m := newMainLayer()
	a := &app{conn: conn, layers: []Layer{m}}
	m.focus = FocusExplorer
	expandExplorerToOrders(m.explorer)

	m.HandleKey(a, Key{Kind: KeyEnter})

	if !strings.Contains(m.status, "columns unavailable; generated SELECT *: permission denied") {
		t.Fatalf("status = %q, want explicit SELECT * fallback", m.status)
	}
	got := m.editor.buf.Text()
	if !strings.Contains(got, "SELECT TOP 100") || !strings.Contains(got, "*") || !strings.Contains(got, "[dbo].[orders]") {
		t.Fatalf("editor SQL = %q, want SELECT * fallback", got)
	}
}

func TestExplorerRichDesignFailureFallsBackToColumns(t *testing.T) {
	t.Parallel()

	conn := &explorerDesignFallbackConn{
		explorerColumnConn: explorerColumnConn{cols: []db.Column{{Name: "id"}, {Name: "name"}}},
	}
	m := newMainLayer()
	a := &app{conn: conn, layers: []Layer{m}}
	m.focus = FocusExplorer
	expandExplorerToOrders(m.explorer)

	m.HandleKey(a, Key{Kind: KeyEnter})

	got := m.editor.buf.Text()
	if !strings.Contains(got, "[id]") || !strings.Contains(got, "[name]") || strings.Contains(got, "*") {
		t.Fatalf("editor SQL = %q, want columns from fallback Columns path", got)
	}
}

func TestCrossDatabaseDesignFailureFallsBackToColumnsInOnly(t *testing.T) {
	t.Parallel()

	conn := &explorerCrossDesignFallbackConn{
		explorerColumnConn: explorerColumnConn{cols: []db.Column{{Name: "id"}}},
	}
	design, err := fetchTableDesign(context.Background(), conn, db.TableRef{
		Catalog: "SqlgoA",
		Schema:  "dbo",
		Name:    "orders",
		Kind:    db.TableKindTable,
	})
	if err != nil {
		t.Fatalf("fetchTableDesign: %v", err)
	}
	if conn.tableDesignInCalls != 1 {
		t.Fatalf("TableDesignIn calls = %d, want 1", conn.tableDesignInCalls)
	}
	if conn.tableDesignCalls != 0 {
		t.Fatalf("TableDesign calls = %d, want 0 to avoid default database metadata", conn.tableDesignCalls)
	}
	if conn.columnsInCalls != 1 || conn.lastDatabase != "SqlgoA" {
		t.Fatalf("ColumnsIn calls/database = %d/%q, want 1/SqlgoA", conn.columnsInCalls, conn.lastDatabase)
	}
	if len(design.Columns) != 1 || design.Columns[0].Name != "id" {
		t.Fatalf("design columns = %+v, want id from ColumnsIn fallback", design.Columns)
	}
}

func TestExplorerActionGeneratesInsertAndUpdate(t *testing.T) {
	t.Parallel()

	conn := &explorerColumnConn{}
	m := newMainLayer()
	a := &app{conn: conn, layers: []Layer{m}}
	tbl := db.TableRef{Schema: "dbo", Name: "orders", Kind: db.TableKindTable}
	m.explorer.SetTableDesign(tbl, db.TableDesign{Columns: []db.ColumnDetail{
		{Name: "id", TypeName: "int", PrimaryKey: true, Identity: true},
		{Name: "order id", TypeName: "int"},
		{Name: "total", TypeName: "int", Computed: true},
		{Name: "note", TypeName: "nvarchar"},
	}})

	m.runExplorerObjectAction(a, tbl, explorerActionInsert)
	insertSQL := m.editor.buf.Text()
	if strings.Contains(insertSQL, "[id]") || strings.Contains(insertSQL, "[total]") {
		t.Fatalf("INSERT SQL = %q, should exclude identity/computed columns", insertSQL)
	}
	if !strings.Contains(insertSQL, "INSERT INTO") || !strings.Contains(insertSQL, "[dbo].[orders]") || !strings.Contains(insertSQL, "[order id]") || !strings.Contains(insertSQL, "[note]") {
		t.Fatalf("INSERT SQL = %q, want quoted writable columns", insertSQL)
	}
	if strings.Contains(insertSQL, "NULL") || !strings.Contains(insertSQL, "<") {
		t.Fatalf("INSERT SQL = %q, want non-runnable value placeholders instead of NULL", insertSQL)
	}

	m.runExplorerObjectAction(a, tbl, explorerActionUpdate)
	updateSQL := m.editor.buf.Text()
	if strings.Contains(updateSQL, "[id] = NULL") || strings.Contains(updateSQL, "[total] = NULL") {
		t.Fatalf("UPDATE SQL = %q, should exclude key/computed assignments", updateSQL)
	}
	if !strings.Contains(updateSQL, "UPDATE") || !strings.Contains(updateSQL, "[dbo].[orders]") || !strings.Contains(updateSQL, "WHERE") || !strings.Contains(updateSQL, "1 = 0") || !strings.Contains(updateSQL, "[note] = NULL") {
		t.Fatalf("UPDATE SQL = %q, want safe update scaffold", updateSQL)
	}
}

func TestExplorerActionPickerOpensFromTable(t *testing.T) {
	t.Parallel()

	m := newMainLayer()
	a := &app{layers: []Layer{m}}
	m.focus = FocusExplorer
	expandExplorerToOrders(m.explorer)

	m.HandleKey(a, Key{Kind: KeyRune, Rune: 'a'})

	if _, ok := a.topLayer().(*objectActionLayer); !ok {
		t.Fatalf("top layer = %T, want objectActionLayer", a.topLayer())
	}
}

func TestExplorerDesignKeyOpensTableDesign(t *testing.T) {
	t.Parallel()

	conn := &explorerColumnConn{}
	m := newMainLayer()
	a := &app{conn: conn, layers: []Layer{m}}
	m.focus = FocusExplorer
	expandExplorerToOrders(m.explorer)
	tbl := m.explorer.items[m.explorer.cursor].table
	m.explorer.SetTableDesign(tbl, db.TableDesign{Columns: []db.ColumnDetail{{Name: "id", TypeName: "int"}}})

	m.HandleKey(a, Key{Kind: KeyRune, Rune: 'd'})

	if _, ok := a.topLayer().(*tableDesignLayer); !ok {
		t.Fatalf("top layer = %T, want tableDesignLayer", a.topLayer())
	}
}

func TestExplorerInsertRequiresWritableColumns(t *testing.T) {
	t.Parallel()

	conn := &explorerColumnConn{}
	m := newMainLayer()
	a := &app{conn: conn, layers: []Layer{m}}
	tbl := db.TableRef{Schema: "dbo", Name: "audit", Kind: db.TableKindTable}
	m.explorer.SetTableDesign(tbl, db.TableDesign{Columns: []db.ColumnDetail{
		{Name: "id", Identity: true},
		{Name: "total", Computed: true},
	}})

	m.runExplorerObjectAction(a, tbl, explorerActionInsert)

	if got := m.status; got != "insert: no writable columns available" {
		t.Fatalf("status = %q, want no writable columns", got)
	}
	if len(m.sessions) != 0 {
		t.Fatalf("sessions len = %d, want no generated preview", len(m.sessions))
	}
}

func TestExplorerDeepSearchLoadsUnloadedColumns(t *testing.T) {
	t.Parallel()

	conn := &explorerColumnConn{cols: []db.Column{{Name: "customer_id", TypeName: "int"}}}
	m := newMainLayer()
	a := &app{conn: conn, layers: []Layer{m}}
	m.explorer.SetSchema(fixtureSchema(), db.SchemaDepthSchemas)
	m.explorer.ActivateSearch()
	m.explorer.searchFocused = false

	a.deepSearchExplorerColumns()

	if conn.columnsCalls != len(fixtureSchema().Tables) {
		t.Fatalf("Columns calls = %d, want %d", conn.columnsCalls, len(fixtureSchema().Tables))
	}
	if got := m.explorer.searchInput.String(); got != "" {
		t.Fatalf("search input changed to %q", got)
	}
	found := false
	for _, it := range m.explorer.items {
		if it.kind == itemColumn && it.label == "customer_id" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("deep search loaded column missing from filtered items: %+v", m.explorer.items)
	}
	if a.metadataBusy {
		t.Fatal("metadataBusy = true after synchronous deep search, want cleared")
	}
}

func TestRunQueryBlockedWhileMetadataBusy(t *testing.T) {
	t.Parallel()

	m := newMainLayer()
	a := &app{conn: &explorerColumnConn{}, layers: []Layer{m}, metadataBusy: true}
	sess := m.ensureActiveTab()
	sess.editor.buf.SetText("SELECT 1")

	a.runQuery()

	if got := sess.status; got != "metadata loading; try again when it finishes" {
		t.Fatalf("session status = %q, want metadata busy message", got)
	}
}

func TestRunQueryPromotesPreviewTab(t *testing.T) {
	t.Parallel()

	m := newMainLayer()
	a := &app{
		conn:     &explorerColumnConn{},
		layers:   []Layer{m},
		resultCh: make(chan queryEvent, 2),
	}
	sess := m.ensureActiveTab()
	sess.preview = true
	sess.editor.buf.SetText("SELECT 1")

	a.runQuery()

	if sess.preview {
		t.Fatal("preview = true after run, want promoted")
	}
	if !sess.running {
		t.Fatal("session did not start running")
	}
	if sess.cancel != nil {
		sess.cancel()
	}
}
