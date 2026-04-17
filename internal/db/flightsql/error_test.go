//go:build integration

package flightsql

import (
	"context"
	"strconv"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/db/dbtest"
)

func TestIntegrationFlightSQLErrors(t *testing.T) {
	port, _ := strconv.Atoi(envOr("SQLGO_IT_FLIGHTSQL_PORT", "19070"))
	cfg := db.Config{
		Host:     envOr("SQLGO_IT_FLIGHTSQL_HOST", "127.0.0.1"),
		Port:     port,
		User:     envOr("SQLGO_IT_FLIGHTSQL_USER", "sqlflite_username"),
		Password: envOr("SQLGO_IT_FLIGHTSQL_PASSWORD", "sqlgo_dev"),
	}
	d, err := db.Get(driverName)
	if err != nil {
		t.Fatalf("db.Get flightsql: %v", err)
	}
	conn, err := d.Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("open flightsql (is podman compose up?): %v", err)
	}
	defer conn.Close()

	dbtest.ExerciseErrorParsing(t, conn, driverName, []dbtest.ErrorCase{
		{
			Name:                "syntax_line1_plain",
			SQL:                 "SELEC 1",
			WantMessageContains: []string{`near "SELEC": syntax error`},
		},
		{
			Name:                "syntax_line2_plain",
			SQL:                 "SELECT *\nFORM foo",
			WantMessageContains: []string{`near "FORM": syntax error`},
		},
		{
			Name:                "missing_table_plain",
			SQL:                 "SELECT * FROM widgetsz",
			WantMessageContains: []string{"widgetsz"},
		},
	})
}
