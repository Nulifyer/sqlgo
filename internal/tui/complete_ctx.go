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

type statementKind int

const (
	stmtGeneric statementKind = iota
	stmtSelect
	stmtInsert
	stmtUpdate
	stmtDelete
)

// clauseKind classifies the SQL clause under the cursor.
type clauseKind int

const (
	clauseGeneric clauseKind = iota
	clauseSelectList
	clauseFromTarget    // after FROM or JOIN, cursor expects a table
	clauseAfterTableRef // FROM has a satisfied table ref; next up is JOIN/WHERE/GROUP/...
	clauseWhereish      // WHERE / ON / HAVING / GROUP BY / ORDER BY
	clauseGroupBy
	clauseOrderBy
	clauseInsertTarget
	clauseInsertColumns
	clauseValuesList
	clauseUpdateTarget
	clauseUpdateSet
	clauseDeleteTarget
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
	case clauseGroupBy:
		return "groupBy"
	case clauseOrderBy:
		return "orderBy"
	case clauseInsertTarget:
		return "insertTarget"
	case clauseInsertColumns:
		return "insertColumns"
	case clauseValuesList:
		return "values"
	case clauseUpdateTarget:
		return "updateTarget"
	case clauseUpdateSet:
		return "updateSet"
	case clauseDeleteTarget:
		return "deleteTarget"
	}
	return "generic"
}

// tableScope is one entry in the FROM/JOIN list. schema is empty
// on bare names; alias is empty when no alias was given. cols is
// populated for derived refs (subquery-FROM aliases) whose column
// list comes from the inner SELECT list, not the live schema.
type tableScope struct {
	catalog string // set from session.activeCatalog so cache is per-DB
	schema  string
	name    string
	alias   string
	cols    []string
}

type qualifierKind int

const (
	qualifierNone qualifierKind = iota
	qualifierUnknown
	qualifierCTE
	qualifierScope
	qualifierSchema
)

// cteDef is one CTE from a WITH clause. columns is populated only
// when spelled out as `WITH name (a, b) AS ...`.
type cteDef struct {
	name    string
	columns []string
}

// completionCtx carries the cursor analysis. prefix/qualifier/
// startCol are filled in by openCompletion after analyze returns.
type completionCtx struct {
	statement         statementKind
	clause            clauseKind
	inScope           []tableScope
	ctes              []cteDef
	target            tableScope
	catalog           string // "c" in "c.s.name" (three-part names, MSSQL/Sybase)
	qualifier         string // "x" in "x.name"
	qualifierKind     qualifierKind
	qualifierResolved bool
	awaitingColumns   bool
	prefix            string // identifier chars under cursor
	startCol          int    // rune col where prefix starts
	suppress          bool   // cursor inside string or comment
}

// analyzeCursorContext tokenizes text and classifies the cursor.
// cursorOffset is a rune offset into text. Walks the whole
// current statement (not just pre-cursor) so SELECT-list
// completion sees FROM tables typed later in source order.
func analyzeCursorContext(text string, cursorOffset int) completionCtx {
	ctx := completionCtx{statement: stmtGeneric, clause: clauseGeneric}

	tokens := sqltok.TokenizeText(text)

	// Suppress inside string or comment. Bracketed identifiers
	// (MSSQL/Sybase "[name]") lex as String but are identifier-typing
	// context for completion — never suppress inside them.
	for _, t := range tokens {
		if t.Kind != sqltok.String && t.Kind != sqltok.Comment {
			continue
		}
		if t.Kind == sqltok.String && isIdentifierLiteral(t.Text) {
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
	ctx.statement = statementOf(stmt)
	ctx.clause = classifyClause(stmt, pre, cursorOffset, ctx.statement)

	// Scope extraction walks depth-0 tokens so subquery FROMs
	// don't leak into the outer scope.
	ctx.inScope = extractFromScope(stmt, cursorOffset)
	ctx.ctes = extractCTEs(stmt)
	switch ctx.statement {
	case stmtInsert:
		ctx.target = extractInsertTarget(stmt, cursorOffset)
	case stmtUpdate:
		ctx.target = extractUpdateTarget(stmt, cursorOffset)
	case stmtDelete:
		ctx.target = extractDeleteTarget(stmt, cursorOffset)
	}
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
	if isIdentifierLiteral(s) {
		return true
	}
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

func isIdentifierLiteral(s string) bool {
	return strings.HasPrefix(s, "[") || strings.HasPrefix(s, "\"") || strings.HasPrefix(s, "`")
}

func isIdentifierToken(t sqltok.Token) bool {
	if t.Kind == sqltok.Ident {
		return true
	}
	return t.Kind == sqltok.String && isIdentifierLiteral(t.Text)
}

func tokenIdentifierText(t sqltok.Token) string {
	if t.Kind == sqltok.Ident {
		return t.Text
	}
	if !isIdentifierToken(t) {
		return ""
	}
	if strings.HasPrefix(t.Text, "[") && strings.HasSuffix(t.Text, "]") {
		return strings.TrimSuffix(strings.TrimPrefix(t.Text, "["), "]")
	}
	if (strings.HasPrefix(t.Text, "\"") && strings.HasSuffix(t.Text, "\"")) ||
		(strings.HasPrefix(t.Text, "`") && strings.HasSuffix(t.Text, "`")) {
		return t.Text[1 : len(t.Text)-1]
	}
	return t.Text
}

func statementOf(stmt []sqltok.Token) statementKind {
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
		if depth != 0 || t.Kind != sqltok.Keyword {
			continue
		}
		switch strings.ToUpper(t.Text) {
		case "SELECT":
			return stmtSelect
		case "INSERT":
			return stmtInsert
		case "UPDATE":
			return stmtUpdate
		case "DELETE":
			return stmtDelete
		}
	}
	return stmtGeneric
}

// classifyClause walks pre backwards for the last clause keyword.
// cursorOffset lets us tell a table-name-being-typed
// (`FROM prod|`) from a cursor past a complete table reference
// (`FROM products <ws> |` or `FROM products <ws> w|`) -- the latter
// reports clauseAfterTableRef so JOIN/WHERE/GROUP/... keywords
// show up in the popup.
func classifyClause(stmt, pre []sqltok.Token, cursorOffset int, stmtKind statementKind) clauseKind {
	switch stmtKind {
	case stmtInsert:
		return classifyInsertClause(stmt, pre, cursorOffset)
	case stmtUpdate:
		return classifyUpdateClause(pre)
	case stmtDelete:
		return classifyDeleteClause(pre)
	}
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
				if upper == "GROUP" {
					return clauseGroupBy
				}
				return clauseOrderBy
			}
		case "FROM", "JOIN", "INNER", "LEFT", "RIGHT", "FULL", "CROSS":
			if isAfterTableRef(pre[i+1:], cursorOffset) {
				return clauseAfterTableRef
			}
			return clauseFromTarget
		}
	}
	return clauseGeneric
}

func classifyInsertClause(stmt, pre []sqltok.Token, cursorOffset int) clauseKind {
	if insertColumnListContainsCursor(stmt, cursorOffset) {
		return clauseInsertColumns
	}
	if valuesListContainsCursor(stmt, cursorOffset) {
		return clauseValuesList
	}
	for i := len(pre) - 1; i >= 0; i-- {
		if pre[i].Kind != sqltok.Keyword {
			continue
		}
		switch strings.ToUpper(pre[i].Text) {
		case "VALUES":
			return clauseValuesList
		case "INTO":
			return clauseInsertTarget
		}
	}
	return clauseGeneric
}

func classifyUpdateClause(pre []sqltok.Token) clauseKind {
	for i := len(pre) - 1; i >= 0; i-- {
		if pre[i].Kind != sqltok.Keyword {
			continue
		}
		switch strings.ToUpper(pre[i].Text) {
		case "SET":
			return clauseUpdateSet
		case "UPDATE":
			return clauseUpdateTarget
		case "WHERE":
			return clauseWhereish
		}
	}
	return clauseGeneric
}

func classifyDeleteClause(pre []sqltok.Token) clauseKind {
	for i := len(pre) - 1; i >= 0; i-- {
		if pre[i].Kind != sqltok.Keyword {
			continue
		}
		switch strings.ToUpper(pre[i].Text) {
		case "FROM":
			return clauseDeleteTarget
		case "WHERE":
			return clauseWhereish
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
		if isIdentifierToken(last) && last.EndCol == cursorOffset {
			tail = tail[:len(tail)-1]
		}
	}
	if len(tail) == 0 {
		return false
	}
	last := tail[len(tail)-1]
	if last.Kind == sqltok.Punct && last.Text == "." {
		return false
	}
	if last.Kind == sqltok.Punct && last.Text == "," {
		return false
	}
	if last.Kind == sqltok.Keyword && strings.EqualFold(last.Text, "AS") {
		return false
	}
	for _, t := range tail {
		if isIdentifierToken(t) {
			return true
		}
	}
	return false
}

func insertColumnListContainsCursor(stmt []sqltok.Token, cursorOffset int) bool {
	depth := 0
	foundInto := false
	targetDone := false
	for i := 0; i < len(stmt); i++ {
		t := stmt[i]
		if t.Kind == sqltok.Punct && t.Text == "(" {
			if foundInto && targetDone && depth == 0 {
				end := matchingParen(stmt, i)
				if end > i && cursorOffset >= t.EndCol && cursorOffset <= stmt[end].StartCol {
					return true
				}
				if end > i {
					i = end
					continue
				}
			}
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
		if t.Kind == sqltok.Keyword {
			switch strings.ToUpper(t.Text) {
			case "INTO":
				foundInto = true
				targetDone = false
			case "VALUES":
				return false
			}
			continue
		}
		if foundInto && !targetDone && isIdentifierToken(t) {
			_, consumed := parseTableRef(stmt[i:], cursorOffset)
			if consumed > 0 {
				targetDone = true
				i += consumed - 1
			}
		}
	}
	return false
}

func valuesListContainsCursor(stmt []sqltok.Token, cursorOffset int) bool {
	depth := 0
	inValues := false
	for i := 0; i < len(stmt); i++ {
		t := stmt[i]
		if t.Kind == sqltok.Punct && t.Text == "(" {
			if inValues && depth == 0 {
				end := matchingParen(stmt, i)
				if end > i && cursorOffset >= t.EndCol && cursorOffset <= stmt[end].StartCol {
					return true
				}
				if end > i {
					i = end
					continue
				}
			}
			depth++
			continue
		}
		if t.Kind == sqltok.Punct && t.Text == ")" {
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth != 0 || t.Kind != sqltok.Keyword {
			continue
		}
		if strings.EqualFold(t.Text, "VALUES") {
			inValues = true
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
		if !isIdentifierToken(stmt[i]) {
			break
		}
		def := cteDef{name: tokenIdentifierText(stmt[i])}
		i++

		// Optional column list.
		if i < len(stmt) && stmt[i].Kind == sqltok.Punct && stmt[i].Text == "(" {
			i++
			for i < len(stmt) && !(stmt[i].Kind == sqltok.Punct && stmt[i].Text == ")") {
				if isIdentifierToken(stmt[i]) {
					def.columns = append(def.columns, tokenIdentifierText(stmt[i]))
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
		if g[i].Kind == sqltok.Keyword && strings.EqualFold(g[i].Text, "AS") && isIdentifierToken(g[i+1]) {
			return tokenIdentifierText(g[i+1])
		}
	}
	// Check for implicit alias: last ident when previous ident
	// isn't a dot-qualifier. "ident1 ident2" → ident2.
	if len(g) >= 2 {
		last := g[len(g)-1]
		prev := g[len(g)-2]
		if isIdentifierToken(last) && isIdentifierToken(prev) {
			return tokenIdentifierText(last)
		}
	}
	// Single bare ident or dotted path: take the last ident.
	for i := len(g) - 1; i >= 0; i-- {
		if isIdentifierToken(g[i]) {
			return tokenIdentifierText(g[i])
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
		key := ref.catalog + "\x00" + ref.schema + "\x00" + ref.name + "\x00" + ref.alias
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
						isIdentifierToken(stmt[after+1]) {
						ref.name = tokenIdentifierText(stmt[after+1])
						ref.alias = tokenIdentifierText(stmt[after+1])
						after += 2
					} else if after < len(stmt) && isIdentifierToken(stmt[after]) {
						ref.name = tokenIdentifierText(stmt[after])
						ref.alias = tokenIdentifierText(stmt[after])
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

		if isIdentifierToken(t) {
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
//	catalog.schema.tableName
//	tableName alias
//	tableName AS alias
//	schema.tableName alias
//	schema.tableName AS alias
func parseTableRef(tokens []sqltok.Token, cursorOffset int) (tableScope, int) {
	var ref tableScope
	if len(tokens) == 0 || !isIdentifierToken(tokens[0]) {
		return ref, 0
	}
	parts, consumed := parseIdentifierParts(tokens, 3)
	switch len(parts) {
	case 0:
		return ref, 0
	case 1:
		ref.name = parts[0]
	case 2:
		ref.schema = parts[0]
		ref.name = parts[1]
	default:
		ref.catalog = parts[0]
		ref.schema = parts[1]
		ref.name = parts[2]
	}
	for consumed < len(tokens) && tokens[consumed].Kind == sqltok.Whitespace {
		consumed++
	}
	if consumed < len(tokens) && tokens[consumed].Kind == sqltok.Punct && tokens[consumed].Text == "." {
		return tableScope{}, 0
	}
	// Optional alias: bare ident OR "AS" <ident>. Skip if the candidate
	// alias is the token being typed at the cursor: that's the user's
	// completion prefix, not a real alias, and emitting it as an alias
	// floats it to the top of the popup before it exists in the query.
	if consumed < len(tokens) {
		t := tokens[consumed]
		if t.Kind == sqltok.Keyword && strings.EqualFold(t.Text, "AS") && consumed+1 < len(tokens) && isIdentifierToken(tokens[consumed+1]) {
			aliasTok := tokens[consumed+1]
			if !isBeingTyped(aliasTok, cursorOffset) {
				ref.alias = tokenIdentifierText(aliasTok)
			}
			consumed += 2
		} else if isIdentifierToken(t) {
			// Bare ident after a table ref is an alias
			// ("FROM a b" = "FROM a AS b" per SQL standard).
			// Consume it either way so the caller doesn't re-enter
			// parseTableRef on the being-typed prefix and register
			// it as a phantom table.
			if !isBeingTyped(t, cursorOffset) {
				ref.alias = tokenIdentifierText(t)
			}
			consumed++
		}
	}
	return ref, consumed
}

func parseIdentifierParts(tokens []sqltok.Token, maxParts int) ([]string, int) {
	if len(tokens) == 0 || !isIdentifierToken(tokens[0]) {
		return nil, 0
	}
	parts := []string{tokenIdentifierText(tokens[0])}
	consumed := 1
	for consumed+1 < len(tokens) && len(parts) < maxParts {
		if tokens[consumed].Kind != sqltok.Punct || tokens[consumed].Text != "." || !isIdentifierToken(tokens[consumed+1]) {
			break
		}
		parts = append(parts, tokenIdentifierText(tokens[consumed+1]))
		consumed += 2
	}
	return parts, consumed
}

func extractInsertTarget(stmt []sqltok.Token, cursorOffset int) tableScope {
	return extractTargetAfterKeyword(stmt, cursorOffset, "INTO")
}

func extractUpdateTarget(stmt []sqltok.Token, cursorOffset int) tableScope {
	return extractTargetAfterKeyword(stmt, cursorOffset, "UPDATE")
}

func extractDeleteTarget(stmt []sqltok.Token, cursorOffset int) tableScope {
	return extractTargetAfterKeyword(stmt, cursorOffset, "FROM")
}

func extractTargetAfterKeyword(stmt []sqltok.Token, cursorOffset int, keyword string) tableScope {
	depth := 0
	for i := 0; i < len(stmt); i++ {
		t := stmt[i]
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
		if depth != 0 || t.Kind != sqltok.Keyword || !strings.EqualFold(t.Text, keyword) {
			continue
		}
		ref, _ := parseTableRef(stmt[i+1:], cursorOffset)
		return ref
	}
	return tableScope{}
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
// inflight tracks fetches currently running in a background
// goroutine so concurrent openCompletion calls for the same
// table dedupe onto a single DB round-trip. Entries expire
// after columnCacheTTL so ALTER TABLE eventually becomes
// visible without forcing a reconnect.
type columnCache struct {
	mu       sync.Mutex
	entries  map[string]columnCacheEntry
	inflight map[string]struct{}
}

type columnCacheEntry struct {
	cols      []db.Column
	fetchedAt time.Time
}

// columnCacheTTL is how long a fetched column list stays
// authoritative before the next miss triggers a refetch. Short
// enough that schema edits become visible in the same session,
// long enough that a burst of keystrokes doesn't hammer the DB.
const columnCacheTTL = 5 * time.Minute

func newColumnCache() *columnCache {
	return &columnCache{
		entries:  map[string]columnCacheEntry{},
		inflight: map[string]struct{}{},
	}
}

func (c *columnCache) get(t tableScope) ([]db.Column, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[columnCacheKey(t)]
	if !ok {
		return nil, false
	}
	if time.Since(e.fetchedAt) > columnCacheTTL {
		delete(c.entries, columnCacheKey(t))
		return nil, false
	}
	return e.cols, true
}

func (c *columnCache) put(t tableScope, cols []db.Column) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[columnCacheKey(t)] = columnCacheEntry{cols: cols, fetchedAt: time.Now()}
}

// tryMarkInflight returns true when the caller became the owner
// of the fetch for t (was not already in flight). Caller must
// call clearInflight when its goroutine exits.
func (c *columnCache) tryMarkInflight(t tableScope) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := columnCacheKey(t)
	if _, ok := c.inflight[k]; ok {
		return false
	}
	c.inflight[k] = struct{}{}
	return true
}

func (c *columnCache) clearInflight(t tableScope) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.inflight, columnCacheKey(t))
}

func (c *columnCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = map[string]columnCacheEntry{}
	c.inflight = map[string]struct{}{}
}

func columnCacheKey(t tableScope) string {
	return t.catalog + "\x00" + t.schema + "\x00" + t.name
}

type completionGatherResult struct {
	items           []completionItem
	awaitingColumns bool
	signature       string
}

type qualifiedResultStatus int

const (
	qualifiedUnresolved qualifiedResultStatus = iota
	qualifiedResolved
	qualifiedAwaiting
)

type qualifiedCompletionResult struct {
	status        qualifiedResultStatus
	items         []completionItem
	qualifierKind qualifierKind
}

// gatherCompletionsCtx picks candidate buckets by clause and
// qualifier:
//   - FROM target:  schemas + tables + views + CTE names
//   - SELECT list:  columns + aliases + functions + SELECT kw
//   - WHERE-ish:    columns + aliases + functions + keywords
//   - Generic:      keywords + functions + schemas + tables
//
// Qualified prefix narrows to CTE cols → alias cols → schema tables.
func (a *app) gatherCompletionsCtx(ctx completionCtx) completionGatherResult {
	result := completionGatherResult{signature: ctx.signature()}
	if ctx.qualifier != "" {
		q := a.gatherQualified(ctx)
		ctx.qualifierKind = q.qualifierKind
		ctx.qualifierResolved = q.status != qualifiedUnresolved
		result.signature = ctx.signature()
		switch q.status {
		case qualifiedResolved:
			result.items = q.items
			return result
		case qualifiedAwaiting:
			result.awaitingColumns = true
			return result
		}
		// Fall through when qualifier truly unknown.
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
		items = append(items, afterTableKeywordCandidates(a)...)
		items = append(items, a.aliasCandidates(ctx)...)
	case clauseSelectList:
		items = append(items, a.inScopeColumnCandidates(ctx)...)
		items = append(items, a.aliasCandidates(ctx)...)
		items = append(items, a.functionCandidates()...)
		items = append(items, a.keywordItems("FROM", "DISTINCT", "TOP", "*")...)
	case clauseWhereish, clauseGroupBy, clauseOrderBy:
		items = append(items, a.inScopeColumnCandidates(ctx)...)
		items = append(items, a.aliasCandidates(ctx)...)
		items = append(items, a.functionCandidates()...)
		items = append(items, a.keywordCandidates()...)
	case clauseInsertTarget, clauseDeleteTarget, clauseUpdateTarget:
		items = append(items, a.schemaCandidates()...)
		items = append(items, a.tableCandidates()...)
	case clauseInsertColumns:
		items = append(items, a.targetColumnCandidates(ctx)...)
	case clauseValuesList:
		items = append(items, a.targetColumnCandidates(ctx)...)
		items = append(items, a.functionCandidates()...)
		items = append(items, a.keywordItems("DEFAULT", "NULL")...)
	case clauseUpdateSet:
		items = append(items, a.targetColumnCandidates(ctx)...)
		items = append(items, a.inScopeColumnCandidates(ctx)...)
		items = append(items, a.aliasCandidates(ctx)...)
		items = append(items, a.functionCandidates()...)
		items = append(items, a.keywordItems("DEFAULT", "NULL")...)
	default:
		items = append(items, a.keywordCandidates()...)
		items = append(items, a.functionCandidates()...)
		items = append(items, a.schemaCandidates()...)
		items = append(items, a.tableCandidates()...)
	}
	result.items = items
	return result
}

// gatherQualified handles "q.prefix". Returns nil on no match so
// the caller falls back to the unqualified list. Order:
//  1. CTE name → declared columns (CTEs shadow real tables).
//  2. Alias/table in FROM scope → columns.
//  3. Schema name → tables/views.
func (a *app) gatherQualified(ctx completionCtx) qualifiedCompletionResult {
	q := ctx.qualifier

	for _, c := range ctx.ctes {
		if !strings.EqualFold(c.name, q) {
			continue
		}
		if len(c.columns) == 0 {
			// CTE with no declared cols: return empty (not nil)
			// so we don't fall through to the underlying table.
			return qualifiedCompletionResult{status: qualifiedResolved, qualifierKind: qualifierCTE}
		}
		out := make([]completionItem, 0, len(c.columns))
		for _, col := range c.columns {
			out = append(out, completionItem{text: col, kind: completeColumn})
		}
		return qualifiedCompletionResult{status: qualifiedResolved, items: out, qualifierKind: qualifierCTE}
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
			return qualifiedCompletionResult{status: qualifiedResolved, items: out, qualifierKind: qualifierScope}
		}
		cols, pending := a.fetchColumnsFor(t, ctx.signature())
		if pending {
			return qualifiedCompletionResult{status: qualifiedAwaiting, qualifierKind: qualifierScope}
		}
		if len(cols) == 0 {
			return qualifiedCompletionResult{status: qualifiedResolved, qualifierKind: qualifierScope}
		}
		var items []completionItem
		for _, c := range cols {
			items = append(items, completionItem{
				text:     c.Name,
				kind:     completeColumn,
				typeHint: c.TypeName,
			})
		}
		return qualifiedCompletionResult{status: qualifiedResolved, items: items, qualifierKind: qualifierScope}
	}

	tables := a.completionTablesFor(ctx.catalog)
	if len(tables) == 0 {
		return qualifiedCompletionResult{}
	}
	var items []completionItem
	for _, t := range tables {
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
		return qualifiedCompletionResult{}
	}
	return qualifiedCompletionResult{status: qualifiedResolved, items: items, qualifierKind: qualifierSchema}
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

func afterTableKeywordCandidates(a *app) []completionItem {
	out := make([]completionItem, 0, len(afterTableKeywords))
	allowed := a.keywordCandidateSet()
	for _, kw := range afterTableKeywords {
		if kw != "BY" && kw != "AS" && !allowed[kw] {
			continue
		}
		out = append(out, completionItem{text: kw, kind: completeKeyword})
	}
	return out
}

func (a *app) keywordCandidates() []completionItem {
	// Use the connected engine's dialect overlay so we don't suggest
	// TOP on Postgres or RETURNING on MSSQL. Disconnected editors fall
	// back to the full cross-engine set.
	var kws []string
	if a.conn != nil {
		if d := a.conn.Capabilities().Dialect; d != 0 {
			kws = sqltok.KeywordsFor(d)
		}
	}
	if kws == nil {
		kws = sqltok.Keywords()
	}
	out := make([]completionItem, 0, len(kws))
	for _, kw := range kws {
		out = append(out, completionItem{text: kw, kind: completeKeyword})
	}
	return out
}

func (a *app) keywordCandidateSet() map[string]bool {
	out := map[string]bool{}
	for _, kw := range a.keywordCandidates() {
		out[strings.ToUpper(kw.text)] = true
	}
	return out
}

func (a *app) keywordItems(words ...string) []completionItem {
	allowed := a.keywordCandidateSet()
	var out []completionItem
	for _, word := range words {
		if word == "*" {
			out = append(out, completionItem{text: word, kind: completeKeyword})
			continue
		}
		if !allowed[strings.ToUpper(word)] {
			continue
		}
		out = append(out, completionItem{text: word, kind: completeKeyword})
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
	tables := a.completionTables()
	if len(tables) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	var out []completionItem
	for _, t := range tables {
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
	tables := a.completionTables()
	if len(tables) == 0 {
		return nil
	}
	var out []completionItem
	for _, t := range tables {
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
		if t.Catalog != "" && t.Schema != "" {
			out = append(out, completionItem{
				text: t.Catalog + "." + t.Schema + "." + t.Name,
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
	for _, t := range ctx.allScopeTables() {
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
		cols, pending := a.fetchColumnsFor(t, ctx.signature())
		if pending {
			ctx.awaitingColumns = true
			continue
		}
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
	for _, t := range ctx.allScopeTables() {
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

func (a *app) targetColumnCandidates(ctx completionCtx) []completionItem {
	if ctx.target.name == "" {
		return nil
	}
	if cteCols, ok := ctx.lookupCTEColumns(ctx.target.name); ok {
		var out []completionItem
		for _, col := range cteCols {
			out = append(out, completionItem{text: col, kind: completeColumn})
		}
		return out
	}
	if len(ctx.target.cols) > 0 {
		var out []completionItem
		for _, col := range ctx.target.cols {
			out = append(out, completionItem{text: col, kind: completeColumn})
		}
		return out
	}
	cols, _ := a.fetchColumnsFor(ctx.target, ctx.signature())
	var out []completionItem
	for _, c := range cols {
		out = append(out, completionItem{
			text:     c.Name,
			kind:     completeColumn,
			typeHint: c.TypeName,
		})
	}
	return out
}

// fetchColumnsFor returns a table's columns from the cache. On a
// miss it spawns a background goroutine to dial the live connection
// (1.5s deadline) and returns nil immediately so the main loop never
// blocks on a slow driver. The async callback populates the cache
// and re-runs the active editor's completion so the popup refreshes
// once results arrive. Concurrent misses for the same table dedupe
// via the cache's inflight set. Errors cache as empty so broken
// tables don't re-hit every keystroke.
func (a *app) fetchColumnsFor(t tableScope, refreshSig string) ([]db.Column, bool) {
	if a == nil {
		return nil, false
	}
	if a.columnCache == nil {
		a.columnCache = newColumnCache()
	}
	if t.catalog == "" {
		t.catalog = a.completionCatalog()
	}
	if cols, ok := a.columnCache.get(t); ok {
		return cols, false
	}
	if a.conn == nil {
		return nil, false
	}
	ref := a.resolveTableRef(t)
	if ref.Name == "" {
		return nil, false
	}
	// Tests construct *app without an asyncCh, and for them the async
	// path would deadlock on the first send. Fall back to a synchronous
	// fetch so unit tests keep exercising the full cache path.
	if a.asyncCh == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		defer cancel()
		var (
			cols []db.Column
			err  error
		)
		if t.catalog != "" {
			if colr, ok := a.conn.(db.DatabaseColumner); ok {
				cols, err = colr.ColumnsIn(ctx, t.catalog, ref)
			} else {
				cols, err = a.conn.Columns(ctx, ref)
			}
		} else {
			cols, err = a.conn.Columns(ctx, ref)
		}
		if err != nil {
			a.columnCache.put(t, nil)
			return nil, false
		}
		a.columnCache.put(t, cols)
		return cols, false
	}
	if !a.columnCache.tryMarkInflight(t) {
		return nil, true
	}
	conn := a.conn
	cache := a.columnCache
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		defer cancel()
		var (
			cols []db.Column
			err  error
		)
		// Route through ColumnsIn when the tab has a pinned catalog so
		// the driver's session-scoped columns query (e.g. MSSQL's
		// INFORMATION_SCHEMA.COLUMNS) runs against the right DB instead
		// of whatever the pool's next conn lands in.
		if t.catalog != "" {
			if colr, ok := conn.(db.DatabaseColumner); ok {
				cols, err = colr.ColumnsIn(ctx, t.catalog, ref)
			} else {
				cols, err = conn.Columns(ctx, ref)
			}
		} else {
			cols, err = conn.Columns(ctx, ref)
		}
		a.asyncCh <- func(a *app) {
			cache.clearInflight(t)
			if a.conn != conn {
				return
			}
			if err != nil {
				cache.put(t, nil)
				return
			}
			cache.put(t, cols)
			// Refresh the active editor's completion popup so the
			// newly-arrived columns become visible without requiring
			// another keystroke.
			if m := a.mainLayerPtr(); m != nil && m.session != nil && m.focus == FocusQuery {
				if e := m.session.editor; e != nil && e.complete != nil &&
					e.complete.ctxSig == refreshSig &&
					completionSignatureForEditor(e, a) == refreshSig {
					e.openCompletion(a)
				}
			}
		}
	}()
	return nil, true
}

// resolveTableRef attaches a schema to a bare tableScope using
// the explorer's loaded schema info for the active catalog.
func (a *app) resolveTableRef(t tableScope) db.TableRef {
	tables := a.completionTablesFor(t.catalog)
	if len(tables) == 0 {
		return db.TableRef{Catalog: t.catalog, Schema: t.schema, Name: t.name}
	}
	if t.schema != "" {
		for _, tr := range tables {
			if strings.EqualFold(tr.Catalog, t.catalog) && strings.EqualFold(tr.Schema, t.schema) && strings.EqualFold(tr.Name, t.name) {
				return tr
			}
		}
		return db.TableRef{Catalog: t.catalog, Schema: t.schema, Name: t.name}
	}
	for _, tr := range tables {
		if strings.EqualFold(tr.Catalog, t.catalog) && strings.EqualFold(tr.Name, t.name) {
			return tr
		}
	}
	return db.TableRef{Catalog: t.catalog, Name: t.name}
}

func (ctx completionCtx) allScopeTables() []tableScope {
	if ctx.target.name == "" {
		return ctx.inScope
	}
	for _, t := range ctx.inScope {
		if sameTableScope(t, ctx.target) {
			return ctx.inScope
		}
	}
	out := make([]tableScope, 0, len(ctx.inScope)+1)
	out = append(out, ctx.target)
	out = append(out, ctx.inScope...)
	return out
}

func sameTableScope(a, b tableScope) bool {
	return strings.EqualFold(a.catalog, b.catalog) &&
		strings.EqualFold(a.schema, b.schema) &&
		strings.EqualFold(a.name, b.name) &&
		strings.EqualFold(a.alias, b.alias)
}

func (ctx completionCtx) signature() string {
	return ctx.clause.String() + "\x00" +
		strings.ToLower(ctx.qualifier) + "\x00" +
		strings.ToLower(ctx.catalog) + "\x00" +
		strings.ToLower(ctx.prefix)
}

func completionSignatureForEditor(e *editor, a *app) string {
	if e == nil {
		return ""
	}
	row, col := e.buf.Cursor()
	line := e.buf.Line(row)
	word, startCol := wordBeforeCursor(line, col)
	qualifier, catalog := qualifiersBeforeCursor(line, startCol)
	ctx := analyzeCursorContext(e.buf.Text(), runeOffsetOf(e.buf, row, col))
	ctx.qualifier = qualifier
	ctx.catalog = catalog
	ctx.prefix = word
	ctx.startCol = startCol
	return ctx.signature()
}

// completionCatalog returns the active-tab catalog used to scope
// autocomplete. Empty when no tab/session or no per-tab override; in
// that case lookups fall through to the single-DB explorer.info path.
func (a *app) completionCatalog() string {
	m := a.mainLayerPtr()
	if m == nil || m.session == nil {
		return ""
	}
	return m.session.activeCatalog
}

// completionTablesFor returns the table list for a specific catalog
// when non-empty (three-part "cat.schema.name" completions); falls
// back to completionTables() when empty.
func (a *app) completionTablesFor(cat string) []db.TableRef {
	if cat == "" {
		return a.completionTables()
	}
	m := a.mainLayerPtr()
	if m == nil || m.explorer == nil {
		return nil
	}
	e := m.explorer
	if e.dbMode {
		info := e.dbSchemas[cat]
		if info == nil {
			return nil
		}
		return info.Tables
	}
	// Single-DB mode: only honor the catalog qualifier if it names
	// the current DB; otherwise no suggestions.
	if e.info == nil {
		return nil
	}
	if active := a.completionCatalog(); active != "" && !strings.EqualFold(active, cat) {
		return nil
	}
	return e.info.Tables
}

// completionTables returns the table list to feed autocomplete. In
// dbMode (SupportsCrossDatabase + blank login DB) it pulls from the
// per-catalog schema map keyed by the tab's activeCatalog; otherwise
// it returns the single-DB explorer.info.Tables.
func (a *app) completionTables() []db.TableRef {
	m := a.mainLayerPtr()
	if m == nil || m.explorer == nil {
		return nil
	}
	e := m.explorer
	if e.dbMode {
		cat := a.completionCatalog()
		if cat == "" {
			return nil
		}
		info := e.dbSchemas[cat]
		if info == nil {
			return nil
		}
		return info.Tables
	}
	if e.info == nil {
		return nil
	}
	return e.info.Tables
}
