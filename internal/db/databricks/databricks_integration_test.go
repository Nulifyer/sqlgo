//go:build integration

package databricks

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/db/dbtest"
)

// TestIntegrationDatabricks exercises the driver against a live
// Databricks workspace. Databricks SQL is cloud-only (no self-
// hostable image), so the test is gated on SQLGO_IT_DATABRICKS_*
// env vars and SKIPs cleanly when they're unset.
//
// Required env vars:
//
//	SQLGO_IT_DATABRICKS_HOST       -- workspace host, e.g. dbc-abc.cloud.databricks.com
//	SQLGO_IT_DATABRICKS_HTTP_PATH  -- /sql/1.0/warehouses/<id>
//	SQLGO_IT_DATABRICKS_TOKEN      -- personal access token (dapi...)
//	SQLGO_IT_DATABRICKS_CATALOG    -- default catalog (e.g. MAIN)
//
// Optional:
//
//	SQLGO_IT_DATABRICKS_SCHEMA     -- default schema (test CREATEs sqlgo_it otherwise)
//	SQLGO_IT_DATABRICKS_PORT       -- HTTPS port (default 443)
//
// Warehouses bill per-second, so the test is intentionally minimal:
// one table, one view, exercise driver + view-definition, tear down.
func TestIntegrationDatabricks(t *testing.T) {
	host := os.Getenv("SQLGO_IT_DATABRICKS_HOST")
	httpPath := os.Getenv("SQLGO_IT_DATABRICKS_HTTP_PATH")
	token := os.Getenv("SQLGO_IT_DATABRICKS_TOKEN")
	catalog := os.Getenv("SQLGO_IT_DATABRICKS_CATALOG")
	if host == "" || httpPath == "" || token == "" || catalog == "" {
		t.Skip("SQLGO_IT_DATABRICKS_* env vars not set; skipping Databricks integration test")
	}

	schema := os.Getenv("SQLGO_IT_DATABRICKS_SCHEMA")
	if schema == "" {
		schema = "sqlgo_it"
	}

	opts := map[string]string{"http_path": httpPath, "schema": schema}
	port := 443
	if v := os.Getenv("SQLGO_IT_DATABRICKS_PORT"); v != "" {
		// Best-effort parse; fall through to 443 on bad input.
		var p int
		for _, c := range v {
			if c < '0' || c > '9' {
				p = 0
				break
			}
			p = p*10 + int(c-'0')
		}
		if p > 0 {
			port = p
		}
	}

	cfg := db.Config{
		Host:     host,
		Port:     port,
		Password: token,
		Database: catalog,
		Options:  opts,
	}
	d, err := db.Get("databricks")
	if err != nil {
		t.Fatalf("db.Get databricks: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	conn, err := d.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("open databricks: %v", err)
	}
	defer conn.Close()

	// Ensure the test schema exists. CREATE SCHEMA IF NOT EXISTS has
	// been supported since Databricks SQL 2021.x.
	if err := conn.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS `"+schema+"`"); err != nil {
		t.Fatalf("create schema %s: %v", schema, err)
	}

	dbtest.ExerciseDriver(t, conn, schema,
		"CREATE OR REPLACE TABLE `"+schema+"`.`sqlgo_it_databricks` (id BIGINT, label STRING)",
		"`"+schema+"`.`sqlgo_it_databricks`",
	)

	t.Run("view_definition", func(t *testing.T) {
		dbtest.ExerciseDefinition(t, conn, "view",
			"CREATE OR REPLACE VIEW `"+schema+"`.`sqlgo_it_databricks_view` AS SELECT 42 AS sqlgo_marker",
			"DROP VIEW IF EXISTS `"+schema+"`.`sqlgo_it_databricks_view`",
			schema, "sqlgo_it_databricks_view", "sqlgo_marker",
		)
	})
}
