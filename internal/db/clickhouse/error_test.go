//go:build integration

package clickhouse

import (
	"context"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/db/dbtest"
	"github.com/Nulifyer/sqlgo/internal/db/errinfo"
)

func TestIntegrationClickHouseErrors(t *testing.T) {
	cfg := db.Config{
		Host:     envOr("SQLGO_IT_CLICKHOUSE_HOST", "127.0.0.1"),
		Port:     19000,
		User:     envOr("SQLGO_IT_CLICKHOUSE_USER", "default"),
		Password: envOr("SQLGO_IT_CLICKHOUSE_PASSWORD", ""),
		Database: envOr("SQLGO_IT_CLICKHOUSE_DB", "default"),
	}
	d, err := db.Get(driverName)
	if err != nil {
		t.Fatalf("db.Get clickhouse: %v", err)
	}
	conn, err := d.Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("open clickhouse (is podman compose up?): %v", err)
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
			WantNumber:          62,
			WantName:            "DB::Exception",
			WantMessageContains: []string{"Syntax error: failed at position 1 ('SELEC')"},
		},
		{
			Name:                "syntax_line2",
			SQL:                 "SELECT *\nFORM system.tables",
			WantEngine:          driverName,
			CheckLine:           true,
			WantLine:            2,
			CheckColumn:         true,
			WantColumn:          6,
			CheckNumber:         true,
			WantNumber:          62,
			WantName:            "DB::Exception",
			WantMessageContains: []string{"Syntax error"},
		},
		{
			Name:                "missing_table",
			SQL:                 "SELECT * FROM widgetsz",
			WantEngine:          driverName,
			WantName:            "DB::Exception",
			WantMessageContains: []string{"widgetsz"},
			Check: func(t *testing.T, info errinfo.Info) {
				if info.Number == 0 {
					t.Fatalf("expected non-zero missing-table code: %+v", info)
				}
			},
		},
	})
}
