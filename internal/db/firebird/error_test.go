//go:build integration

package firebird

import (
	"context"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/db/dbtest"
)

func TestIntegrationFirebirdErrors(t *testing.T) {
	cfg := db.Config{
		Host:     envOr("SQLGO_IT_FB_HOST", "127.0.0.1"),
		Port:     13050,
		User:     envOr("SQLGO_IT_FB_USER", "sqlgo"),
		Password: envOr("SQLGO_IT_FB_PASSWORD", "sqlgo_dev"),
		Database: envOr("SQLGO_IT_FB_DB", "/var/lib/firebird/data/sqlgo_test.fdb"),
	}
	d, err := db.Get(driverName)
	if err != nil {
		t.Fatalf("db.Get firebird: %v", err)
	}
	conn, err := d.Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("open firebird (is podman compose up?): %v", err)
	}
	defer conn.Close()

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
			WantNumber:          -104,
			CheckCodes:          true,
			WantCodesLen:        4,
			WantMessageContains: []string{"Token unknown - line 1, column 1"},
			WantFormatContains:  []string{"sql code: -104", "gds codes:"},
		},
		{
			Name:                "syntax_line2",
			SQL:                 "SELECT *\nFORM RDB$DATABASE",
			WantEngine:          driverName,
			CheckLine:           true,
			WantLine:            2,
			CheckColumn:         true,
			WantColumn:          1,
			CheckNumber:         true,
			WantNumber:          -104,
			WantMessageContains: []string{"Token unknown - line 2, column 1"},
		},
		{
			Name:                "missing_table",
			SQL:                 "SELECT * FROM WIDGETSZ",
			WantEngine:          driverName,
			CheckNumber:         true,
			WantNumber:          -204,
			WantMessageContains: []string{"Table unknown", "WIDGETSZ"},
		},
	})
}
