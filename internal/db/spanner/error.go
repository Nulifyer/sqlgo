package spanner

import (
	"errors"
	"regexp"
	"strconv"

	spannerdb "cloud.google.com/go/spanner"
	"github.com/Nulifyer/sqlgo/internal/db/errinfo"
	"github.com/googleapis/gax-go/v2/apierror"
)

var spannerAtLineColRE = regexp.MustCompile(`\[at (\d+):(\d+)\]`)

func (preset) ParseError(err error, sql string) errinfo.Info {
	info := errinfo.Plain(err)

	var se *spannerdb.Error
	if !errors.As(err, &se) {
		return info
	}

	info.Engine = driverName
	info.Message = se.Desc
	info.Type = spannerdb.ErrCode(err).String()
	info.RequestID = se.RequestID
	if m := spannerAtLineColRE.FindStringSubmatch(se.Desc); len(m) == 3 {
		line, lerr := strconv.Atoi(m[1])
		col, cerr := strconv.Atoi(m[2])
		if lerr == nil && line > 0 {
			info.Location.Line = line
		}
		if cerr == nil && col > 0 {
			info.Location.Column = col
		}
	}
	if ae, ok := apierror.FromError(err); ok {
		if info.Reason == "" {
			info.Reason = ae.Reason()
		}
		if info.RequestID == "" && ae.Details().RequestInfo != nil {
			info.RequestID = ae.Details().RequestInfo.GetRequestId()
		}
	}
	return info
}
