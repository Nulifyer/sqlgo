package mysql

import (
	"errors"
	"regexp"
	"strconv"

	"github.com/Nulifyer/sqlgo/internal/db/errinfo"
	gomysql "github.com/go-sql-driver/mysql"
)

var mysqlLineRE = regexp.MustCompile(`(?i)\bat line (\d+)\b`)

func (preset) ParseError(err error, sql string) errinfo.Info {
	info := errinfo.Plain(err)

	var me *gomysql.MySQLError
	if !errors.As(err, &me) {
		return info
	}

	info.Engine = driverName
	info.Message = me.Message
	info.Number = int(me.Number)
	if me.SQLState != [5]byte{} {
		info.SQLState = string(me.SQLState[:])
	}
	if m := mysqlLineRE.FindStringSubmatch(me.Message); len(m) == 2 {
		if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
			info.Location = errinfo.Location{Line: n}
		}
	}
	return info
}
