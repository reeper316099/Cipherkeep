//go:build !windows

package main

import (
	"runtime"
	"syscall"
)

// peakRSSMB is the process's peak resident memory.
func peakRSSMB() float64 {
	var ru syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	if runtime.GOOS == "darwin" {
		return float64(ru.Maxrss) / (1 << 20) // bytes on macOS
	}
	return float64(ru.Maxrss) / 1024 // KiB on Linux
}
