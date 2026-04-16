//go:build integration

package vertica

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/db/dbtest"
)

// TestIntegrationVertica exercises the driver against a live Vertica CE
// instance. Defaults target the compose.yaml vertica service (15433 on
// the host, 5433 in-container, database VMart which is pre-created in
// the vertica/vertica-ce image).
//
// Vertica supports transactions but the profile does not advertise
// SupportsCrossDatabase (one-DB-per-connection like Postgres), so we
// exercise only ExerciseDriver + view definition -- ExerciseCatalogs
// does not apply here.
func TestIntegrationVertica(t *testing.T) {
	port, _ := strconv.Atoi(envOr("SQLGO_IT_VERTICA_PORT", "15433"))
	cfg := db.Config{
		Host:     envOr("SQLGO_IT_VERTICA_HOST", "127.0.0.1"),
		Port:     port,
		User:     envOr("SQLGO_IT_VERTICA_USER", "dbadmin"),
		Password: envOr("SQLGO_IT_VERTICA_PASSWORD", ""),
		Database: envOr("SQLGO_IT_VERTICA_DATABASE", "VMart"),
		Options: map[string]string{
			"tlsmode": envOr("SQLGO_IT_VERTICA_TLSMODE", "none"),
		},
	}
	d, err := db.Get("vertica")
	if err != nil {
		t.Fatalf("db.Get vertica: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	conn, err := d.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("open vertica (is podman compose up?): %v", err)
	}
	defer conn.Close()

	// Ensure the test schema exists. public schema is created by the
	// bootstrap but tests may land before seed scripts run; create it
	// idempotently so the test is self-sufficient.
	if err := conn.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS public"); err != nil {
		t.Fatalf("create schema public: %v", err)
	}

	dbtest.ExerciseDriver(t, conn, "public",
		`CREATE TABLE public.sqlgo_it_vertica (id INTEGER, label VARCHAR(64))`,
		"public.sqlgo_it_vertica",
	)

	t.Run("view_definition", func(t *testing.T) {
		dbtest.ExerciseDefinition(t, conn, "view",
			`CREATE OR REPLACE VIEW public.sqlgo_it_vertica_view AS SELECT 42 AS sqlgo_marker`,
			`DROP VIEW public.sqlgo_it_vertica_view`,
			"public", "sqlgo_it_vertica_view", "sqlgo_marker",
		)
	})
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
