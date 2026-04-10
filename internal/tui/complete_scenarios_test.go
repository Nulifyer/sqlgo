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
	got := completionTextSet(e.complete.items)
	if !got["SELECT"] {
		t.Errorf("SELECT missing from no-connection popup: %+v", e.complete.items)
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
