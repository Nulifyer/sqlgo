//go:build integration

package trino

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/db/dbtest"
)

// TestIntegrationTrino exercises the driver end to end against a live
// Trino coordinator. Defaults target the compose.yaml trino service
// (18081 on the host, 8080 in-container, catalog "memory" which is a
// writable in-memory connector with no persistence).
//
// Trino has no stored procedures/triggers and most deployments disable
// transactions, so only view_definition is exercised beyond the core
// ExerciseDriver path. ExerciseCatalogs is skipped because the profile
// does not advertise SupportsCrossDatabase -- a Trino connection is
// pinned to a single catalog by DSN.
func TestIntegrationTrino(t *testing.T) {
	port, _ := strconv.Atoi(envOr("SQLGO_IT_TRINO_PORT", "18081"))
	cfg := db.Config{
		Host:     envOr("SQLGO_IT_TRINO_HOST", "127.0.0.1"),
		Port:     port,
		User:     envOr("SQLGO_IT_TRINO_USER", "sqlgo"),
		Database: envOr("SQLGO_IT_TRINO_CATALOG", "memory"),
		Options: map[string]string{
			// Bind the connection to the default schema so unqualified
			// table references in ExerciseDriver resolve. Memory connector
			// creates it on demand when we run CREATE SCHEMA below.
			"schema": envOr("SQLGO_IT_TRINO_SCHEMA", "default"),
			"source": "sqlgo-integration",
		},
	}
	d, err := db.Get("trino")
	if err != nil {
		t.Fatalf("db.Get trino: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := d.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("open trino (is podman compose up?): %v", err)
	}
	defer conn.Close()

	// Memory connector requires the schema to exist before any CREATE
	// TABLE; other connectors come with default schemas pre-created.
	if err := conn.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS memory.default"); err != nil {
		t.Fatalf("create schema memory.default: %v", err)
	}

	dbtest.ExerciseDriver(t, conn, "default",
		`CREATE TABLE sqlgo_it_trino (id INTEGER, label VARCHAR)`,
		"sqlgo_it_trino",
	)

	t.Run("view_definition", func(t *testing.T) {
		dbtest.ExerciseDefinition(t, conn, "view",
			`CREATE VIEW sqlgo_it_trino_view AS SELECT 42 AS sqlgo_marker`,
			`DROP VIEW sqlgo_it_trino_view`,
			"default", "sqlgo_it_trino_view", "sqlgo_marker",
		)
	})
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
