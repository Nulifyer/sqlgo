package tui

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// documentFormat describes disk serialization. The editor always uses LF text.
type documentFormat struct {
	Encoding string
	BOM      bool
	EOL      string
}

func decodeDocument(data []byte) (string, documentFormat, error) {
	format := documentFormat{Encoding: "utf-8", EOL: "\n"}
	var text string
	switch {
	case bytes.HasPrefix(data, []byte{0xff, 0xfe, 0, 0}) || bytes.HasPrefix(data, []byte{0, 0, 0xfe, 0xff}):
		return "", format, fmt.Errorf("UTF-32 is unsupported; use UTF-8 or BOM-marked UTF-16")
	case bytes.HasPrefix(data, []byte{0xff, 0xfe}) || bytes.HasPrefix(data, []byte{0xfe, 0xff}):
		var order binary.ByteOrder = binary.LittleEndian
		format.Encoding, format.BOM = "utf-16le", true
		if data[0] == 0xfe {
			order = binary.BigEndian
			format.Encoding = "utf-16be"
		}
		data = data[2:]
		if len(data)%2 != 0 {
			return "", format, fmt.Errorf("incomplete UTF-16 code unit")
		}
		units := make([]uint16, len(data)/2)
		for i := range units {
			units[i] = order.Uint16(data[i*2:])
		}
		for i := 0; i < len(units); i++ {
			if units[i] >= 0xd800 && units[i] <= 0xdbff {
				if i+1 >= len(units) || units[i+1] < 0xdc00 || units[i+1] > 0xdfff {
					return "", format, fmt.Errorf("incomplete UTF-16 surrogate pair")
				}
				i++
			} else if units[i] >= 0xdc00 && units[i] <= 0xdfff {
				return "", format, fmt.Errorf("unpaired UTF-16 surrogate")
			}
		}
		text = string(utf16.Decode(units))
	default:
		if bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}) {
			format.BOM = true
			data = data[3:]
		}
		if !utf8.Valid(data) {
			return "", format, fmt.Errorf("file is not valid UTF-8; use UTF-8 or BOM-marked UTF-16")
		}
		text = string(data)
		if strings.ContainsRune(text, 0) {
			return "", format, fmt.Errorf("file contains NUL bytes; use UTF-8 or BOM-marked UTF-16")
		}
	}
	crlf := strings.Count(text, "\r\n")
	lf := strings.Count(text, "\n") - crlf
	cr := strings.Count(text, "\r") - crlf
	if crlf > lf && crlf >= cr {
		format.EOL = "\r\n"
	} else if cr > crlf && cr > lf {
		format.EOL = "\r"
	}
	return normalizeEditorLineEndings(text), format, nil
}

func encodeDocument(text string, format documentFormat) []byte {
	if format.EOL != "" && format.EOL != "\n" {
		text = strings.ReplaceAll(text, "\n", format.EOL)
	}
	if format.Encoding == "utf-16le" || format.Encoding == "utf-16be" {
		var order binary.ByteOrder = binary.LittleEndian
		prefix := []byte{0xff, 0xfe}
		if format.Encoding == "utf-16be" {
			order = binary.BigEndian
			prefix = []byte{0xfe, 0xff}
		}
		units := utf16.Encode([]rune(text))
		out := make([]byte, len(prefix)+len(units)*2)
		copy(out, prefix)
		for i, v := range units {
			order.PutUint16(out[len(prefix)+i*2:], v)
		}
		return out
	}
	if format.BOM {
		return append([]byte{0xef, 0xbb, 0xbf}, []byte(text)...)
	}
	return []byte(text)
}
