//go:build integration

package sqlite

import (
	"context"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/db/dbtest"
)

func TestIntegrationSQLiteErrors(t *testing.T) {
	d, err := db.Get(driverName)
	if err != nil {
		t.Fatalf("db.Get sqlite: %v", err)
	}
	conn, err := d.Open(context.Background(), db.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
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
			WantMessageContains: []string{"no such table: widgetsz"},
		},
	})
}
