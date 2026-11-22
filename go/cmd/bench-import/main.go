// Command bench-import measures the Go import and export code on the same on-disk data as scripts/bench-import.ts, in
// process.
//
//	go run ./cmd/bench-import <dataset-dir> <library-dir>
//
// <dataset-dir> must hold export.zip (made by the Node script); <library-dir> is copied before use.
package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/reeper316099/Cipherkeep/go/internal/exporter"
	"github.com/reeper316099/Cipherkeep/go/internal/importer"
	"github.com/reeper316099/Cipherkeep/go/internal/library"
)

const rounds = 3

func rssMB() float64 {
	out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(os.Getpid())).Output()
	if err != nil {
		return 0
	}
	kb, _ := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	return kb / 1024
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func die(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: bench-import <dataset-dir> <library-dir>")
		os.Exit(2)
	}
	dataset, libSrc := os.Args[1], os.Args[2]
	zipBuf, err := os.ReadFile(filepath.Join(dataset, "export.zip"))
	die(err)

	libCopy, err := os.MkdirTemp("", "ck-bench-imp-lib-")
	die(err)
	defer os.RemoveAll(libCopy)
	die(copyDir(libSrc, libCopy))
	_ = os.Remove(filepath.Join(libCopy, ".lock"))
	lib, err := library.Open(library.Options{DataDir: libCopy, LaunchBackupDelay: -1})
	die(err)

	results := map[string][]float64{}
	var order []string
	rec := func(k string, v float64) {
		if _, ok := results[k]; !ok {
			order = append(order, k)
		}
		results[k] = append(results[k], v)
	}
	for r := 0; r < rounds; r++ {
		t := time.Now()
		vfs, err := importer.FromZip(zipBuf)
		die(err)
		rec("read zip into memory", ms(time.Since(t)))
		t = time.Now()
		plan := importer.PlanNotion(vfs, importer.NotionOptions{SourceName: "bench"})
		rec("plan Notion export", ms(time.Since(t)))

		dir, err := os.MkdirTemp("", "ck-bench-imp-")
		die(err)
		dst, err := library.Open(library.Options{DataDir: dir, LaunchBackupDelay: -1})
		die(err)
		t = time.Now()
		rep, err := importer.Commit(dst, plan, importer.Options{Strategy: importer.StrategyUpdateEmptyOnly}, &importer.Source{VFS: vfs})
		die(err)
		rec("commit (create items, write files)", ms(time.Since(t)))
		if r == 0 {
			b, _ := json.Marshal(rep.Created)
			fmt.Println("items", len(plan.Items), "created", string(b), "attachments", rep.Attachments)
		}
		t = time.Now()
		_, err = importer.Commit(dst, importer.PlanNotion(vfs, importer.NotionOptions{SourceName: "bench"}), importer.Options{Strategy: importer.StrategyUpdateEmptyOnly}, &importer.Source{VFS: vfs})
		die(err)
		rec("re-import of the same export (no changes)", ms(time.Since(t)))
		_ = dst.Close()
		_ = os.RemoveAll(dir)

		t = time.Now()
		vault, err := exporter.Markdown(lib, exporter.Options{})
		die(err)
		rec("export Markdown vault (15k+ files)", ms(time.Since(t)))
		if r == 0 {
			fmt.Println("vault bytes", len(vault))
		}
		t = time.Now()
		csv := exporter.CSV(lib, "commands", exporter.Options{})
		rec("export commands as CSV", ms(time.Since(t)))
		t = time.Now()
		js, err := json.Marshal(exporter.JSON(lib, exporter.Options{}))
		die(err)
		rec("export everything as JSON", ms(time.Since(t)))
		if r == 0 {
			fmt.Println("csv bytes", len(csv), "json bytes", len(js))
		}
	}
	_ = lib.Close()
	for _, k := range order {
		v := append([]float64(nil), results[k]...)
		sort.Float64s(v)
		fmt.Printf("%-48s %9.1f ms   (%.1f to %.1f)\n", k, v[len(v)/2], v[0], v[len(v)-1])
	}
	fmt.Printf("peak RSS %.0f MB\n", rssMB())
}
