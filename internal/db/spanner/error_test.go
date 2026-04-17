//go:build integration

package spanner

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/db/dbtest"
)

func TestIntegrationSpannerErrors(t *testing.T) {
	port, _ := strconv.Atoi(envOr("SQLGO_IT_SPANNER_PORT", "19010"))
	cfg := db.Config{
		Host:     envOr("SQLGO_IT_SPANNER_HOST", "127.0.0.1"),
		Port:     port,
		Database: envOr("SQLGO_IT_SPANNER_DATABASE", "sqlgo_test"),
		Options: map[string]string{
			"project":            envOr("SQLGO_IT_SPANNER_PROJECT", "sqlgo-emu"),
			"instance":           envOr("SQLGO_IT_SPANNER_INSTANCE", "sqlgo"),
			"autoConfigEmulator": "true",
		},
	}
	d, err := db.Get(driverName)
	if err != nil {
		t.Fatalf("db.Get spanner: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	conn, err := d.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("open spanner (is podman compose up?): %v", err)
	}
	defer conn.Close()

	dbtest.ExerciseErrorParsing(t, conn, driverName, []dbtest.ErrorCase{
		{
			Name:                "syntax_line1",
			SQL:                 "SELECT * FORM foo",
			WantEngine:          driverName,
			CheckLine:           true,
			WantLine:            1,
			CheckColumn:         true,
			WantColumn:          10,
			WantType:            "InvalidArgument",
			WantMessageContains: []string{"Expected end of input but got identifier \"FORM\""},
		},
		{
			Name:                "syntax_line2",
			SQL:                 "SELECT *\nFORM foo",
			WantEngine:          driverName,
			CheckLine:           true,
			WantLine:            2,
			CheckColumn:         true,
			WantColumn:          1,
			WantType:            "InvalidArgument",
			WantMessageContains: []string{"Expected end of input but got identifier \"FORM\""},
		},
	})
}
