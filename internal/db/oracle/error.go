package oracle

import (
	"errors"

	"github.com/Nulifyer/sqlgo/internal/db/errinfo"
	ora "github.com/sijms/go-ora/v2/network"
)

func (preset) ParseError(err error, sql string) errinfo.Info {
	info := errinfo.Plain(err)

	var oe *ora.OracleError
	if !errors.As(err, &oe) {
		return info
	}

	info.Engine = driverName
	info.Message = oe.ErrMsg
	if info.Message == "" {
		info.Message = oe.Error()
	}
	info.Number = oe.ErrCode
	if pos := oe.ErrPos(); pos >= 0 {
		info.Location = errinfo.LocationFromPos(sql, pos+1)
	}
	return info
}
