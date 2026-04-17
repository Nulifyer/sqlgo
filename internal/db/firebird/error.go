package firebird

import (
	"errors"
	"regexp"
	"strconv"

	"github.com/Nulifyer/sqlgo/internal/db/errinfo"
	firebirdsql "github.com/nakagami/firebirdsql"
)

var (
	firebirdLineColRE = regexp.MustCompile(`(?i)\bline (\d+), column (\d+)\b`)
	firebirdSQLCodeRE = regexp.MustCompile(`(?im)^\s*SQL error code = (-?\d+)\s*$`)
)

func (preset) ParseError(err error, sql string) errinfo.Info {
	info := errinfo.Plain(err)

	var fe *firebirdsql.FbError
	if !errors.As(err, &fe) {
		return info
	}

	info.Engine = driverName
	info.Message = fe.Message
	if m := firebirdSQLCodeRE.FindStringSubmatch(fe.Message); len(m) == 2 {
		if n, err := strconv.Atoi(m[1]); err == nil {
			info.Number = n
		}
	}
	if len(fe.GDSCodes) > 0 {
		info.Codes = append([]int(nil), fe.GDSCodes...)
	}
	if m := firebirdLineColRE.FindStringSubmatch(fe.Message); len(m) == 3 {
		line, lerr := strconv.Atoi(m[1])
		col, cerr := strconv.Atoi(m[2])
		if lerr == nil && line > 0 {
			info.Location.Line = line
		}
		if cerr == nil && col > 0 {
			info.Location.Column = col
		}
	}
	return info
}
