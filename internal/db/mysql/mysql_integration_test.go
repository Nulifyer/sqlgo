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

	t.Run("seeded_catalogs", func(t *testing.T) {
		dbtest.ExerciseCatalogs(t, conn, map[string]string{
			"sqlgo_a": "widgets",
			"sqlgo_b": "gadgets",
		})
	})

	t.Run("view_definition", func(t *testing.T) {
		dbtest.ExerciseDefinition(t, conn, "view",
			`CREATE VIEW sqlgo_it_mysql_view AS SELECT 42 AS sqlgo_marker`,
			`DROP VIEW sqlgo_it_mysql_view`,
			cfg.Database, "sqlgo_it_mysql_view", "sqlgo_marker",
		)
	})
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
