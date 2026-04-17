//go:build integration

package sybase

import (
	"context"
	"strconv"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/db/dbtest"
	"github.com/Nulifyer/sqlgo/internal/db/errinfo"
)

func TestIntegrationSybaseErrors(t *testing.T) {
	port, _ := strconv.Atoi(envOr("SQLGO_IT_SYBASE_PORT", "15000"))
	cfg := db.Config{
		Host:     envOr("SQLGO_IT_SYBASE_HOST", "127.0.0.1"),
		Port:     port,
		User:     envOr("SQLGO_IT_SYBASE_USER", "tester"),
		Password: envOr("SQLGO_IT_SYBASE_PASSWORD", "guest1234"),
		Database: envOr("SQLGO_IT_SYBASE_DB", "testdb"),
	}
	d, err := db.Get(driverName)
	if err != nil {
		t.Fatalf("db.Get sybase: %v", err)
	}
	conn, err := d.Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("open sybase (is podman compose up?): %v", err)
	}
	defer conn.Close()

	dbtest.ExerciseErrorParsing(t, conn, driverName, []dbtest.ErrorCase{
		{
			Name:                "missing_proc",
			SQL:                 "SELEC 1",
			WantEngine:          driverName,
			CheckLine:           true,
			WantLine:            1,
			CheckNumber:         true,
			WantNumber:          2812,
			CheckState:          true,
			WantState:           5,
			CheckClass:          true,
			WantClass:           16,
			WantSQLState:        "ZZZZZ",
			WantMessageContains: []string{"Stored procedure 'SELEC' not found"},
		},
		{
			Name:                "syntax_line2",
			SQL:                 "SELECT *\nFRM sysobjects",
			WantEngine:          driverName,
			CheckLine:           true,
			WantLine:            2,
			WantMessageContains: []string{"Incorrect syntax near"},
			Check: func(t *testing.T, info errinfo.Info) {
				if info.Number == 0 {
					t.Fatalf("expected non-zero syntax number: %+v", info)
				}
			},
		},
		{
			Name:                "missing_table",
			SQL:                 "SELECT * FROM tester.widgetsz",
			WantEngine:          driverName,
			CheckLine:           true,
			WantLine:            1,
			WantMessageContains: []string{"widgetsz"},
			Check: func(t *testing.T, info errinfo.Info) {
				if info.Number == 0 {
					t.Fatalf("expected non-zero missing-table number: %+v", info)
				}
			},
		},
	})
}
