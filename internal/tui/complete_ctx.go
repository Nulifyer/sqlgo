package tui

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/sqltok"
)

// clauseKind classifies the SQL clause under the cursor.
type clauseKind int

const (
	clauseGeneric clauseKind = iota
	clauseSelectList
	clauseFromTarget // after FROM or JOIN
	clauseWhereish   // WHERE / ON / HAVING / GROUP BY / ORDER BY
)

func (k clauseKind) String() string {
	switch k {
	case clauseSelectList:
		return "select"
	case clauseFromTarget:
		return "from"
	case clauseWhereish:
		return "where"
	}
	return "generic"
}

// tableScope is one entry in the FROM/JOIN list. schema is empty
// on bare names; alias is empty when no alias was given.
type tableScope struct {
	schema string
	name   string
	alias  string
}

// cteDef is one CTE from a WITH clause. columns is populated only
// when spelled out as `WITH name (a, b) AS ...`.
type cteDef struct {
	name    string
	columns []string
}

// completionCtx carries the cursor analysis. prefix/qualifier/
// startCol are filled in by openCompletion after analyze returns.
type completionCtx struct {
	clause    clauseKind
	inScope   []tableScope
	ctes      []cteDef
	qualifier string // "x" in "x.name"
	prefix    string // identifier chars under cursor
	startCol  int    // rune col where prefix starts
	suppress  bool   // cursor inside string or comment
}

// analyzeCursorContext tokenizes text and classifies the cursor.
// cursorOffset is a rune offset into text. Walks the whole
// current statement (not just pre-cursor) so SELECT-list
// completion sees FROM tables typed later in source order.
func analyzeCursorContext(text string, cursorOffset int) completionCtx {
	ctx := completionCtx{clause: clauseGeneric}

	tokens := sqltok.TokenizeText(text)

	// Suppress inside string or comment.
	for _, t := range tokens {
		if t.Kind != sqltok.String && t.Kind != sqltok.Comment {
			continue
		}
		if cursorOffset > t.StartCol && cursorOffset <= t.EndCol {
			if cursorOffset == t.EndCol && t.Kind == sqltok.String && terminatedString(t.Text) {
				continue
			}
			if cursorOffset == t.EndCol && t.Kind == sqltok.Comment && terminatedComment(t.Text) {
				continue
			}
			ctx.suppress = true
			return ctx
		}
	}

	var meaningful []sqltok.Token
	for _, t := range tokens {
		if t.Kind == sqltok.Whitespace || t.Kind == sqltok.Comment {
			continue
		}
		meaningful = append(meaningful, t)
	}

	stmtStart, stmtEnd := statementBounds(meaningful, cursorOffset)
	stmt := meaningful[stmtStart:stmtEnd]

	// Classify at the cursor's own paren depth so an inner SELECT
	// in a subquery body classifies locally, not against the outer.
	pre := cursorLocalPreTokens(stmt, cursorOffset)
	ctx.clause = classifyClause(pre)

	// Scope extraction walks depth-0 tokens so subquery FROMs
	// don't leak into the outer scope.
	ctx.inScope = extractFromScope(stmt)
	ctx.ctes = extractCTEs(stmt)
	return ctx
}

// statementBounds returns [start, end) indices in meaningful
// around the statement containing cursorOffset. Boundaries are
// semicolons (or ends of the token stream).
func statementBounds(meaningful []sqltok.Token, cursorOffset int) (int, int) {
	start := 0
	end := len(meaningful)
	for i, t := range meaningful {
		if t.Kind == sqltok.Punct && t.Text == ";" {
			if t.EndCol <= cursorOffset {
				start = i + 1
			} else if i < end {
				end = i
				break
			}
		}
	}
	if start > end {
		start = end
	}
	return start, end
}

// terminatedString reports whether a string token has its
// closing quote (first and last runes match).
func terminatedString(s string) bool {
	if len(s) < 2 {
		return false
	}
	r := []rune(s)
	return r[len(r)-1] == r[0]
}

// terminatedComment reports whether a comment token has its
// closer. Line comments (-- ...) always count as terminated at EOL.
func terminatedComment(s string) bool {
	if strings.HasPrefix(s, "--") {
		return true
	}
	return strings.HasSuffix(s, "*/")
}

// classifyClause walks pre backwards for the last clause keyword.
func classifyClause(pre []sqltok.Token) clauseKind {
	if len(pre) == 0 {
		return clauseGeneric
	}
	last := strings.ToUpper(pre[len(pre)-1].Text)
	if last == "FROM" || last == "JOIN" {
		return clauseFromTarget
	}
	for i := len(pre) - 1; i >= 0; i-- {
		t := pre[i]
		if t.Kind != sqltok.Keyword {
			continue
		}
		upper := strings.ToUpper(t.Text)
		switch upper {
		case "SELECT":
			return clauseSelectList
		case "WHERE", "HAVING", "ON":
			return clauseWhereish
		case "GROUP", "ORDER":
			if i+1 < len(pre) && strings.EqualFold(pre[i+1].Text, "BY") {
				return clauseWhereish
			}
		case "FROM", "JOIN", "INNER", "LEFT", "RIGHT", "FULL", "CROSS":
			return clauseFromTarget
		case "INSERT", "UPDATE", "DELETE", "SET", "VALUES":
			return clauseGeneric
		}
	}
	return clauseGeneric
}

// cursorLocalPreTokens returns pre-cursor tokens at the cursor's
// own paren depth. Lets classifyClause see the local context of
// an inner subquery instead of the outer statement.
func cursorLocalPreTokens(stmt []sqltok.Token, cursorOffset int) []sqltok.Token {
	cursorDepth := 0
	depth := 0
	for _, t := range stmt {
		if t.EndCol > cursorOffset {
			cursorDepth = depth
			break
		}
		if t.Kind == sqltok.Punct && t.Text == "(" {
			depth++
		} else if t.Kind == sqltok.Punct && t.Text == ")" {
			if depth > 0 {
				depth--
			}
		}
	}
	var out []sqltok.Token
	depth = 0
	for _, t := range stmt {
		if t.EndCol > cursorOffset {
			break
		}
		if t.Kind == sqltok.Punct && t.Text == "(" {
			depth++
			continue
		}
		if t.Kind == sqltok.Punct && t.Text == ")" {
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth == cursorDepth {
			out = append(out, t)
		}
	}
	return out
}

// extractCTEs walks stmt for a top-level WITH clause. Handles:
//
//	WITH [RECURSIVE] name [(col, col)] AS (body) [, ...]
//
// CTE bodies are skipped via paren matching, not parsed --
// recursive analysis of the body is out of scope.
func extractCTEs(stmt []sqltok.Token) []cteDef {
	if len(stmt) == 0 {
		return nil
	}
	depth := 0
	start := -1
	for i, t := range stmt {
		if t.Kind == sqltok.Punct && t.Text == "(" {
			depth++
			continue
		}
		if t.Kind == sqltok.Punct && t.Text == ")" {
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth != 0 {
			continue
		}
		if t.Kind == sqltok.Keyword && strings.EqualFold(t.Text, "WITH") {
			start = i + 1
			break
		}
		// Any other top-level keyword before WITH means no CTE.
		if t.Kind == sqltok.Keyword {
			return nil
		}
	}
	if start < 0 {
		return nil
	}
	// Skip optional RECURSIVE.
	if start < len(stmt) && stmt[start].Kind == sqltok.Keyword && strings.EqualFold(stmt[start].Text, "RECURSIVE") {
		start++
	}

	var out []cteDef
	i := start
	for i < len(stmt) {
		if stmt[i].Kind != sqltok.Ident {
			break
		}
		def := cteDef{name: stmt[i].Text}
		i++

		// Optional column list.
		if i < len(stmt) && stmt[i].Kind == sqltok.Punct && stmt[i].Text == "(" {
			i++
			for i < len(stmt) && !(stmt[i].Kind == sqltok.Punct && stmt[i].Text == ")") {
				if stmt[i].Kind == sqltok.Ident {
					def.columns = append(def.columns, stmt[i].Text)
				}
				i++
			}
			if i < len(stmt) {
				i++
			}
		}

		if i >= len(stmt) || !(stmt[i].Kind == sqltok.Keyword && strings.EqualFold(stmt[i].Text, "AS")) {
			break
		}
		i++

		// Skip body via balanced parens.
		if i >= len(stmt) || !(stmt[i].Kind == sqltok.Punct && stmt[i].Text == "(") {
			break
		}
		depth = 1
		i++
		for i < len(stmt) && depth > 0 {
			if stmt[i].Kind == sqltok.Punct && stmt[i].Text == "(" {
				depth++
			} else if stmt[i].Kind == sqltok.Punct && stmt[i].Text == ")" {
				depth--
			}
			i++
		}

		out = append(out, def)

		if i < len(stmt) && stmt[i].Kind == sqltok.Punct && stmt[i].Text == "," {
			i++
			continue
		}
		break
	}
	return out
}

// depthZeroTokens returns tokens outside any parens. Used by
// extractFromScope so subquery / CTE-body FROMs don't leak out.
func depthZeroTokens(stmt []sqltok.Token) []sqltok.Token {
	out := make([]sqltok.Token, 0, len(stmt))
	depth := 0
	for _, t := range stmt {
		if t.Kind == sqltok.Punct && t.Text == "(" {
			depth++
			continue
		}
		if t.Kind == sqltok.Punct && t.Text == ")" {
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth == 0 {
			out = append(out, t)
		}
	}
	return out
}

// extractFromScope walks depth-0 tokens for every FROM/JOIN
// table reference. Accepts comma-separated lists, schema.name,
// optional AS alias. Walks the whole statement so SELECT-list
// completion sees tables typed after the cursor.
func extractFromScope(stmt []sqltok.Token) []tableScope {
	cur := depthZeroTokens(stmt)

	var out []tableScope
	seen := map[string]struct{}{}
	i := 0
	for i < len(cur) {
		t := cur[i]
		if t.Kind == sqltok.Keyword {
			up := strings.ToUpper(t.Text)
			if up == "FROM" || up == "JOIN" {
				i++
				for i < len(cur) {
					if cur[i].Kind == sqltok.Keyword {
						stopUp := strings.ToUpper(cur[i].Text)
						if stopUp == "WHERE" || stopUp == "GROUP" || stopUp == "ORDER" ||
							stopUp == "HAVING" || stopUp == "LIMIT" || stopUp == "OFFSET" ||
							stopUp == "UNION" || stopUp == "INTERSECT" || stopUp == "EXCEPT" ||
							stopUp == "ON" || stopUp == "USING" {
							break
						}
						if stopUp == "JOIN" || stopUp == "INNER" || stopUp == "LEFT" ||
							stopUp == "RIGHT" || stopUp == "FULL" || stopUp == "CROSS" ||
							stopUp == "NATURAL" {
							break
						}
					}
					if cur[i].Kind == sqltok.Punct && cur[i].Text == "," {
						i++
						continue
					}
					if cur[i].Kind != sqltok.Ident {
						i++
						continue
					}
					ref, consumed := parseTableRef(cur[i:])
					if ref.name != "" {
						key := ref.schema + "\x00" + ref.name + "\x00" + ref.alias
						if _, ok := seen[key]; !ok {
							seen[key] = struct{}{}
							out = append(out, ref)
						}
					}
					if consumed == 0 {
						i++
					} else {
						i += consumed
					}
				}
				continue
			}
		}
		i++
	}
	return out
}

// parseTableRef reads a single table reference starting at
// tokens[0]. Returns the parsed reference and the number of tokens
// it consumed. Accepts:
//
//	tableName
//	schema.tableName
//	tableName alias
//	tableName AS alias
//	schema.tableName alias
//	schema.tableName AS alias
func parseTableRef(tokens []sqltok.Token) (tableScope, int) {
	var ref tableScope
	if len(tokens) == 0 || tokens[0].Kind != sqltok.Ident {
		return ref, 0
	}
	ref.name = tokens[0].Text
	consumed := 1
	// Look for "." <ident> meaning this was a schema qualifier.
	if consumed+1 < len(tokens) &&
		tokens[consumed].Kind == sqltok.Punct && tokens[consumed].Text == "." &&
		tokens[consumed+1].Kind == sqltok.Ident {
		ref.schema = ref.name
		ref.name = tokens[consumed+1].Text
		consumed += 2
	}
	// Optional alias: bare ident OR "AS" <ident>.
	if consumed < len(tokens) {
		t := tokens[consumed]
		if t.Kind == sqltok.Keyword && strings.EqualFold(t.Text, "AS") && consumed+1 < len(tokens) && tokens[consumed+1].Kind == sqltok.Ident {
			ref.alias = tokens[consumed+1].Text
			consumed += 2
		} else if t.Kind == sqltok.Ident {
			// Bare ident after a table ref is an alias
			// ("FROM a b" = "FROM a AS b" per SQL standard).
			ref.alias = t.Text
			consumed++
		}
	}
	return ref, consumed
}

// columnCache is the per-connection column store, populated
// lazily on first completion request. Cleared on disconnect.
type columnCache struct {
	mu      sync.Mutex
	entries map[string][]db.Column
}

func newColumnCache() *columnCache {
	return &columnCache{entries: map[string][]db.Column{}}
}

func (c *columnCache) get(t tableScope) ([]db.Column, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cols, ok := c.entries[columnCacheKey(t)]
	return cols, ok
}

func (c *columnCache) put(t tableScope, cols []db.Column) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[columnCacheKey(t)] = cols
}

func (c *columnCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = map[string][]db.Column{}
}

func columnCacheKey(t tableScope) string {
	return t.schema + "\x00" + t.name
}

// gatherCompletionsCtx picks candidate buckets by clause and
// qualifier:
//   - FROM target:  schemas + tables + views + CTE names
//   - SELECT list:  columns + aliases + functions + SELECT kw
//   - WHERE-ish:    columns + aliases + functions + keywords
//   - Generic:      keywords + functions + schemas + tables
//
// Qualified prefix narrows to CTE cols → alias cols → schema tables.
func (a *app) gatherCompletionsCtx(ctx completionCtx) []completionItem {
	if ctx.qualifier != "" {
		if items := a.gatherQualified(ctx); items != nil {
			return items
		}
		// Fall through when qualifier unknown.
	}

	var items []completionItem
	switch ctx.clause {
	case clauseFromTarget:
		items = append(items, a.schemaCandidates()...)
		items = append(items, a.tableCandidates()...)
		for _, c := range ctx.ctes {
			items = append(items, completionItem{text: c.name, kind: completeTable})
		}
	case clauseSelectList:
		items = append(items, a.inScopeColumnCandidates(ctx)...)
		items = append(items, a.aliasCandidates(ctx)...)
		items = append(items, a.functionCandidates()...)
		items = append(items, completionItem{text: "*", kind: completeKeyword})
		items = append(items, completionItem{text: "FROM", kind: completeKeyword})
		items = append(items, completionItem{text: "DISTINCT", kind: completeKeyword})
		items = append(items, completionItem{text: "TOP", kind: completeKeyword})
	case clauseWhereish:
		items = append(items, a.inScopeColumnCandidates(ctx)...)
		items = append(items, a.aliasCandidates(ctx)...)
		items = append(items, a.functionCandidates()...)
		items = append(items, a.keywordCandidates()...)
	default:
		items = append(items, a.keywordCandidates()...)
		items = append(items, a.functionCandidates()...)
		items = append(items, a.schemaCandidates()...)
		items = append(items, a.tableCandidates()...)
	}
	return items
}

// gatherQualified handles "q.prefix". Returns nil on no match so
// the caller falls back to the unqualified list. Order:
//  1. CTE name → declared columns (CTEs shadow real tables).
//  2. Alias/table in FROM scope → columns.
//  3. Schema name → tables/views.
func (a *app) gatherQualified(ctx completionCtx) []completionItem {
	q := ctx.qualifier

	for _, c := range ctx.ctes {
		if !strings.EqualFold(c.name, q) {
			continue
		}
		if len(c.columns) == 0 {
			// CTE with no declared cols: return empty (not nil)
			// so we don't fall through to the underlying table.
			return []completionItem{}
		}
		out := make([]completionItem, 0, len(c.columns))
		for _, col := range c.columns {
			out = append(out, completionItem{text: col, kind: completeColumn})
		}
		return out
	}

	for _, t := range ctx.inScope {
		match := t.alias != "" && strings.EqualFold(t.alias, q)
		if !match {
			match = strings.EqualFold(t.name, q)
		}
		if match {
			cols := a.fetchColumnsFor(t)
			if len(cols) == 0 {
				return nil
			}
			var items []completionItem
			for _, c := range cols {
				items = append(items, completionItem{text: c.Name, kind: completeColumn})
			}
			return items
		}
	}

	m := a.mainLayerPtr()
	if m == nil || m.explorer == nil || m.explorer.info == nil {
		return nil
	}
	var items []completionItem
	for _, t := range m.explorer.info.Tables {
		if !strings.EqualFold(t.Schema, q) {
			continue
		}
		kind := completeTable
		if t.Kind == db.TableKindView {
			kind = completeView
		}
		items = append(items, completionItem{text: t.Name, kind: kind})
	}
	if len(items) == 0 {
		return nil
	}
	return items
}

func (a *app) keywordCandidates() []completionItem {
	out := make([]completionItem, 0, 128)
	for _, kw := range sqltok.Keywords() {
		out = append(out, completionItem{text: kw, kind: completeKeyword})
	}
	return out
}

// sqlFunctions is the dialect-agnostic core function set. Stays
// narrow on purpose so it's useful across all four engines.
var sqlFunctions = []string{
	"AVG", "COUNT", "MAX", "MIN", "SUM",
	"COALESCE", "NULLIF",
	"CONCAT", "LENGTH", "LOWER", "LTRIM", "REPLACE", "RTRIM",
	"SUBSTRING", "TRIM", "UPPER",
	"ABS", "CEILING", "FLOOR", "MOD", "POWER", "ROUND", "SQRT",
	"CURRENT_DATE", "CURRENT_TIME", "CURRENT_TIMESTAMP", "EXTRACT",
	"CASE", "CAST",
}

func (a *app) functionCandidates() []completionItem {
	out := make([]completionItem, 0, len(sqlFunctions))
	for _, f := range sqlFunctions {
		out = append(out, completionItem{text: f, kind: completeFunction})
	}
	return out
}

func (a *app) schemaCandidates() []completionItem {
	m := a.mainLayerPtr()
	if m == nil || m.explorer == nil || m.explorer.info == nil {
		return nil
	}
	seen := map[string]struct{}{}
	var out []completionItem
	for _, t := range m.explorer.info.Tables {
		if t.Schema == "" {
			continue
		}
		if _, ok := seen[t.Schema]; ok {
			continue
		}
		seen[t.Schema] = struct{}{}
		out = append(out, completionItem{text: t.Schema, kind: completeSchema})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].text < out[j].text })
	return out
}

// tableCandidates returns every table/view as both bare and
// "schema.name" so either form completes.
func (a *app) tableCandidates() []completionItem {
	m := a.mainLayerPtr()
	if m == nil || m.explorer == nil || m.explorer.info == nil {
		return nil
	}
	var out []completionItem
	for _, t := range m.explorer.info.Tables {
		kind := completeTable
		if t.Kind == db.TableKindView {
			kind = completeView
		}
		out = append(out, completionItem{text: t.Name, kind: kind})
		if t.Schema != "" {
			out = append(out, completionItem{
				text: t.Schema + "." + t.Name,
				kind: kind,
			})
		}
	}
	return out
}

// inScopeColumnCandidates fetches columns for every in-scope
// table. CTE matches use declared columns instead of hitting the
// DB (CTE isn't persisted).
func (a *app) inScopeColumnCandidates(ctx completionCtx) []completionItem {
	var out []completionItem
	for _, t := range ctx.inScope {
		if cteCols, ok := ctx.lookupCTEColumns(t.name); ok {
			for _, col := range cteCols {
				out = append(out, completionItem{text: col, kind: completeColumn})
			}
			continue
		}
		cols := a.fetchColumnsFor(t)
		for _, c := range cols {
			out = append(out, completionItem{text: c.Name, kind: completeColumn})
		}
	}
	return out
}

func (c *completionCtx) lookupCTEColumns(name string) ([]string, bool) {
	for _, cte := range c.ctes {
		if strings.EqualFold(cte.name, name) {
			return cte.columns, true
		}
	}
	return nil, false
}

// aliasCandidates surfaces aliases + bare table names so they
// appear alongside columns in SELECT/WHERE contexts.
func (a *app) aliasCandidates(ctx completionCtx) []completionItem {
	var out []completionItem
	seen := map[string]struct{}{}
	for _, t := range ctx.inScope {
		if t.alias != "" {
			if _, ok := seen[t.alias]; !ok {
				seen[t.alias] = struct{}{}
				out = append(out, completionItem{text: t.alias, kind: completeAlias})
			}
		}
		if _, ok := seen[t.name]; !ok {
			seen[t.name] = struct{}{}
			out = append(out, completionItem{text: t.name, kind: completeTable})
		}
	}
	return out
}

// fetchColumnsFor returns a table's columns from the cache, or
// dials the live connection with a 1.5s deadline on miss. Errors
// cache as empty so broken tables don't re-hit every keystroke.
func (a *app) fetchColumnsFor(t tableScope) []db.Column {
	if a == nil || a.columnCache == nil {
		if a != nil {
			a.columnCache = newColumnCache()
		} else {
			return nil
		}
	}
	if cols, ok := a.columnCache.get(t); ok {
		return cols
	}
	if a.conn == nil {
		return nil
	}

	ref := a.resolveTableRef(t)
	if ref.Name == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	cols, err := a.conn.Columns(ctx, ref)
	if err != nil {
		a.columnCache.put(t, nil)
		return nil
	}
	a.columnCache.put(t, cols)
	return cols
}

// resolveTableRef attaches a schema to a bare tableScope using
// the explorer's loaded schema info.
func (a *app) resolveTableRef(t tableScope) db.TableRef {
	m := a.mainLayerPtr()
	if m == nil || m.explorer == nil || m.explorer.info == nil {
		return db.TableRef{Schema: t.schema, Name: t.name}
	}
	info := m.explorer.info
	if t.schema != "" {
		for _, tr := range info.Tables {
			if strings.EqualFold(tr.Schema, t.schema) && strings.EqualFold(tr.Name, t.name) {
				return tr
			}
		}
		return db.TableRef{Schema: t.schema, Name: t.name}
	}
	for _, tr := range info.Tables {
		if strings.EqualFold(tr.Name, t.name) {
			return tr
		}
	}
	return db.TableRef{Name: t.name}
}
