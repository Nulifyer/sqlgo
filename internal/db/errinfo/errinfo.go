// Package errinfo defines the shared structured query-error shape used
// across drivers. Driver packages parse their native errors into Info;
// the TUI formats and renders that one shape without branching on DB
// engine names or driver types.
package errinfo

import (
	"fmt"
	"strconv"
	"strings"
)

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

// Info is the structured view of a query error. Fields are populated on
// a best-effort basis from whichever driver-specific error type is
// available; zero values mean the driver did not supply that piece.
type Info struct {
	Location Location

	Engine   string
	Message  string
	Severity string
	Name     string
	Type     string
	Reason   string

	// SQLState is set for engines that expose one (Postgres/MySQL,
	// and some Trino/Sybase paths).
	SQLState string
	// Number/State/Class cover engines that surface numeric server codes
	// (MySQL error number, MSSQL/Sybase state/class, Oracle/Firebird SQL
	// code, ClickHouse exception code, BigQuery HTTP code, etc.).
	Number int
	State  int
	Class  int

	Detail     string
	Hint       string
	Where      string
	Schema     string
	Table      string
	Column     string
	Constraint string
	DataType   string
	Server     string
	Procedure  string
	RequestID  string
	Codes      []int
}

// Plain wraps err in the shared structured shape without any
// driver-specific fields. Nil errors return the zero Info.
func Plain(err error) Info {
	if err == nil {
		return Info{}
	}
	return Info{Message: err.Error()}
}

// LocationFromPos converts a 1-based character index into sql to a
// 1-based line / column pair. Engines such as Postgres and ClickHouse
// report positions in characters, so walk the string as runes rather
// than bytes.
func LocationFromPos(sql string, pos int) Location {
	if pos <= 0 {
		return Location{}
	}
	loc := Location{Line: 1, Column: 1}
	charPos := 1
	for _, r := range sql {
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

// Format returns a user-facing multiline string that keeps the driver's
// primary message first and appends any additional structured fields on
// separate lines.
func (info Info) Format() string {
	if info.Message == "" {
		return ""
	}
	lines := []string{info.primaryLine()}
	switch info.Engine {
	case "postgres":
		appendKV(&lines, "schema", info.Schema)
		appendKV(&lines, "table", info.Table)
		appendKV(&lines, "column", info.Column)
		appendKV(&lines, "constraint", info.Constraint)
		appendKV(&lines, "data type", info.DataType)
		appendKV(&lines, "detail", info.Detail)
		appendKV(&lines, "hint", info.Hint)
		appendKV(&lines, "where", info.Where)
	case "mssql":
		if info.Number > 0 {
			lines = append(lines, fmt.Sprintf("number: %d", info.Number))
		}
		if info.State > 0 {
			lines = append(lines, fmt.Sprintf("state: %d", info.State))
		}
		if info.Class > 0 {
			lines = append(lines, fmt.Sprintf("class: %d", info.Class))
		}
		appendKV(&lines, "server", info.Server)
		appendKV(&lines, "procedure", info.Procedure)
	case "sybase":
		if info.Number > 0 {
			lines = append(lines, fmt.Sprintf("number: %d", info.Number))
		}
		if info.State > 0 {
			lines = append(lines, fmt.Sprintf("state: %d", info.State))
		}
		if info.Class > 0 {
			lines = append(lines, fmt.Sprintf("class: %d", info.Class))
		}
		appendKV(&lines, "sqlstate", info.SQLState)
		appendKV(&lines, "server", info.Server)
		appendKV(&lines, "procedure", info.Procedure)
	case "trino":
		if info.Number > 0 {
			lines = append(lines, fmt.Sprintf("code: %d", info.Number))
		}
		appendKV(&lines, "name", info.Name)
		appendKV(&lines, "type", info.Type)
		appendKV(&lines, "detail", info.Detail)
	case "clickhouse":
		appendKV(&lines, "name", info.Name)
	case "libsql":
		appendKV(&lines, "code", info.Name)
	case "firebird":
		if info.Number != 0 {
			lines = append(lines, fmt.Sprintf("sql code: %d", info.Number))
		}
		switch len(info.Codes) {
		case 1:
			lines = append(lines, fmt.Sprintf("gds code: %d", info.Codes[0]))
		case 2, 3, 4, 5, 6, 7, 8:
			var parts []string
			for _, code := range info.Codes {
				parts = append(parts, strconv.Itoa(code))
			}
			lines = append(lines, "gds codes: "+strings.Join(parts, ", "))
		default:
			if len(info.Codes) > 0 {
				lines = append(lines, fmt.Sprintf("gds code: %d", info.Codes[0]))
			}
		}
	case "spanner", "bigquery":
		appendKV(&lines, "reason", info.Reason)
		appendKV(&lines, "request id", info.RequestID)
	}
	return strings.Join(lines, "\n")
}

func (info Info) primaryLine() string {
	switch info.Engine {
	case "postgres":
		msg := info.Message
		if info.Severity != "" {
			msg = info.Severity + ": " + msg
		}
		if info.SQLState != "" {
			msg += " (SQLSTATE " + info.SQLState + ")"
		}
		return msg
	case "mysql":
		if info.Number > 0 && info.SQLState != "" {
			return fmt.Sprintf("Error %d (%s): %s", info.Number, info.SQLState, info.Message)
		}
		if info.Number > 0 {
			return fmt.Sprintf("Error %d: %s", info.Number, info.Message)
		}
	case "mssql":
		return "mssql: " + info.Message
	case "trino":
		msg := info.Message
		if info.Type != "" {
			msg = info.Type + ": " + msg
		}
		if info.SQLState != "" {
			msg += " (SQLSTATE " + info.SQLState + ")"
		}
		return "trino: " + msg
	case "clickhouse":
		msg := info.Message
		if info.Number > 0 {
			msg += fmt.Sprintf(" (code %d)", info.Number)
		}
		return "clickhouse: " + msg
	case "libsql":
		msg := info.Message
		if info.Name != "" {
			msg += " (" + info.Name + ")"
		}
		return "libsql: " + msg
	case "sybase":
		return "sybase: " + info.Message
	case "oracle":
		return "oracle: " + info.Message
	case "firebird":
		return "firebird: " + info.Message
	case "spanner":
		msg := info.Message
		if info.Type != "" {
			msg = info.Type + ": " + msg
		}
		return "spanner: " + msg
	case "bigquery":
		if info.Number > 0 {
			return fmt.Sprintf("bigquery: Error %d: %s", info.Number, info.Message)
		}
		return "bigquery: " + info.Message
	}
	return info.Message
}

func appendKV(lines *[]string, key, val string) {
	if val == "" {
		return
	}
	*lines = append(*lines, key+": "+val)
}
