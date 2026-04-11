//go:build integration

package mssql

import (
	"context"
	"os"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/db/dbtest"
)

// TestIntegrationMSSQL exercises the full driver round trip
// against a live MSSQL. Defaults match the compose.yaml mssql
// service (port 11433, sa / SqlGo_dev_Pass1!).
func TestIntegrationMSSQL(t *testing.T) {
	cfg := db.Config{
		Host:     envOr("SQLGO_IT_MSSQL_HOST", "127.0.0.1"),
		Port:     11433,
		User:     envOr("SQLGO_IT_MSSQL_USER", "sa"),
		Password: envOr("SQLGO_IT_MSSQL_PASSWORD", "SqlGo_dev_Pass1!"),
		Database: envOr("SQLGO_IT_MSSQL_DB", "master"),
		Options: map[string]string{
			"encrypt":                "disable",
			"TrustServerCertificate": "true",
		},
	}
	d, err := db.Get("mssql")
	if err != nil {
		t.Fatalf("db.Get mssql: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn, err := d.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("open mssql (is podman compose up?): %v", err)
	}
	defer conn.Close()

	// "dbo" is the default schema in master.
	dbtest.ExerciseDriver(t, conn, "dbo",
		`CREATE TABLE sqlgo_it_mssql (id INT, label NVARCHAR(50))`,
		"sqlgo_it_mssql",
	)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
