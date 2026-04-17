package db

import "github.com/Nulifyer/sqlgo/internal/db/errinfo"

// ErrorParser is an optional driver capability: it turns a native
// driver/server error into sqlgo's shared structured error shape. The
// TUI and tests use this instead of branching on engine names.
type ErrorParser interface {
	ParseError(err error, sql string) errinfo.Info
}

// ParseErrorInfo asks the registered driver to parse err into the shared
// structured error shape. When the driver doesn't expose a parser, it
// falls back to the plain error text.
func ParseErrorInfo(driverName string, err error, sql string) errinfo.Info {
	if err == nil {
		return errinfo.Info{}
	}
	if driverName != "" {
		if d, derr := Get(driverName); derr == nil {
			if parser, ok := d.(ErrorParser); ok {
				info := parser.ParseError(err, sql)
				if info.Message == "" {
					info.Message = err.Error()
				}
				return info
			}
		}
	}
	return errinfo.Plain(err)
}
