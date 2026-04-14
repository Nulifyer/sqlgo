//go:build integration

package oracle

import (
	"context"
	"os"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/db/dbtest"
)

// TestIntegrationOracle runs the shared round-trip against the
// compose oracle service on port 11521. gvenzl/oracle-free boots
// with service name FREEPDB1 and creates APP_USER sqlgo.
//
// Oracle folds unquoted identifiers to UPPERCASE, so both schema
// (owner) and table name are uppercase to match dictionary views.
func TestIntegrationOracle(t *testing.T) {
	cfg := db.Config{
		Host:     envOr("SQLGO_IT_ORA_HOST", "127.0.0.1"),
		Port:     11521,
		User:     envOr("SQLGO_IT_ORA_USER", "sqlgo"),
		Password: envOr("SQLGO_IT_ORA_PASSWORD", "sqlgo_dev"),
		Database: envOr("SQLGO_IT_ORA_DB", "FREEPDB1"),
	}
	d, err := db.Get("oracle")
	if err != nil {
		t.Fatalf("db.Get oracle: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn, err := d.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("open oracle (is podman compose up?): %v", err)
	}
	defer conn.Close()

	// Default schema = owner = uppercase user name.
	dbtest.ExerciseDriver(t, conn, "SQLGO",
		`CREATE TABLE SQLGO_IT_ORA (id NUMBER(10), label VARCHAR2(50))`,
		"SQLGO_IT_ORA",
	)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
