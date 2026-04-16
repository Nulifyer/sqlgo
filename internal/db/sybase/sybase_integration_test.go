//go:build integration

package sybase

import (
	"context"
	"os"
	"strconv"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/db/dbtest"
)

// TestIntegrationSybase exercises the full driver round trip against
// a live Sybase ASE over TDS 5.0. Defaults target the compose.yaml
// sybase service (port 15000, datagrip/sybase image, preseeded
// testdb owned by tester/password).
func TestIntegrationSybase(t *testing.T) {
	port, _ := strconv.Atoi(envOr("SQLGO_IT_SYBASE_PORT", "15000"))
	cfg := db.Config{
		Host:     envOr("SQLGO_IT_SYBASE_HOST", "127.0.0.1"),
		Port:     port,
		User:     envOr("SQLGO_IT_SYBASE_USER", "tester"),
		Password: envOr("SQLGO_IT_SYBASE_PASSWORD", "guest1234"),
		Database: envOr("SQLGO_IT_SYBASE_DB", "testdb"),
	}
	d, err := db.Get("sybase")
	if err != nil {
		t.Fatalf("db.Get sybase: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn, err := d.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("open sybase (is podman compose up?): %v", err)
	}
	defer conn.Close()

	// "tester" owns objects it creates in testdb (the image's
	// seeded user has DBO-equivalent rights on that database).
	dbtest.ExerciseDriver(t, conn, "tester",
		`CREATE TABLE sqlgo_it_sybase (id INT, label VARCHAR(50))`,
		"sqlgo_it_sybase",
	)

	t.Run("seeded_catalogs", func(t *testing.T) {
		dbtest.ExerciseCatalogs(t, conn, map[string]string{
			"sqlgo_a": "widgets",
			"sqlgo_b": "gadgets",
		})
	})

	t.Run("view_definition", func(t *testing.T) {
		dbtest.ExerciseDefinition(t, conn, "view",
			`CREATE VIEW sqlgo_it_sybase_view AS SELECT 42 AS sqlgo_marker`,
			`DROP VIEW sqlgo_it_sybase_view`,
			"tester", "sqlgo_it_sybase_view", "sqlgo_marker",
		)
	})
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
