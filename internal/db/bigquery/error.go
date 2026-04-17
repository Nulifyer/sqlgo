package bigquery

import (
	"encoding/binary"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/Nulifyer/sqlgo/internal/db/errinfo"
	"github.com/googleapis/gax-go/v2/apierror"
	"google.golang.org/api/googleapi"
)

var (
	bigQueryAtLineColRE = regexp.MustCompile(`\[at (\d+):(\d+)\]`)
	bigQueryProtoLocRE  = regexp.MustCompile(`ErrorLocation[^']*'([^']+)'`)
)

func (driver) ParseError(err error, sql string) errinfo.Info {
	info := errinfo.Plain(err)

	var gae *googleapi.Error
	if !errors.As(err, &gae) {
		return info
	}

	info.Engine = driverName
	info.Message = gae.Message
	if info.Message == "" {
		info.Message = err.Error()
	}
	info.Number = gae.Code
	if len(gae.Errors) > 0 {
		info.Reason = gae.Errors[0].Reason
	}
	if loc := findBigQueryLocation(gae.Details); loc.Line > 0 {
		info.Location = loc
	} else if loc := parseAtLineCol(gae.Message); loc.Line > 0 {
		info.Location = loc
	} else if loc := parseAtLineCol(gae.Body); loc.Line > 0 {
		info.Location = loc
	} else if loc := locationFromProtoText(gae.Error()); loc.Line > 0 {
		info.Location = loc
	}
	if ae, ok := apierror.FromError(err); ok {
		if info.Reason == "" {
			info.Reason = ae.Reason()
		}
		if ae.Details().RequestInfo != nil {
			info.RequestID = ae.Details().RequestInfo.GetRequestId()
		}
	}
	return info
}

func findBigQueryLocation(v any) errinfo.Location {
	switch x := v.(type) {
	case []interface{}:
		for _, item := range x {
			if loc := findBigQueryLocation(item); loc.Line > 0 {
				return loc
			}
		}
	case map[string]interface{}:
		line := anyInt(x["line"])
		col := anyInt(x["column"])
		if line == 0 {
			line = anyInt(x["lineNumber"])
			col = anyInt(x["columnNumber"])
		}
		if line > 0 {
			return errinfo.Location{Line: line, Column: col}
		}

		typeHint, _ := x["@type"].(string)
		if typeHint == "" {
			typeHint, _ = x["typeUrl"].(string)
		}
		if strings.Contains(typeHint, "ErrorLocation") {
			for _, key := range []string{"value", "message", "data"} {
				raw, _ := x[key].(string)
				if loc := decodeProtoLocation(raw); loc.Line > 0 {
					return loc
				}
			}
		}

		for _, item := range x {
			if loc := findBigQueryLocation(item); loc.Line > 0 {
				return loc
			}
		}
	case string:
		if loc := parseAtLineCol(x); loc.Line > 0 {
			return loc
		}
		if loc := locationFromProtoText(x); loc.Line > 0 {
			return loc
		}
	}
	return errinfo.Location{}
}

func parseAtLineCol(msg string) errinfo.Location {
	m := bigQueryAtLineColRE.FindStringSubmatch(msg)
	if len(m) != 3 {
		return errinfo.Location{}
	}
	line, lerr := strconv.Atoi(m[1])
	col, cerr := strconv.Atoi(m[2])
	if lerr != nil || line <= 0 {
		return errinfo.Location{}
	}
	if cerr != nil || col <= 0 {
		return errinfo.Location{Line: line}
	}
	return errinfo.Location{Line: line, Column: col}
}

func anyInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int32:
		return int(n)
	case int64:
		return int(n)
	case float64:
		return int(n)
	case string:
		i, _ := strconv.Atoi(strings.TrimSpace(n))
		return i
	default:
		return 0
	}
}

func locationFromProtoText(s string) errinfo.Location {
	m := bigQueryProtoLocRE.FindStringSubmatch(s)
	if len(m) != 2 {
		return errinfo.Location{}
	}
	return decodeProtoLocation(m[1])
}

func decodeProtoLocation(raw string) errinfo.Location {
	if raw == "" {
		return errinfo.Location{}
	}
	if loc := decodeProtoLocationBytes([]byte(raw)); loc.Line > 0 {
		return loc
	}
	quoted := `"` + strings.ReplaceAll(raw, `"`, `\"`) + `"`
	unescaped, err := strconv.Unquote(quoted)
	if err != nil {
		return errinfo.Location{}
	}
	return decodeProtoLocationBytes([]byte(unescaped))
}

func decodeProtoLocationBytes(b []byte) errinfo.Location {
	var loc errinfo.Location
	for len(b) > 0 {
		tag, n := binary.Uvarint(b)
		if n <= 0 {
			break
		}
		b = b[n:]
		field := int(tag >> 3)
		wire := int(tag & 0x7)
		if wire != 0 {
			break
		}
		val, n := binary.Uvarint(b)
		if n <= 0 {
			break
		}
		b = b[n:]
		switch field {
		case 1:
			loc.Line = int(val)
		case 2:
			loc.Column = int(val)
		}
	}
	if loc.Line <= 0 {
		return errinfo.Location{}
	}
	return loc
}
