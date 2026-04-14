package tui

import (
	"context"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/db"
	_ "github.com/Nulifyer/sqlgo/internal/db/sqlite"
)

func TestParsePostgresExplainBasic(t *testing.T) {
	t.Parallel()
	rows := [][]any{
		{`[{"Plan":{"Node Type":"Seq Scan","Relation Name":"users","Alias":"u","Total Cost":12.5,"Plan Rows":100}}]`},
	}
	tree, err := parsePostgresExplain(rows)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if tree.root == nil {
		t.Fatal("root nil")
	}
	if tree.root.label != "Seq Scan on users (u)" {
		t.Errorf("label = %q", tree.root.label)
	}
	if len(tree.root.details) == 0 {
		t.Error("expected detail lines")
	}
}

func TestParsePostgresExplainNested(t *testing.T) {
	t.Parallel()
	rows := [][]any{
		{`[{"Plan":{"Node Type":"Hash Join","Plans":[
			{"Node Type":"Seq Scan","Relation Name":"users"},
			{"Node Type":"Seq Scan","Relation Name":"orders"}
		]}}]`},
	}
	tree, err := parsePostgresExplain(rows)
	if err != nil {
		t.Fatal(err)
	}
	if tree.root == nil || len(tree.root.children) != 2 {
		t.Fatalf("expected 2 children, got %+v", tree.root)
	}
	if tree.root.children[0].label != "Seq Scan on users" {
		t.Errorf("child[0] label = %q", tree.root.children[0].label)
	}
}

func TestParseMySQLExplainBasic(t *testing.T) {
	t.Parallel()
	rows := [][]any{
		{`{"query_block":{"select_id":1,"cost_info":{"query_cost":"5.00"},"table":{"table_name":"users","access_type":"ALL","rows_examined_per_scan":100}}}`},
	}
	tree, err := parseMySQLExplain(rows)
	if err != nil {
		t.Fatal(err)
	}
	if tree.root == nil {
		t.Fatal("root nil")
	}
	if tree.root.label != "query_block" {
		t.Errorf("root label = %q", tree.root.label)
	}
	if len(tree.root.children) != 1 || tree.root.children[0].label != "table users" {
		t.Errorf("children = %+v", tree.root.children)
	}
}

func TestParseSQLiteExplainReparent(t *testing.T) {
	t.Parallel()
	rows := [][]any{
		{int64(2), int64(0), int64(0), "SCAN users"},
		{int64(3), int64(2), int64(0), "USE INDEX idx_email"},
		{int64(4), int64(0), int64(0), "SCAN orders"},
	}
	tree, err := parseSQLiteExplain(rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.root.children) != 2 {
		t.Fatalf("top-level nodes = %d, want 2", len(tree.root.children))
	}
	if tree.root.children[0].label != "SCAN users" {
		t.Errorf("first = %q", tree.root.children[0].label)
	}
	if len(tree.root.children[0].children) != 1 {
		t.Errorf("users should have 1 child")
	}
}

func TestParseMSSQLExplainBasic(t *testing.T) {
	t.Parallel()
	xmlDoc := `<ShowPlanXML xmlns="http://schemas.microsoft.com/sqlserver/2004/07/showplan">
<BatchSequence><Batch><Statements>
<StmtSimple StatementText="SELECT 1" StatementType="SELECT" StatementSubTreeCost="0.01">
<QueryPlan>
<RelOp NodeId="0" PhysicalOp="Hash Match" LogicalOp="Inner Join" EstimateRows="10" EstimateIO="0.1" EstimateCPU="0.2" EstimatedTotalSubtreeCost="0.3">
  <Hash>
    <RelOp NodeId="1" PhysicalOp="Table Scan" LogicalOp="Table Scan" EstimateRows="5" EstimateIO="0.05" EstimateCPU="0.02"/>
    <RelOp NodeId="2" PhysicalOp="Index Seek" LogicalOp="Index Seek" EstimateRows="2" EstimateIO="0.01" EstimateCPU="0.01"/>
  </Hash>
</RelOp>
</QueryPlan>
</StmtSimple>
</Statements></Batch></BatchSequence>
</ShowPlanXML>`
	tree, err := parseMSSQLExplain([][]any{{xmlDoc}})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if tree.root == nil || len(tree.root.children) != 1 {
		t.Fatalf("expected one stmt, got %+v", tree.root)
	}
	stmt := tree.root.children[0]
	if stmt.label != "SELECT" {
		t.Errorf("stmt label = %q", stmt.label)
	}
	if len(stmt.children) != 1 {
		t.Fatalf("expected one root RelOp, got %d", len(stmt.children))
	}
	root := stmt.children[0]
	if root.label != "Hash Match (Inner Join)" {
		t.Errorf("root relop label = %q", root.label)
	}
	if len(root.children) != 2 {
		t.Fatalf("expected 2 child RelOps, got %d", len(root.children))
	}
	if root.children[0].label != "Table Scan" {
		t.Errorf("child[0] label = %q", root.children[0].label)
	}
	if root.children[1].label != "Index Seek" {
		t.Errorf("child[1] label = %q", root.children[1].label)
	}
}

// TestRunExplainSQLiteLive dials an in-memory sqlite and runs a
// real EXPLAIN QUERY PLAN end-to-end.
func TestRunExplainSQLiteLive(t *testing.T) {
	a := &app{}
	a.layers = []Layer{newMainLayer()}
	a.term = &terminal{width: 80, height: 24}

	d, _ := db.Get("sqlite")
	conn, err := d.Open(context.Background(), db.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	a.conn = conn

	if err := conn.Exec(context.Background(), `CREATE TABLE widgets (id INTEGER PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err := conn.Exec(context.Background(), `INSERT INTO widgets VALUES (1, 'a')`); err != nil {
		t.Fatal(err)
	}

	tree, err := a.runExplain("SELECT * FROM widgets WHERE id = 1")
	if err != nil {
		t.Fatalf("runExplain: %v", err)
	}
	if tree == nil || tree.root == nil {
		t.Fatal("nil tree")
	}
	if len(tree.root.children) == 0 {
		t.Errorf("expected at least one plan node, got %+v", tree.root)
	}
}

// TestRunExplainNoConnection returns a friendly error.
func TestRunExplainNoConnection(t *testing.T) {
	t.Parallel()
	a := &app{}
	a.layers = []Layer{newMainLayer()}
	_, err := a.runExplain("SELECT 1")
	if err == nil {
		t.Error("expected error when disconnected")
	}
}
