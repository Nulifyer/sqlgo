package trino

import (
	"errors"

	"github.com/Nulifyer/sqlgo/internal/db/errinfo"
	trinoclient "github.com/trinodb/trino-go-client/trino"
)

func (preset) ParseError(err error, sql string) errinfo.Info {
	info := errinfo.Plain(err)

	var te *trinoclient.ErrTrino
	if !errors.As(err, &te) {
		return info
	}

	info.Engine = driverName
	info.Message = te.Message
	info.SQLState = te.SqlState
	info.Number = te.ErrorCode
	info.Name = te.ErrorName
	info.Type = te.ErrorType
	if te.ErrorLocation.LineNumber > 0 {
		info.Location = errinfo.Location{
			Line:   te.ErrorLocation.LineNumber,
			Column: te.ErrorLocation.ColumnNumber,
		}
	} else if te.FailureInfo.ErrorLocation.LineNumber > 0 {
		info.Location = errinfo.Location{
			Line:   te.FailureInfo.ErrorLocation.LineNumber,
			Column: te.FailureInfo.ErrorLocation.ColumnNumber,
		}
	}
	if te.FailureInfo.Message != "" && te.FailureInfo.Message != te.Message {
		info.Detail = te.FailureInfo.Message
	}
	return info
}
