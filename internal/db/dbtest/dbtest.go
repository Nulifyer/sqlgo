//go:build integration

// Package dbtest holds shared helpers for the driver integration
// test suites. Behind the `integration` build tag so live-DB code
// stays out of the default `go test ./...` run.
package dbtest

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Nulifyer/sqlgo/internal/db"
)

// ExerciseDriver runs Ping → Exec → Schema → Columns → Query
// against a live connection. tableSchema is the driver's native
// schema name (public / dbo / dbname / main). createTable is a
// dialect-specific CREATE for a 2-column (id, label) fixture.
func ExerciseDriver(t *testing.T, conn db.Conn, tableSchema, createTable, tableName string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := conn.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}

	// Drop-then-create; dialects disagree on IF NOT EXISTS.
	_ = conn.Exec(ctx, "DROP TABLE "+tableName)
	if err := conn.Exec(ctx, createTable); err != nil {
		t.Fatalf("create table: %v", err)
	}
	defer func() {
		_ = conn.Exec(context.Background(), "DROP TABLE "+tableName)
	}()

	if err := conn.Exec(ctx, "INSERT INTO "+tableName+" (id, label) VALUES (1, 'one')"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := conn.Exec(ctx, "INSERT INTO "+tableName+" (id, label) VALUES (2, 'two')"); err != nil {
		t.Fatalf("insert: %v", err)
	}

	info, err := conn.Schema(ctx)
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	// Engines like Oracle/Firebird fold unquoted identifiers to
	// uppercase, so compare case-insensitively.
	found := false
	for _, tr := range info.Tables {
		if strings.EqualFold(tr.Name, tableName) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("table %q not in Schema() result", tableName)
	}

	cols, err := conn.Columns(ctx, db.TableRef{Schema: tableSchema, Name: tableName})
	if err != nil {
		t.Fatalf("columns: %v", err)
	}
	if len(cols) != 2 {
		t.Fatalf("len(cols) = %d, want 2 (%+v)", len(cols), cols)
	}
	if !strings.EqualFold(cols[0].Name, "id") || !strings.EqualFold(cols[1].Name, "label") {
		t.Errorf("cols = %+v, want [id, label] (case-insensitive)", cols)
	}

	rows, err := conn.Query(ctx, "SELECT id, label FROM "+tableName+" ORDER BY id")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	var gotIDs []int64
	var gotLabels []string
	for rows.Next() {
		row, err := rows.Scan()
		if err != nil {
			t.Fatalf("scan: %v", err)
		}
		// Drivers disagree on int width; accept any.
		switch v := row[0].(type) {
		case int64:
			gotIDs = append(gotIDs, v)
		case int32:
			gotIDs = append(gotIDs, int64(v))
		case int:
			gotIDs = append(gotIDs, int64(v))
		case string:
			n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			if err != nil {
				t.Fatalf("row[0] = string %q, parse: %v", v, err)
			}
			gotIDs = append(gotIDs, n)
		default:
			t.Fatalf("row[0] = %T (%v), want integer", row[0], row[0])
		}
		gotLabels = append(gotLabels, row[1].(string))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows err: %v", err)
	}
	if len(gotIDs) != 2 || gotIDs[0] != 1 || gotIDs[1] != 2 {
		t.Errorf("ids = %v, want [1, 2]", gotIDs)
	}
	if len(gotLabels) != 2 || gotLabels[0] != "one" || gotLabels[1] != "two" {
		t.Errorf("labels = %v, want [one, two]", gotLabels)
	}
}

// ExerciseCatalogs verifies the seed script's sqlgo_a/sqlgo_b (or engine
// equivalent) are visible via the DatabaseLister capability, and that
// SchemaForDatabase returns the expected user table in each. catalogTable
// maps catalog name -> expected table name (case-insensitive match).
// Skips cleanly when the driver doesn't advertise SupportsCrossDatabase
// or doesn't implement DatabaseLister.
func ExerciseCatalogs(t *testing.T, conn db.Conn, catalogTable map[string]string) {
	t.Helper()
	if !conn.Capabilities().SupportsCrossDatabase {
		t.Skip("driver does not support cross-database")
	}
	lister, ok := conn.(db.DatabaseLister)
	if !ok {
		t.Skip("driver does not implement DatabaseLister")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	names, err := lister.ListDatabases(ctx)
	if err != nil {
		t.Fatalf("ListDatabases: %v", err)
	}
	seen := map[string]bool{}
	for _, n := range names {
		seen[strings.ToLower(n)] = true
	}
	for cat := range catalogTable {
		if !seen[strings.ToLower(cat)] {
			t.Errorf("ListDatabases missing %q; got %v (seed script not run?)", cat, names)
		}
	}

	for cat, wantTable := range catalogTable {
		info, err := lister.SchemaForDatabase(ctx, cat)
		if err != nil {
			t.Errorf("SchemaForDatabase(%q): %v", cat, err)
			continue
		}
		found := false
		for _, tr := range info.Tables {
			if strings.EqualFold(tr.Name, wantTable) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("SchemaForDatabase(%q): table %q missing (seed script not run?)", cat, wantTable)
		}
	}
}

// ExerciseDefinition creates an object, fetches its DDL via
// Conn.Definition, asserts the returned text contains wantSubstr, then
// drops the object. Skips cleanly when the driver returns
// ErrDefinitionUnsupported for the given kind.
func ExerciseDefinition(t *testing.T, conn db.Conn, kind, createSQL, dropSQL, schema, name, wantSubstr string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Best-effort pre-clean; may not exist yet.
	_ = conn.Exec(ctx, dropSQL)
	if err := conn.Exec(ctx, createSQL); err != nil {
		t.Fatalf("create %s: %v", kind, err)
	}
	defer func() {
		_ = conn.Exec(context.Background(), dropSQL)
	}()

	def, err := conn.Definition(ctx, kind, schema, name)
	if errors.Is(err, db.ErrDefinitionUnsupported) {
		t.Skipf("definition unsupported for kind %q", kind)
	}
	if err != nil {
		t.Fatalf("Definition(%s, %s, %s): %v", kind, schema, name, err)
	}
	if !strings.Contains(strings.ToLower(def), strings.ToLower(wantSubstr)) {
		t.Errorf("Definition body missing %q; got:\n%s", wantSubstr, def)
	}
}
