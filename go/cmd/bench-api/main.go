// Command bench-api times the API on a copy of an existing library, with the requests of `npm run bench`. Pairs with
// scripts/bench-api.ts (Node). It measures the handler twice: in process (as the desktop app calls it, no socket) and
// over loopback HTTP (as the Node app is called). It prints JSON.
//
//	go run ./cmd/bench-api <library-dir> [rounds]
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/reeper316099/Cipherkeep/go/internal/api"
)

type request struct {
	label string
	n     int
	path  func(i int) string
}

var requests = []request{
	{"GET /notes?limit=50", 10, func(int) string { return "/notes?limit=50" }},
	{"GET /notes?limit=500 (full bodies)", 5, func(int) string { return "/notes?limit=500" }},
	{"GET /notes?limit=500&omit=markdown", 5, func(int) string { return "/notes?limit=500&omit=markdown" }},
	{"GET /commands?limit=500", 5, func(int) string { return "/commands?limit=500" }},
	{`search "nmap" (all types)`, 20, func(int) string { return "/search?q=nmap" }},
	{`search "-sV" flag`, 20, func(int) string { return "/search?q=" + url.QueryEscape("-sV") }},
	{"search cmd:cmd42 (5k commands)", 20, func(i int) string { return "/search?q=" + url.QueryEscape("cmd:cmd"+strconv.Itoa(40+i)) }},
	{`search prefix "kern" type=notes`, 20, func(int) string { return "/search?q=kern&type=notes&limit=50" }},
	{"GET /graph", 3, func(int) string { return "/graph?includeOrphans=true" }},
	{"GET /stats/overview", 3, func(int) string { return "/stats/overview" }},
	{"GET /notes/:id", 20, func(int) string { return "/notes/{first}" }},
}

const token = "bench-token"

// caller sends one request and returns its time in milliseconds and the body.
type caller func(method, path string, body []byte) (float64, []byte)

func inProcess(h http.Handler) caller {
	return func(method, path string, body []byte) (float64, []byte) {
		t := time.Now()
		req := httptest.NewRequest(method, "/api/v1"+path, bytes.NewReader(body))
		req.Host = "127.0.0.1"
		req.Header.Set("authorization", "Bearer "+token)
		if body != nil {
			req.Header.Set("content-type", "application/json")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		out := rec.Body.Bytes()
		if rec.Code >= 300 {
			log.Fatalf("%s %s: %d %s", method, path, rec.Code, out)
		}
		return float64(time.Since(t).Microseconds()) / 1000, out
	}
}

func loopback(base string) caller {
	client := &http.Client{}
	return func(method, path string, body []byte) (float64, []byte) {
		t := time.Now()
		req, _ := http.NewRequest(method, base+"/api/v1"+path, bytes.NewReader(body))
		req.Header.Set("authorization", "Bearer "+token)
		if body != nil {
			req.Header.Set("content-type", "application/json")
		}
		res, err := client.Do(req)
		if err != nil {
			log.Fatal(err)
		}
		out, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode >= 300 {
			log.Fatalf("%s %s: %d %s", method, path, res.StatusCode, out)
		}
		return float64(time.Since(t).Microseconds()) / 1000, out
	}
}

func median(xs []float64) float64 {
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	return s[len(s)/2]
}

type row struct {
	Median float64   `json:"median"`
	All    []float64 `json:"all"`
}

func run(call caller, rounds int) map[string]row {
	_, b := call("GET", "/notes?limit=1", nil)
	var first struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(b, &first); err != nil || len(first.Items) == 0 {
		log.Fatal("no notes in the library")
	}
	id := first.Items[0].ID
	rows := map[string][]float64{}
	for r := 0; r < rounds; r++ {
		for _, q := range requests {
			ms := make([]float64, q.n)
			for i := range ms {
				ms[i], _ = call("GET", strings.ReplaceAll(q.path(i), "{first}", id), nil)
			}
			rows[q.label] = append(rows[q.label], median(ms))
		}
		body, _ := json.Marshal(map[string]any{"markdown": fmt.Sprintf("edited %d", r)})
		t, _ := call("PATCH", "/notes/"+id, body)
		rows["PATCH one note"] = append(rows["PATCH one note"], t)
	}
	out := map[string]row{}
	for k, v := range rows {
		out[k] = row{median(v), v}
	}
	return out
}

func listeningSockets() int {
	out, err := exec.Command("lsof", "-a", "-p", strconv.Itoa(os.Getpid()), "-iTCP", "-sTCP:LISTEN", "-Fn").Output()
	if err != nil {
		return 0 // lsof exits 1 when nothing matches
	}
	n := 0
	for _, l := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(l, "n") {
			n++
		}
	}
	return n
}

func copyDir(src, dst string) {
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		log.Fatal(err)
	}
}

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: bench-api <library-dir> [rounds]")
	}
	rounds := 3
	if len(os.Args) > 2 {
		rounds, _ = strconv.Atoi(os.Args[2])
	}
	dir, err := os.MkdirTemp("", "ck-bench-api-")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)
	copyDir(os.Args[1], dir)

	t0 := time.Now()
	srv, err := api.New(api.Options{DataDir: dir, Token: token, LaunchBackupDelay: -1})
	if err != nil {
		log.Fatal(err)
	}
	startMs := float64(time.Since(t0).Microseconds()) / 1000

	inproc := run(inProcess(srv), rounds)
	socketsInProcess := listeningSockets()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	hs := &http.Server{Handler: srv}
	go func() { _ = hs.Serve(ln) }()
	loop := run(loopback("http://"+ln.Addr().String()), rounds)
	socketsLoopback := listeningSockets()
	_ = hs.Close()
	peak := peakRSSMB()
	if err := srv.Close(); err != nil {
		log.Fatal(err)
	}

	// Unlock: a small encrypted library at the real scrypt cost (2^15), so the time is the key derivation plus opening.
	vdir, err := os.MkdirTemp("", "ck-bench-vault-")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(vdir)
	const pass = "benchmark passphrase"
	v, err := api.New(api.Options{DataDir: vdir, LaunchBackupDelay: -1})
	if err != nil {
		log.Fatal(err)
	}
	vc := inProcess(v)
	vc("POST", "/commands", []byte(`{"name":"ls","category":"File System"}`))
	vc("POST", "/vault/enable", []byte(`{"passphrase":"`+pass+`"}`))
	_ = v.Close()
	var unlock []float64
	for r := 0; r < rounds; r++ {
		v, err := api.New(api.Options{DataDir: vdir, LaunchBackupDelay: -1})
		if err != nil {
			log.Fatal(err)
		}
		t, _ := inProcess(v)("POST", "/vault/unlock", []byte(`{"passphrase":"`+pass+`"}`))
		unlock = append(unlock, t)
		_ = v.Close()
	}

	out, _ := json.MarshalIndent(map[string]any{
		"version": "go", "startMs": startMs, "peakRssMB": peak,
		"listeningSockets": map[string]int{"inProcess": socketsInProcess, "loopback": socketsLoopback},
		"inProcess":        inproc, "loopback": loop,
		"unlockMs": row{median(unlock), unlock},
	}, "", "  ")
	fmt.Println(string(out))
}
