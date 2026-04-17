//go:build integration

package trino

import (
	"context"
	"testing"
	"time"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/db/dbtest"
)

func TestIntegrationTrinoErrors(t *testing.T) {
	cfg := db.Config{
		Host:     envOr("SQLGO_IT_TRINO_HOST", "127.0.0.1"),
		Port:     18081,
		User:     envOr("SQLGO_IT_TRINO_USER", "sqlgo"),
		Database: envOr("SQLGO_IT_TRINO_CATALOG", "memory"),
		Options: map[string]string{
			"schema": envOr("SQLGO_IT_TRINO_SCHEMA", "default"),
			"source": "sqlgo-error-integration",
		},
	}
	d, err := db.Get(driverName)
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

	if err := conn.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS memory.default"); err != nil {
		t.Fatalf("create schema memory.default: %v", err)
	}

	dbtest.ExerciseErrorParsing(t, conn, driverName, []dbtest.ErrorCase{
		{
			Name:                "syntax_line1",
			SQL:                 "SELEC 1",
			WantEngine:          driverName,
			CheckLine:           true,
			WantLine:            1,
			CheckColumn:         true,
			WantColumn:          1,
			CheckNumber:         true,
			WantNumber:          1,
			WantName:            "SYNTAX_ERROR",
			WantType:            "USER_ERROR",
			WantMessageContains: []string{"mismatched input 'SELEC'"},
		},
		{
			Name:                "syntax_line2",
			SQL:                 "SELECT *\nFORM foo",
			WantEngine:          driverName,
			CheckLine:           true,
			WantLine:            2,
			CheckColumn:         true,
			WantColumn:          1,
			CheckNumber:         true,
			WantNumber:          1,
			WantName:            "SYNTAX_ERROR",
			WantType:            "USER_ERROR",
			WantMessageContains: []string{"mismatched input 'FORM'"},
		},
		{
			Name:                "function_resolution",
			SQL:                 "SELECT lower(1)",
			WantEngine:          driverName,
			CheckLine:           true,
			WantLine:            1,
			CheckColumn:         true,
			WantColumn:          8,
			WantType:            "USER_ERROR",
			WantMessageContains: []string{"Unexpected parameters (integer) for function lower"},
		},
	})
}
