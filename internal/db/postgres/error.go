package postgres

import (
	"errors"

	"github.com/Nulifyer/sqlgo/internal/db/errinfo"
	"github.com/jackc/pgx/v5/pgconn"
)

func (preset) ParseError(err error, sql string) errinfo.Info {
	info := errinfo.Plain(err)

	var pe *pgconn.PgError
	if !errors.As(err, &pe) {
		return info
	}

	info.Engine = driverName
	info.Message = pe.Message
	info.Severity = pe.Severity
	if info.Severity == "" {
		info.Severity = pe.SeverityUnlocalized
	}
	info.SQLState = pe.Code
	info.Detail = pe.Detail
	info.Hint = pe.Hint
	info.Where = pe.Where
	info.Schema = pe.SchemaName
	info.Table = pe.TableName
	info.Column = pe.ColumnName
	info.Constraint = pe.ConstraintName
	info.DataType = pe.DataTypeName
	if pe.Position > 0 {
		info.Location = errinfo.LocationFromPos(sql, int(pe.Position))
	}
	return info
}
