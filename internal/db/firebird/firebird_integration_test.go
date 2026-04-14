//go:build integration

package firebird

import (
	"context"
	"os"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/db/dbtest"
)

// TestIntegrationFirebird runs the shared round-trip against the
// compose firebird service on port 13050. Defaults match the
// FIREBIRD_DATABASE fixture (sqlgo_test.fdb) owned by user "sqlgo".
//
// Firebird folds unquoted identifiers to UPPERCASE, so the table
// name is uppercase to match what catalog queries return.
func TestIntegrationFirebird(t *testing.T) {
	cfg := db.Config{
		Host:     envOr("SQLGO_IT_FB_HOST", "127.0.0.1"),
		Port:     13050,
		User:     envOr("SQLGO_IT_FB_USER", "sqlgo"),
		Password: envOr("SQLGO_IT_FB_PASSWORD", "sqlgo_dev"),
		Database: envOr("SQLGO_IT_FB_DB", "/var/lib/firebird/data/sqlgo_test.fdb"),
	}
	d, err := db.Get("firebird")
	if err != nil {
		t.Fatalf("db.Get firebird: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn, err := d.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("open firebird (is podman compose up?): %v", err)
	}
	defer conn.Close()

	// Firebird has no schemas; the loader synthesizes "main".
	dbtest.ExerciseDriver(t, conn, "main",
		`CREATE TABLE SQLGO_IT_FB (id INTEGER, label VARCHAR(50))`,
		"SQLGO_IT_FB",
	)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
