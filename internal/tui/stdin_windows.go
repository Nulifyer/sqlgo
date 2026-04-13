//go:build windows

package tui

import (
	"io"
	"os"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

// stdinReader returns a byte source that reads console input via
// ReadConsoleW. Two layers on Windows translate ^Z -> EOF and close
// the app on Ctrl+Z:
//  1. Go's internal/poll console wrapper (golang/go#3530) -- bypassed
//     by not using os.Stdin.Read.
//  2. The Win32 ReadFile path itself, at the ConDrv driver layer
//     (microsoft/terminal#4958) -- raw mode does not disable this.
// ReadConsoleW is the only documented API that never processes ^Z,
// so we use it and decode the UTF-16 result to UTF-8 bytes.
//
// The handle is os.Stdin's existing handle, which term.MakeRaw
// already put into raw mode (console modes are per-handle, so a
// fresh CONIN$ open would come back cooked).
func stdinReader() io.Reader {
	return &conInReader{h: windows.Handle(os.Stdin.Fd())}
}

type conInReader struct {
	h    windows.Handle
	wbuf [256]uint16
	buf  []byte
}

func (c *conInReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(c.buf) == 0 {
		var read uint32
		if err := windows.ReadConsole(c.h, &c.wbuf[0], uint32(len(c.wbuf)), &read, nil); err != nil {
			return 0, err
		}
		if read == 0 {
			return 0, io.EOF
		}
		runes := utf16.Decode(c.wbuf[:read])
		c.buf = []byte(string(runes))
	}
	n := copy(p, c.buf)
	c.buf = c.buf[n:]
	return n, nil
}
