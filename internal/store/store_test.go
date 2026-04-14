package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// TestOpenAtCreatesFile verifies that opening the store against a fresh
// path materializes the file, runs the bootstrap migration, and leaves
// a usable handle.
func TestOpenAtCreatesFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "sqlgo.db")

	s, err := OpenAt(context.Background(), path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	// schema_migrations should have one row per shipping migration.
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	if n != len(migrations) {
		t.Errorf("schema_migrations rows = %d, want %d", n, len(migrations))
	}

	// connections table from v1 must be present and empty.
	var count int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM connections`).Scan(&count); err != nil {
		t.Fatalf("connections table missing: %v", err)
	}
	if count != 0 {
		t.Errorf("fresh store connections count = %d, want 0", count)
	}
}

// TestReopenIsIdempotent proves that reopening an existing store does not
// re-run migrations or fail on the pre-existing schema_migrations table.
// Important because Open() is called on every sqlgo startup.
func TestReopenIsIdempotent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "sqlgo.db")

	for i := 0; i < 3; i++ {
		s, err := OpenAt(context.Background(), path)
		if err != nil {
			t.Fatalf("open #%d: %v", i, err)
		}
		if err := s.Close(); err != nil {
			t.Fatalf("close #%d: %v", i, err)
		}
	}
}

// TestMigrationsApplyInOrder exercises the migrateWith machinery against
// a fixture list of migrations, bypassing OpenAt so the test doesn't have
// to touch the package-level `migrations` var (which would race with the
// other parallel tests in this package).
func TestMigrationsApplyInOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sqlgo.db")

	db, err := sql.Open("sqlite3", dsnFor(path))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}

	list := []string{
		`CREATE TABLE t1 (id INTEGER PRIMARY KEY)`,
		`CREATE TABLE t2 (id INTEGER PRIMARY KEY)`,
	}
	if err := migrateWith(ctx, db, list); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var version int
	if err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatalf("version query: %v", err)
	}
	if version != 2 {
		t.Errorf("version = %d, want 2", version)
	}
	for _, name := range []string{"t1", "t2"} {
		var got string
		if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&got); err != nil {
			t.Errorf("missing table %q: %v", name, err)
		}
	}

	// Re-running migrateWith with the same list should be a no-op: the
	// version stays at 2 and the tables already exist.
	if err := migrateWith(ctx, db, list); err != nil {
		t.Fatalf("migrate rerun: %v", err)
	}
	var again int
	if err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&again); err != nil {
		t.Fatalf("rerun version query: %v", err)
	}
	if again != 2 {
		t.Errorf("version after rerun = %d, want 2", again)
	}
}
