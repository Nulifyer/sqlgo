//go:build integration

package azuresql

import (
	"context"
	"os"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/db/dbtest"
)

// TestIntegrationAzureSQL exercises the full driver round trip against
// a live Azure SQL Database. Azure SQL is cloud-only (no self-hostable
// image), so the test skips unless SQLGO_IT_AZURESQL_HOST is set.
//
// Supported fedauth modes are selected via SQLGO_IT_AZURESQL_FEDAUTH:
//
//	""                                    -> SQL auth (contained user)
//	"ActiveDirectoryPassword"             -> AAD user/password
//	"ActiveDirectoryServicePrincipal"     -> SP with secret or cert
//	"ActiveDirectoryManagedIdentity"      -> MI (user-assigned if USER set)
//	"ActiveDirectoryInteractive"          -> browser prompt (not useful in CI)
//	"ActiveDirectoryDefault"              -> DefaultAzureCredential chain
//
// Per-mode env vars follow the semantic-field mapping in buildDSN:
//
//	SQLGO_IT_AZURESQL_USER         -> cfg.User
//	SQLGO_IT_AZURESQL_PASSWORD     -> cfg.Password
//	SQLGO_IT_AZURESQL_TENANT_ID    -> Options["tenant_id"]
//	SQLGO_IT_AZURESQL_CERT_PATH    -> Options["cert_path"]
//	SQLGO_IT_AZURESQL_CERT_PASSWORD-> Options["cert_password"]
//	SQLGO_IT_AZURESQL_DB           -> cfg.Database (defaults to "master")
func TestIntegrationAzureSQL(t *testing.T) {
	host := os.Getenv("SQLGO_IT_AZURESQL_HOST")
	if host == "" {
		t.Skip("SQLGO_IT_AZURESQL_HOST unset; Azure SQL is cloud-only, no local emulator available")
	}

	opts := map[string]string{}
	if v := os.Getenv("SQLGO_IT_AZURESQL_FEDAUTH"); v != "" {
		opts["fedauth"] = v
	}
	if v := os.Getenv("SQLGO_IT_AZURESQL_TENANT_ID"); v != "" {
		opts["tenant_id"] = v
	}
	if v := os.Getenv("SQLGO_IT_AZURESQL_CERT_PATH"); v != "" {
		opts["cert_path"] = v
	}
	if v := os.Getenv("SQLGO_IT_AZURESQL_CERT_PASSWORD"); v != "" {
		opts["cert_password"] = v
	}

	cfg := db.Config{
		Host:     host,
		Port:     1433,
		User:     os.Getenv("SQLGO_IT_AZURESQL_USER"),
		Password: os.Getenv("SQLGO_IT_AZURESQL_PASSWORD"),
		Database: envOr("SQLGO_IT_AZURESQL_DB", "master"),
		Options:  opts,
	}

	d, err := db.Get("azuresql")
	if err != nil {
		t.Fatalf("db.Get azuresql: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn, err := d.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("open azuresql (fedauth=%q): %v", opts["fedauth"], err)
	}
	defer conn.Close()

	// Reuse the mssql schema ("dbo") -- Azure SQL is MSSQL on the wire.
	dbtest.ExerciseDriver(t, conn, "dbo",
		`CREATE TABLE sqlgo_it_azuresql (id INT, label NVARCHAR(50))`,
		"sqlgo_it_azuresql",
	)

	t.Run("view_definition", func(t *testing.T) {
		dbtest.ExerciseDefinition(t, conn, "view",
			`CREATE VIEW dbo.sqlgo_it_azuresql_view AS SELECT 42 AS sqlgo_marker`,
			`DROP VIEW dbo.sqlgo_it_azuresql_view`,
			"dbo", "sqlgo_it_azuresql_view", "sqlgo_marker",
		)
	})
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
