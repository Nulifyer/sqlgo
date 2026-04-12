package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

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

// runExplain wraps sql in the driver's EXPLAIN form, runs it
// against the live connection with a 5s deadline, and parses the
// rows into an explainTree. ExplainFormatNone drivers return an
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

	wrapped := explainWrapSQL(caps.ExplainFormat, sql)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rows, err := a.conn.Query(ctx, wrapped)
	if err != nil {
		return nil, fmt.Errorf("explain: %w", err)
	}
	defer rows.Close()

	var raw [][]any
	for rows.Next() {
		r, err := rows.Scan()
		if err != nil {
			return nil, fmt.Errorf("explain scan: %w", err)
		}
		raw = append(raw, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("explain rows: %w", err)
	}

	return explainParse(caps.ExplainFormat, raw)
}

// explainWrapSQL builds the driver-specific EXPLAIN prefix.
// Strips trailing semicolons + whitespace so the wrapper SQL
// lands as one statement.
func explainWrapSQL(format db.ExplainFormat, sql string) string {
	trimmed := strings.TrimRightFunc(strings.TrimSpace(sql), func(r rune) bool {
		return r == ';' || r == ' ' || r == '\t' || r == '\n'
	})
	switch format {
	case db.ExplainFormatPostgresJSON:
		return "EXPLAIN (FORMAT JSON) " + trimmed
	case db.ExplainFormatMySQLJSON:
		return "EXPLAIN FORMAT=JSON " + trimmed
	case db.ExplainFormatSQLiteRows:
		return "EXPLAIN QUERY PLAN " + trimmed
	}
	return trimmed
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
	}
	return &explainTree{root: &explainNode{label: "unsupported format"}}, nil
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
