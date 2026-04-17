//go:build integration

package errinfo_test

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/db"
	_ "github.com/Nulifyer/sqlgo/internal/db/bigquery"
	_ "github.com/Nulifyer/sqlgo/internal/db/clickhouse"
	_ "github.com/Nulifyer/sqlgo/internal/db/firebird"
	_ "github.com/Nulifyer/sqlgo/internal/db/flightsql"
	_ "github.com/Nulifyer/sqlgo/internal/db/libsql"
	_ "github.com/Nulifyer/sqlgo/internal/db/oracle"
	_ "github.com/Nulifyer/sqlgo/internal/db/spanner"
	_ "github.com/Nulifyer/sqlgo/internal/db/sybase"
	_ "github.com/Nulifyer/sqlgo/internal/db/trino"
)

func TestIntegrationProbeRunningDrivers(t *testing.T) {
	t.Parallel()

	chPort, _ := strconv.Atoi(envOr("SQLGO_IT_CLICKHOUSE_PORT", "19000"))
	trinoPort, _ := strconv.Atoi(envOr("SQLGO_IT_TRINO_PORT", "18081"))
	spannerPort, _ := strconv.Atoi(envOr("SQLGO_IT_SPANNER_PORT", "19010"))
	bigQueryPort, _ := strconv.Atoi(envOr("SQLGO_IT_BIGQUERY_PORT", "19050"))
	flightSQLPort, _ := strconv.Atoi(envOr("SQLGO_IT_FLIGHTSQL_PORT", "19070"))
	sybasePort, _ := strconv.Atoi(envOr("SQLGO_IT_SYBASE_PORT", "15000"))

	cases := []struct {
		name         string
		cfg          db.Config
		sql          string
		wantEngine   string
		wantLine     bool
		wantColumn   bool
		wantNumber   bool
		wantSQLState bool
		wantName     bool
		wantType     bool
		wantReason   bool
	}{
		{
			name:         "postgres_syntax",
			wantEngine:   "postgres",
			wantLine:     true,
			wantColumn:   true,
			wantSQLState: true,
			cfg: db.Config{
				Host:     envOr("SQLGO_IT_PG_HOST", "127.0.0.1"),
				Port:     15432,
				User:     envOr("SQLGO_IT_PG_USER", "sqlgo"),
				Password: envOr("SQLGO_IT_PG_PASSWORD", "sqlgo_dev"),
				Database: envOr("SQLGO_IT_PG_DB", "sqlgo_test"),
				Options:  map[string]string{"sslmode": "disable"},
			},
			sql: "SELEC 1",
		},
		{
			name:         "postgres_hint",
			wantEngine:   "postgres",
			wantLine:     true,
			wantColumn:   true,
			wantSQLState: true,
			cfg: db.Config{
				Host:     envOr("SQLGO_IT_PG_HOST", "127.0.0.1"),
				Port:     15432,
				User:     envOr("SQLGO_IT_PG_USER", "sqlgo"),
				Password: envOr("SQLGO_IT_PG_PASSWORD", "sqlgo_dev"),
				Database: envOr("SQLGO_IT_PG_DB", "sqlgo_test"),
				Options:  map[string]string{"sslmode": "disable"},
			},
			sql: "SELECT lower(1)",
		},
		{
			name:         "mysql",
			wantEngine:   "mysql",
			wantLine:     true,
			wantNumber:   true,
			wantSQLState: true,
			cfg: db.Config{
				Host:     envOr("SQLGO_IT_MYSQL_HOST", "127.0.0.1"),
				Port:     13306,
				User:     envOr("SQLGO_IT_MYSQL_USER", "sqlgo"),
				Password: envOr("SQLGO_IT_MYSQL_PASSWORD", "sqlgo_dev"),
				Database: envOr("SQLGO_IT_MYSQL_DB", "sqlgo_test"),
			},
			sql: "SELEC 1",
		},
		{
			name:       "mssql",
			wantEngine: "mssql",
			wantLine:   true,
			wantNumber: true,
			cfg: db.Config{
				Host:     envOr("SQLGO_IT_MSSQL_HOST", "127.0.0.1"),
				Port:     11433,
				User:     envOr("SQLGO_IT_MSSQL_USER", "sa"),
				Password: envOr("SQLGO_IT_MSSQL_PASSWORD", "SqlGo_dev_Pass1!"),
				Database: envOr("SQLGO_IT_MSSQL_DB", "master"),
				Options: map[string]string{
					"encrypt":                "disable",
					"TrustServerCertificate": "true",
				},
			},
			sql: "SELEC 1",
		},
		{
			name: "sqlite",
			cfg:  db.Config{},
			sql:  "SELEC 1",
		},
		{
			name:       "libsql",
			wantEngine: "libsql",
			wantLine:   true,
			wantColumn: true,
			wantName:   true,
			cfg: db.Config{
				Host:     envOr("SQLGO_IT_LIBSQL_URL", "http://127.0.0.1:18080"),
				Password: os.Getenv("SQLGO_IT_LIBSQL_TOKEN"),
			},
			sql: "SELEC 1",
		},
		{
			name:       "firebird",
			wantEngine: "firebird",
			wantLine:   true,
			wantColumn: true,
			wantNumber: true,
			cfg: db.Config{
				Host:     envOr("SQLGO_IT_FB_HOST", "127.0.0.1"),
				Port:     13050,
				User:     envOr("SQLGO_IT_FB_USER", "sqlgo"),
				Password: envOr("SQLGO_IT_FB_PASSWORD", "sqlgo_dev"),
				Database: envOr("SQLGO_IT_FB_DB", "/var/lib/firebird/data/sqlgo_test.fdb"),
			},
			sql: "SELEC 1",
		},
		{
			name:         "sybase",
			wantEngine:   "sybase",
			wantLine:     true,
			wantNumber:   true,
			wantSQLState: true,
			cfg: db.Config{
				Host:     envOr("SQLGO_IT_SYBASE_HOST", "127.0.0.1"),
				Port:     sybasePort,
				User:     envOr("SQLGO_IT_SYBASE_USER", "tester"),
				Password: envOr("SQLGO_IT_SYBASE_PASSWORD", "guest1234"),
				Database: envOr("SQLGO_IT_SYBASE_DB", "testdb"),
			},
			sql: "SELEC 1",
		},
		{
			name:       "oracle",
			wantEngine: "oracle",
			wantLine:   true,
			wantColumn: true,
			wantNumber: true,
			cfg: db.Config{
				Host:     envOr("SQLGO_IT_ORA_HOST", "127.0.0.1"),
				Port:     11521,
				User:     envOr("SQLGO_IT_ORA_USER", "sqlgo"),
				Password: envOr("SQLGO_IT_ORA_PASSWORD", "sqlgo_dev"),
				Database: envOr("SQLGO_IT_ORA_DB", "FREEPDB1"),
			},
			sql: "SELEC 1",
		},
		{
			name:       "clickhouse",
			wantEngine: "clickhouse",
			wantLine:   true,
			wantColumn: true,
			wantNumber: true,
			wantName:   true,
			cfg: db.Config{
				Host:     envOr("SQLGO_IT_CLICKHOUSE_HOST", "127.0.0.1"),
				Port:     chPort,
				User:     envOr("SQLGO_IT_CLICKHOUSE_USER", "default"),
				Password: os.Getenv("SQLGO_IT_CLICKHOUSE_PASSWORD"),
				Database: envOr("SQLGO_IT_CLICKHOUSE_DB", "default"),
			},
			sql: "SELEC 1",
		},
		{
			name:       "trino",
			wantEngine: "trino",
			wantLine:   true,
			wantColumn: true,
			wantNumber: true,
			wantName:   true,
			wantType:   true,
			cfg: db.Config{
				Host:     envOr("SQLGO_IT_TRINO_HOST", "127.0.0.1"),
				Port:     trinoPort,
				User:     envOr("SQLGO_IT_TRINO_USER", "sqlgo"),
				Database: envOr("SQLGO_IT_TRINO_CATALOG", "memory"),
				Options: map[string]string{
					"schema": envOr("SQLGO_IT_TRINO_SCHEMA", "default"),
					"source": "sqlgo-errinfo-matrix",
				},
			},
			sql: "SELEC 1",
		},
		{
			name:       "spanner",
			wantEngine: "spanner",
			wantLine:   true,
			wantColumn: true,
			wantType:   true,
			cfg: db.Config{
				Host:     envOr("SQLGO_IT_SPANNER_HOST", "127.0.0.1"),
				Port:     spannerPort,
				Database: envOr("SQLGO_IT_SPANNER_DATABASE", "sqlgo_test"),
				Options: map[string]string{
					"project":            envOr("SQLGO_IT_SPANNER_PROJECT", "sqlgo-emu"),
					"instance":           envOr("SQLGO_IT_SPANNER_INSTANCE", "sqlgo"),
					"autoConfigEmulator": "true",
				},
			},
			sql: "SELECT * FORM foo",
		},
		{
			name:       "bigquery",
			wantEngine: "bigquery",
			wantNumber: true,
			wantReason: true,
			cfg: db.Config{
				Host:     envOr("SQLGO_IT_BIGQUERY_HOST", "127.0.0.1"),
				Port:     bigQueryPort,
				Database: envOr("SQLGO_IT_BIGQUERY_DATABASE", "sqlgo_test"),
				Options: map[string]string{
					"project": envOr("SQLGO_IT_BIGQUERY_PROJECT", "sqlgo-emu"),
				},
			},
			sql: "SELEC 1",
		},
		{
			name: "flightsql",
			cfg: db.Config{
				Host:     envOr("SQLGO_IT_FLIGHTSQL_HOST", "127.0.0.1"),
				Port:     flightSQLPort,
				User:     envOr("SQLGO_IT_FLIGHTSQL_USER", "sqlflite_username"),
				Password: envOr("SQLGO_IT_FLIGHTSQL_PASSWORD", "sqlgo_dev"),
			},
			sql: "SELEC 1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn := openIntegrationConn(t, driverNameForProbe(tc.name), tc.cfg)
			defer conn.Close()

			err := queryErr(t, conn, tc.sql)
			info := db.ParseErrorInfo(driverNameForProbe(tc.name), err, tc.sql)
			t.Logf("sql: %q", tc.sql)
			t.Logf("error chain:\n%s", formatErrorChain(err))
			t.Logf("display:\n%s", info.Format())
			t.Logf("info: %+v", info)

			if info.Message == "" {
				t.Fatal("expected non-empty extracted message")
			}
			if tc.wantEngine != "" && info.Engine != tc.wantEngine {
				t.Fatalf("Engine = %q, want %q", info.Engine, tc.wantEngine)
			}
			if tc.wantLine && info.Location.Line <= 0 {
				t.Fatalf("Location = %+v, want a line", info.Location)
			}
			if tc.wantColumn && info.Location.Column <= 0 {
				t.Fatalf("Location = %+v, want a column", info.Location)
			}
			if tc.wantNumber && info.Number == 0 {
				t.Fatalf("Number = %d, want non-zero", info.Number)
			}
			if tc.wantSQLState && info.SQLState == "" {
				t.Fatal("expected SQLState")
			}
			if tc.wantName && info.Name == "" {
				t.Fatal("expected Name")
			}
			if tc.wantType && info.Type == "" {
				t.Fatal("expected Type")
			}
			if tc.wantReason && info.Reason == "" {
				t.Fatal("expected Reason")
			}
		})
	}
}

func driverNameForProbe(name string) string {
	if strings.HasPrefix(name, "postgres_") {
		return "postgres"
	}
	return name
}

func formatErrorChain(err error) string {
	if err == nil {
		return "<nil>"
	}
	var parts []string
	for cur := err; cur != nil; cur = errors.Unwrap(cur) {
		parts = append(parts, fmt.Sprintf("%T: %v", cur, cur))
	}
	return strings.Join(parts, "\n")
}
