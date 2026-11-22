//go:build windows

package jsonstore

import "syscall"

const (
	processQueryLimitedInformation = 0x1000
	stillActive                    = 259
)

func processAlive(pid int) bool {
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		// Access denied means the process exists but belongs to someone else.
		return err == syscall.ERROR_ACCESS_DENIED
	}
	defer syscall.CloseHandle(h)
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return true
	}
	return code == stillActive
}
