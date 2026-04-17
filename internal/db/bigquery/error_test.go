//go:build integration

package bigquery

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/db/dbtest"
)

func TestIntegrationBigQueryErrors(t *testing.T) {
	port, _ := strconv.Atoi(envOr("SQLGO_IT_BIGQUERY_PORT", "19050"))
	cfg := db.Config{
		Host:     envOr("SQLGO_IT_BIGQUERY_HOST", "127.0.0.1"),
		Port:     port,
		Database: envOr("SQLGO_IT_BIGQUERY_DATABASE", "sqlgo_test"),
		Options: map[string]string{
			"project": envOr("SQLGO_IT_BIGQUERY_PROJECT", "sqlgo-emu"),
		},
	}
	d, err := db.Get(driverName)
	if err != nil {
		t.Fatalf("db.Get bigquery: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	conn, err := d.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("open bigquery (is podman compose up?): %v", err)
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
			WantNumber:          400,
			WantReason:          "jobInternalError",
			WantMessageContains: []string{`Unexpected identifier "SELEC"`},
		},
		{
			Name:                "syntax_line2",
			SQL:                 "SELECT *\nFORM foo",
			WantEngine:          driverName,
			CheckLine:           true,
			WantLine:            2,
			CheckColumn:         true,
			WantColumn:          1,
			CheckNumber:         true,
			WantNumber:          400,
			WantReason:          "jobInternalError",
			WantMessageContains: []string{`Expected end of input but got identifier "FORM"`},
		},
	})
}
