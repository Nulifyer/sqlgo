// Package errinfo extracts line-number information from driver-specific
// query errors. The TUI uses this to show "Line N:" above the wrapped
// error body in the Results pane error view.
//
// Kept in its own package so internal/db stays driver-agnostic; only
// this file imports pgconn and go-mssqldb directly.
package errinfo

import (
	"errors"
	"regexp"
	"strconv"

	gomysql "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	mssql "github.com/microsoft/go-mssqldb"
)

var mysqlLineRE = regexp.MustCompile(`(?i)\bat line (\d+)\b`)

// Location is a 1-based line / column pair into the user's SQL text.
// Zero values mean the driver didn't supply that part of the location.
type Location struct {
	Line   int
	Column int
}

// ShiftLines moves loc by delta lines. When the resulting line would be
// before the start of the user's SQL, it returns the zero location.
func (loc Location) ShiftLines(delta int) Location {
	if loc.Line == 0 || delta == 0 {
		return loc
	}
	loc.Line += delta
	if loc.Line <= 0 {
		return Location{}
	}
	return loc
}

// Line reports the 1-based line in sql where the server located the
// error, or 0 if the driver didn't supply one. sql is the query text
// that produced err; it's only consulted for engines that report a
// character position rather than a line number (Postgres).
func Line(err error, sql string) int {
	return Locate(err, sql).Line
}

// Column reports the 1-based column in sql where the server located the
// error, or 0 if the driver didn't supply one.
func Column(err error, sql string) int {
	return Locate(err, sql).Column
}

// Locate reports the 1-based line / column in sql where the server
// located the error, or the zero location when the driver didn't supply
// one. sql is only consulted for engines that report a character
// position rather than an explicit line / column (Postgres).
func Locate(err error, sql string) Location {
	if err == nil {
		return Location{}
	}
	var me mssql.Error
	if errors.As(err, &me) {
		if me.LineNo > 0 {
			return Location{Line: int(me.LineNo)}
		}
		for _, e := range me.All {
			if e.LineNo > 0 {
				return Location{Line: int(e.LineNo)}
			}
		}
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Position > 0 {
		return locationFromPos(sql, int(pe.Position))
	}
	var my *gomysql.MySQLError
	if errors.As(err, &my) {
		if line := parseMySQLLine(my.Message); line > 0 {
			return Location{Line: line}
		}
	}
	return Location{}
}

// locationFromPos converts a 1-based character index into sql to a
// 1-based line / column pair. Postgres reports Position in characters,
// so walk the string as runes rather than bytes.
func locationFromPos(s string, pos int) Location {
	if pos <= 0 {
		return Location{}
	}

	loc := Location{Line: 1, Column: 1}
	charPos := 1
	for _, r := range s {
		if charPos >= pos {
			break
		}
		if r == '\n' {
			loc.Line++
			loc.Column = 1
		} else {
			loc.Column++
		}
		charPos++
	}
	return loc
}

func parseMySQLLine(msg string) int {
	m := mysqlLineRE.FindStringSubmatch(msg)
	if len(m) != 2 {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n <= 0 {
		return 0
	}
	return n
}
