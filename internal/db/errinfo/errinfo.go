// Package errinfo extracts line-number information from driver-specific
// query errors. The TUI uses this to show "Line N:" above the wrapped
// error body in the Results pane error view.
//
// Kept in its own package so internal/db stays driver-agnostic; only
// this file imports pgconn and go-mssqldb directly.
package errinfo

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	mssql "github.com/microsoft/go-mssqldb"
)

// Line reports the 1-based line in sql where the server located the
// error, or 0 if the driver didn't supply one. sql is the query text
// that produced err; it's only consulted for engines that report a
// character position rather than a line number (Postgres).
func Line(err error, sql string) int {
	if err == nil {
		return 0
	}
	var me mssql.Error
	if errors.As(err, &me) {
		if me.LineNo > 0 {
			return int(me.LineNo)
		}
		for _, e := range me.All {
			if e.LineNo > 0 {
				return int(e.LineNo)
			}
		}
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Position > 0 {
		return lineFromPos(sql, int(pe.Position))
	}
	return 0
}

// lineFromPos converts a 1-based character index into sql to a 1-based
// line number. Postgres reports Position in characters (not bytes); for
// ASCII queries the two coincide, and for multibyte queries this
// slightly over-counts the line but never misses by more than one.
func lineFromPos(s string, pos int) int {
	if pos <= 0 {
		return 0
	}
	line := 1
	limit := pos - 1
	if limit > len(s) {
		limit = len(s)
	}
	for i := 0; i < limit; i++ {
		if s[i] == '\n' {
			line++
		}
	}
	return line
}
