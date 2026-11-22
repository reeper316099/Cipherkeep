// Command bench-lib measures the Go library on the same on-disk dataset as scripts/bench-lib.ts, in process.
//
//	go run ./cmd/bench-lib <dataset-dir>
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

	"github.com/reeper316099/Cipherkeep/go/internal/domain"
	"github.com/reeper316099/Cipherkeep/go/internal/library"
	"github.com/reeper316099/Cipherkeep/go/internal/links"
	"github.com/reeper316099/Cipherkeep/go/internal/repo"
	"github.com/reeper316099/Cipherkeep/go/internal/search"
)

func rssMB() float64 {
	out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(os.Getpid())).Output()
	if err != nil {
		return 0
	}
	kb, _ := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	return kb / 1024
}

func median(xs []float64) float64 {
	c := append([]float64(nil), xs...)
	sort.Float64s(c)
	return c[len(c)/2]
}
func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func timeN(n int, fn func()) float64 {
	var ts []float64
	for i := 0; i < n; i++ {
		t := time.Now()
		fn()
		ts = append(ts, ms(time.Since(t)))
	}
	return median(ts)
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		t := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(t, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(t, b, 0o644)
	})
}

func omitMarkdown(es []domain.Entity) []byte {
	out := make([]map[string]any, len(es))
	for i, e := range es {
		m := make(map[string]any, len(e))
		for k, v := range e {
			if k != "markdown" {
				m[k] = v
			}
		}
		out[i] = m
	}
	b, _ := json.Marshal(out)
	return b
}

func run(dataset string) (map[string]float64, error) {
	tmp, err := os.MkdirTemp("", "ck-bl-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	if err := copyTree(dataset, tmp); err != nil {
		return nil, err
	}
	res := map[string]float64{}
	t := time.Now()
	lib, err := library.Open(library.Options{DataDir: tmp, LaunchBackupDelay: -1})
	if err != nil {
		return nil, err
	}
	defer lib.Close()
	res["open library (load 10k notes + 5k commands, index) ms"] = ms(time.Since(t))
	notes := lib.Repos["notes"].All()
	res[`search "nmap" ms`] = timeN(15, func() { lib.Search.Search("nmap", search.Options{}) })
	res[`search "-sV" ms`] = timeN(15, func() { lib.Search.Search("-sV", search.Options{}) })
	res["search cmd:cmd42 ms"] = timeN(15, func() { lib.Search.Search("cmd:cmd42", search.Options{}) })
	res[`search prefix "kern" notes ms`] = timeN(15, func() { lib.Search.Search("kern", search.Options{Types: []string{"notes"}}) })
	res[`search typo "enumarate" ms`] = timeN(15, func() { lib.Search.Search("enumarate", search.Options{}) })
	res["list 500 notes sorted, bodies omitted (JSON) ms"] = timeN(10, func() {
		omitMarkdown(repo.Query(lib.Repos["notes"].All(), repo.ListQuery{Limit: 500}).Items)
	})
	res["graph (JSON) ms"] = timeN(10, func() { b, _ := json.Marshal(lib.Links.Graph(links.GraphOptions{})); _ = b })
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	res["dashboard overview ms"] = timeN(5, func() {
		o, err := lib.BuildOverview(library.OverviewOptions{Now: now})
		if err != nil {
			panic(err)
		}
		_, _ = json.Marshal(o)
	})
	res["get one note ms"] = timeN(2000, func() { lib.Repos["notes"].Get(notes[5000].ID()) })
	i := 0
	res["patch one note (write to disk) ms"] = timeN(20, func() {
		i++
		if _, err := lib.Repos["notes"].Patch(notes[100+i].ID(), map[string]any{"markdown": "edited " + strconv.Itoa(i)}); err != nil {
			panic(err)
		}
	})
	cmds := lib.Repos["commands"].All()
	res["patch one command (write shard) ms"] = timeN(20, func() {
		i++
		if _, err := lib.Repos["commands"].Patch(cmds[10+i].ID(), map[string]any{"notes": "x" + strconv.Itoa(i)}); err != nil {
			panic(err)
		}
	})
	res["memory, RSS MB"] = rssMB()
	return res, nil
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: bench-lib <dataset-dir>")
		os.Exit(2)
	}
	dataset, _ := filepath.Abs(os.Args[1])
	if out := os.Getenv("BENCH_PROFILE"); out != "" {
		if err := profileOverview(dataset, out); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	var runs []map[string]float64
	for i := 0; i < 3; i++ {
		r, err := run(dataset)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		runs = append(runs, r)
	}
	agg := func(f func([]float64) float64) map[string]float64 {
		out := map[string]float64{}
		for k := range runs[0] {
			var xs []float64
			for _, r := range runs {
				xs = append(xs, r[k])
			}
			out[k] = float64(int(f(xs)*100)) / 100
		}
		return out
	}
	lo := func(xs []float64) float64 { c := append([]float64(nil), xs...); sort.Float64s(c); return c[0] }
	hi := func(xs []float64) float64 { c := append([]float64(nil), xs...); sort.Float64s(c); return c[len(c)-1] }
	b, _ := json.Marshal(map[string]any{"engine": "go", "results": agg(median), "min": agg(lo), "max": agg(hi)})
	fmt.Println(string(b))
}
