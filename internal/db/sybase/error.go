package sybase

import (
	"errors"

	tds "github.com/Nulifyer/go-tds"
	"github.com/Nulifyer/sqlgo/internal/db/errinfo"
)

func (preset) ParseError(err error, sql string) errinfo.Info {
	info := errinfo.Plain(err)

	var se tds.SybError
	if !errors.As(err, &se) {
		return info
	}

	info.Engine = driverName
	info.Message = se.Message
	info.Number = int(se.MsgNumber)
	info.State = int(se.State)
	info.Class = int(se.Severity)
	info.SQLState = se.SQLState
	info.Server = se.Server
	info.Procedure = se.Procedure
	if se.LineNumber > 0 {
		info.Location = errinfo.Location{Line: int(se.LineNumber)}
	}
	return info
}
