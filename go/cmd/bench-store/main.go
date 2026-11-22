// Command bench-store measures the Go storage engine on the same on-disk dataset as scripts/bench-store.ts.
//
//	go run ./cmd/bench-store <dataset-dir>
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
	"sync"
	"sync/atomic"
	"time"

	"github.com/reeper316099/Cipherkeep/go/internal/jsonstore"
)

const (
	shards   = 100
	perShard = 50
	notes    = 10_000
)

type example struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}
type cmd struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Category    string    `json:"category"`
	Syntax      string    `json:"syntax"`
	Explanation string    `json:"explanation"`
	Examples    []example `json:"examples"`
	Tags        []string  `json:"tags"`
	Mastery     int       `json:"mastery"`
}
type shard struct {
	Items []cmd `json:"items"`
}

func opts() jsonstore.Options[shard] {
	return jsonstore.Options[shard]{Version: 1, Defaults: func() shard { return shard{Items: []cmd{}} }}
}

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

// workers is the number of concurrent readers (BENCH_WORKERS overrides the default of 48, which matches the Node script).
func workers() int {
	if n, err := strconv.Atoi(os.Getenv("BENCH_WORKERS")); err == nil && n > 0 {
		return n
	}
	return 48
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func run(dataset, kind string) (map[string]float64, error) {
	tmp, err := os.MkdirTemp("", "ck-bs-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	if err := copyTree(filepath.Join(dataset, kind), tmp); err != nil {
		return nil, err
	}
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	backups := jsonstore.NewBackupManager(jsonstore.BackupOptions{DataDir: tmp, BackupDir: filepath.Join(tmp, "backups")})
	store := jsonstore.NewStore(tmp, backups, nil)
	if kind == "encrypted" {
		c, err := jsonstore.NewAESGCMCodec(key)
		if err != nil {
			return nil, err
		}
		store.Codec.Set(c)
	}
	res := map[string]float64{}

	t := time.Now()
	files := make([]*jsonstore.JsonFile[shard], shards)
	total := 0
	for s := 0; s < shards; s++ {
		f, err := jsonstore.Open(store, fmt.Sprintf("data/commands/cat-%d.json", s), opts())
		if err != nil {
			return nil, err
		}
		files[s] = f
		total += len(f.Data().Items)
	}
	res["open 100 shards (5,000 commands) ms"] = ms(time.Since(t))
	if total != shards*perShard {
		return nil, fmt.Errorf("bad load: %d", total)
	}

	// Read the dataset in place (no per-run copy of 10,000 fresh files, which the OS indexes and scans unevenly):
	// one warm-up pass, then five timed passes; the median and the best are reported.
	var paths []string
	_ = filepath.WalkDir(filepath.Join(dataset, kind, "data/notes"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			paths = append(paths, p)
		}
		return nil
	})
	if len(paths) != notes {
		return nil, fmt.Errorf("bad scan: %d", len(paths))
	}
	readAll := func() int64 {
		var next, bytes atomic.Int64
		var wg sync.WaitGroup
		for w := 0; w < workers(); w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					i := int(next.Add(1)) - 1
					if i >= len(paths) {
						return
					}
					raw, err := os.ReadFile(paths[i])
					if err != nil {
						panic(err)
					}
					plain, err := store.Codec.Decode(raw)
					if err != nil {
						panic(err)
					}
					bytes.Add(int64(len(plain)))
				}
			}()
		}
		wg.Wait()
		return bytes.Load()
	}
	readAll()
	var passes []float64
	var decoded int64
	best := 1e18
	for i := 0; i < 5; i++ {
		t = time.Now()
		decoded = readAll()
		d := ms(time.Since(t))
		passes = append(passes, d)
		if d < best {
			best = d
		}
	}
	res["read + decode 10,000 note files, median of 5 warm passes ms"] = median(passes)
	res["read + decode 10,000 note files, best of 5 ms"] = best
	res["(bytes decoded)"] = float64(decoded)

	var times []float64
	for i := 0; i < 40; i++ {
		t = time.Now()
		if _, err := files[i%shards].Update(func(d *shard) error { d.Items[0].Mastery = i; return nil }); err != nil {
			return nil, err
		}
		times = append(times, ms(time.Since(t)))
	}
	res["update one 50-command shard, median ms"] = median(times)

	t = time.Now()
	if _, err := backups.Create("bench"); err != nil {
		return nil, err
	}
	res["zip backup of the whole library ms"] = ms(time.Since(t))
	res["memory after load, RSS MB"] = rssMB()
	return res, nil
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: bench-store <dataset-dir>")
		os.Exit(2)
	}
	dataset, _ := filepath.Abs(os.Args[1])
	for _, kind := range []string{"plain", "encrypted"} {
		var runs []map[string]float64
		for i := 0; i < 3; i++ {
			r, err := run(dataset, kind)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			runs = append(runs, r)
		}
		out := map[string]float64{}
		for k := range runs[0] {
			var xs []float64
			for _, r := range runs {
				xs = append(xs, r[k])
			}
			out[k] = float64(int(median(xs)*10)) / 10
		}
		b, _ := json.Marshal(map[string]any{"engine": "go", "kind": kind, "results": out})
		fmt.Println(string(b))
	}
}
