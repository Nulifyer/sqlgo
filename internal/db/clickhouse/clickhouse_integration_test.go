//go:build integration

package clickhouse

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/db/dbtest"
)

// TestIntegrationClickHouse exercises the full driver round trip
// against a live ClickHouse server on the native TCP protocol.
// Defaults target the compose.yaml clickhouse service (port 19000
// on the host, 9000 in-container, default user with no password).
//
// ClickHouse has no transactions and no CREATE OR REPLACE -- the
// test uses the MergeTree engine with ORDER BY () so INSERT works
// without a partition/sort key dance. ExerciseCatalogs is skipped
// because the profile advertises SupportsCrossDatabase=false (CH
// databases are modeled as schemas in the explorer).
func TestIntegrationClickHouse(t *testing.T) {
	port, _ := strconv.Atoi(envOr("SQLGO_IT_CLICKHOUSE_PORT", "19000"))
	cfg := db.Config{
		Host:     envOr("SQLGO_IT_CLICKHOUSE_HOST", "127.0.0.1"),
		Port:     port,
		User:     envOr("SQLGO_IT_CLICKHOUSE_USER", "default"),
		Password: os.Getenv("SQLGO_IT_CLICKHOUSE_PASSWORD"),
		Database: envOr("SQLGO_IT_CLICKHOUSE_DB", "default"),
	}
	d, err := db.Get("clickhouse")
	if err != nil {
		t.Fatalf("db.Get clickhouse: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn, err := d.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("open clickhouse (is podman compose up?): %v", err)
	}
	defer conn.Close()

	dbtest.ExerciseDriver(t, conn, "default",
		`CREATE TABLE sqlgo_it_clickhouse (id Int32, label String) ENGINE = MergeTree() ORDER BY id`,
		"sqlgo_it_clickhouse",
	)

	t.Run("view_definition", func(t *testing.T) {
		dbtest.ExerciseDefinition(t, conn, "view",
			`CREATE VIEW sqlgo_it_clickhouse_view AS SELECT 42 AS sqlgo_marker`,
			`DROP VIEW sqlgo_it_clickhouse_view`,
			"default", "sqlgo_it_clickhouse_view", "sqlgo_marker",
		)
	})
}

// TestIntegrationClickHouseTLS dials the compose clickhouse server on
// its TLS port (host 19440 -> container 9440) and drives a round trip
// with only a CA bundle supplied. Proves the "verify server, no client
// cert" handshake path. The dev CA at compose/clickhouse/certs/ca.crt
// signs the server cert; SANs include localhost + 127.0.0.1, so the
// host field and tls_server_name can match either.
func TestIntegrationClickHouseTLS(t *testing.T) {
	certsDir := envOr("SQLGO_IT_CLICKHOUSE_CERTS_DIR", filepath.Join("..", "..", "..", "compose", "clickhouse", "certs"))
	caPath, err := filepath.Abs(filepath.Join(certsDir, "ca.crt"))
	if err != nil {
		t.Fatalf("abs ca path: %v", err)
	}
	port, _ := strconv.Atoi(envOr("SQLGO_IT_CLICKHOUSE_TLS_PORT", "19440"))
	cfg := db.Config{
		Host:     envOr("SQLGO_IT_CLICKHOUSE_HOST", "127.0.0.1"),
		Port:     port,
		User:     envOr("SQLGO_IT_CLICKHOUSE_USER", "default"),
		Password: os.Getenv("SQLGO_IT_CLICKHOUSE_PASSWORD"),
		Database: envOr("SQLGO_IT_CLICKHOUSE_DB", "default"),
		Options: map[string]string{
			"secure":          "true",
			"tls_ca_file":     caPath,
			"tls_server_name": "localhost",
		},
	}
	d, err := db.Get("clickhouse")
	if err != nil {
		t.Fatalf("db.Get clickhouse: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn, err := d.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("open clickhouse tls (is podman compose up?): %v", err)
	}
	defer conn.Close()

	if err := conn.Ping(ctx); err != nil {
		t.Fatalf("ping over TLS: %v", err)
	}
	rows, err := conn.Query(ctx, "SELECT 1")
	if err != nil {
		t.Fatalf("query SELECT 1: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatalf("no row from SELECT 1: %v", rows.Err())
	}
	vals, err := rows.Scan()
	if err != nil {
		t.Fatalf("scan SELECT 1: %v", err)
	}
	if len(vals) != 1 {
		t.Fatalf("SELECT 1 returned %d cols, want 1", len(vals))
	}
}

// TestIntegrationClickHouseMTLS exercises the full mutual-TLS handshake
// (client presents a cert signed by the dev CA). Driven through the
// openClickHouse -> ParseDSN -> Options.TLS -> OpenDB path, which is
// the only way to deliver tls.Certificates to clickhouse-go/v2 -- a
// DSN query string can't carry a programmatic *tls.Config.
func TestIntegrationClickHouseMTLS(t *testing.T) {
	certsDir := envOr("SQLGO_IT_CLICKHOUSE_CERTS_DIR", filepath.Join("..", "..", "..", "compose", "clickhouse", "certs"))
	absDir, err := filepath.Abs(certsDir)
	if err != nil {
		t.Fatalf("abs certs dir: %v", err)
	}
	port, _ := strconv.Atoi(envOr("SQLGO_IT_CLICKHOUSE_TLS_PORT", "19440"))
	cfg := db.Config{
		Host:     envOr("SQLGO_IT_CLICKHOUSE_HOST", "127.0.0.1"),
		Port:     port,
		User:     envOr("SQLGO_IT_CLICKHOUSE_USER", "default"),
		Password: os.Getenv("SQLGO_IT_CLICKHOUSE_PASSWORD"),
		Database: envOr("SQLGO_IT_CLICKHOUSE_DB", "default"),
		Options: map[string]string{
			"secure":          "true",
			"tls_ca_file":     filepath.Join(absDir, "ca.crt"),
			"tls_cert_file":   filepath.Join(absDir, "client.crt"),
			"tls_key_file":    filepath.Join(absDir, "client.key"),
			"tls_server_name": "localhost",
		},
	}
	d, err := db.Get("clickhouse")
	if err != nil {
		t.Fatalf("db.Get clickhouse: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn, err := d.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("open clickhouse mtls (is podman compose up?): %v", err)
	}
	defer conn.Close()

	if err := conn.Ping(ctx); err != nil {
		t.Fatalf("ping over mTLS: %v", err)
	}
	rows, err := conn.Query(ctx, "SELECT 1")
	if err != nil {
		t.Fatalf("query SELECT 1: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatalf("no row from SELECT 1: %v", rows.Err())
	}
	vals, err := rows.Scan()
	if err != nil {
		t.Fatalf("scan SELECT 1: %v", err)
	}
	if len(vals) != 1 {
		t.Fatalf("SELECT 1 returned %d cols, want 1", len(vals))
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
