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
	clauseFromTarget    // after FROM or JOIN, cursor expects a table
	clauseAfterTableRef // FROM has a satisfied table ref; next up is JOIN/WHERE/GROUP/...
	clauseWhereish      // WHERE / ON / HAVING / GROUP BY / ORDER BY
)

func (k clauseKind) String() string {
	switch k {
	case clauseSelectList:
		return "select"
	case clauseFromTarget:
		return "from"
	case clauseAfterTableRef:
		return "afterTable"
	case clauseWhereish:
		return "where"
	}
	return "generic"
}

// tableScope is one entry in the FROM/JOIN list. schema is empty
// on bare names; alias is empty when no alias was given. cols is
// populated for derived refs (subquery-FROM aliases) whose column
// list comes from the inner SELECT list, not the live schema.
type tableScope struct {
	schema string
	name   string
	alias  string
	cols   []string
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
	ctx.clause = classifyClause(pre, cursorOffset)

	// Scope extraction walks depth-0 tokens so subquery FROMs
	// don't leak into the outer scope.
	ctx.inScope = extractFromScope(stmt, cursorOffset)
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
// cursorOffset lets us tell a table-name-being-typed
// (`FROM prod|`) from a cursor past a complete table reference
// (`FROM products <ws> |` or `FROM products <ws> w|`) -- the latter
// reports clauseAfterTableRef so JOIN/WHERE/GROUP/... keywords
// show up in the popup.
func classifyClause(pre []sqltok.Token, cursorOffset int) clauseKind {
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
			if isAfterTableRef(pre[i+1:], cursorOffset) {
				return clauseAfterTableRef
			}
			return clauseFromTarget
		case "INSERT", "UPDATE", "DELETE", "SET", "VALUES":
			return clauseGeneric
		}
	}
	return clauseGeneric
}

// isAfterTableRef reports whether the tokens following a FROM/JOIN
// keyword form a satisfied table reference -- i.e. the cursor has
// moved past the table name into "what comes next" territory (JOIN,
// WHERE, GROUP BY, ...). The ident being actively typed is
// recognized by its EndCol matching the cursor; it doesn't count
// as "committed". A trailing comma or AS keeps us in FROM-target
// mode (new table coming / alias slot).
func isAfterTableRef(tail []sqltok.Token, cursorOffset int) bool {
	if len(tail) > 0 {
		last := tail[len(tail)-1]
		if last.Kind == sqltok.Ident && last.EndCol == cursorOffset {
			tail = tail[:len(tail)-1]
		}
	}
	if len(tail) == 0 {
		return false
	}
	last := tail[len(tail)-1]
	if last.Kind == sqltok.Punct && last.Text == "," {
		return false
	}
	if last.Kind == sqltok.Keyword && strings.EqualFold(last.Text, "AS") {
		return false
	}
	for _, t := range tail {
		if t.Kind == sqltok.Ident {
			return true
		}
	}
	return false
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

		// Capture the body span so we can derive columns when
		// no explicit list was given.
		if i >= len(stmt) || !(stmt[i].Kind == sqltok.Punct && stmt[i].Text == "(") {
			break
		}
		bodyStart := i + 1
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
		bodyEnd := i - 1 // closing paren index
		if len(def.columns) == 0 && bodyStart < bodyEnd {
			def.columns = parseSelectListCols(stmt[bodyStart:bodyEnd])
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

// parseSelectListCols walks a SELECT ... FROM span at depth 0 of
// the given tokens and returns the column labels as they'd be
// exposed by that subquery. For each SELECT item:
//   - bare ident → that ident
//   - ident AS alias / ident alias → alias
//   - qualified.ident → ident (last segment)
//   - function(args) AS alias → alias
//   - anything else without an alias → "" (skipped)
//   - '*' → nil (bail; can't resolve without the schema)
//
// Used by subquery-FROM alias derivation and CTE body
// derivation. Caller passes depth-filtered tokens.
func parseSelectListCols(tokens []sqltok.Token) []string {
	depth0 := depthZeroTokens(tokens)
	// Find SELECT ... FROM span.
	start := -1
	end := len(depth0)
	for i, t := range depth0 {
		if t.Kind == sqltok.Keyword && strings.EqualFold(t.Text, "SELECT") {
			start = i + 1
			continue
		}
		if start >= 0 && t.Kind == sqltok.Keyword && strings.EqualFold(t.Text, "FROM") {
			end = i
			break
		}
	}
	if start < 0 || start >= end {
		return nil
	}
	// Walk comma-separated items. Parens at depth 0 were already
	// stripped; function calls show up as `ident ( ... )` which
	// depthZeroTokens collapsed to just `ident`. That's fine --
	// we only want the item's visible column name anyway.
	items := depth0[start:end]
	var groups [][]sqltok.Token
	cur := []sqltok.Token{}
	for _, t := range items {
		if t.Kind == sqltok.Punct && t.Text == "," {
			if len(cur) > 0 {
				groups = append(groups, cur)
				cur = []sqltok.Token{}
			}
			continue
		}
		cur = append(cur, t)
	}
	if len(cur) > 0 {
		groups = append(groups, cur)
	}

	var out []string
	for _, g := range groups {
		name := selectItemColName(g)
		if name == "*" {
			return nil // star: bail, can't resolve
		}
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

// selectItemColName returns the column name a SELECT-list item
// exposes. Handles bare ident, "ident AS alias", "ident alias",
// "schema.ident", and plain "*".
func selectItemColName(g []sqltok.Token) string {
	if len(g) == 0 {
		return ""
	}
	// Check for explicit AS alias at the tail.
	for i := len(g) - 2; i >= 0; i-- {
		if g[i].Kind == sqltok.Keyword && strings.EqualFold(g[i].Text, "AS") && g[i+1].Kind == sqltok.Ident {
			return g[i+1].Text
		}
	}
	// Check for implicit alias: last ident when previous ident
	// isn't a dot-qualifier. "ident1 ident2" → ident2.
	if len(g) >= 2 {
		last := g[len(g)-1]
		prev := g[len(g)-2]
		if last.Kind == sqltok.Ident && prev.Kind == sqltok.Ident {
			return last.Text
		}
	}
	// Single bare ident or dotted path: take the last ident.
	for i := len(g) - 1; i >= 0; i-- {
		if g[i].Kind == sqltok.Ident {
			return g[i].Text
		}
	}
	// Star?
	for _, t := range g {
		if t.Kind == sqltok.Operator && t.Text == "*" {
			return "*"
		}
	}
	return ""
}

// extractFromScope walks the raw stmt tokens with paren-depth
// tracking and emits a tableScope for every FROM/JOIN reference
// at depth 0. Handles named tables, schema-qualified names,
// aliases (with or without AS), comma-separated lists, and
// `(subquery) alias` derived refs whose columns come from the
// inner SELECT list.
func extractFromScope(stmt []sqltok.Token, cursorOffset int) []tableScope {
	var out []tableScope
	seen := map[string]struct{}{}
	addRef := func(ref tableScope) {
		if ref.name == "" {
			return
		}
		key := ref.schema + "\x00" + ref.name + "\x00" + ref.alias
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, ref)
	}

	depth := 0
	i := 0
	collecting := false
	for i < len(stmt) {
		t := stmt[i]
		// Paren depth tracking.
		if t.Kind == sqltok.Punct && t.Text == "(" {
			// If we're in collecting mode and this `(` sits at
			// depth 0, it's a subquery-FROM item. Find the
			// matching `)`, parse the inner SELECT list, then
			// read the alias.
			if collecting && depth == 0 {
				end := matchingParen(stmt, i)
				if end > i {
					inner := stmt[i+1 : end]
					cols := parseSelectListCols(inner)
					after := end + 1
					ref := tableScope{cols: cols}
					if after < len(stmt) &&
						stmt[after].Kind == sqltok.Keyword &&
						strings.EqualFold(stmt[after].Text, "AS") &&
						after+1 < len(stmt) &&
						stmt[after+1].Kind == sqltok.Ident {
						ref.name = stmt[after+1].Text
						ref.alias = stmt[after+1].Text
						after += 2
					} else if after < len(stmt) && stmt[after].Kind == sqltok.Ident {
						ref.name = stmt[after].Text
						ref.alias = stmt[after].Text
						after++
					}
					addRef(ref)
					i = after
					continue
				}
			}
			depth++
			i++
			continue
		}
		if t.Kind == sqltok.Punct && t.Text == ")" {
			if depth > 0 {
				depth--
			}
			i++
			continue
		}
		if depth > 0 {
			i++
			continue
		}

		// Depth 0: clause + collection state machine.
		if t.Kind == sqltok.Keyword {
			up := strings.ToUpper(t.Text)
			if up == "FROM" || up == "JOIN" {
				collecting = true
				i++
				continue
			}
			if collecting {
				if up == "WHERE" || up == "GROUP" || up == "ORDER" ||
					up == "HAVING" || up == "LIMIT" || up == "OFFSET" ||
					up == "UNION" || up == "INTERSECT" || up == "EXCEPT" ||
					up == "ON" || up == "USING" {
					collecting = false
					i++
					continue
				}
				if up == "INNER" || up == "LEFT" || up == "RIGHT" ||
					up == "FULL" || up == "CROSS" || up == "NATURAL" {
					// Stays in collecting; the following JOIN
					// keyword is fine.
					i++
					continue
				}
			}
			i++
			continue
		}

		if !collecting {
			i++
			continue
		}

		if t.Kind == sqltok.Punct && t.Text == "," {
			i++
			continue
		}

		if t.Kind == sqltok.Ident {
			// Collect the depth-0 run starting here for the
			// table-ref parser (it wants a contiguous ident/
			// punct sequence without paren noise).
			run := collectDepthZeroRun(stmt[i:])
			ref, consumed := parseTableRef(run, cursorOffset)
			if consumed == 0 {
				i++
				continue
			}
			addRef(ref)
			// Advance i by the matching number of raw tokens.
			i += consumed
			continue
		}

		i++
	}
	return out
}

// matchingParen returns the index of the `)` that closes the `(`
// at stmt[open]. Returns -1 on mismatch. Caller must ensure
// stmt[open] is the opening paren.
func matchingParen(stmt []sqltok.Token, open int) int {
	depth := 0
	for i := open; i < len(stmt); i++ {
		t := stmt[i]
		if t.Kind == sqltok.Punct && t.Text == "(" {
			depth++
		} else if t.Kind == sqltok.Punct && t.Text == ")" {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// collectDepthZeroRun returns the contiguous run of depth-0
// tokens starting at stmt[0] until a paren or end of slice.
// Used to hand a clean slice to parseTableRef.
func collectDepthZeroRun(stmt []sqltok.Token) []sqltok.Token {
	var out []sqltok.Token
	for _, t := range stmt {
		if t.Kind == sqltok.Punct && (t.Text == "(" || t.Text == ")") {
			break
		}
		out = append(out, t)
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
func parseTableRef(tokens []sqltok.Token, cursorOffset int) (tableScope, int) {
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
	// Optional alias: bare ident OR "AS" <ident>. Skip if the candidate
	// alias is the token being typed at the cursor: that's the user's
	// completion prefix, not a real alias, and emitting it as an alias
	// floats it to the top of the popup before it exists in the query.
	if consumed < len(tokens) {
		t := tokens[consumed]
		if t.Kind == sqltok.Keyword && strings.EqualFold(t.Text, "AS") && consumed+1 < len(tokens) && tokens[consumed+1].Kind == sqltok.Ident {
			aliasTok := tokens[consumed+1]
			if !isBeingTyped(aliasTok, cursorOffset) {
				ref.alias = aliasTok.Text
			}
			consumed += 2
		} else if t.Kind == sqltok.Ident {
			// Bare ident after a table ref is an alias
			// ("FROM a b" = "FROM a AS b" per SQL standard).
			// Consume it either way so the caller doesn't re-enter
			// parseTableRef on the being-typed prefix and register
			// it as a phantom table.
			if !isBeingTyped(t, cursorOffset) {
				ref.alias = t.Text
			}
			consumed++
		}
	}
	return ref, consumed
}

// isBeingTyped reports whether tok is the identifier currently under
// the cursor -- i.e. the user's completion prefix. Such tokens must
// not be promoted to aliases, because the query does not yet contain
// them as a committed reference.
func isBeingTyped(tok sqltok.Token, cursorOffset int) bool {
	return cursorOffset > tok.StartCol && cursorOffset <= tok.EndCol
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
	case clauseAfterTableRef:
		// Past a complete table ref: offer the keywords that can
		// legally follow (JOIN family, WHERE, GROUP/ORDER BY,
		// HAVING, UNION/INTERSECT/EXCEPT, LIMIT/OFFSET) plus
		// aliases so qualifier dots still work.
		items = append(items, afterTableKeywordCandidates()...)
		items = append(items, a.aliasCandidates(ctx)...)
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
		if !match {
			continue
		}
		// Derived subquery-FROM refs: use the pre-parsed
		// column list, skip the DB entirely.
		if len(t.cols) > 0 {
			out := make([]completionItem, 0, len(t.cols))
			for _, col := range t.cols {
				out = append(out, completionItem{text: col, kind: completeColumn})
			}
			return out
		}
		cols := a.fetchColumnsFor(t)
		if len(cols) == 0 {
			return nil
		}
		var items []completionItem
		for _, c := range cols {
			items = append(items, completionItem{
				text:     c.Name,
				kind:     completeColumn,
				typeHint: c.TypeName,
			})
		}
		return items
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

// afterTableKeywords is the set a user is likely to type next when
// the cursor sits past a complete table ref in a FROM/JOIN list.
var afterTableKeywords = []string{
	"WHERE",
	"JOIN", "INNER", "LEFT", "RIGHT", "FULL", "CROSS", "OUTER", "ON", "USING",
	"GROUP", "ORDER", "BY", "HAVING",
	"UNION", "INTERSECT", "EXCEPT", "ALL",
	"LIMIT", "OFFSET", "FETCH",
	"AS",
}

func afterTableKeywordCandidates() []completionItem {
	out := make([]completionItem, 0, len(afterTableKeywords))
	for _, kw := range afterTableKeywords {
		out = append(out, completionItem{text: kw, kind: completeKeyword})
	}
	return out
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
		// Derived subquery-FROM: pre-parsed cols win.
		if len(t.cols) > 0 {
			for _, col := range t.cols {
				out = append(out, completionItem{text: col, kind: completeColumn})
			}
			continue
		}
		if cteCols, ok := ctx.lookupCTEColumns(t.name); ok {
			for _, col := range cteCols {
				out = append(out, completionItem{text: col, kind: completeColumn})
			}
			continue
		}
		cols := a.fetchColumnsFor(t)
		for _, c := range cols {
			out = append(out, completionItem{
				text:     c.Name,
				kind:     completeColumn,
				typeHint: c.TypeName,
			})
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
