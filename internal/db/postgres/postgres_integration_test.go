//go:build integration

package postgres

import (
	"context"
	"os"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/db/dbtest"
)

// TestIntegrationPostgres exercises the Open -> Ping -> Exec ->
// Schema -> Columns -> Query round trip against a live Postgres.
// Uses compose.yaml defaults when SQLGO_IT_PG_* env vars aren't
// set so a plain `podman compose up -d postgres` + `go test
// -tags integration ./internal/db/postgres/` just works.
func TestIntegrationPostgres(t *testing.T) {
	cfg := db.Config{
		Host:     envOr("SQLGO_IT_PG_HOST", "127.0.0.1"),
		Port:     15432,
		User:     envOr("SQLGO_IT_PG_USER", "sqlgo"),
		Password: envOr("SQLGO_IT_PG_PASSWORD", "sqlgo_dev"),
		Database: envOr("SQLGO_IT_PG_DB", "sqlgo_test"),
		Options:  map[string]string{"sslmode": "disable"},
	}
	d, err := db.Get("postgres")
	if err != nil {
		t.Fatalf("db.Get postgres: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn, err := d.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("open postgres (is podman compose up?): %v", err)
	}
	defer conn.Close()

	// ExerciseDriver runs the common round-trip shape.
	// "public" is postgres' default user schema.
	dbtest.ExerciseDriver(t, conn, "public",
		`CREATE TABLE sqlgo_it_pg (id INTEGER, label TEXT)`,
		"sqlgo_it_pg",
	)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
