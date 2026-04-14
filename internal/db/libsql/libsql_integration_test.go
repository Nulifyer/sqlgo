//go:build integration

package libsql

import (
	"context"
	"os"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/db/dbtest"
)

// TestIntegrationLibSQL exercises the hrana HTTP client against the
// compose libsql service on port 18080. Auth is disabled for dev so
// the password slot carries an empty token.
func TestIntegrationLibSQL(t *testing.T) {
	cfg := db.Config{
		Host:     envOr("SQLGO_IT_LIBSQL_URL", "http://127.0.0.1:18080"),
		Password: os.Getenv("SQLGO_IT_LIBSQL_TOKEN"),
	}
	d, err := db.Get("libsql")
	if err != nil {
		t.Fatalf("db.Get libsql: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn, err := d.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("open libsql (is podman compose up?): %v", err)
	}
	defer conn.Close()

	// SQLite's default schema bucket is "main".
	dbtest.ExerciseDriver(t, conn, "main",
		`CREATE TABLE sqlgo_it_libsql (id INTEGER, label TEXT)`,
		"sqlgo_it_libsql",
	)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
