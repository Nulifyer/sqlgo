package mssql

import (
	"errors"

	"github.com/Nulifyer/sqlgo/internal/db/errinfo"
	mssql "github.com/microsoft/go-mssqldb"
)

func (preset) ParseError(err error, sql string) errinfo.Info {
	info := errinfo.Plain(err)

	var me mssql.Error
	if !errors.As(err, &me) {
		return info
	}

	info.Engine = driverName
	info.Message = me.Message
	info.Number = int(me.Number)
	info.State = int(me.State)
	info.Class = int(me.Class)
	info.Server = me.ServerName
	info.Procedure = me.ProcName
	if me.LineNo > 0 {
		info.Location = errinfo.Location{Line: int(me.LineNo)}
		return info
	}
	for _, e := range me.All {
		if e.LineNo > 0 {
			info.Location = errinfo.Location{Line: int(e.LineNo)}
			break
		}
	}
	return info
}
