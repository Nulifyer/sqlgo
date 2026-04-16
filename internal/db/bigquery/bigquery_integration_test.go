//go:build integration

package bigquery

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/db/dbtest"
)

// TestIntegrationBigQuery exercises the driver end to end against the
// local goccy/bigquery-emulator (compose service "bigquery" on host
// 19050 -> container 9050, REST). The emulator boots with a preseeded
// project + dataset via the --project / --dataset flags in compose.yaml,
// so no seed step is required -- buildClientOptions auto-implies
// WithoutAuthentication when Host+Port resolve to an endpoint without
// explicit credentials.
//
// BigQuery has no stored procedures/triggers and no SHOW CREATE TABLE;
// only view_definition is exercised beyond the core ExerciseDriver path.
// Cross-database is not supported (connection is pinned to one project),
// so ExerciseCatalogs is skipped.
func TestIntegrationBigQuery(t *testing.T) {
	port, _ := strconv.Atoi(envOr("SQLGO_IT_BIGQUERY_PORT", "19050"))
	cfg := db.Config{
		Host:     envOr("SQLGO_IT_BIGQUERY_HOST", "127.0.0.1"),
		Port:     port,
		Database: envOr("SQLGO_IT_BIGQUERY_DATABASE", "sqlgo_test"),
		Options: map[string]string{
			"project": envOr("SQLGO_IT_BIGQUERY_PROJECT", "sqlgo-emu"),
		},
	}
	d, err := db.Get("bigquery")
	if err != nil {
		t.Fatalf("db.Get bigquery: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	conn, err := d.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("open bigquery (is podman compose up?): %v", err)
	}
	defer conn.Close()

	dbtest.ExerciseDriver(t, conn, cfg.Database,
		"CREATE TABLE sqlgo_it_bigquery (id INT64, label STRING)",
		"sqlgo_it_bigquery",
	)

	t.Run("view_definition", func(t *testing.T) {
		dbtest.ExerciseDefinition(t, conn, "view",
			"CREATE VIEW sqlgo_it_bigquery_view AS SELECT 42 AS sqlgo_marker",
			"DROP VIEW sqlgo_it_bigquery_view",
			cfg.Database, "sqlgo_it_bigquery_view", "sqlgo_marker",
		)
	})
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
