package errinfo_test

import (
	"strings"
	"testing"

	spannerdb "cloud.google.com/go/spanner"
	chproto "github.com/ClickHouse/clickhouse-go/v2/lib/proto"
	tds "github.com/Nulifyer/go-tds"
	"github.com/Nulifyer/sqlgo/internal/db"
	_ "github.com/Nulifyer/sqlgo/internal/db/bigquery"
	_ "github.com/Nulifyer/sqlgo/internal/db/clickhouse"
	ei "github.com/Nulifyer/sqlgo/internal/db/errinfo"
	_ "github.com/Nulifyer/sqlgo/internal/db/firebird"
	_ "github.com/Nulifyer/sqlgo/internal/db/libsql"
	_ "github.com/Nulifyer/sqlgo/internal/db/mssql"
	_ "github.com/Nulifyer/sqlgo/internal/db/mysql"
	_ "github.com/Nulifyer/sqlgo/internal/db/oracle"
	_ "github.com/Nulifyer/sqlgo/internal/db/postgres"
	_ "github.com/Nulifyer/sqlgo/internal/db/spanner"
	_ "github.com/Nulifyer/sqlgo/internal/db/sybase"
	_ "github.com/Nulifyer/sqlgo/internal/db/trino"
	gomysql "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	mssql "github.com/microsoft/go-mssqldb"
	firebirdsql "github.com/nakagami/firebirdsql"
	oracle "github.com/sijms/go-ora/v2/network"
	trinoclient "github.com/trinodb/trino-go-client/trino"
	"google.golang.org/api/googleapi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestLocationFromPosUsesCharacters(t *testing.T) {
	loc := ei.LocationFromPos("SELECT 'é'X", 11)
	if loc != (ei.Location{Line: 1, Column: 11}) {
		t.Fatalf("LocationFromPos() = %+v, want line 1 / col 11", loc)
	}
}

func TestPlainAndShiftLines(t *testing.T) {
	info := ei.Plain(assertErr{"syntax error"})
	if info.Message != "syntax error" {
		t.Fatalf("Plain() message = %q, want syntax error", info.Message)
	}

	loc := (ei.Location{Line: 5, Column: 9}).ShiftLines(-2)
	if loc != (ei.Location{Line: 3, Column: 9}) {
		t.Fatalf("ShiftLines() = %+v, want line 3 / col 9", loc)
	}
	if got := (ei.Location{Line: 1, Column: 2}).ShiftLines(-1); got != (ei.Location{}) {
		t.Fatalf("ShiftLines() before start = %+v, want zero location", got)
	}
}

func TestParseErrorInfoPostgresFields(t *testing.T) {
	err := &pgconn.PgError{
		Severity:       "ERROR",
		Code:           "42P01",
		Message:        `relation "public.widgetsz" does not exist`,
		Detail:         "detail text",
		Hint:           "hint text",
		Where:          "where text",
		SchemaName:     "public",
		TableName:      "widgetsz",
		ConstraintName: "widgets_pkey",
		Position:       15,
	}
	info := db.ParseErrorInfo("postgres", err, "SELECT 1\nFROM\nusers")
	if info.Engine != "postgres" || info.SQLState != "42P01" {
		t.Fatalf("info = %+v, want postgres SQLSTATE 42P01", info)
	}
	if info.Location != (ei.Location{Line: 3, Column: 1}) {
		t.Fatalf("Location = %+v, want line 3 / col 1", info.Location)
	}
	got := info.Format()
	for _, want := range []string{
		`ERROR: relation "public.widgetsz" does not exist (SQLSTATE 42P01)`,
		"schema: public",
		"table: widgetsz",
		"constraint: widgets_pkey",
		"detail: detail text",
		"hint: hint text",
		"where: where text",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Format() missing %q in %q", want, got)
		}
	}
}

func TestParseErrorInfoMSSQLFields(t *testing.T) {
	err := mssql.Error{
		Number:     156,
		State:      1,
		Class:      15,
		Message:    "Incorrect syntax near 'FROM'.",
		ServerName: "sqlgo-mssql",
		ProcName:   "sp_executesql",
		LineNo:     7,
	}
	info := db.ParseErrorInfo("mssql", err, "")
	if info.Engine != "mssql" || info.Number != 156 || info.State != 1 || info.Class != 15 {
		t.Fatalf("info = %+v, want mssql codes", info)
	}
	if info.Location != (ei.Location{Line: 7}) {
		t.Fatalf("Location = %+v, want line 7", info.Location)
	}
}

func TestParseErrorInfoMySQLFields(t *testing.T) {
	err := &gomysql.MySQLError{
		Number:   1064,
		SQLState: [5]byte{'4', '2', '0', '0', '0'},
		Message:  "You have an error in your SQL syntax; check the manual that corresponds to your MySQL server version for the right syntax to use near 'users' at line 3",
	}
	info := db.ParseErrorInfo("mysql", err, "")
	if info.Engine != "mysql" || info.Number != 1064 || info.SQLState != "42000" {
		t.Fatalf("info = %+v, want mysql 1064/42000", info)
	}
	if info.Location != (ei.Location{Line: 3}) {
		t.Fatalf("Location = %+v, want line 3", info.Location)
	}
	if got := info.Format(); !strings.Contains(got, "Error 1064 (42000):") {
		t.Fatalf("Format() = %q, want MySQL headline", got)
	}
}

func TestParseErrorInfoTrinoFields(t *testing.T) {
	err := &trinoclient.ErrTrino{
		Message:   "line 1:1: mismatched input 'SELEC'. Expecting: 'SELECT'",
		SqlState:  "42000",
		ErrorCode: 1,
		ErrorName: "SYNTAX_ERROR",
		ErrorType: "USER_ERROR",
		ErrorLocation: trinoclient.ErrorLocation{
			LineNumber:   1,
			ColumnNumber: 1,
		},
		FailureInfo: trinoclient.FailureInfo{
			Message: "Query failed (#1): line 1:1: mismatched input 'SELEC'",
		},
	}
	info := db.ParseErrorInfo("trino", err, "SELEC 1")
	if info.Engine != "trino" || info.Name != "SYNTAX_ERROR" || info.Type != "USER_ERROR" {
		t.Fatalf("info = %+v, want trino syntax error", info)
	}
	if info.Location != (ei.Location{Line: 1, Column: 1}) {
		t.Fatalf("Location = %+v, want line 1 / col 1", info.Location)
	}
}

func TestParseErrorInfoClickHouseFields(t *testing.T) {
	err := &chproto.Exception{
		Code:    62,
		Name:    "DB::Exception",
		Message: "Syntax error: failed at position 8 ('SELEC')",
	}
	info := db.ParseErrorInfo("clickhouse", err, "SELECT\nSELEC")
	if info.Engine != "clickhouse" || info.Number != 62 || info.Name != "DB::Exception" {
		t.Fatalf("info = %+v, want clickhouse exception", info)
	}
	if info.Location != (ei.Location{Line: 2, Column: 1}) {
		t.Fatalf("Location = %+v, want line 2 / col 1", info.Location)
	}
}

func TestParseErrorInfoFirebirdFields(t *testing.T) {
	err := &firebirdsql.FbError{
		GDSCodes: []int{335544569, 335544436},
		Message:  "Dynamic SQL Error\nSQL error code = -104\nToken unknown - line 1, column 1\nSELEC",
	}
	info := db.ParseErrorInfo("firebird", err, "SELEC")
	if info.Engine != "firebird" || info.Number != -104 || len(info.Codes) != 2 {
		t.Fatalf("info = %+v, want firebird sql code + gds codes", info)
	}
	if info.Location != (ei.Location{Line: 1, Column: 1}) {
		t.Fatalf("Location = %+v, want line 1 / col 1", info.Location)
	}
	if got := info.Format(); !strings.Contains(got, "sql code: -104") {
		t.Fatalf("Format() = %q, want firebird sql code", got)
	}
}

func TestParseErrorInfoSybaseFields(t *testing.T) {
	err := tds.SybError{
		MsgNumber:  2812,
		State:      5,
		Severity:   16,
		SQLState:   "42000",
		Message:    "Stored procedure 'SELEC' not found.",
		Server:     "MYSYBASE",
		Procedure:  "sp_executesql",
		LineNumber: 4,
	}
	info := db.ParseErrorInfo("sybase", err, "")
	if info.Engine != "sybase" || info.Number != 2812 || info.State != 5 || info.Class != 16 {
		t.Fatalf("info = %+v, want sybase codes", info)
	}
	if info.Location != (ei.Location{Line: 4}) {
		t.Fatalf("Location = %+v, want line 4", info.Location)
	}
}

func TestParseErrorInfoOracleFields(t *testing.T) {
	err := oracle.NewOracleError(900)
	info := db.ParseErrorInfo("oracle", err, "SELEC 1")
	if info.Engine != "oracle" || info.Number != 900 {
		t.Fatalf("info = %+v, want oracle 900", info)
	}
	if !strings.Contains(info.Format(), "ORA-00900") {
		t.Fatalf("Format() = %q, want ORA-00900", info.Format())
	}
}

func TestParseErrorInfoSpannerFields(t *testing.T) {
	err := spannerdb.ToSpannerError(status.Error(codes.InvalidArgument, "Syntax error: Unexpected keyword FORM [at 1:10]"))
	info := db.ParseErrorInfo("spanner", err, "SELECT * FORM foo")
	if info.Engine != "spanner" || info.Type != "InvalidArgument" {
		t.Fatalf("info = %+v, want spanner InvalidArgument", info)
	}
	if info.Location != (ei.Location{Line: 1, Column: 10}) {
		t.Fatalf("Location = %+v, want line 1 / col 10", info.Location)
	}
}

func TestParseErrorInfoBigQueryFields(t *testing.T) {
	err := &googleapi.Error{
		Code:    400,
		Message: "Syntax error",
		Errors: []googleapi.ErrorItem{
			{Reason: "invalidQuery", Message: "Syntax error"},
		},
		Details: []interface{}{
			map[string]interface{}{"line": float64(2), "column": float64(4)},
		},
	}
	info := db.ParseErrorInfo("bigquery", err, "SELECT\nSELEC")
	if info.Engine != "bigquery" || info.Number != 400 || info.Reason != "invalidQuery" {
		t.Fatalf("info = %+v, want bigquery 400/invalidQuery", info)
	}
	if info.Location != (ei.Location{Line: 2, Column: 4}) {
		t.Fatalf("Location = %+v, want line 2 / col 4", info.Location)
	}
}

func TestParseErrorInfoFallsBackToPlainError(t *testing.T) {
	info := db.ParseErrorInfo("sqlite", assertErr{"syntax error"}, "SELECT")
	if info.Message != "syntax error" {
		t.Fatalf("info = %+v, want plain message", info)
	}
	if got := info.Format(); got != "syntax error" {
		t.Fatalf("Format() = %q, want plain error", got)
	}
}

type assertErr struct{ msg string }

func (e assertErr) Error() string { return e.msg }
