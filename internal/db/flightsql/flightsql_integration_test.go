//go:build integration

package flightsql

import (
	"context"
	"os"
	"strconv"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/db/dbtest"
)

// TestIntegrationFlightSQL exercises the full driver round trip against
// the voltrondata/sqlflite compose service (DuckDB backend, gRPC on
// host port 19070, basic auth, no TLS). The DuckDB backend uses
// schema "main" by default.
func TestIntegrationFlightSQL(t *testing.T) {
	port, _ := strconv.Atoi(envOr("SQLGO_IT_FLIGHTSQL_PORT", "19070"))
	cfg := db.Config{
		Host:     envOr("SQLGO_IT_FLIGHTSQL_HOST", "127.0.0.1"),
		Port:     port,
		User:     envOr("SQLGO_IT_FLIGHTSQL_USER", "sqlflite_username"),
		Password: envOr("SQLGO_IT_FLIGHTSQL_PASSWORD", "sqlgo_dev"),
	}
	d, err := db.Get("flightsql")
	if err != nil {
		t.Fatalf("db.Get flightsql: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn, err := d.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("open flightsql (is podman compose up?): %v", err)
	}
	defer conn.Close()

	dbtest.ExerciseDriver(t, conn, "",
		`CREATE TABLE sqlgo_it_flightsql (id INTEGER, label VARCHAR)`,
		"sqlgo_it_flightsql",
	)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
