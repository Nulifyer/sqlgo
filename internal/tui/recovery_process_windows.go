//go:build windows

package tui

import "golang.org/x/sys/windows"

func recoveryProcessAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return err == windows.ERROR_ACCESS_DENIED
	}
	defer windows.CloseHandle(h)
	status, err := windows.WaitForSingleObject(h, 0)
	return err == nil && status == uint32(windows.WAIT_TIMEOUT)
}
