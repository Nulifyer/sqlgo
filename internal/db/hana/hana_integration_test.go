//go:build integration

package hana

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/db/dbtest"
)

// TestIntegrationHANA exercises the driver against a live SAP HANA
// Express instance. Defaults target the compose.yaml `hana` service
// (host port 13901 -> container 39017, tenant database HXE, SYSTEM
// user). saplabs/hanaexpress is ~20GB and requires 8GB+ RAM, so the
// compose service sits behind the `heavy` profile:
//
//	podman compose --profile heavy up -d hana
//
// HANA profile pins SupportsCrossDatabase=false (one tenant per
// connection), so this exercises ExerciseDriver + the view-definition
// subtest only.
func TestIntegrationHANA(t *testing.T) {
	port, _ := strconv.Atoi(envOr("SQLGO_IT_HANA_PORT", "13901"))
	cfg := db.Config{
		Host:     envOr("SQLGO_IT_HANA_HOST", "127.0.0.1"),
		Port:     port,
		User:     envOr("SQLGO_IT_HANA_USER", "SYSTEM"),
		Password: envOr("SQLGO_IT_HANA_PASSWORD", "HXEHana1"),
		Database: envOr("SQLGO_IT_HANA_DATABASE", "HXE"),
		Options: map[string]string{
			// HANA Express dev instances ship self-signed certs; accept
			// them under the integration test. Real deployments should
			// unset this and supply tls_root_ca_file.
			"tls_insecure_skip_verify": envOr("SQLGO_IT_HANA_TLS_INSECURE", "true"),
		},
	}
	d, err := db.Get("hana")
	if err != nil {
		t.Fatalf("db.Get hana: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	conn, err := d.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("open hana (is podman compose --profile heavy up hana running?): %v", err)
	}
	defer conn.Close()

	// Ensure the SQLGO schema exists. HANA supports IF NOT EXISTS on
	// CREATE SCHEMA from 2.0 SPS 05 onward (HXE qualifies).
	if err := conn.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS "SQLGO"`); err != nil {
		t.Fatalf("create schema SQLGO: %v", err)
	}

	dbtest.ExerciseDriver(t, conn, "SQLGO",
		`CREATE TABLE "SQLGO"."sqlgo_it_hana" (id INTEGER, label NVARCHAR(64))`,
		`"SQLGO"."sqlgo_it_hana"`,
	)

	t.Run("view_definition", func(t *testing.T) {
		dbtest.ExerciseDefinition(t, conn, "view",
			`CREATE VIEW "SQLGO"."sqlgo_it_hana_view" AS SELECT 42 AS sqlgo_marker FROM DUMMY`,
			`DROP VIEW "SQLGO"."sqlgo_it_hana_view"`,
			"SQLGO", "sqlgo_it_hana_view", "sqlgo_marker",
		)
	})
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
