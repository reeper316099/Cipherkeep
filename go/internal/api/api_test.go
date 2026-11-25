package api

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/reeper316099/Cipherkeep/go/internal/jsonstore"
	"github.com/reeper316099/Cipherkeep/go/internal/notionapi"
	"github.com/reeper316099/Cipherkeep/go/internal/vault"
)

// Most of the API is tested by running the TypeScript API, vault and UI-serving tests against the Go server
// (`npm run test:go-contract`). These Go tests cover what only Go has (the in-process handler, which needs no port and
// no token) and repeat the vault checks so `go test` alone catches a regression.

const sentinel = "ZEBRA-SENTINEL-4471"
const passphrase = "correct horse battery"

type resp struct {
	status int
	body   map[string]any
	list   []any
	header http.Header
}

func call(t *testing.T, h http.Handler, method, path string, body any, hdr ...string) resp {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, "/api/v1"+path, rd)
	if body != nil {
		req.Header.Set("content-type", "application/json")
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		if hdr[i] == "host" {
			req.Host = hdr[i+1]
		} else {
			req.Header.Set(hdr[i], hdr[i+1])
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	r := resp{status: rec.Code, header: rec.Header()}
	raw := rec.Body.Bytes()
	if len(raw) > 0 && raw[0] == '[' {
		_ = json.Unmarshal(raw, &r.list)
	} else if len(raw) > 0 {
		_ = json.Unmarshal(raw, &r.body)
	}
	return r
}

func newServer(t *testing.T, o Options) *Server {
	t.Helper()
	o.LaunchBackupDelay = -1
	o.KDF = vault.KDF{Algo: "scrypt", N: 1024, R: 8, P: 1}
	s, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func errCode(r resp) string {
	e, _ := r.body["error"].(map[string]any)
	c, _ := e["code"].(string)
	return c
}

func TestInProcessHandlerNeedsNoPortNoTokenAndAnyHost(t *testing.T) {
	s := newServer(t, Options{DataDir: t.TempDir()})
	defer s.Close()
	// The desktop shell calls the handler directly; the request never touches a socket, so there is nothing for
	// another program to reach and no token to steal.
	r := call(t, s, "GET", "/health", nil, "host", "wails.localhost")
	if r.status != 200 || r.header.Get("x-request-id") == "" || r.header.Get("cache-control") != "no-store" {
		t.Fatalf("health: %d %v", r.status, r.header)
	}
	c := call(t, s, "POST", "/commands", map[string]any{"name": "ls", "category": "File System"})
	if c.status != 201 || len(c.body["id"].(string)) != 26 {
		t.Fatalf("create: %d %v", c.status, c.body)
	}
	l := call(t, s, "GET", "/commands?omit=whatItDoes,notes", nil)
	items := l.body["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["whatItDoes"] != nil {
		t.Fatalf("list: %v", l.body)
	}
	if r := call(t, s, "GET", "/meta", nil); r.body["counts"].(map[string]any)["commands"] != 1.0 {
		t.Fatalf("meta: %v", r.body)
	}
}

func TestLoopbackModeRequiresTheTokenAndALoopbackHost(t *testing.T) {
	ui := fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>ck</title>")}}
	s := newServer(t, Options{DataDir: t.TempDir(), Token: "launch-token", Static: ui})
	defer s.Close()
	auth := []string{"authorization", "Bearer launch-token", "host", "127.0.0.1:5000"}
	if r := call(t, s, "GET", "/health", nil, "host", "127.0.0.1:5000"); r.status != 401 || errCode(r) != "UNAUTHORIZED" {
		t.Fatalf("no token: %d", r.status)
	}
	if r := call(t, s, "GET", "/health", nil, "authorization", "Bearer wrong", "host", "localhost"); r.status != 401 {
		t.Fatalf("wrong token: %d", r.status)
	}
	if r := call(t, s, "GET", "/health", nil, "authorization", "Bearer launch-token", "host", "evil.example"); r.status != 403 {
		t.Fatalf("bad host: %d", r.status)
	}
	if r := call(t, s, "GET", "/health", nil, auth...); r.status != 200 {
		t.Fatalf("good request: %d", r.status)
	}
	if r := call(t, s, "GET", "/health", nil, "authorization", "Bearer launch-token", "host", "[::1]:80"); r.status != 200 {
		t.Fatalf("ipv6 loopback: %d", r.status)
	}
	// The UI shell needs no token (it is what asks for one), but still only on loopback.
	req := httptest.NewRequest("GET", "/commands/abc", nil)
	req.Host = "127.0.0.1:5000"
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "<title>ck</title>") ||
		!strings.Contains(rec.Header().Get("content-security-policy"), "frame-ancestors 'none'") {
		t.Fatalf("ui: %d %q", rec.Code, rec.Body.String())
	}
}

func TestUnknownRoutesAndWrongMethodsGetTheJSON404(t *testing.T) {
	s := newServer(t, Options{DataDir: t.TempDir()})
	defer s.Close()
	for _, c := range [][2]string{{"GET", "/nope"}, {"PUT", "/health"}, {"GET", "/commands/"}, {"POST", "/commands/x"}} {
		r := call(t, s, c[0], c[1], nil)
		if r.status != 404 || errCode(r) != "NOT_FOUND" || r.body["error"].(map[string]any)["requestId"] == "" {
			t.Fatalf("%s %s: %d %v", c[0], c[1], r.status, r.body)
		}
	}
	if r := call(t, s, "GET", "/search?limit=1001", nil); r.status != 400 || errCode(r) != "VALIDATION" {
		t.Fatalf("limit: %d", r.status)
	}
	if r := call(t, s, "GET", "/search?type=nope", nil); r.status != 404 {
		t.Fatalf("unknown type: %d", r.status)
	}
}

// plaintextOffenders lists every file on disk, and every file inside a backup, that is readable without the key.
func plaintextOffenders(t *testing.T, dir string) []string {
	t.Helper()
	var bad []string
	check := func(name string, raw []byte) {
		switch {
		case bytes.Contains(raw, []byte(sentinel)):
			bad = append(bad, name+" (sentinel)")
		case strings.HasSuffix(name, ".jsonl"):
			for _, l := range strings.Split(string(raw), "\n") {
				if l != "" && !jsonstore.IsEncryptedLine(l) {
					bad = append(bad, name+" (plain log line)")
					break
				}
			}
		case !jsonstore.IsEncrypted(raw):
			bad = append(bad, name+" (not encrypted)")
		}
	}
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		top := strings.Split(filepath.ToSlash(rel), "/")[0]
		if d.IsDir() {
			if p != dir && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") || top == "vault.json" || top == "meta.json" {
			return nil
		}
		raw, _ := os.ReadFile(p)
		if top != "backups" {
			check(filepath.ToSlash(rel), raw)
			return nil
		}
		zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
		if err != nil {
			bad = append(bad, rel+" (unreadable zip)")
			return nil
		}
		for _, f := range zr.File {
			if f.FileInfo().IsDir() || f.Name == "meta.json" || f.Name == "vault.json" {
				continue
			}
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			rc.Close()
			check(d.Name()+":"+f.Name, b)
		}
		return nil
	})
	return bad
}

func TestVaultEncryptsEverythingLocksAndDecryptsBack(t *testing.T) {
	dir := t.TempDir()
	s := newServer(t, Options{DataDir: dir})
	cmd := call(t, s, "POST", "/commands", map[string]any{"name": "zebra-cmd", "category": "File System",
		"notes": "private " + sentinel})
	note := call(t, s, "POST", "/notes", map[string]any{"title": "Secret " + sentinel, "folder": "Secret Folder",
		"markdown": "# body\n\n" + sentinel + "\n"})
	call(t, s, "POST", "/study/answer", map[string]any{"id": cmd.body["id"], "rating": "good", "cardType": "name"})
	call(t, s, "PUT", "/templates/commands", map[string]any{"name": sentinel, "sections": []any{map[string]any{"key": "a", "title": "A"}}})
	call(t, s, "POST", "/starter-pack/load", map[string]any{"strategy": "skip"})
	call(t, s, "POST", "/backups", map[string]any{"reason": "plain-before"})
	noteID := note.body["id"].(string)

	if r := call(t, s, "POST", "/vault/enable", map[string]any{"passphrase": "short"}); errCode(r) != "WEAK_PASSPHRASE" {
		t.Fatalf("weak: %v", r.body)
	}
	en := call(t, s, "POST", "/vault/enable", map[string]any{"passphrase": passphrase})
	if en.status != 200 || en.body["recoveryKey"] == nil {
		t.Fatalf("enable: %d %v", en.status, en.body)
	}
	if bad := plaintextOffenders(t, dir); len(bad) > 0 {
		t.Fatalf("readable after enabling: %v", bad)
	}
	if r := call(t, s, "GET", "/notes/"+noteID, nil); !strings.Contains(r.body["markdown"].(string), sentinel) {
		t.Fatal("the running app must keep reading the data")
	}
	call(t, s, "POST", "/notes", map[string]any{"title": "New " + sentinel, "markdown": sentinel})
	call(t, s, "POST", "/backups", map[string]any{"reason": "after"})
	if bad := plaintextOffenders(t, dir); len(bad) > 0 {
		t.Fatalf("readable after writing: %v", bad)
	}

	// Lock now: every route but status and unlock is 423, and the open library is closed.
	if r := call(t, s, "POST", "/vault/lock", nil); r.status != 200 || !s.Locked() || s.Library() != nil {
		t.Fatalf("lock: %d", r.status)
	}
	if r := call(t, s, "GET", "/commands", nil); r.status != 423 || errCode(r) != "LOCKED" {
		t.Fatalf("locked list: %d", r.status)
	}
	if r := call(t, s, "GET", "/health", nil); r.body["locked"] != true {
		t.Fatalf("health: %v", r.body)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// Restart: locked, wrong passphrase refused, right one opens it.
	s = newServer(t, Options{DataDir: dir})
	defer func() { _ = s.Close() }()
	if r := call(t, s, "GET", "/vault", nil); r.body["enabled"] != true || r.body["locked"] != true {
		t.Fatalf("vault: %v", r.body)
	}
	if r := call(t, s, "POST", "/vault/unlock", map[string]any{"passphrase": "not the passphrase"}); r.status != 401 {
		t.Fatalf("wrong: %d", r.status)
	}
	if r := call(t, s, "POST", "/vault/unlock", map[string]any{"passphrase": passphrase, "recoveryKey": "x"}); r.status != 400 {
		t.Fatalf("both: %d", r.status)
	}
	if r := call(t, s, "POST", "/vault/unlock", map[string]any{"passphrase": passphrase}); r.status != 200 {
		t.Fatalf("unlock: %d %v", r.status, r.body)
	}
	if r := call(t, s, "GET", "/commands/"+cmd.body["id"].(string), nil); r.body["practiceCount"] != 1.0 {
		t.Fatalf("command after unlock: %v", r.body)
	}

	// Disable: everything is plain again, notes back in their folders.
	if r := call(t, s, "POST", "/vault/disable", map[string]any{"passphrase": passphrase}); r.status != 200 {
		t.Fatalf("disable: %d %v", r.status, r.body)
	}
	if _, err := os.Stat(filepath.Join(dir, vault.File)); !os.IsNotExist(err) {
		t.Fatal("vault.json must be gone")
	}
	ents, _ := os.ReadDir(filepath.Join(dir, "data/notes/secret-folder"))
	if len(ents) != 1 {
		t.Fatalf("notes folder: %v", ents)
	}
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && !strings.Contains(p, string(filepath.Separator)+"backups"+string(filepath.Separator)) {
			if raw, _ := os.ReadFile(p); jsonstore.IsEncrypted(raw) {
				t.Errorf("still encrypted: %s", p)
			}
		}
		return nil
	})
}

func TestEncryptionChangesAreExclusiveAndOtherRequestsWait(t *testing.T) {
	s := newServer(t, Options{DataDir: t.TempDir()})
	defer s.Close()
	s.mu.Lock()
	s.busy = true
	s.mu.Unlock()
	if r := call(t, s, "GET", "/commands", nil); r.status != 503 || errCode(r) != "BUSY" {
		t.Fatalf("busy: %d", r.status)
	}
	if r := call(t, s, "GET", "/vault", nil); r.status != 200 || r.body["busy"] != true {
		t.Fatalf("vault status while busy: %d", r.status)
	}
	if r := call(t, s, "POST", "/vault/enable", map[string]any{"passphrase": passphrase}); r.status != 409 {
		t.Fatalf("second change: %d", r.status)
	}
}

func TestEventsStreamHelloAndChanges(t *testing.T) {
	s := newServer(t, Options{DataDir: t.TempDir()})
	ts := httptest.NewServer(s)
	defer func() { ts.Close(); _ = s.Close() }()
	res, err := http.Get(ts.URL + "/api/v1/events")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.Header.Get("content-type") != "text/event-stream" {
		t.Fatalf("content-type %q", res.Header.Get("content-type"))
	}
	lines := make(chan string, 32)
	go func() {
		sc := bufio.NewScanner(res.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	want := []string{"event: hello", "event: change"}
	created := false
	deadline := time.After(5 * time.Second)
	for len(want) > 0 {
		select {
		case l, open := <-lines:
			if !open {
				t.Fatalf("stream ended; still waiting for %v", want)
			}
			if l == want[0] {
				want = want[1:]
			}
			if !created {
				created = true
				call(t, s, "POST", "/snippets", map[string]any{"title": "sse"})
			}
		case <-deadline:
			t.Fatalf("still waiting for %v", want)
		}
	}
}

// The optional Notion API import through the API (the route test of the Node server's notion-api.test.ts): off by
// default, then a job against a fake Notion that only api.notion.com is asked, with the token as a bearer header and
// written nowhere.
func TestNotionAPIImportIsOffByDefaultAndKeepsTheTokenOffDisk(t *testing.T) {
	const token = "secret_fake-notion-token-for-tests-only"
	var hosts []string
	var mu sync.Mutex
	fake := func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		hosts = append(hosts, r.URL.Host)
		mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+token {
			return &http.Response{StatusCode: 401, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"code":"unauthorized"}`))}, nil
		}
		// One loose page in the workspace; no databases, and the page has no blocks.
		results := `[]`
		if r.Body != nil {
			b, _ := io.ReadAll(r.Body)
			if bytes.Contains(b, []byte(`"value":"page"`)) {
				results = `[{"object":"page","id":"1234567890abcdef1234567890abcdef","parent":{"type":"workspace","workspace":true},` +
					`"properties":{"Name":{"type":"title","title":[{"plain_text":"Fake Notion page"}]}}}]`
			}
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{"object":"list","results":` + results + `,"has_more":false,"next_cursor":null}`))}, nil
	}
	dir := t.TempDir()
	s := newServer(t, Options{DataDir: dir, Notion: notionapi.ClientOptions{Do: fake, MinInterval: time.Nanosecond,
		Sleep: func(context.Context, time.Duration) error { return nil }}})
	defer s.Close()

	if r := call(t, s, "POST", "/import/notion-api/start", map[string]any{"token": token}); r.status != 403 || errCode(r) != "NOTION_API_DISABLED" {
		t.Fatalf("while off: %d %v", r.status, r.body)
	}
	if r := call(t, s, "PUT", "/settings", map[string]any{"notionApiEnabled": true}); r.status != 200 {
		t.Fatalf("turn on: %d", r.status)
	}
	if r := call(t, s, "POST", "/import/notion-api/start", map[string]any{"token": "short"}); r.status != 400 {
		t.Fatalf("a too-short token must be refused: %d", r.status)
	}
	start := call(t, s, "POST", "/import/notion-api/start", map[string]any{"token": token})
	id, _ := start.body["jobId"].(string)
	if start.status != 200 || id == "" {
		t.Fatalf("start: %d %v", start.status, start.body)
	}
	var job resp
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		job = call(t, s, "GET", "/import/notion-api/"+id, nil)
		if job.body["state"] != "running" {
			break
		}
	}
	if job.body["state"] != "done" || job.body["preview"] == nil {
		t.Fatalf("job: %v", job.body)
	}
	items, _ := job.body["preview"].(map[string]any)["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["title"] != "Fake Notion page" {
		t.Fatalf("preview items: %v", items)
	}
	mu.Lock()
	for _, h := range hosts {
		if h != "api.notion.com" {
			t.Errorf("contacted %s", h)
		}
	}
	mu.Unlock()
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if raw, _ := os.ReadFile(p); bytes.Contains(raw, []byte(token)) {
				t.Errorf("the token was written to %s", p)
			}
		}
		return nil
	})
}
