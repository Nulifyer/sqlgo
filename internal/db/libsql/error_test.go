//go:build integration

package libsql

import (
	"context"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/db/dbtest"
)

func TestIntegrationLibSQLErrors(t *testing.T) {
	cfg := db.Config{
		Host:     envOr("SQLGO_IT_LIBSQL_URL", "http://127.0.0.1:18080"),
		Password: envOr("SQLGO_IT_LIBSQL_TOKEN", ""),
	}
	d, err := db.Get(driverName)
	if err != nil {
		t.Fatalf("db.Get libsql: %v", err)
	}
	conn, err := d.Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("open libsql (is podman compose up?): %v", err)
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
			WantColumn:          6,
			WantName:            "SQL_PARSE_ERROR",
			WantMessageContains: []string{"syntax error around L1:6"},
		},
		{
			Name:                "syntax_line2",
			SQL:                 "SELECT *\nFORM foo",
			WantEngine:          driverName,
			CheckLine:           true,
			WantLine:            2,
			CheckColumn:         true,
			WantColumn:          5,
			WantName:            "SQL_PARSE_ERROR",
			WantMessageContains: []string{"syntax error around L2:5"},
		},
	})
}
