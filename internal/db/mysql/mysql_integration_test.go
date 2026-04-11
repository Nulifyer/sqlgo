//go:build integration

package mysql

import (
	"context"
	"os"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/db/dbtest"
)

// TestIntegrationMySQL exercises the full driver round trip
// against a live MySQL. Defaults match the compose.yaml mysql
// service (port 13306, sqlgo / sqlgo_dev / sqlgo_test).
func TestIntegrationMySQL(t *testing.T) {
	cfg := db.Config{
		Host:     envOr("SQLGO_IT_MYSQL_HOST", "127.0.0.1"),
		Port:     13306,
		User:     envOr("SQLGO_IT_MYSQL_USER", "sqlgo"),
		Password: envOr("SQLGO_IT_MYSQL_PASSWORD", "sqlgo_dev"),
		Database: envOr("SQLGO_IT_MYSQL_DB", "sqlgo_test"),
	}
	d, err := db.Get("mysql")
	if err != nil {
		t.Fatalf("db.Get mysql: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn, err := d.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("open mysql (is podman compose up?): %v", err)
	}
	defer conn.Close()

	// MySQL reports the database as the "schema" in
	// information_schema.columns, so the schema arg to
	// Columns() is the DB name.
	dbtest.ExerciseDriver(t, conn, cfg.Database,
		`CREATE TABLE sqlgo_it_mysql (id INT, label VARCHAR(50))`,
		"sqlgo_it_mysql",
	)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
