//go:build integration

package postgres

import (
	"context"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/db/dbtest"
)

func TestIntegrationPostgresErrors(t *testing.T) {
	cfg := db.Config{
		Host:     envOr("SQLGO_IT_PG_HOST", "127.0.0.1"),
		Port:     15432,
		User:     envOr("SQLGO_IT_PG_USER", "sqlgo"),
		Password: envOr("SQLGO_IT_PG_PASSWORD", "sqlgo_dev"),
		Database: envOr("SQLGO_IT_PG_DB", "sqlgo_test"),
		Options:  map[string]string{"sslmode": "disable"},
	}
	d, err := db.Get(driverName)
	if err != nil {
		t.Fatalf("db.Get postgres: %v", err)
	}
	conn, err := d.Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("open postgres (is podman compose up?): %v", err)
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
			WantSQLState:        "42601",
			WantMessageContains: []string{`syntax error at or near "SELEC"`},
		},
		{
			Name:                "missing_relation",
			SQL:                 "SELECT * FROM\n\"public\".\"widgetsz\"\nLIMIT 100",
			WantEngine:          driverName,
			CheckLine:           true,
			WantLine:            2,
			CheckColumn:         true,
			WantColumn:          1,
			WantSQLState:        "42P01",
			WantMessageContains: []string{`relation "public.widgetsz" does not exist`},
		},
		{
			Name:                "hinted_function",
			SQL:                 "SELECT lower(1)",
			WantEngine:          driverName,
			CheckLine:           true,
			WantLine:            1,
			CheckColumn:         true,
			WantColumn:          8,
			WantSQLState:        "42883",
			WantMessageContains: []string{`function lower(integer) does not exist`},
			WantFormatContains:  []string{"hint: No function matches the given name and argument types."},
		},
	})
}
