package tui

import (
	"testing"

	"github.com/Nulifyer/sqlgo/internal/db"
)

// fixtureSchema mirrors what sqlConn.Schema produces: tables already sorted
// by (schema, name). explorer.rebuild trusts that invariant.
func fixtureSchema() *db.SchemaInfo {
	return &db.SchemaInfo{
		Tables: []db.TableRef{
			{Schema: "dbo", Name: "orders", Kind: db.TableKindTable},
			{Schema: "dbo", Name: "users", Kind: db.TableKindTable},
			{Schema: "dbo", Name: "v_active", Kind: db.TableKindView},
			{Schema: "hr", Name: "employees", Kind: db.TableKindTable},
		},
	}
}

func TestExplorerBuildsTreeCollapsed(t *testing.T) {
	t.Parallel()
	e := newExplorer()
	e.SetSchema(fixtureSchema(), db.SchemaDepthSchemas)

	// Schemas start collapsed: only schema headers visible.
	want := []struct {
		kind  explorerItemKind
		label string
	}{
		{itemSchema, "dbo"},
		{itemSchema, "hr"},
	}
	if len(e.items) != len(want) {
		t.Fatalf("items len = %d, want %d: %+v", len(e.items), len(want), e.items)
	}
	for i, w := range want {
		if e.items[i].kind != w.kind || e.items[i].label != w.label {
			t.Errorf("items[%d] = (%d %q), want (%d %q)", i, e.items[i].kind, e.items[i].label, w.kind, w.label)
		}
	}
}

func TestExplorerExpandsSchemaManually(t *testing.T) {
	t.Parallel()
	e := newExplorer()
	e.SetSchema(fixtureSchema(), db.SchemaDepthSchemas)

	// Expand dbo schema, then its Tables and Views subgroups.
	e.cursor = 0
	e.Toggle() // expand dbo
	// dbo expanded shows subgroup headers (collapsed).
	e.cursor = 1 // Tables subgroup
	e.Toggle()   // expand Tables
	e.cursor = 4 // Views subgroup (after dbo + Tables + orders + users)
	e.Toggle()   // expand Views

	want := []struct {
		kind  explorerItemKind
		label string
	}{
		{itemSchema, "dbo"},
		{itemSubgroup, "Tables"},
		{itemTable, "orders"},
		{itemTable, "users"},
		{itemSubgroup, "Views"},
		{itemView, "v_active"},
		{itemSchema, "hr"},
	}
	if len(e.items) != len(want) {
		t.Fatalf("items len = %d, want %d: %+v", len(e.items), len(want), e.items)
	}
	for i, w := range want {
		if e.items[i].kind != w.kind || e.items[i].label != w.label {
			t.Errorf("items[%d] = (%d %q), want (%d %q)", i, e.items[i].kind, e.items[i].label, w.kind, w.label)
		}
	}
}

func TestExplorerToggleCollapsesSchema(t *testing.T) {
	t.Parallel()
	e := newExplorer()
	e.SetSchema(fixtureSchema(), db.SchemaDepthSchemas)

	// Expand dbo, then collapse it again.
	e.cursor = 0
	e.Toggle() // expand dbo
	e.cursor = 0
	e.Toggle() // collapse dbo
	// After collapse: both schemas collapsed.
	want := []struct {
		kind  explorerItemKind
		label string
	}{
		{itemSchema, "dbo"},
		{itemSchema, "hr"},
	}
	if len(e.items) != len(want) {
		t.Fatalf("items len after collapse = %d, want %d: %+v", len(e.items), len(want), e.items)
	}
	for i, w := range want {
		if e.items[i].kind != w.kind || e.items[i].label != w.label {
			t.Errorf("items[%d] = (%d %q), want (%d %q)", i, e.items[i].kind, e.items[i].label, w.kind, w.label)
		}
	}
}

func TestExplorerToggleCollapsesSubgroup(t *testing.T) {
	t.Parallel()
	e := newExplorer()
	e.SetSchema(fixtureSchema(), db.SchemaDepthSchemas)

	// Expand dbo schema first (starts collapsed).
	e.cursor = 0
	e.Toggle()

	// Expand Views subgroup so we can then collapse it.
	target := -1
	for i, it := range e.items {
		if it.kind == itemSubgroup && it.schemaName == "dbo" && it.subgroup == subgroupViews {
			target = i
			break
		}
	}
	if target < 0 {
		t.Fatalf("dbo/Views subgroup not found in tree")
	}
	e.cursor = target
	e.Toggle() // expand Views

	// Verify v_active is visible.
	found := false
	for _, it := range e.items {
		if it.kind == itemView && it.label == "v_active" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("v_active should be visible after expanding Views")
	}

	// Now collapse Views.
	e.cursor = target
	e.Toggle()

	key := subgroupExpansionKey("", "dbo", subgroupViews)
	if e.expanded[key] {
		t.Fatalf("expected dbo/Views collapsed after toggle")
	}
	// The v_active leaf must no longer be in the visible list.
	for _, it := range e.items {
		if it.kind == itemView && it.label == "v_active" {
			t.Errorf("v_active still visible after subgroup collapse")
		}
	}
	// The "Views" header itself should still be present.
	found = false
	for _, it := range e.items {
		if it.kind == itemSubgroup && it.schemaName == "dbo" && it.subgroup == subgroupViews {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("dbo/Views header missing after subgroup collapse")
	}
}

func TestExplorerMoveCursorClamps(t *testing.T) {
	t.Parallel()
	e := newExplorer()
	e.SetSchema(fixtureSchema(), db.SchemaDepthSchemas)

	// Expand dbo so there's more than 2 items to test with.
	e.cursor = 0
	e.Toggle()

	e.MoveCursor(-50)
	if e.cursor != 0 {
		t.Errorf("cursor after underflow = %d, want 0", e.cursor)
	}
	e.MoveCursor(1000)
	if e.cursor != len(e.items)-1 {
		t.Errorf("cursor after overflow = %d, want %d", e.cursor, len(e.items)-1)
	}
}

func TestExplorerSelectedOnlyOnLeaf(t *testing.T) {
	t.Parallel()
	e := newExplorer()
	e.SetSchema(fixtureSchema(), db.SchemaDepthSchemas)

	// Expand dbo schema and Tables subgroup to reach leaf rows.
	e.cursor = 0
	e.Toggle() // expand dbo
	e.cursor = 1
	e.Toggle() // expand Tables

	// cursor on the dbo schema header -> no selection
	e.cursor = 0
	if _, ok := e.Selected(); ok {
		t.Errorf("selection on schema header should be empty")
	}

	// cursor on the Tables subgroup header -> no selection
	e.cursor = 1
	if _, ok := e.Selected(); ok {
		t.Errorf("selection on subgroup header should be empty")
	}

	// cursor on first leaf (dbo.orders)
	e.cursor = 2
	got, ok := e.Selected()
	if !ok {
		t.Fatalf("expected selection on leaf row")
	}
	if got.Schema != "dbo" || got.Name != "orders" {
		t.Errorf("selected = %+v, want dbo.orders", got)
	}
}

func TestBuildSelectDriverSpecific(t *testing.T) {
	t.Parallel()
	tr := db.TableRef{Schema: "dbo", Name: "users"}
	cases := []struct {
		name string
		caps db.Capabilities
		want string
	}{
		{
			name: "mssql-top-brackets",
			caps: db.Capabilities{SchemaDepth: db.SchemaDepthSchemas, LimitSyntax: db.LimitSyntaxSelectTop, IdentifierQuote: '['},
			want: "SELECT TOP 100 * FROM [dbo].[users]",
		},
		{
			name: "mysql-limit-backticks",
			caps: db.Capabilities{SchemaDepth: db.SchemaDepthSchemas, LimitSyntax: db.LimitSyntaxLimit, IdentifierQuote: '`'},
			want: "SELECT * FROM `dbo`.`users` LIMIT 100",
		},
		{
			name: "postgres-limit-doublequotes",
			caps: db.Capabilities{SchemaDepth: db.SchemaDepthSchemas, LimitSyntax: db.LimitSyntaxLimit, IdentifierQuote: '"'},
			want: `SELECT * FROM "dbo"."users" LIMIT 100`,
		},
		{
			name: "oracle-fetchfirst-doublequotes",
			caps: db.Capabilities{SchemaDepth: db.SchemaDepthSchemas, LimitSyntax: db.LimitSyntaxFetchFirst, IdentifierQuote: '"'},
			want: `SELECT * FROM "dbo"."users" OFFSET 0 ROWS FETCH NEXT 100 ROWS ONLY`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := BuildSelect(c.caps, tr, 100)
			if got != c.want {
				t.Errorf("BuildSelect(%+v) = %q, want %q", c.caps, got, c.want)
			}
		})
	}
}

// TestExplorerFlatSchemaOmitsSchemaHeader proves SchemaDepthFlat drops
// the per-schema parent node entirely, emitting the Tables/Views
// subgroups at the root so a SQLite connection doesn't show a stray
// "main" header above every object.
func TestExplorerFlatSchemaOmitsSchemaHeader(t *testing.T) {
	t.Parallel()
	info := &db.SchemaInfo{
		Tables: []db.TableRef{
			{Schema: "main", Name: "orders", Kind: db.TableKindTable},
			{Schema: "main", Name: "users", Kind: db.TableKindTable},
			{Schema: "main", Name: "v_active", Kind: db.TableKindView},
		},
	}
	e := newExplorer()
	e.SetSchema(info, db.SchemaDepthFlat)

	// Flat mode starts with just the two subgroup headers collapsed; no
	// schema header appears even before expansion.
	collapsed := []struct {
		kind  explorerItemKind
		label string
	}{
		{itemSubgroup, "Tables"},
		{itemSubgroup, "Views"},
	}
	if len(e.items) != len(collapsed) {
		t.Fatalf("collapsed items len = %d, want %d: %+v", len(e.items), len(collapsed), e.items)
	}
	for i, w := range collapsed {
		if e.items[i].kind != w.kind || e.items[i].label != w.label {
			t.Errorf("collapsed items[%d] = (%d %q), want (%d %q)",
				i, e.items[i].kind, e.items[i].label, w.kind, w.label)
		}
	}
	for _, it := range e.items {
		if it.kind == itemSchema {
			t.Errorf("flat mode emitted a schema header: %+v", it)
		}
	}

	// Expand both subgroups to confirm leaves still materialize under the
	// flat layout.
	e.cursor = 0
	e.Toggle() // Tables
	e.cursor = 3
	e.Toggle() // Views

	expanded := []struct {
		kind  explorerItemKind
		label string
	}{
		{itemSubgroup, "Tables"},
		{itemTable, "orders"},
		{itemTable, "users"},
		{itemSubgroup, "Views"},
		{itemView, "v_active"},
	}
	if len(e.items) != len(expanded) {
		t.Fatalf("expanded items len = %d, want %d: %+v", len(e.items), len(expanded), e.items)
	}
	for i, w := range expanded {
		if e.items[i].kind != w.kind || e.items[i].label != w.label {
			t.Errorf("expanded items[%d] = (%d %q), want (%d %q)",
				i, e.items[i].kind, e.items[i].label, w.kind, w.label)
		}
	}
	for _, it := range e.items {
		if it.kind == itemSchema {
			t.Errorf("flat mode emitted a schema header after expansion: %+v", it)
		}
	}
}

// TestQualifiedNameFlatSchema covers the SchemaDepthFlat case used by
// SQLite and Firebird, where tables live at the root with no schema prefix.
// Even when a synthesized schema (e.g. "main") is present on the TableRef,
// SchemaDepthFlat must suppress it.
func TestQualifiedNameFlatSchema(t *testing.T) {
	t.Parallel()
	caps := db.Capabilities{IdentifierQuote: '"', SchemaDepth: db.SchemaDepthFlat}
	cases := []struct {
		name string
		tr   db.TableRef
		want string
	}{
		{"empty schema", db.TableRef{Name: "users"}, `"users"`},
		{"synthesized schema stripped", db.TableRef{Schema: "main", Name: "GADGETS"}, `"GADGETS"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := QualifiedName(caps, c.tr)
			if got != c.want {
				t.Errorf("QualifiedName(flat, %+v) = %q, want %q", c.tr, got, c.want)
			}
		})
	}
}
