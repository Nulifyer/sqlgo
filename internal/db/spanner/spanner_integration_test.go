//go:build integration

package spanner

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/db/dbtest"
)

// TestIntegrationSpanner exercises the driver end to end against the
// local Cloud Spanner Emulator (compose service "spanner" on host
// 19010 -> container 9010). autoConfigEmulator=true tells the driver
// to both dial the plain-text emulator endpoint and auto-create the
// target instance + database on first Open, so the test has no prior
// dependency on gcloud / seed scripts.
//
// Spanner has no stored procedures/triggers and no SHOW CREATE TABLE,
// so only view_definition is exercised beyond the core ExerciseDriver
// path. Cross-database is not supported (a connection is pinned to
// projects/P/instances/I/databases/D), so ExerciseCatalogs is skipped.
func TestIntegrationSpanner(t *testing.T) {
	port, _ := strconv.Atoi(envOr("SQLGO_IT_SPANNER_PORT", "19010"))
	cfg := db.Config{
		Host:     envOr("SQLGO_IT_SPANNER_HOST", "127.0.0.1"),
		Port:     port,
		Database: envOr("SQLGO_IT_SPANNER_DATABASE", "sqlgo_test"),
		Options: map[string]string{
			"project":            envOr("SQLGO_IT_SPANNER_PROJECT", "sqlgo-emu"),
			"instance":           envOr("SQLGO_IT_SPANNER_INSTANCE", "sqlgo"),
			"autoConfigEmulator": "true",
		},
	}
	d, err := db.Get("spanner")
	if err != nil {
		t.Fatalf("db.Get spanner: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	conn, err := d.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("open spanner (is podman compose up?): %v", err)
	}
	defer conn.Close()

	dbtest.ExerciseDriver(t, conn, "",
		`CREATE TABLE sqlgo_it_spanner (id INT64, label STRING(MAX)) PRIMARY KEY (id)`,
		"sqlgo_it_spanner",
	)

	t.Run("view_definition", func(t *testing.T) {
		dbtest.ExerciseDefinition(t, conn, "view",
			`CREATE VIEW sqlgo_it_spanner_view SQL SECURITY INVOKER AS SELECT 42 AS sqlgo_marker`,
			`DROP VIEW sqlgo_it_spanner_view`,
			"", "sqlgo_it_spanner_view", "sqlgo_marker",
		)
	})
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
