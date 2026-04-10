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

// clauseKind classifies what SQL clause the cursor sits in. The
// analyzer walks the token stream backwards from the cursor until
// it finds the last clause-opener; the completion gather path
// uses the result to pick which candidate categories to surface.
type clauseKind int

const (
	// clauseGeneric means we couldn't identify a specific clause.
	// Usually fires at the very start of a statement or after a
	// semicolon; produces the everything-list (keywords + tables
	// + schemas).
	clauseGeneric clauseKind = iota
	// clauseSelectList is between SELECT and FROM. Columns from
	// the in-scope tables are the most useful suggestions here,
	// followed by "*" and the FROM keyword.
	clauseSelectList
	// clauseFromTarget is immediately after FROM or JOIN (waiting
	// for a table/view reference). Schemas + tables/views only;
	// columns and most keywords would be noise.
	clauseFromTarget
	// clauseWhereish covers WHERE / ON / HAVING / GROUP BY /
	// ORDER BY -- anywhere that column names are the primary
	// expected identifier.
	clauseWhereish
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

// tableScope is one entry in the FROM/JOIN table list, with the
// alias if one was given. Used by the column-completion path so
// `u.name|` can be resolved by looking up the table whose alias
// matches "u".
type tableScope struct {
	schema string // empty when the user wrote a bare table name
	name   string
	alias  string // empty when no explicit alias
}

// completionCtx is the result of analyzing the cursor position.
// prefix / qualifier / startCol are filled in by openCompletion
// after analyzeCursorContext returns (the analyzer doesn't know
// about the cursor's rune column in the current *line*, only its
// offset in the whole text).
type completionCtx struct {
	clause    clauseKind
	inScope   []tableScope
	qualifier string // leading "schema_or_alias." when the prefix is dotted
	prefix    string // identifier characters under the cursor (post-dot)
	startCol  int    // rune column in the current line where the prefix starts
	suppress  bool   // cursor is inside a string literal or comment
}

// analyzeCursorContext tokenizes text and classifies the cursor's
// position. cursorOffset is a rune offset into text (buffer.Text()
// joins lines with a single '\n' between them, so the offset is
// the same thing the editor tracks via row/col plus one '\n' per
// line).
//
// The analyzer works in three passes:
//
//  1. Suppress check: cursor inside a string literal or comment.
//  2. Clause classification: uses tokens strictly before the
//     cursor (and after the nearest preceding ';') to figure out
//     which SQL clause the cursor is in.
//  3. Scope extraction: walks the ENTIRE current statement -- not
//     just the pre-cursor half -- so `SELECT | FROM users` knows
//     about `users` even though the FROM keyword is typed after
//     the columns. Statement bounds are the nearest semicolons
//     around the cursor.
//
// The analyzer runs on every Ctrl+Space press, so it stays linear
// in the token count. For the buffer sizes sqlgo deals with
// (typically <200 lines), this is free.
func analyzeCursorContext(text string, cursorOffset int) completionCtx {
	ctx := completionCtx{clause: clauseGeneric}

	tokens := sqltok.TokenizeText(text)

	// Pass 1: detect if the cursor is inside a string or comment.
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

	// Filter to non-whitespace, non-comment tokens -- clauses and
	// scope both ignore those.
	var meaningful []sqltok.Token
	for _, t := range tokens {
		if t.Kind == sqltok.Whitespace || t.Kind == sqltok.Comment {
			continue
		}
		meaningful = append(meaningful, t)
	}

	// Find the current statement's bounds as indices into
	// `meaningful`: [stmtStart, stmtEnd). stmtStart is the token
	// right after the last ';' before the cursor (or 0), stmtEnd
	// is the token right before the first ';' at or after the
	// cursor (or len).
	stmtStart, stmtEnd := statementBounds(meaningful, cursorOffset)
	stmt := meaningful[stmtStart:stmtEnd]

	// Pass 2: classify the clause using tokens in the current
	// statement that come strictly before the cursor.
	var pre []sqltok.Token
	for _, t := range stmt {
		if t.EndCol > cursorOffset {
			break
		}
		pre = append(pre, t)
	}
	ctx.clause = classifyClause(pre)

	// Pass 3: extract the FROM/JOIN table list from the ENTIRE
	// current statement. Using the whole statement is what lets
	// SELECT-list completion know about tables that come after
	// the cursor in source order.
	ctx.inScope = extractFromScope(stmt)
	return ctx
}

// statementBounds returns [start, end) indices into meaningful
// that bracket the current statement at cursorOffset. A statement
// is a run of tokens between two semicolons (or the start/end of
// the token stream).
func statementBounds(meaningful []sqltok.Token, cursorOffset int) (int, int) {
	start := 0
	end := len(meaningful)
	for i, t := range meaningful {
		if t.Kind == sqltok.Punct && t.Text == ";" {
			if t.EndCol <= cursorOffset {
				// Semicolon strictly before the cursor -- the
				// current statement begins after it.
				start = i + 1
			} else if i < end {
				// First semicolon at or after the cursor -- the
				// current statement ends here (exclusive).
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

// terminatedString reports whether the token text's first char
// (the opening quote) appears again at the end unescaped. Very
// loose heuristic: sqltok's lexer already guarantees the token
// spans the whole string literal if there is a closing quote, so
// we just need to confirm the first and last runes match.
func terminatedString(s string) bool {
	if len(s) < 2 {
		return false
	}
	r := []rune(s)
	return r[len(r)-1] == r[0]
}

// terminatedComment reports whether a block comment token ends in
// */. Line comments (-- ...) are always "terminated" at EOL, which
// sqltok counts as inside the comment until the newline.
func terminatedComment(s string) bool {
	if strings.HasPrefix(s, "--") {
		return true
	}
	return strings.HasSuffix(s, "*/")
}

// classifyClause walks pre (tokens strictly before the cursor,
// already bounded to the current statement) backwards looking
// for the last clause keyword.
func classifyClause(pre []sqltok.Token) clauseKind {
	if len(pre) == 0 {
		return clauseGeneric
	}

	// If the most recent significant token is FROM or a JOIN
	// keyword, the cursor is in a from-target position.
	last := strings.ToUpper(pre[len(pre)-1].Text)
	if last == "FROM" || last == "JOIN" {
		return clauseFromTarget
	}

	// Otherwise, walk backwards and classify by the last
	// clause-opening keyword we encounter.
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
			// Require a following "BY" to consider us in that
			// clause; otherwise treat as generic.
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

// extractFromScope scans stmt (the full current statement with
// whitespace/comments already stripped) and returns every table
// reference that appears after a FROM or JOIN keyword. Aliases
// are captured when present, with or without the AS keyword.
//
// The parser is deliberately loose -- it accepts comma-separated
// FROM lists, dotted schema.table forms, and optional AS -- but
// doesn't try to handle subqueries, CTEs, or lateral joins.
// Those can always be added later; the point is to cover the
// common `FROM users u JOIN orders o ON ...` shape so column
// completion has something to look up.
//
// Walks the whole statement (not just the pre-cursor half) so
// SELECT-list completion can see tables that appear later in
// source order.
func extractFromScope(stmt []sqltok.Token) []tableScope {
	cur := stmt

	var out []tableScope
	seen := map[string]struct{}{}
	i := 0
	for i < len(cur) {
		t := cur[i]
		if t.Kind == sqltok.Keyword {
			up := strings.ToUpper(t.Text)
			if up == "FROM" || up == "JOIN" {
				// Consume table references until we hit a clause
				// boundary (WHERE, GROUP BY, ORDER BY, HAVING,
				// LIMIT, UNION) or a new JOIN/ON keyword.
				i++
				for i < len(cur) {
					// Stop on clause boundary.
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
							// End of this FROM/JOIN run; the
							// outer loop will pick up the JOIN
							// keyword on the next iteration.
							break
						}
					}
					// Skip commas between comma-separated tables.
					if cur[i].Kind == sqltok.Punct && cur[i].Text == "," {
						i++
						continue
					}
					// Expect an identifier, optionally "schema.name".
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
			// Heuristic: a bare identifier right after a table
			// ref is an alias. This overmatches on constructs
			// like "FROM a b, c" where "b" would be interpreted
			// as an alias of "a" -- which is exactly what SQL
			// standard semantics say, so it's correct.
			ref.alias = t.Text
			consumed++
		}
	}
	return ref, consumed
}

// columnCache is the app-level per-connection column store keyed
// by "schema\x00name" (with the synthetic "" schema for engines
// without schemas). Populated lazily on the first completion
// request for a given table.
type columnCache struct {
	mu      sync.Mutex
	entries map[string][]db.Column
}

func newColumnCache() *columnCache {
	return &columnCache{entries: map[string][]db.Column{}}
}

// get returns the cached columns for a table or nil.
func (c *columnCache) get(t tableScope) ([]db.Column, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cols, ok := c.entries[columnCacheKey(t)]
	return cols, ok
}

// put stores a column list for a table.
func (c *columnCache) put(t tableScope, cols []db.Column) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[columnCacheKey(t)] = cols
}

// clear wipes the cache. Called when the app disconnects so a
// reconnect to a different database doesn't surface stale columns.
func (c *columnCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = map[string][]db.Column{}
}

func columnCacheKey(t tableScope) string {
	return t.schema + "\x00" + t.name
}

// gatherCompletionsCtx is the context-aware replacement for the
// v1 gatherCompletions. Based on ctx.clause and ctx.qualifier it
// picks which of the following candidate buckets to include:
//
//   - FROM target:     schemas + tables + views
//   - SELECT list:     columns from in-scope tables + aliases +
//                      FROM keyword + "*"
//   - WHERE-ish:       columns + aliases + SQL keywords
//   - Generic:         keywords + schemas + tables + views
//
// A qualified prefix ("u." or "dbo.") narrows further:
//   - If the qualifier matches a table alias in scope, only that
//     table's columns are returned.
//   - If the qualifier matches a schema name, only tables under
//     that schema are returned.
//
// Column lookups go through the app's columnCache, which is
// populated on-demand via a short-deadline Columns() call on the
// live connection. Cache misses with no connection simply return
// no columns -- the popup stays useful (it'll still show
// keywords/tables) instead of failing.
func (a *app) gatherCompletionsCtx(ctx completionCtx) []completionItem {
	// Qualified prefix: either alias->columns or schema->tables.
	if ctx.qualifier != "" {
		if items := a.gatherQualified(ctx); items != nil {
			return items
		}
		// Fall through to the unqualified list when the
		// qualifier didn't match anything -- the user might be
		// typing "WHERE tbl.co|" before the alias is actually
		// in scope, or the connection has no live schema.
	}

	var items []completionItem
	switch ctx.clause {
	case clauseFromTarget:
		items = append(items, a.schemaCandidates()...)
		items = append(items, a.tableCandidates()...)
	case clauseSelectList:
		items = append(items, a.inScopeColumnCandidates(ctx)...)
		items = append(items, a.aliasCandidates(ctx)...)
		// Surface "*" as a keyword-ish candidate so typing "*"
		// still matches it (prefix of one literal char).
		items = append(items, completionItem{text: "*", kind: completeKeyword})
		items = append(items, completionItem{text: "FROM", kind: completeKeyword})
		items = append(items, completionItem{text: "DISTINCT", kind: completeKeyword})
		items = append(items, completionItem{text: "TOP", kind: completeKeyword})
	case clauseWhereish:
		items = append(items, a.inScopeColumnCandidates(ctx)...)
		items = append(items, a.aliasCandidates(ctx)...)
		items = append(items, a.keywordCandidates()...)
	default:
		// Generic: the old v1 list.
		items = append(items, a.keywordCandidates()...)
		items = append(items, a.schemaCandidates()...)
		items = append(items, a.tableCandidates()...)
	}
	return items
}

// gatherQualified handles "qualifier.prefix" cases. Returns nil
// if the qualifier didn't match anything -- the caller treats nil
// as "fall back to the unqualified list".
func (a *app) gatherQualified(ctx completionCtx) []completionItem {
	q := ctx.qualifier

	// First: alias or table name in the current FROM scope -> columns.
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

	// Second: schema name -> tables/views in that schema.
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

// keywordCandidates returns the full SQL keyword set as completion
// items. Split from gatherCompletionsCtx so each branch can pick
// whether to include them.
func (a *app) keywordCandidates() []completionItem {
	out := make([]completionItem, 0, 128)
	for _, kw := range sqltok.Keywords() {
		out = append(out, completionItem{text: kw, kind: completeKeyword})
	}
	return out
}

// schemaCandidates returns unique schema names from the loaded
// schema info. Empty when no connection is active.
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

// tableCandidates returns every table and view in the loaded
// schema as both a bare name and a "schema.name" qualified name
// so the user can complete either form.
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

// inScopeColumnCandidates fetches column lists for every table
// referenced in the current FROM clause and flattens them into
// completion items. Duplicates across tables are kept because
// the user may genuinely want to see which tables have the same
// column name (e.g. a shared "id" primary key).
func (a *app) inScopeColumnCandidates(ctx completionCtx) []completionItem {
	var out []completionItem
	for _, t := range ctx.inScope {
		cols := a.fetchColumnsFor(t)
		for _, c := range cols {
			out = append(out, completionItem{text: c.Name, kind: completeColumn})
		}
	}
	return out
}

// aliasCandidates surfaces the aliases and bare table names from
// the current FROM scope as completion candidates. Useful in the
// SELECT list and WHERE-ish clauses where typing the alias and
// pressing Tab to refine with columns is the natural flow.
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

// fetchColumnsFor returns the column list for a table, pulling
// from the per-connection cache when possible and falling back to
// a live Columns() call on the active connection. Cache misses
// with no connection produce a nil slice -- the caller treats
// that as "no columns, skip them" rather than an error.
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

	// Resolve the real schema from the loaded schema info. The
	// user may have typed the bare name, in which case we need
	// to find the schema from the explorer's table list so the
	// driver's Columns query has the right argument.
	ref := a.resolveTableRef(t)
	if ref.Name == "" {
		return nil
	}
	// Short deadline so a stale connection doesn't freeze the UI.
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	cols, err := a.conn.Columns(ctx, ref)
	if err != nil {
		// Cache the empty result so we don't re-hit a broken
		// table on every keystroke. The cache clears on
		// disconnect so a reconnect gives us a fresh try.
		a.columnCache.put(t, nil)
		return nil
	}
	a.columnCache.put(t, cols)
	return cols
}

// resolveTableRef converts a tableScope (which may or may not
// have a schema) into a db.TableRef with a schema attached, using
// the explorer's loaded schema info as the source of truth. If
// the table isn't found, returns a ref with the scope's own
// fields -- the driver will return an error and we cache it.
func (a *app) resolveTableRef(t tableScope) db.TableRef {
	m := a.mainLayerPtr()
	if m == nil || m.explorer == nil || m.explorer.info == nil {
		return db.TableRef{Schema: t.schema, Name: t.name}
	}
	info := m.explorer.info
	// Prefer exact (schema, name) match when both were given.
	if t.schema != "" {
		for _, tr := range info.Tables {
			if strings.EqualFold(tr.Schema, t.schema) && strings.EqualFold(tr.Name, t.name) {
				return tr
			}
		}
		return db.TableRef{Schema: t.schema, Name: t.name}
	}
	// Bare name: first match wins. Users with same-named tables
	// in multiple schemas can disambiguate with a qualifier in
	// the FROM clause; column completion will follow.
	for _, tr := range info.Tables {
		if strings.EqualFold(tr.Name, t.name) {
			return tr
		}
	}
	return db.TableRef{Name: t.name}
}
