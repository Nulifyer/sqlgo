//go:build integration

package sqlite

import (
	"context"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/db/dbtest"
)

// TestIntegrationSQLite runs the shared exercise shape against
// an in-memory sqlite. SQLite is in-process so no compose service
// is needed; the test still lives behind the integration tag so
// the default `go test ./...` stays fast and consistent with the
// other drivers' gating (you turn on integration, you get all of
// them, you leave it off and every driver is tested only at the
// DSN / helper level).
func TestIntegrationSQLite(t *testing.T) {
	d, err := db.Get("sqlite")
	if err != nil {
		t.Fatalf("db.Get sqlite: %v", err)
	}
	conn, err := d.Open(context.Background(), db.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer conn.Close()

	dbtest.ExerciseDriver(t, conn, "main",
		`CREATE TABLE sqlgo_it_sqlite (id INTEGER, label TEXT)`,
		"sqlgo_it_sqlite",
	)
}
