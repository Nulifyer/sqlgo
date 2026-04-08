//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

const enableVirtualTerminalInput = 0x0200

// enableVTInput is best-effort. When stdin is a real console, this asks
// Windows to deliver VT input sequences such as backtab (ESC [ Z).
func enableVTInput() {
	handle := windows.Handle(os.Stdin.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		return
	}
	_ = windows.SetConsoleMode(handle, mode|enableVirtualTerminalInput)
}
