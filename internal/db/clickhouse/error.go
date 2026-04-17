package clickhouse

import (
	"errors"
	"regexp"
	"strconv"

	chproto "github.com/ClickHouse/clickhouse-go/v2/lib/proto"
	"github.com/Nulifyer/sqlgo/internal/db/errinfo"
)

var clickhousePositionRE = regexp.MustCompile(`(?i)\bposition (\d+)\b`)

func (preset) ParseError(err error, sql string) errinfo.Info {
	info := errinfo.Plain(err)

	var ce *chproto.Exception
	if !errors.As(err, &ce) {
		return info
	}

	info.Engine = driverName
	info.Message = ce.Message
	info.Name = ce.Name
	info.Number = int(ce.Code)
	if m := clickhousePositionRE.FindStringSubmatch(ce.Message); len(m) == 2 {
		if pos, err := strconv.Atoi(m[1]); err == nil && pos > 0 {
			info.Location = errinfo.LocationFromPos(sql, pos)
		}
	}
	return info
}
