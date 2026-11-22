package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/pprof"
	"time"

	"github.com/reeper316099/Cipherkeep/go/internal/library"
)

// profileOverview runs the dashboard repeatedly under the CPU profiler (BENCH_PROFILE=<file>); a development aid,
// not part of the measured results.
func profileOverview(dataset, out string) error {
	tmp, err := os.MkdirTemp("", "ck-prof-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := copyTree(dataset, tmp); err != nil {
		return err
	}
	lib, err := library.Open(library.Options{DataDir: filepath.Clean(tmp), LaunchBackupDelay: -1})
	if err != nil {
		return err
	}
	defer lib.Close()
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	if _, err := lib.BuildOverview(library.OverviewOptions{Now: now}); err != nil { // warm up
		return err
	}
	if err := pprof.StartCPUProfile(f); err != nil {
		return err
	}
	defer pprof.StopCPUProfile()
	for i := 0; i < 60; i++ {
		if _, err := lib.BuildOverview(library.OverviewOptions{Now: now}); err != nil {
			return err
		}
	}
	fmt.Println("profile written to", out)
	return nil
}
