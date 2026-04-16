//go:build integration

package snowflake

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/db/dbtest"
)

// TestIntegrationSnowflake exercises the driver against a live
// Snowflake account. Snowflake is cloud-only -- there is no
// self-hostable image -- so this test is gated on the SQLGO_IT_
// SNOWFLAKE_* env vars and SKIPs cleanly when they're unset.
//
// Required env vars:
//
//	SQLGO_IT_SNOWFLAKE_ACCOUNT   -- account identifier (xy12345.us-east-1)
//	SQLGO_IT_SNOWFLAKE_USER
//	SQLGO_IT_SNOWFLAKE_PASSWORD
//	SQLGO_IT_SNOWFLAKE_DATABASE
//	SQLGO_IT_SNOWFLAKE_WAREHOUSE
//
// Optional:
//
//	SQLGO_IT_SNOWFLAKE_ROLE      -- session role
//	SQLGO_IT_SNOWFLAKE_SCHEMA    -- default schema (test CREATEs SQLGO otherwise)
//
// Snowflake billing is per-second of warehouse usage, so the test
// is intentionally minimal: one table, one view, exercise driver +
// view-definition, tear down. Under 10s wall-clock at typical
// warehouse sizes.
func TestIntegrationSnowflake(t *testing.T) {
	account := os.Getenv("SQLGO_IT_SNOWFLAKE_ACCOUNT")
	user := os.Getenv("SQLGO_IT_SNOWFLAKE_USER")
	password := os.Getenv("SQLGO_IT_SNOWFLAKE_PASSWORD")
	database := os.Getenv("SQLGO_IT_SNOWFLAKE_DATABASE")
	warehouse := os.Getenv("SQLGO_IT_SNOWFLAKE_WAREHOUSE")
	if account == "" || user == "" || password == "" || database == "" || warehouse == "" {
		t.Skip("SQLGO_IT_SNOWFLAKE_* env vars not set; skipping Snowflake integration test")
	}

	opts := map[string]string{"warehouse": warehouse}
	if v := os.Getenv("SQLGO_IT_SNOWFLAKE_ROLE"); v != "" {
		opts["role"] = v
	}
	if v := os.Getenv("SQLGO_IT_SNOWFLAKE_SCHEMA"); v != "" {
		opts["schema"] = v
	}

	cfg := db.Config{
		Host:     account,
		User:     user,
		Password: password,
		Database: database,
		Options:  opts,
	}
	d, err := db.Get("snowflake")
	if err != nil {
		t.Fatalf("db.Get snowflake: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	conn, err := d.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("open snowflake: %v", err)
	}
	defer conn.Close()

	// Ensure a SQLGO schema exists for the driver probe. CREATE SCHEMA
	// IF NOT EXISTS is supported everywhere post-2019.
	if err := conn.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS "SQLGO"`); err != nil {
		t.Fatalf("create schema SQLGO: %v", err)
	}

	dbtest.ExerciseDriver(t, conn, "SQLGO",
		`CREATE OR REPLACE TABLE "SQLGO"."sqlgo_it_snowflake" (id NUMBER, label VARCHAR(64))`,
		`"SQLGO"."sqlgo_it_snowflake"`,
	)

	t.Run("view_definition", func(t *testing.T) {
		dbtest.ExerciseDefinition(t, conn, "view",
			`CREATE OR REPLACE VIEW "SQLGO"."sqlgo_it_snowflake_view" AS SELECT 42 AS sqlgo_marker`,
			`DROP VIEW IF EXISTS "SQLGO"."sqlgo_it_snowflake_view"`,
			"SQLGO", "sqlgo_it_snowflake_view", "sqlgo_marker",
		)
	})
}
