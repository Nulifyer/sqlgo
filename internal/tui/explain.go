package tui

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Nulifyer/sqlgo/internal/db"
)

// explainNode is a normalized node in an EXPLAIN tree. Label is
// the display title (e.g. "Seq Scan on users"); details are dim
// sub-lines (cost, rows, type). Children are drawn indented.
type explainNode struct {
	label    string
	details  []string
	children []*explainNode
}

// explainTree is the top-level result of an EXPLAIN. Root may be
// nil when the driver returned nothing parseable; in that case the
// Raw field carries the original driver text for a fallback view.
type explainTree struct {
	root *explainNode
	raw  string
}

// runExplain calls the connection's Explain and parses the rows
// into an explainTree. ExplainFormatNone drivers return an
// "unsupported" sentinel tree.
func (a *app) runExplain(sql string) (*explainTree, error) {
	if a.conn == nil {
		return nil, fmt.Errorf("no active connection")
	}
	caps := a.conn.Capabilities()
	if caps.ExplainFormat == db.ExplainFormatNone {
		return &explainTree{
			root: &explainNode{label: "EXPLAIN not supported for " + a.conn.Driver()},
		}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), explainTimeout)
	defer cancel()
	raw, err := a.conn.Explain(ctx, sql)
	if err != nil {
		if errors.Is(err, db.ErrExplainUnsupported) {
			return &explainTree{
				root: &explainNode{label: "EXPLAIN not supported for " + a.conn.Driver()},
			}, nil
		}
		return nil, fmt.Errorf("explain: %w", err)
	}
	return explainParse(caps.ExplainFormat, raw)
}

// explainParse dispatches to the format-specific parser.
func explainParse(format db.ExplainFormat, rows [][]any) (*explainTree, error) {
	switch format {
	case db.ExplainFormatPostgresJSON:
		return parsePostgresExplain(rows)
	case db.ExplainFormatMySQLJSON:
		return parseMySQLExplain(rows)
	case db.ExplainFormatSQLiteRows:
		return parseSQLiteExplain(rows)
	case db.ExplainFormatMSSQLXML:
		return parseMSSQLExplain(rows)
	}
	return &explainTree{root: &explainNode{label: "unsupported format"}}, nil
}

// ShowPlanXML minimal schema: top-level ShowPlanXML has
// BatchSequence -> Batch -> Statements -> StmtSimple, each with a
// QueryPlan containing a single root RelOp. RelOp nests recursively.
type mssqlShowPlan struct {
	XMLName   xml.Name       `xml:"ShowPlanXML"`
	Batches   []mssqlBatch   `xml:"BatchSequence>Batch"`
}

type mssqlBatch struct {
	Statements []mssqlStmt `xml:"Statements>StmtSimple"`
}

type mssqlStmt struct {
	StatementText     string       `xml:"StatementText,attr"`
	StatementType     string       `xml:"StatementType,attr"`
	StatementSubTreeCost string    `xml:"StatementSubTreeCost,attr"`
	QueryPlan         *mssqlQPlan  `xml:"QueryPlan"`
}

type mssqlQPlan struct {
	RelOp *mssqlRelOp `xml:"RelOp"`
}

// mssqlRelOp captures attributes on a single plan node. ShowPlanXML
// wraps children in operator-specific elements (Hash, NestedLoops,
// IndexScan, ...) so we capture the body as raw XML and re-scan it
// for any nested <RelOp> tags rather than modelling every wrapper.
type mssqlRelOp struct {
	NodeID                    string `xml:"NodeId,attr"`
	PhysicalOp                string `xml:"PhysicalOp,attr"`
	LogicalOp                 string `xml:"LogicalOp,attr"`
	EstimateRows              string `xml:"EstimateRows,attr"`
	EstimateIO                string `xml:"EstimateIO,attr"`
	EstimateCPU               string `xml:"EstimateCPU,attr"`
	EstimatedTotalSubtreeCost string `xml:"EstimatedTotalSubtreeCost,attr"`
	InnerXML                  string `xml:",innerxml"`
}

// parseMSSQLExplain parses the ShowPlanXML document returned by
// SET SHOWPLAN_XML ON. Each StmtSimple becomes a top-level node;
// RelOp children nest under it.
func parseMSSQLExplain(rows [][]any) (*explainTree, error) {
	if len(rows) == 0 || len(rows[0]) == 0 {
		return nil, fmt.Errorf("explain: empty result")
	}
	raw := toString(rows[0][0])
	var plan mssqlShowPlan
	if err := xml.Unmarshal([]byte(raw), &plan); err != nil {
		return nil, fmt.Errorf("explain parse: %w", err)
	}
	root := &explainNode{label: "query plan"}
	for _, b := range plan.Batches {
		for _, s := range b.Statements {
			stmtNode := &explainNode{label: mssqlStmtLabel(s)}
			if s.StatementSubTreeCost != "" {
				stmtNode.details = append(stmtNode.details, "subtree cost "+s.StatementSubTreeCost)
			}
			if s.QueryPlan != nil && s.QueryPlan.RelOp != nil {
				stmtNode.children = append(stmtNode.children, mssqlRelOpNode(*s.QueryPlan.RelOp))
			}
			root.children = append(root.children, stmtNode)
		}
	}
	if len(root.children) == 0 {
		root.label = "empty plan"
	}
	return &explainTree{root: root, raw: raw}, nil
}

func mssqlStmtLabel(s mssqlStmt) string {
	if s.StatementType != "" {
		return s.StatementType
	}
	if s.StatementText != "" {
		return s.StatementText
	}
	return "statement"
}

func mssqlRelOpNode(r mssqlRelOp) *explainNode {
	label := r.PhysicalOp
	if r.LogicalOp != "" && r.LogicalOp != r.PhysicalOp {
		label += " (" + r.LogicalOp + ")"
	}
	if label == "" {
		label = "RelOp"
	}
	n := &explainNode{label: label}
	addIf := func(key, v string) {
		if v != "" {
			n.details = append(n.details, key+" "+v)
		}
	}
	addIf("rows", r.EstimateRows)
	addIf("io", r.EstimateIO)
	addIf("cpu", r.EstimateCPU)
	addIf("subtree cost", r.EstimatedTotalSubtreeCost)
	for _, c := range mssqlChildRelOps(r.InnerXML) {
		n.children = append(n.children, mssqlRelOpNode(c))
	}
	return n
}

// mssqlChildRelOps walks inner xml tokens and returns the immediate
// <RelOp> descendants — skipping past wrapper elements like <Hash>,
// <NestedLoops>, <IndexScan>, which vary per physical operator. We
// stop descending into a <RelOp> once found; its own InnerXML will
// be walked on the recursive call.
func mssqlChildRelOps(inner string) []mssqlRelOp {
	if inner == "" {
		return nil
	}
	dec := xml.NewDecoder(strings.NewReader(inner))
	var out []mssqlRelOp
	for {
		tok, err := dec.Token()
		if err != nil {
			return out
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if se.Name.Local == "RelOp" {
			var r mssqlRelOp
			if err := dec.DecodeElement(&r, &se); err == nil {
				out = append(out, r)
			}
		}
	}
}

// parsePostgresExplain reads the single JSON row pgx returns for
// EXPLAIN (FORMAT JSON). The top level is a JSON array with one
// object whose "Plan" key is the root.
func parsePostgresExplain(rows [][]any) (*explainTree, error) {
	if len(rows) == 0 || len(rows[0]) == 0 {
		return nil, fmt.Errorf("explain: empty result")
	}
	raw := toString(rows[0][0])
	var top []map[string]any
	if err := json.Unmarshal([]byte(raw), &top); err != nil {
		return nil, fmt.Errorf("explain parse: %w", err)
	}
	if len(top) == 0 {
		return &explainTree{raw: raw}, nil
	}
	plan, _ := top[0]["Plan"].(map[string]any)
	if plan == nil {
		return &explainTree{raw: raw}, nil
	}
	return &explainTree{root: pgNodeFromMap(plan), raw: raw}, nil
}

// pgNodeFromMap converts one Postgres plan node. The label is
// "Node Type" + optional "Relation Name"; details pick out cost,
// rows, and a handful of common fields.
func pgNodeFromMap(m map[string]any) *explainNode {
	n := &explainNode{}
	nodeType := toString(m["Node Type"])
	if rel := toString(m["Relation Name"]); rel != "" {
		n.label = nodeType + " on " + rel
		if alias := toString(m["Alias"]); alias != "" && alias != rel {
			n.label += " (" + alias + ")"
		}
	} else {
		n.label = nodeType
	}
	addDetail := func(key, fmtStr string) {
		if v, ok := m[key]; ok {
			n.details = append(n.details, fmt.Sprintf(fmtStr, v))
		}
	}
	addDetail("Startup Cost", "startup cost %v")
	addDetail("Total Cost", "total cost %v")
	addDetail("Plan Rows", "rows %v")
	addDetail("Plan Width", "width %v")
	addDetail("Filter", "filter %v")
	addDetail("Index Cond", "index cond %v")
	addDetail("Join Type", "join %v")
	addDetail("Hash Cond", "hash cond %v")

	if kids, ok := m["Plans"].([]any); ok {
		for _, kid := range kids {
			if km, ok := kid.(map[string]any); ok {
				n.children = append(n.children, pgNodeFromMap(km))
			}
		}
	}
	return n
}

// parseMySQLExplain reads the "query_block" object MySQL returns.
// MySQL's plan shape is messier -- we surface table-name scans
// plus cost info and recurse on nested_loop arrays.
func parseMySQLExplain(rows [][]any) (*explainTree, error) {
	if len(rows) == 0 || len(rows[0]) == 0 {
		return nil, fmt.Errorf("explain: empty result")
	}
	raw := toString(rows[0][0])
	var top map[string]any
	if err := json.Unmarshal([]byte(raw), &top); err != nil {
		return nil, fmt.Errorf("explain parse: %w", err)
	}
	qb, _ := top["query_block"].(map[string]any)
	if qb == nil {
		return &explainTree{raw: raw}, nil
	}
	return &explainTree{root: mysqlNodeFromMap("query_block", qb), raw: raw}, nil
}

func mysqlNodeFromMap(label string, m map[string]any) *explainNode {
	n := &explainNode{label: label}
	if id, ok := m["select_id"]; ok {
		n.details = append(n.details, fmt.Sprintf("select_id %v", id))
	}
	if ci, ok := m["cost_info"].(map[string]any); ok {
		if v, ok := ci["query_cost"]; ok {
			n.details = append(n.details, fmt.Sprintf("cost %v", v))
		}
	}
	// Table nodes: leaves with access method + rows.
	if tbl, ok := m["table"].(map[string]any); ok {
		n.children = append(n.children, mysqlTableNode(tbl))
	}
	// Nested_loop: array of child objects, each with a "table".
	if nl, ok := m["nested_loop"].([]any); ok {
		for _, item := range nl {
			if im, ok := item.(map[string]any); ok {
				if tbl, ok := im["table"].(map[string]any); ok {
					n.children = append(n.children, mysqlTableNode(tbl))
				}
			}
		}
	}
	// Ordering / grouping operations.
	for _, k := range []string{"ordering_operation", "grouping_operation", "duplicates_removal"} {
		if inner, ok := m[k].(map[string]any); ok {
			n.children = append(n.children, mysqlNodeFromMap(k, inner))
		}
	}
	return n
}

func mysqlTableNode(m map[string]any) *explainNode {
	n := &explainNode{label: "table " + toString(m["table_name"])}
	for _, k := range []string{"access_type", "key", "rows_examined_per_scan", "filtered", "attached_condition"} {
		if v, ok := m[k]; ok {
			n.details = append(n.details, fmt.Sprintf("%s %v", k, v))
		}
	}
	return n
}

// parseSQLiteExplain builds a tree from the (id, parent, notused,
// detail) rows that PRAGMA-less EXPLAIN QUERY PLAN returns.
func parseSQLiteExplain(rows [][]any) (*explainTree, error) {
	nodes := map[int64]*explainNode{}
	var ids []int64
	parents := map[int64]int64{}
	for _, r := range rows {
		if len(r) < 4 {
			continue
		}
		id := toInt64(r[0])
		parent := toInt64(r[1])
		detail := toString(r[3])
		nodes[id] = &explainNode{label: detail}
		ids = append(ids, id)
		parents[id] = parent
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	root := &explainNode{label: "query plan"}
	for _, id := range ids {
		node := nodes[id]
		parentID := parents[id]
		if parentID == 0 {
			root.children = append(root.children, node)
		} else if p, ok := nodes[parentID]; ok {
			p.children = append(p.children, node)
		} else {
			root.children = append(root.children, node)
		}
	}
	if len(root.children) == 0 {
		root.label = "empty plan"
	}
	return &explainTree{root: root}, nil
}

// toString coerces a value from the streaming Rows.Scan path into
// a display string. The db layer already turns []byte into string,
// so the usual case is a plain string.
func toString(v any) string {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	}
	return fmt.Sprintf("%v", v)
}

// toInt64 coerces sqlite's integer columns. Drivers disagree on
// int width; accept any.
func toInt64(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int32:
		return int64(x)
	case int:
		return int64(x)
	case string:
		var n int64
		fmt.Sscanf(x, "%d", &n)
		return n
	}
	return 0
}
