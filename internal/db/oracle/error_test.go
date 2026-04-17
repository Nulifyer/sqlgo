//go:build integration

package oracle

import (
	"context"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/db/dbtest"
	"github.com/Nulifyer/sqlgo/internal/db/errinfo"
)

func TestIntegrationOracleErrors(t *testing.T) {
	cfg := db.Config{
		Host:     envOr("SQLGO_IT_ORA_HOST", "127.0.0.1"),
		Port:     11521,
		User:     envOr("SQLGO_IT_ORA_USER", "sqlgo"),
		Password: envOr("SQLGO_IT_ORA_PASSWORD", "sqlgo_dev"),
		Database: envOr("SQLGO_IT_ORA_DB", "FREEPDB1"),
	}
	d, err := db.Get(driverName)
	if err != nil {
		t.Fatalf("db.Get oracle: %v", err)
	}
	conn, err := d.Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("open oracle (is podman compose up?): %v", err)
	}
	defer conn.Close()

	dbtest.ExerciseErrorParsing(t, conn, driverName, []dbtest.ErrorCase{
		{
			Name:                "invalid_sql",
			SQL:                 "SELEC 1",
			WantEngine:          driverName,
			CheckLine:           true,
			WantLine:            1,
			CheckColumn:         true,
			WantColumn:          1,
			CheckNumber:         true,
			WantNumber:          900,
			WantMessageContains: []string{"ORA-00900"},
		},
		{
			Name:                "syntax_position",
			SQL:                 "SELECT * FORM dual",
			WantEngine:          driverName,
			WantMessageContains: []string{"ORA-00923"},
			Check: func(t *testing.T, info errinfo.Info) {
				if info.Number == 0 || info.Location.Line == 0 {
					t.Fatalf("expected sql code + location: %+v", info)
				}
			},
		},
		{
			Name:                "missing_table",
			SQL:                 "SELECT * FROM WIDGETSZ",
			WantEngine:          driverName,
			CheckNumber:         true,
			WantNumber:          942,
			WantMessageContains: []string{"ORA-00942"},
		},
	})
}
