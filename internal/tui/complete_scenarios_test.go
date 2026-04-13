package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/db"
	// Register the sqlite driver so tests can open a real in-memory
	// connection. Using a live driver keeps the column-cache path
	// exercised end-to-end instead of stubbed out.
	_ "github.com/Nulifyer/sqlgo/internal/db/sqlite"
)

// setupAppWithSchema builds an *app backed by an in-memory sqlite
// connection, creates the given tables, and primes the explorer's
// schema info so gatherCompletionsCtx has something to read. The
// returned cleanup closes the connection; tests should defer it.
//
// This is the fixture that makes scenario tests possible without
// touching a real database server. sqlite is in-process and
// comes up in milliseconds, so these tests stay fast.
func setupAppWithSchema(t *testing.T, createSQL ...string) (*app, func()) {
	t.Helper()
	a := &app{}
	a.layers = []Layer{newMainLayer()}
	a.term = &terminal{width: 120, height: 40}
	a.columnCache = newColumnCache()

	d, err := db.Get("sqlite")
	if err != nil {
		t.Fatalf("db.Get sqlite: %v", err)
	}
	conn, err := d.Open(context.Background(), db.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	a.conn = conn

	for _, stmt := range createSQL {
		if err := conn.Exec(context.Background(), stmt); err != nil {
			_ = conn.Close()
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}

	info, err := conn.Schema(context.Background())
	if err != nil {
		_ = conn.Close()
		t.Fatalf("schema: %v", err)
	}
	a.mainLayerPtr().explorer.SetSchema(info, db.SchemaDepthFlat)

	return a, func() { _ = conn.Close() }
}

// typeInto sets the editor's buffer to text and positions the
// cursor at the first occurrence of the marker rune `|`, which
// is then removed. "SELECT | FROM users" → buffer "SELECT  FROM
// users", cursor at col 7 on row 0. If the marker isn't present
// the cursor lands at the end of text.
func typeInto(e *editor, text string) {
	e.buf.Clear()
	row, col := 0, 0
	found := false
	for i, line := range strings.Split(text, "\n") {
		if i > 0 {
			e.buf.InsertNewline()
		}
		for _, r := range line {
			if r == '|' && !found {
				row = i
				col = len(e.buf.Line(i))
				found = true
				continue
			}
			e.buf.Insert(r)
		}
	}
	if !found {
		return
	}
	// Drive the cursor to (row, col). The buffer already has the
	// text; we just need to move the cursor without inserting.
	curRow, _ := e.buf.Cursor()
	for curRow > row {
		e.buf.MoveUp()
		curRow, _ = e.buf.Cursor()
	}
	for curRow < row {
		e.buf.MoveDown()
		curRow, _ = e.buf.Cursor()
	}
	e.buf.MoveHome()
	for i := 0; i < col; i++ {
		e.buf.MoveRight()
	}
}

// completionTextSet extracts every candidate's text into a set
// for assertion-friendly membership checks.
func completionTextSet(items []completionItem) map[string]bool {
	out := make(map[string]bool, len(items))
	for _, it := range items {
		out[it.text] = true
	}
	return out
}

// completionKindSet returns the set of kinds present in items.
func completionKindSet(items []completionItem) map[completionKind]bool {
	out := map[completionKind]bool{}
	for _, it := range items {
		out[it.kind] = true
	}
	return out
}

// ---------------------------------------------------------------------------
// Context analyzer -- pure function, no DB required.
// ---------------------------------------------------------------------------

func TestAnalyzeCursorContextSelectList(t *testing.T) {
	t.Parallel()
	text := "SELECT "
	ctx := analyzeCursorContext(text, len([]rune(text)))
	if ctx.clause != clauseSelectList {
		t.Errorf("clause = %s, want select (%+v)", ctx.clause, ctx)
	}
}

func TestAnalyzeCursorContextAfterFrom(t *testing.T) {
	t.Parallel()
	text := "SELECT * FROM "
	ctx := analyzeCursorContext(text, len([]rune(text)))
	if ctx.clause != clauseFromTarget {
		t.Errorf("clause = %s, want from", ctx.clause)
	}
}

func TestAnalyzeCursorContextWhere(t *testing.T) {
	t.Parallel()
	text := "SELECT id FROM users WHERE "
	ctx := analyzeCursorContext(text, len([]rune(text)))
	if ctx.clause != clauseWhereish {
		t.Errorf("clause = %s, want where", ctx.clause)
	}
	if len(ctx.inScope) != 1 || ctx.inScope[0].name != "users" {
		t.Errorf("inScope = %+v, want [users]", ctx.inScope)
	}
}

func TestAnalyzeCursorContextJoinOn(t *testing.T) {
	t.Parallel()
	text := "SELECT * FROM users u JOIN orders o ON "
	ctx := analyzeCursorContext(text, len([]rune(text)))
	if ctx.clause != clauseWhereish {
		t.Errorf("clause = %s, want where (ON is where-ish)", ctx.clause)
	}
	wantTables := map[string]string{"users": "u", "orders": "o"}
	got := map[string]string{}
	for _, t := range ctx.inScope {
		got[t.name] = t.alias
	}
	for name, alias := range wantTables {
		if got[name] != alias {
			t.Errorf("table %q alias = %q, want %q (got: %+v)", name, got[name], alias, ctx.inScope)
		}
	}
}

func TestAnalyzeCursorContextGroupByOrderByHaving(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"group by":  "SELECT count(*) FROM users GROUP BY ",
		"order by":  "SELECT id FROM users ORDER BY ",
		"having":    "SELECT id FROM users GROUP BY id HAVING ",
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := analyzeCursorContext(text, len([]rune(text)))
			if ctx.clause != clauseWhereish {
				t.Errorf("clause = %s, want where", ctx.clause)
			}
			if len(ctx.inScope) == 0 || ctx.inScope[0].name != "users" {
				t.Errorf("inScope = %+v, want users", ctx.inScope)
			}
		})
	}
}

func TestAnalyzeCursorContextMultipleJoins(t *testing.T) {
	t.Parallel()
	text := "SELECT * FROM a INNER JOIN b ON a.id=b.a_id LEFT JOIN c ON b.id=c.b_id WHERE "
	ctx := analyzeCursorContext(text, len([]rune(text)))
	if ctx.clause != clauseWhereish {
		t.Errorf("clause = %s, want where", ctx.clause)
	}
	names := map[string]bool{}
	for _, tr := range ctx.inScope {
		names[tr.name] = true
	}
	for _, want := range []string{"a", "b", "c"} {
		if !names[want] {
			t.Errorf("missing table %q from inScope: %+v", want, ctx.inScope)
		}
	}
}

func TestAnalyzeCursorContextSchemaQualifiedTable(t *testing.T) {
	t.Parallel()
	text := "SELECT * FROM dbo.users u WHERE "
	ctx := analyzeCursorContext(text, len([]rune(text)))
	if len(ctx.inScope) != 1 {
		t.Fatalf("inScope = %+v, want 1 entry", ctx.inScope)
	}
	got := ctx.inScope[0]
	if got.schema != "dbo" || got.name != "users" || got.alias != "u" {
		t.Errorf("inScope[0] = %+v, want {dbo, users, u}", got)
	}
}

func TestAnalyzeCursorContextAliasWithAsKeyword(t *testing.T) {
	t.Parallel()
	text := "SELECT * FROM users AS u WHERE "
	ctx := analyzeCursorContext(text, len([]rune(text)))
	if len(ctx.inScope) != 1 || ctx.inScope[0].alias != "u" {
		t.Errorf("inScope = %+v, want users AS u", ctx.inScope)
	}
}

func TestAnalyzeCursorContextCommaSeparatedFrom(t *testing.T) {
	t.Parallel()
	text := "SELECT * FROM users u, orders o WHERE "
	ctx := analyzeCursorContext(text, len([]rune(text)))
	if len(ctx.inScope) != 2 {
		t.Fatalf("inScope = %+v, want 2 entries", ctx.inScope)
	}
}

func TestAnalyzeCursorContextInsideStringSuppresses(t *testing.T) {
	t.Parallel()
	// Cursor inside the string literal.
	text := "SELECT 'abc"
	ctx := analyzeCursorContext(text, 9) // right after 'ab
	if !ctx.suppress {
		t.Errorf("expected suppress inside unterminated string, got %+v", ctx)
	}
}

func TestAnalyzeCursorContextInsideLineCommentSuppresses(t *testing.T) {
	t.Parallel()
	text := "SELECT 1 -- here"
	ctx := analyzeCursorContext(text, 12)
	if !ctx.suppress {
		t.Errorf("expected suppress inside line comment, got %+v", ctx)
	}
}

func TestAnalyzeCursorContextAfterStringStillCompletes(t *testing.T) {
	t.Parallel()
	// Cursor RIGHT after a closed string. Should not suppress.
	text := "SELECT 'abc' "
	ctx := analyzeCursorContext(text, len([]rune(text)))
	if ctx.suppress {
		t.Errorf("completion after closed string should not be suppressed")
	}
}

func TestAnalyzeCursorContextSecondStatementResetsScope(t *testing.T) {
	t.Parallel()
	text := "SELECT * FROM users; SELECT "
	ctx := analyzeCursorContext(text, len([]rune(text)))
	if ctx.clause != clauseSelectList {
		t.Errorf("clause = %s, want select", ctx.clause)
	}
	if len(ctx.inScope) != 0 {
		t.Errorf("inScope = %+v, want empty (new statement)", ctx.inScope)
	}
}

// ---------------------------------------------------------------------------
// Scenarios that hit the live sqlite column lookup.
// ---------------------------------------------------------------------------

// TestScenarioColumnsAfterSelect is the core fix for the user's
// reported bug: "when I am working on the column names for
// instance it isn't showing columns". With a FROM clause in the
// same statement, Ctrl+Space in the select list must surface the
// table's column names.
func TestScenarioColumnsAfterSelect(t *testing.T) {
	a, done := setupAppWithSchema(t,
		`CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT, name TEXT, created_at TEXT)`,
	)
	defer done()

	e := a.mainLayerPtr().editor
	typeInto(e, "SELECT | FROM users")
	e.openCompletion(a)

	if e.complete == nil {
		t.Fatal("popup should open in SELECT list with an in-scope FROM")
	}
	got := completionTextSet(e.complete.items)
	for _, wantCol := range []string{"id", "email", "name", "created_at"} {
		if !got[wantCol] {
			t.Errorf("missing column %q from SELECT list popup: %+v", wantCol, e.complete.items)
		}
	}
}

// TestScenarioColumnsWithPartialPrefix verifies the prefix filter
// still narrows the column list under the new context path.
func TestScenarioColumnsWithPartialPrefix(t *testing.T) {
	a, done := setupAppWithSchema(t,
		`CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT, name TEXT, employee_id INTEGER)`,
	)
	defer done()

	e := a.mainLayerPtr().editor
	typeInto(e, "SELECT e| FROM users")
	e.openCompletion(a)
	if e.complete == nil {
		t.Fatal("popup should open with prefix 'e'")
	}
	got := completionTextSet(e.complete.items)
	if !got["email"] {
		t.Errorf("email missing from 'e' prefix: %+v", e.complete.items)
	}
	if !got["employee_id"] {
		t.Errorf("employee_id missing from 'e' prefix: %+v", e.complete.items)
	}
	if got["id"] {
		t.Errorf("id should be filtered out by 'e' prefix: %+v", e.complete.items)
	}
}

// TestScenarioOnClauseQualifiedAlias covers the exact case
// "SELECT * FROM users u JOIN orders o ON u.id = o.|" -- the
// cursor after the second qualifier in an ON clause. ON is a
// where-ish context and "o" is an alias in scope, so the popup
// must resolve to orders' columns, not users'.
func TestScenarioOnClauseQualifiedAlias(t *testing.T) {
	a, done := setupAppWithSchema(t,
		`CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT)`,
		`CREATE TABLE orders (id INTEGER PRIMARY KEY, user_id INTEGER, total REAL)`,
	)
	defer done()

	e := a.mainLayerPtr().editor
	typeInto(e, "SELECT * FROM users u JOIN orders o ON u.id = o.|")
	e.openCompletion(a)
	if e.complete == nil {
		t.Fatal("popup should open after 'o.' in ON clause")
	}
	got := completionTextSet(e.complete.items)
	for _, want := range []string{"id", "user_id", "total"} {
		if !got[want] {
			t.Errorf("missing orders column %q: %+v", want, e.complete.items)
		}
	}
	if got["email"] {
		t.Errorf("users.email leaked into o. scope: %+v", e.complete.items)
	}
}

// TestScenarioQualifiedAliasShowsOnlyThatTablesColumns covers
// "u." → only users columns, not orders columns, even when the
// statement references both tables.
func TestScenarioQualifiedAliasShowsOnlyThatTablesColumns(t *testing.T) {
	a, done := setupAppWithSchema(t,
		`CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT)`,
		`CREATE TABLE orders (id INTEGER PRIMARY KEY, user_id INTEGER, total REAL)`,
	)
	defer done()

	e := a.mainLayerPtr().editor
	typeInto(e, "SELECT u.| FROM users u JOIN orders o ON u.id = o.user_id")
	e.openCompletion(a)
	if e.complete == nil {
		t.Fatal("popup should open after 'u.'")
	}
	got := completionTextSet(e.complete.items)
	// users columns should be present.
	if !got["id"] || !got["email"] {
		t.Errorf("users columns missing after 'u.': %+v", e.complete.items)
	}
	// orders-only columns should NOT be present.
	if got["total"] || got["user_id"] {
		t.Errorf("orders columns leaked into 'u.' scope: %+v", e.complete.items)
	}
}

// TestScenarioQualifiedAliasInWhereClause is the same narrowing
// in a different clause context. WHERE u.| should behave like
// SELECT u.| -- both should resolve the alias and show users
// columns.
func TestScenarioQualifiedAliasInWhereClause(t *testing.T) {
	a, done := setupAppWithSchema(t,
		`CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT, active INTEGER)`,
		`CREATE TABLE orders (id INTEGER PRIMARY KEY, user_id INTEGER)`,
	)
	defer done()

	e := a.mainLayerPtr().editor
	typeInto(e, "SELECT * FROM users u JOIN orders o ON u.id = o.user_id WHERE u.|")
	e.openCompletion(a)
	if e.complete == nil {
		t.Fatal("popup should open in WHERE u.")
	}
	got := completionTextSet(e.complete.items)
	for _, want := range []string{"id", "email", "active"} {
		if !got[want] {
			t.Errorf("missing users column %q: %+v", want, e.complete.items)
		}
	}
	if got["user_id"] {
		t.Errorf("orders.user_id leaked into u. scope: %+v", e.complete.items)
	}
}

// TestScenarioFromTargetShowsTablesNotColumns covers the reverse:
// in FROM position, columns must NOT appear. Historically every
// candidate came back regardless of context; this test pins the
// new behavior.
func TestScenarioFromTargetShowsTablesNotColumns(t *testing.T) {
	a, done := setupAppWithSchema(t,
		`CREATE TABLE users (id INTEGER, email TEXT)`,
		`CREATE TABLE orders (id INTEGER)`,
	)
	defer done()

	e := a.mainLayerPtr().editor
	typeInto(e, "SELECT * FROM |")
	e.openCompletion(a)
	if e.complete == nil {
		t.Fatal("popup should open in FROM position")
	}
	got := completionTextSet(e.complete.items)
	if !got["users"] || !got["orders"] {
		t.Errorf("tables missing from FROM popup: %+v", e.complete.items)
	}
	// Columns should be suppressed.
	if got["email"] {
		t.Errorf("column 'email' leaked into FROM popup: %+v", e.complete.items)
	}
	// Kind check: no columns at all.
	for _, it := range e.complete.items {
		if it.kind == completeColumn {
			t.Errorf("unexpected completeColumn in FROM popup: %+v", it)
		}
	}
}

// TestScenarioWhereClauseShowsColumnsAndKeywords covers the
// WHERE-ish bucket: columns, aliases, and keywords should all be
// available (the user might want AND/OR or a column name).
func TestScenarioWhereClauseShowsColumnsAndKeywords(t *testing.T) {
	a, done := setupAppWithSchema(t,
		`CREATE TABLE users (id INTEGER, email TEXT)`,
	)
	defer done()

	e := a.mainLayerPtr().editor
	typeInto(e, "SELECT * FROM users WHERE |")
	e.openCompletion(a)
	if e.complete == nil {
		t.Fatal("popup should open in WHERE")
	}
	got := completionTextSet(e.complete.items)
	if !got["id"] || !got["email"] {
		t.Errorf("users columns missing from WHERE: %+v", e.complete.items)
	}
	if !got["AND"] {
		t.Errorf("AND keyword missing from WHERE popup: %+v", e.complete.items)
	}
}

// TestScenarioSuppressInsideStringLiteral: Ctrl+Space inside a
// string shouldn't open the popup at all. Regression guard for
// the v1 behavior where the popup opened everywhere.
func TestScenarioSuppressInsideStringLiteral(t *testing.T) {
	a, done := setupAppWithSchema(t,
		`CREATE TABLE users (id INTEGER)`,
	)
	defer done()

	e := a.mainLayerPtr().editor
	typeInto(e, "SELECT * FROM users WHERE email = 'abc|def'")
	e.openCompletion(a)
	if e.complete != nil {
		t.Errorf("popup should be suppressed inside a string literal, got %+v", e.complete.items)
	}
}

// TestScenarioSuppressInsideLineComment mirrors the string case
// for -- comments.
func TestScenarioSuppressInsideLineComment(t *testing.T) {
	a, done := setupAppWithSchema(t,
		`CREATE TABLE users (id INTEGER)`,
	)
	defer done()

	e := a.mainLayerPtr().editor
	typeInto(e, "SELECT 1 -- pick | a column")
	e.openCompletion(a)
	if e.complete != nil {
		t.Errorf("popup should be suppressed inside a line comment, got %+v", e.complete.items)
	}
}

// TestScenarioNoConnectionStillGivesKeywords: with no connection,
// the popup should still show keywords. This matches the user's
// expectation that Ctrl+Space does *something* useful before
// they've connected.
func TestScenarioNoConnectionStillGivesKeywords(t *testing.T) {
	t.Parallel()
	a := &app{}
	a.layers = []Layer{newMainLayer()}
	a.term = &terminal{width: 80, height: 24}

	e := a.mainLayerPtr().editor
	typeInto(e, "sel|")
	e.openCompletion(a)
	if e.complete == nil {
		t.Fatal("popup should open with keywords-only list")
	}
	// Lowercase prefix → lowercase keyword.
	got := completionTextSet(e.complete.items)
	if !got["select"] {
		t.Errorf("select missing from no-connection popup: %+v", e.complete.items)
	}
}

// TestScenarioColumnCacheAvoidsRepeatedDriverCalls pins the
// caching behavior: two Ctrl+Space presses on the same table
// should hit the driver once, not twice. We verify by checking
// the cache directly rather than counting driver calls, since
// sqlConn doesn't expose a call counter.
func TestScenarioColumnCacheAvoidsRepeatedDriverCalls(t *testing.T) {
	a, done := setupAppWithSchema(t,
		`CREATE TABLE users (id INTEGER, email TEXT)`,
	)
	defer done()

	e := a.mainLayerPtr().editor
	typeInto(e, "SELECT | FROM users")
	e.openCompletion(a)
	if e.complete == nil {
		t.Fatal("first popup should open")
	}

	// Cache should now contain a "main.users" entry.
	if _, ok := a.columnCache.get(tableScope{schema: "main", name: "users"}); !ok {
		// The editor might have passed bare-name scope; check
		// that key too.
		if _, ok := a.columnCache.get(tableScope{name: "users"}); !ok {
			t.Errorf("column cache miss for users after first popup")
		}
	}
}

// TestScenarioCTENameAvailableInOuterFrom covers CTE references:
// WITH x AS (...) SELECT ... FROM | should surface "x" as a
// completable table name, even though x isn't in the database.
func TestScenarioCTENameAvailableInOuterFrom(t *testing.T) {
	a, done := setupAppWithSchema(t,
		`CREATE TABLE users (id INTEGER, email TEXT)`,
	)
	defer done()

	e := a.mainLayerPtr().editor
	typeInto(e, "WITH active_users AS (SELECT * FROM users WHERE 1=1) SELECT * FROM |")
	e.openCompletion(a)
	if e.complete == nil {
		t.Fatal("popup should open in FROM after CTE")
	}
	got := completionTextSet(e.complete.items)
	if !got["active_users"] {
		t.Errorf("CTE name missing from FROM popup: %+v", e.complete.items)
	}
}

// TestScenarioCTEColumnsAvailableViaExplicitList: when the CTE
// declares its column list `WITH cte (col1, col2) AS (...)`, the
// analyzer should treat those names as the CTE's columns and
// surface them under a qualified prefix.
func TestScenarioCTEColumnsAvailableViaExplicitList(t *testing.T) {
	a, done := setupAppWithSchema(t,
		`CREATE TABLE users (id INTEGER, email TEXT)`,
	)
	defer done()

	e := a.mainLayerPtr().editor
	typeInto(e, "WITH au (uid, mail) AS (SELECT id, email FROM users) SELECT au.| FROM au")
	e.openCompletion(a)
	if e.complete == nil {
		t.Fatal("popup should open after 'au.'")
	}
	got := completionTextSet(e.complete.items)
	if !got["uid"] || !got["mail"] {
		t.Errorf("declared CTE columns missing: %+v", e.complete.items)
	}
	// Underlying table's columns should NOT be visible under the
	// CTE alias -- the CTE remapped them.
	if got["id"] || got["email"] {
		t.Errorf("underlying users columns leaked through CTE: %+v", e.complete.items)
	}
}

// TestScenarioMultipleCTEsBothRegistered covers a chained CTE
// sequence like "WITH a AS (...), b AS (SELECT ... FROM a) ...".
// Both names should appear as candidates in the outer FROM.
func TestScenarioMultipleCTEsBothRegistered(t *testing.T) {
	a, done := setupAppWithSchema(t,
		`CREATE TABLE users (id INTEGER)`,
	)
	defer done()

	e := a.mainLayerPtr().editor
	typeInto(e, "WITH a AS (SELECT * FROM users), b AS (SELECT * FROM a) SELECT * FROM |")
	e.openCompletion(a)
	if e.complete == nil {
		t.Fatal("popup should open")
	}
	got := completionTextSet(e.complete.items)
	if !got["a"] || !got["b"] {
		t.Errorf("both CTEs should be present: %+v", e.complete.items)
	}
}

// TestScenarioSubqueryFromAliasColumns: FROM (subquery) alias
// should derive columns from the subquery's SELECT list so
// `alias.col` completes.
func TestScenarioSubqueryFromAliasColumns(t *testing.T) {
	a, done := setupAppWithSchema(t,
		`CREATE TABLE users (id INTEGER, email TEXT)`,
	)
	defer done()

	e := a.mainLayerPtr().editor
	typeInto(e, "SELECT x.| FROM (SELECT id, email FROM users) x")
	e.openCompletion(a)
	if e.complete == nil {
		t.Fatal("popup should open after 'x.'")
	}
	got := completionTextSet(e.complete.items)
	if !got["id"] || !got["email"] {
		t.Errorf("derived subquery cols missing: %+v", e.complete.items)
	}
}

// TestScenarioSubqueryFromAliasWithAS covers "FROM (...) AS x"
// form (explicit AS keyword).
func TestScenarioSubqueryFromAliasWithAS(t *testing.T) {
	a, done := setupAppWithSchema(t,
		`CREATE TABLE users (id INTEGER, email TEXT)`,
	)
	defer done()

	e := a.mainLayerPtr().editor
	typeInto(e, "SELECT x.| FROM (SELECT id FROM users) AS x")
	e.openCompletion(a)
	if e.complete == nil {
		t.Fatal("popup should open")
	}
	got := completionTextSet(e.complete.items)
	if !got["id"] {
		t.Errorf("subquery col missing: %+v", e.complete.items)
	}
}

// TestScenarioSubqueryAliasColumnAlias derives the user's AS
// alias in the inner SELECT, not the original column name.
func TestScenarioSubqueryAliasColumnAlias(t *testing.T) {
	a, done := setupAppWithSchema(t,
		`CREATE TABLE users (id INTEGER, email TEXT)`,
	)
	defer done()

	e := a.mainLayerPtr().editor
	typeInto(e, "SELECT x.| FROM (SELECT id AS uid, email AS mail FROM users) x")
	e.openCompletion(a)
	if e.complete == nil {
		t.Fatal("popup should open")
	}
	got := completionTextSet(e.complete.items)
	if !got["uid"] || !got["mail"] {
		t.Errorf("column aliases missing: %+v", e.complete.items)
	}
	if got["id"] || got["email"] {
		t.Errorf("original column names leaked through alias: %+v", e.complete.items)
	}
}

// TestScenarioCTEBodyColumnDerivation: WITH x AS (SELECT id,
// email FROM users) has no explicit column list, so columns
// should derive from the body SELECT.
func TestScenarioCTEBodyColumnDerivation(t *testing.T) {
	a, done := setupAppWithSchema(t,
		`CREATE TABLE users (id INTEGER, email TEXT)`,
	)
	defer done()

	e := a.mainLayerPtr().editor
	typeInto(e, "WITH x AS (SELECT id, email FROM users) SELECT x.| FROM x")
	e.openCompletion(a)
	if e.complete == nil {
		t.Fatal("popup should open")
	}
	got := completionTextSet(e.complete.items)
	if !got["id"] || !got["email"] {
		t.Errorf("derived CTE cols missing: %+v", e.complete.items)
	}
}

// TestScenarioCTEExplicitListWinsOverBody: an explicit CTE
// column list overrides whatever the body SELECT exposes.
func TestScenarioCTEExplicitListWinsOverBody(t *testing.T) {
	a, done := setupAppWithSchema(t,
		`CREATE TABLE users (id INTEGER, email TEXT)`,
	)
	defer done()

	e := a.mainLayerPtr().editor
	typeInto(e, "WITH x (uid, mail) AS (SELECT id, email FROM users) SELECT x.| FROM x")
	e.openCompletion(a)
	if e.complete == nil {
		t.Fatal("popup should open")
	}
	got := completionTextSet(e.complete.items)
	if !got["uid"] || !got["mail"] {
		t.Errorf("explicit list missing: %+v", e.complete.items)
	}
	if got["id"] || got["email"] {
		t.Errorf("body cols should not leak: %+v", e.complete.items)
	}
}

// TestScenarioLiveRefineNarrowsAsYouType: typing an ident
// character with the popup open should insert the char AND
// re-filter against the new longer prefix.
func TestScenarioLiveRefineNarrowsAsYouType(t *testing.T) {
	a, done := setupAppWithSchema(t,
		`CREATE TABLE users (id INTEGER, email TEXT, employee_id INTEGER)`,
	)
	defer done()

	e := a.mainLayerPtr().editor
	typeInto(e, "SELECT | FROM users")
	e.openCompletion(a)
	if e.complete == nil {
		t.Fatal("popup should open")
	}
	// Type "em" one char at a time with the popup open.
	e.handleInsert(a, Key{Kind: KeyRune, Rune: 'e'})
	e.handleInsert(a, Key{Kind: KeyRune, Rune: 'm'})
	if e.complete == nil {
		t.Fatal("popup should still be open after live refine")
	}
	got := completionTextSet(e.complete.items)
	if !got["email"] {
		t.Errorf("email missing after live refine 'em': %+v", e.complete.items)
	}
	if got["id"] {
		t.Errorf("id should have been filtered out: %+v", e.complete.items)
	}
	// Prefix should have grown to "em".
	if e.complete.prefix != "em" {
		t.Errorf("prefix = %q, want em", e.complete.prefix)
	}
}

// TestScenarioLiveRefineBackspaceWidens: Backspace with popup
// open should delete a char and re-open against the shorter
// prefix.
func TestScenarioLiveRefineBackspaceWidens(t *testing.T) {
	a, done := setupAppWithSchema(t,
		`CREATE TABLE users (id INTEGER, email TEXT)`,
	)
	defer done()

	e := a.mainLayerPtr().editor
	typeInto(e, "SELECT emai| FROM users")
	e.openCompletion(a)
	if e.complete == nil {
		t.Fatal("popup should open")
	}
	e.handleInsert(a, Key{Kind: KeyBackspace})
	e.handleInsert(a, Key{Kind: KeyBackspace})
	if e.complete == nil {
		t.Fatal("popup should still be open after backspace")
	}
	if e.complete.prefix != "em" {
		t.Errorf("prefix = %q, want em", e.complete.prefix)
	}
	got := completionTextSet(e.complete.items)
	if !got["email"] {
		t.Errorf("email missing after backspace refine: %+v", e.complete.items)
	}
}

// TestScenarioLiveRefineNonIdentDismisses: typing a space
// should dismiss the popup and still insert the space.
func TestScenarioLiveRefineNonIdentDismisses(t *testing.T) {
	a, done := setupAppWithSchema(t,
		`CREATE TABLE users (id INTEGER)`,
	)
	defer done()

	e := a.mainLayerPtr().editor
	typeInto(e, "SELECT | FROM users")
	e.openCompletion(a)
	if e.complete == nil {
		t.Fatal("popup should open")
	}
	e.handleInsert(a, Key{Kind: KeyRune, Rune: ' '})
	if e.complete != nil {
		t.Errorf("popup should be dismissed after space: %+v", e.complete.items)
	}
}

// TestScenarioCasePreservationLowercasePrefix: lowercase prefix
// produces lowercase keywords/functions.
func TestScenarioCasePreservationLowercasePrefix(t *testing.T) {
	a, done := setupAppWithSchema(t,
		`CREATE TABLE users (id INTEGER)`,
	)
	defer done()

	e := a.mainLayerPtr().editor
	typeInto(e, "select * fr|")
	e.openCompletion(a)
	if e.complete == nil {
		t.Fatal("popup should open")
	}
	got := completionTextSet(e.complete.items)
	if !got["from"] {
		t.Errorf("lowercase 'from' missing: %+v", e.complete.items)
	}
	if got["FROM"] {
		t.Errorf("uppercase FROM should have been lowered: %+v", e.complete.items)
	}
}

// TestScenarioColumnTypeHintPopulated: column completion items
// carry the type string from db.Column.TypeName.
func TestScenarioColumnTypeHintPopulated(t *testing.T) {
	a, done := setupAppWithSchema(t,
		`CREATE TABLE widgets (id INTEGER, label TEXT, price REAL)`,
	)
	defer done()

	e := a.mainLayerPtr().editor
	typeInto(e, "SELECT | FROM widgets")
	e.openCompletion(a)
	if e.complete == nil {
		t.Fatal("popup should open")
	}
	byName := map[string]string{}
	for _, it := range e.complete.items {
		if it.kind == completeColumn {
			byName[it.text] = it.typeHint
		}
	}
	if byName["id"] == "" {
		t.Errorf("id should carry a type hint: %+v", e.complete.items)
	}
	if byName["label"] == "" {
		t.Errorf("label should carry a type hint: %+v", e.complete.items)
	}
}

// TestScenarioFunctionsAppearInSelect covers function
// completion: `SELECT SU| FROM users` should surface SUBSTRING
// and SUM, not just keywords.
func TestScenarioFunctionsAppearInSelect(t *testing.T) {
	a, done := setupAppWithSchema(t,
		`CREATE TABLE users (id INTEGER, price REAL)`,
	)
	defer done()

	e := a.mainLayerPtr().editor
	typeInto(e, "SELECT SU| FROM users")
	e.openCompletion(a)
	if e.complete == nil {
		t.Fatal("popup should open")
	}
	got := completionTextSet(e.complete.items)
	for _, want := range []string{"SUM", "SUBSTRING"} {
		if !got[want] {
			t.Errorf("function %q missing: %+v", want, e.complete.items)
		}
	}
}

// TestScenarioFunctionsAppearInWhere confirms functions also
// show up in WHERE-ish contexts.
func TestScenarioFunctionsAppearInWhere(t *testing.T) {
	a, done := setupAppWithSchema(t,
		`CREATE TABLE users (id INTEGER, name TEXT)`,
	)
	defer done()

	e := a.mainLayerPtr().editor
	typeInto(e, "SELECT * FROM users WHERE UP|")
	e.openCompletion(a)
	if e.complete == nil {
		t.Fatal("popup should open")
	}
	got := completionTextSet(e.complete.items)
	if !got["UPPER"] {
		t.Errorf("UPPER missing from WHERE popup: %+v", e.complete.items)
	}
}

// TestScenarioFunctionsNotInFromTarget: functions are not valid
// FROM targets, so they should not appear in the FROM popup.
func TestScenarioFunctionsNotInFromTarget(t *testing.T) {
	a, done := setupAppWithSchema(t,
		`CREATE TABLE users (id INTEGER)`,
	)
	defer done()

	e := a.mainLayerPtr().editor
	typeInto(e, "SELECT * FROM |")
	e.openCompletion(a)
	if e.complete == nil {
		t.Fatal("popup should open")
	}
	for _, it := range e.complete.items {
		if it.kind == completeFunction {
			t.Errorf("function %q leaked into FROM popup", it.text)
		}
	}
}

// TestScenarioFunctionKindHasMarker guards the display marker so
// a future refactor that reshuffles completionKind constants
// doesn't silently drop the function marker.
func TestScenarioFunctionKindHasMarker(t *testing.T) {
	t.Parallel()
	if got := completeFunction.marker(); got != "ƒ" {
		t.Errorf("completeFunction.marker() = %q, want %q", got, "ƒ")
	}
}

// TestScenarioInScopeWithoutPrefixShowsColumns is a subtle guard:
// Ctrl+Space with no word under the cursor (just a space) in
// SELECT should still surface columns, because the user may
// want to browse the full list. Previously filter_empty returned
// everything; let's verify that's still true under the new
// context-aware path.
func TestScenarioInScopeWithoutPrefixShowsColumns(t *testing.T) {
	a, done := setupAppWithSchema(t,
		`CREATE TABLE users (id INTEGER, email TEXT)`,
	)
	defer done()

	e := a.mainLayerPtr().editor
	typeInto(e, "SELECT | FROM users")
	e.openCompletion(a)
	if e.complete == nil {
		t.Fatal("popup should open with empty prefix")
	}
	got := completionKindSet(e.complete.items)
	if !got[completeColumn] {
		t.Errorf("no column kinds in empty-prefix SELECT popup: %+v", e.complete.items)
	}
}
