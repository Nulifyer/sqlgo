//go:build integration

// Package dbtest holds shared helpers for the driver integration
// test suites. Gated behind the `integration` build tag so the
// package doesn't compile during normal `go test ./...` runs --
// importing it from a _test.go would drag the compiled package
// in regardless of the test file's own build tags, which would
// pull live-DB code into headless CI where it doesn't belong.
//
// Usage from a driver's *_integration_test.go:
//
//	//go:build integration
//
//	package postgres
//
//	import (
//	    "github.com/Nulifyer/sqlgo/internal/db"
//	    "github.com/Nulifyer/sqlgo/internal/db/dbtest"
//	)
//
//	func TestIntegrationPostgres(t *testing.T) {
//	    conn, _ := driver.Open(...)
//	    defer conn.Close()
//	    dbtest.ExerciseDriver(t, conn, "public", createSQL, tableName)
//	}
package dbtest

import (
	"context"
	"testing"
	"time"

	"github.com/Nulifyer/sqlgo/internal/db"
)

// ExerciseDriver runs the round-trip shape every driver should
// satisfy: Ping -> Exec (CREATE/INSERT) -> Schema -> Columns ->
// Query -> Close. Call from a driver's TestIntegration<Name>
// after dialing the real DSN.
//
// tableSchema is the schema the driver reports (e.g. "public"
// for postgres, "dbo" for mssql, the DB name for mysql, "main"
// for sqlite); it has to be passed in because the dialects
// disagree and the test needs it for the Columns() call.
//
// createTable is the dialect-specific CREATE statement for a
// two-column fixture table (`id INTEGER, label <text>`). The
// caller picks the column types since dialects spell them
// differently.
func ExerciseDriver(t *testing.T, conn db.Conn, tableSchema, createTable, tableName string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := conn.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}

	// Drop-if-exists + create. We intentionally don't use
	// IF NOT EXISTS because the dialects vary; we just drop
	// first and swallow "doesn't exist" errors.
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

	// Schema() should now include our throwaway table under
	// the driver's native schema.
	info, err := conn.Schema(ctx)
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	found := false
	for _, tr := range info.Tables {
		if tr.Name == tableName {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("table %q not in Schema() result", tableName)
	}

	// Columns() should return id + label in order.
	cols, err := conn.Columns(ctx, db.TableRef{Schema: tableSchema, Name: tableName})
	if err != nil {
		t.Fatalf("columns: %v", err)
	}
	if len(cols) != 2 {
		t.Fatalf("len(cols) = %d, want 2 (%+v)", len(cols), cols)
	}
	if cols[0].Name != "id" || cols[1].Name != "label" {
		t.Errorf("cols = %+v, want [id, label]", cols)
	}

	// Query pulls the two rows back, streaming.
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
		// Drivers disagree on integer types for small values:
		// pgx returns int32, go-mssqldb int64, go-sql-driver
		// int64, sqlite int64. Accept any of them.
		switch v := row[0].(type) {
		case int64:
			gotIDs = append(gotIDs, v)
		case int32:
			gotIDs = append(gotIDs, int64(v))
		case int:
			gotIDs = append(gotIDs, int64(v))
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
