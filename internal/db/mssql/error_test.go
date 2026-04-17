//go:build integration

package mssql

import (
	"context"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/db/dbtest"
	"github.com/Nulifyer/sqlgo/internal/db/errinfo"
)

func TestIntegrationMSSQLErrors(t *testing.T) {
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
	d, err := db.Get(driverName)
	if err != nil {
		t.Fatalf("db.Get mssql: %v", err)
	}
	conn, err := d.Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("open mssql (is podman compose up?): %v", err)
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
			WantState:           62,
			CheckClass:          true,
			WantClass:           16,
			WantMessageContains: []string{"Could not find stored procedure 'SELEC'"},
		},
		{
			Name:                "syntax_line2",
			SQL:                 "SELECT *\nFRM sys.objects",
			WantEngine:          driverName,
			CheckLine:           true,
			WantLine:            2,
			WantMessageContains: []string{"Incorrect syntax near"},
			Check: func(t *testing.T, info errinfo.Info) {
				if info.Number == 0 {
					t.Fatalf("expected non-zero syntax error number: %+v", info)
				}
			},
		},
		{
			Name:                "missing_table",
			SQL:                 "SELECT * FROM dbo.widgetsz",
			WantEngine:          driverName,
			CheckLine:           true,
			WantLine:            1,
			CheckNumber:         true,
			WantNumber:          208,
			WantMessageContains: []string{"Invalid object name 'dbo.widgetsz'"},
		},
	})
}
