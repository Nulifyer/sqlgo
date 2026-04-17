package libsql

import (
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/Nulifyer/sqlgo/internal/db/errinfo"
)

var (
	libsqlLineColRE = regexp.MustCompile(`\bL(\d+):(\d+)\b`)
	libsqlCodeRE    = regexp.MustCompile(`\(([A-Z][A-Z0-9_]+)\)\s*$`)
)

func (driver) ParseError(err error, sql string) errinfo.Info {
	info := errinfo.Plain(err)

	var he *hranaError
	if !errors.As(err, &he) {
		return info
	}

	info.Engine = driverName
	info.Message = he.Message
	info.Name = he.Code
	if m := libsqlLineColRE.FindStringSubmatch(he.Message); len(m) == 3 {
		line, lerr := strconv.Atoi(m[1])
		col, cerr := strconv.Atoi(m[2])
		if lerr == nil && line > 0 {
			info.Location.Line = line
		}
		if cerr == nil && col > 0 {
			info.Location.Column = col
		}
	}
	if info.Name == "" {
		if m := libsqlCodeRE.FindStringSubmatch(he.Message); len(m) == 2 {
			info.Name = m[1]
		}
	}
	if info.Name != "" {
		info.Message = strings.TrimSpace(strings.TrimSuffix(info.Message, " ("+info.Name+")"))
	}
	return info
}
