package db

import (
	"context"
	"path/filepath"
	"testing"
)

func TestPingSQLite(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	profile := testSQLiteProfile(t)

	if err := Ping(context.Background(), profile, registry); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
}

func TestRunQuerySQLiteCreateInsertSelect(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	profile := testSQLiteProfile(t)

	statements := []string{
		`CREATE TABLE items (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			note TEXT NULL
		);`,
		`INSERT INTO items (id, name, note) VALUES (1, 'alpha', NULL);`,
		`INSERT INTO items (id, name, note) VALUES (2, 'beta', 'second');`,
	}

	for _, statement := range statements {
		result, err := RunQuery(context.Background(), profile, registry, statement)
		if err != nil {
			t.Fatalf("RunQuery(%q) error = %v", statement, err)
		}
		if result.IsQuery {
			t.Fatalf("RunQuery(%q) unexpectedly returned a query result", statement)
		}
	}

	result, err := RunQuery(
		context.Background(),
		profile,
		registry,
		`SELECT id, name, note FROM items ORDER BY id;`,
	)
	if err != nil {
		t.Fatalf("RunQuery(select) error = %v", err)
	}

	if !result.IsQuery {
		t.Fatalf("RunQuery(select) did not mark result as query")
	}

	wantColumns := []string{"id", "name", "note"}
	if len(result.Columns) != len(wantColumns) {
		t.Fatalf("Columns length = %d, want %d", len(result.Columns), len(wantColumns))
	}
	for i, want := range wantColumns {
		if result.Columns[i] != want {
			t.Fatalf("Columns[%d] = %q, want %q", i, result.Columns[i], want)
		}
	}

	if result.RowsFetched != 2 {
		t.Fatalf("RowsFetched = %d, want 2", result.RowsFetched)
	}
	if len(result.Rows) != 2 {
		t.Fatalf("len(Rows) = %d, want 2", len(result.Rows))
	}

	if got := result.Rows[0][0]; got != "1" {
		t.Fatalf("row 0 id = %q, want %q", got, "1")
	}
	if got := result.Rows[0][1]; got != "alpha" {
		t.Fatalf("row 0 name = %q, want %q", got, "alpha")
	}
	if got := result.Rows[0][2]; got != "NULL" {
		t.Fatalf("row 0 note = %q, want %q", got, "NULL")
	}

	if got := result.Rows[1][0]; got != "2" {
		t.Fatalf("row 1 id = %q, want %q", got, "2")
	}
	if got := result.Rows[1][1]; got != "beta" {
		t.Fatalf("row 1 name = %q, want %q", got, "beta")
	}
	if got := result.Rows[1][2]; got != "second" {
		t.Fatalf("row 1 note = %q, want %q", got, "second")
	}
}

func TestRunQuerySQLitePreviewTruncation(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	profile := testSQLiteProfile(t)

	result, err := RunQuery(
		context.Background(),
		profile,
		registry,
		`WITH RECURSIVE cnt(x) AS (
			SELECT 1
			UNION ALL
			SELECT x + 1 FROM cnt WHERE x < 205
		)
		SELECT x FROM cnt;`,
	)
	if err != nil {
		t.Fatalf("RunQuery(recursive select) error = %v", err)
	}

	if !result.IsQuery {
		t.Fatalf("RunQuery(recursive select) did not mark result as query")
	}
	if result.RowsFetched != 205 {
		t.Fatalf("RowsFetched = %d, want 205", result.RowsFetched)
	}
	if len(result.Rows) != maxPreviewRows {
		t.Fatalf("len(Rows) = %d, want %d", len(result.Rows), maxPreviewRows)
	}
	if !result.Truncated {
		t.Fatalf("Truncated = false, want true")
	}
	if got := result.Rows[0][0]; got != "1" {
		t.Fatalf("first preview row = %q, want %q", got, "1")
	}
	if got := result.Rows[maxPreviewRows-1][0]; got != "200" {
		t.Fatalf("last preview row = %q, want %q", got, "200")
	}
}

func TestRunQueryReadOnlyBlocksWrites(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	profile := testSQLiteProfile(t)
	profile.ReadOnly = true

	_, err := RunQuery(context.Background(), profile, registry, `CREATE TABLE blocked (id INTEGER PRIMARY KEY);`)
	if err == nil {
		t.Fatalf("expected read-only connection to block write statement")
	}
}

func testSQLiteProfile(t *testing.T) ConnectionProfile {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "sqlgo-test.db")
	if err := CreateSQLiteFixture(context.Background(), dbPath); err != nil {
		t.Fatalf("CreateSQLiteFixture() error = %v", err)
	}

	return ConnectionProfile{
		Name:       "sqlite-test",
		ProviderID: ProviderSQLite,
		DSN:        "file:" + filepath.ToSlash(dbPath),
	}
}
