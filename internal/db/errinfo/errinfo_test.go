package errinfo

import (
	"errors"
	"testing"

	gomysql "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	mssql "github.com/microsoft/go-mssqldb"
)

func TestLocatePostgresPosition(t *testing.T) {
	sql := "SELECT 1\nFROM\nusers"
	err := &pgconn.PgError{Position: 15}

	loc := Locate(err, sql)
	if loc.Line != 3 || loc.Column != 1 {
		t.Fatalf("Locate() = %+v, want line 3 / col 1", loc)
	}
}

func TestLocatePostgresPositionUsesCharacters(t *testing.T) {
	sql := "SELECT 'é'X"
	err := &pgconn.PgError{Position: 11}

	loc := Locate(err, sql)
	if loc.Line != 1 || loc.Column != 11 {
		t.Fatalf("Locate() = %+v, want line 1 / col 11", loc)
	}
}

func TestLocateMSSQLLine(t *testing.T) {
	err := mssql.Error{LineNo: 7}

	loc := Locate(err, "")
	if loc.Line != 7 || loc.Column != 0 {
		t.Fatalf("Locate() = %+v, want line 7 / col 0", loc)
	}
}

func TestLocateMSSQLFallsBackToAllErrors(t *testing.T) {
	err := mssql.Error{
		All: []mssql.Error{{LineNo: 4}},
	}

	loc := Locate(err, "")
	if loc.Line != 4 || loc.Column != 0 {
		t.Fatalf("Locate() = %+v, want line 4 / col 0", loc)
	}
}

func TestLocateMySQLLineFromMessage(t *testing.T) {
	err := &gomysql.MySQLError{
		Number:  1064,
		Message: "You have an error in your SQL syntax; check the manual that corresponds to your MySQL server version for the right syntax to use near 'users' at line 3",
	}

	loc := Locate(err, "")
	if loc.Line != 3 || loc.Column != 0 {
		t.Fatalf("Locate() = %+v, want line 3 / col 0", loc)
	}
}

func TestLocateNoStructuredLocation(t *testing.T) {
	loc := Locate(errors.New("syntax error"), "SELECT")
	if loc != (Location{}) {
		t.Fatalf("Locate() = %+v, want zero location", loc)
	}
}

func TestShiftLines(t *testing.T) {
	loc := (Location{Line: 5, Column: 9}).ShiftLines(-2)
	if loc.Line != 3 || loc.Column != 9 {
		t.Fatalf("ShiftLines() = %+v, want line 3 / col 9", loc)
	}

	if got := (Location{Line: 1, Column: 2}).ShiftLines(-1); got != (Location{}) {
		t.Fatalf("ShiftLines() before start = %+v, want zero location", got)
	}
}
