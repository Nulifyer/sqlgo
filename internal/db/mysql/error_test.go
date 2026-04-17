//go:build integration

package mysql

import (
	"context"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/db/dbtest"
)

func TestIntegrationMySQLErrors(t *testing.T) {
	cfg := db.Config{
		Host:     envOr("SQLGO_IT_MYSQL_HOST", "127.0.0.1"),
		Port:     13306,
		User:     envOr("SQLGO_IT_MYSQL_USER", "sqlgo"),
		Password: envOr("SQLGO_IT_MYSQL_PASSWORD", "sqlgo_dev"),
		Database: envOr("SQLGO_IT_MYSQL_DB", "sqlgo_test"),
	}
	d, err := db.Get(driverName)
	if err != nil {
		t.Fatalf("db.Get mysql: %v", err)
	}
	conn, err := d.Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("open mysql (is podman compose up?): %v", err)
	}
	defer conn.Close()

	dbtest.ExerciseErrorParsing(t, conn, driverName, []dbtest.ErrorCase{
		{
			Name:                "syntax_line1",
			SQL:                 "SELEC 1",
			WantEngine:          driverName,
			CheckLine:           true,
			WantLine:            1,
			CheckNumber:         true,
			WantNumber:          1064,
			WantSQLState:        "42000",
			WantMessageContains: []string{"error in your SQL syntax"},
		},
		{
			Name:                "syntax_line2",
			SQL:                 "SELECT *\nFORM widgetsz\nLIMIT 100",
			WantEngine:          driverName,
			CheckLine:           true,
			WantLine:            2,
			CheckNumber:         true,
			WantNumber:          1064,
			WantSQLState:        "42000",
			WantMessageContains: []string{"near 'FORM widgetsz"},
		},
		{
			Name:                "missing_table",
			SQL:                 "SELECT * FROM widgetsz",
			WantEngine:          driverName,
			CheckNumber:         true,
			WantNumber:          1146,
			WantSQLState:        "42S02",
			WantMessageContains: []string{"doesn't exist"},
		},
	})
}
