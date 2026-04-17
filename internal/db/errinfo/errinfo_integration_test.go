//go:build integration

package errinfo_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Nulifyer/sqlgo/internal/db"
	ei "github.com/Nulifyer/sqlgo/internal/db/errinfo"
	_ "github.com/Nulifyer/sqlgo/internal/db/mssql"
	_ "github.com/Nulifyer/sqlgo/internal/db/mysql"
	_ "github.com/Nulifyer/sqlgo/internal/db/postgres"
	_ "github.com/Nulifyer/sqlgo/internal/db/sqlite"
)

func TestIntegrationExtractPostgres(t *testing.T) {
	conn := openIntegrationConn(t, "postgres", db.Config{
		Host:     envOr("SQLGO_IT_PG_HOST", "127.0.0.1"),
		Port:     15432,
		User:     envOr("SQLGO_IT_PG_USER", "sqlgo"),
		Password: envOr("SQLGO_IT_PG_PASSWORD", "sqlgo_dev"),
		Database: envOr("SQLGO_IT_PG_DB", "sqlgo_test"),
		Options:  map[string]string{"sslmode": "disable"},
	})
	defer conn.Close()

	sql := "SELECT * FROM\n\"public\".\"widgetsz\"\nLIMIT 100"
	err := queryErr(t, conn, sql)
	info := db.ParseErrorInfo("postgres", err, sql)
	t.Logf("postgres raw: %T %v", err, err)
	t.Logf("postgres info: %+v", info)

	if info.Engine != "postgres" {
		t.Fatalf("Engine = %q, want postgres", info.Engine)
	}
	if info.SQLState != "42P01" {
		t.Fatalf("SQLState = %q, want 42P01", info.SQLState)
	}
	if info.Location.Line != 2 || info.Location.Column <= 0 {
		t.Fatalf("Location = %+v, want line 2 with a column", info.Location)
	}
	if info.Message == "" {
		t.Fatal("expected postgres message")
	}
	if !strings.Contains(info.Format(), "SQLSTATE 42P01") {
		t.Fatalf("Format() = %q, want SQLSTATE 42P01", info.Format())
	}
}

func TestIntegrationExtractMySQL(t *testing.T) {
	conn := openIntegrationConn(t, "mysql", db.Config{
		Host:     envOr("SQLGO_IT_MYSQL_HOST", "127.0.0.1"),
		Port:     13306,
		User:     envOr("SQLGO_IT_MYSQL_USER", "sqlgo"),
		Password: envOr("SQLGO_IT_MYSQL_PASSWORD", "sqlgo_dev"),
		Database: envOr("SQLGO_IT_MYSQL_DB", "sqlgo_test"),
	})
	defer conn.Close()

	sql := "SELECT *\nFORM widgetsz\nLIMIT 100"
	err := queryErr(t, conn, sql)
	info := db.ParseErrorInfo("mysql", err, sql)
	t.Logf("mysql raw: %T %v", err, err)
	t.Logf("mysql info: %+v", info)

	if info.Engine != "mysql" {
		t.Fatalf("Engine = %q, want mysql", info.Engine)
	}
	if info.Number == 0 {
		t.Fatal("expected mysql error number")
	}
	if info.SQLState == "" {
		t.Fatal("expected mysql SQLSTATE")
	}
	if info.Location.Line != 2 {
		t.Fatalf("Location = %+v, want line 2", info.Location)
	}
	if info.Message == "" {
		t.Fatal("expected mysql message")
	}
}

func TestIntegrationExtractMSSQL(t *testing.T) {
	conn := openIntegrationConn(t, "mssql", db.Config{
		Host:     envOr("SQLGO_IT_MSSQL_HOST", "127.0.0.1"),
		Port:     11433,
		User:     envOr("SQLGO_IT_MSSQL_USER", "sa"),
		Password: envOr("SQLGO_IT_MSSQL_PASSWORD", "SqlGo_dev_Pass1!"),
		Database: envOr("SQLGO_IT_MSSQL_DB", "master"),
		Options: map[string]string{
			"encrypt":                "disable",
			"TrustServerCertificate": "true",
		},
	})
	defer conn.Close()

	sql := "SELECT *\nFRM dbo.widgets"
	err := queryErr(t, conn, sql)
	info := db.ParseErrorInfo("mssql", err, sql)
	t.Logf("mssql raw: %T %v", err, err)
	t.Logf("mssql info: %+v", info)

	if info.Engine != "mssql" {
		t.Fatalf("Engine = %q, want mssql", info.Engine)
	}
	if info.Number == 0 {
		t.Fatal("expected mssql error number")
	}
	if info.Location.Line != 2 {
		t.Fatalf("Location = %+v, want line 2", info.Location)
	}
	if info.Message == "" {
		t.Fatal("expected mssql message")
	}
}

func TestIntegrationExtractSQLite(t *testing.T) {
	conn := openIntegrationConn(t, "sqlite", db.Config{})
	defer conn.Close()

	sql := "SELECT *\nFORM widgetsz\nLIMIT 100"
	err := queryErr(t, conn, sql)
	info := db.ParseErrorInfo("sqlite", err, sql)
	t.Logf("sqlite raw: %T %v", err, err)
	t.Logf("sqlite info: %+v", info)

	if info.Engine != "" {
		t.Fatalf("Engine = %q, want empty for sqlite generic error", info.Engine)
	}
	if info.Location != (ei.Location{}) {
		t.Fatalf("Location = %+v, want zero location", info.Location)
	}
	if info.Message == "" {
		t.Fatal("expected sqlite message")
	}
}

func openIntegrationConn(t *testing.T, driverName string, cfg db.Config) db.Conn {
	t.Helper()
	d, err := db.Get(driverName)
	if err != nil {
		t.Fatalf("db.Get(%q): %v", driverName, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, err := d.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("open %s: %v", driverName, err)
	}
	return conn
}

func queryErr(t *testing.T, conn db.Conn, sql string) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	rows, err := conn.Query(ctx, sql)
	if err != nil {
		if rows != nil {
			rows.Close()
		}
		return err
	}
	if rows == nil {
		t.Fatalf("expected query error for %q", sql)
	}
	defer rows.Close()
	for rows.Next() {
		if _, err := rows.Scan(); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	t.Fatalf("expected query error for %q", sql)
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
