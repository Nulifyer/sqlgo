//go:build integration

package athena

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/db/dbtest"
)

// TestIntegrationAthena exercises the driver against a live AWS
// account. Athena is cloud-only (managed Presto), so the test is
// gated on SQLGO_IT_ATHENA_* env vars and SKIPs cleanly when unset.
//
// Required env vars:
//
//	SQLGO_IT_ATHENA_REGION          -- AWS region, e.g. us-east-1
//	SQLGO_IT_ATHENA_OUTPUT_LOCATION -- s3://bucket/prefix/ for query results
//	SQLGO_IT_ATHENA_DATABASE        -- Athena/Glue schema to use
//
// Credentials (pick one):
//
//	SQLGO_IT_ATHENA_ACCESS_KEY + SQLGO_IT_ATHENA_SECRET_KEY (+ optional SESSION_TOKEN)
//	AWS_PROFILE env + AWS_SDK_LOAD_CONFIG=1 (named profile)
//	IAM instance role (leave creds unset; needs AWS_SDK_LOAD_CONFIG=1 on EC2/ECS)
//
// Optional:
//
//	SQLGO_IT_ATHENA_WORKGROUP       -- workgroup name (default "primary")
//
// Athena is pay-per-query + pay-per-byte-scanned, so the test uses a
// single tiny external table over a one-row inline partition to keep
// scan cost near zero.
func TestIntegrationAthena(t *testing.T) {
	region := os.Getenv("SQLGO_IT_ATHENA_REGION")
	output := os.Getenv("SQLGO_IT_ATHENA_OUTPUT_LOCATION")
	database := os.Getenv("SQLGO_IT_ATHENA_DATABASE")
	if region == "" || output == "" || database == "" {
		t.Skip("SQLGO_IT_ATHENA_* env vars not set; skipping Athena integration test")
	}

	opts := map[string]string{
		"region":          region,
		"output_location": output,
	}
	if v := os.Getenv("SQLGO_IT_ATHENA_WORKGROUP"); v != "" {
		opts["workgroup"] = v
	}
	if v := os.Getenv("SQLGO_IT_ATHENA_SESSION_TOKEN"); v != "" {
		opts["session_token"] = v
	}

	cfg := db.Config{
		User:     os.Getenv("SQLGO_IT_ATHENA_ACCESS_KEY"),
		Password: os.Getenv("SQLGO_IT_ATHENA_SECRET_KEY"),
		Database: database,
		Options:  opts,
	}
	d, err := db.Get("athena")
	if err != nil {
		t.Fatalf("db.Get athena: %v", err)
	}
	// Athena query launch + polling can take 20-60s on cold workgroups;
	// give the whole test 3 minutes so CREATE TABLE / DROP don't race
	// the per-query default poll.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	conn, err := d.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("open athena: %v", err)
	}
	defer conn.Close()

	// Use a view-only driver probe: Athena INSERT into external tables
	// requires explicit S3 locations and table formats, so the generic
	// ExerciseDriver (which INSERTs into the supplied test table)
	// would need a custom DDL. Exercise the view+definition path only
	// -- that still covers schemaQuery, columnsQuery, DefinitionFetcher.
	t.Run("view_definition", func(t *testing.T) {
		dbtest.ExerciseDefinition(t, conn, "view",
			`CREATE OR REPLACE VIEW "`+database+`"."sqlgo_it_athena_view" AS SELECT 42 AS sqlgo_marker`,
			`DROP VIEW IF EXISTS "`+database+`"."sqlgo_it_athena_view"`,
			database, "sqlgo_it_athena_view", "sqlgo_marker",
		)
	})
}
