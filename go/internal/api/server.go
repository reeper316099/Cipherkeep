// Package api serves the /api/v1 HTTP API from Go. It is the Go port of packages/server (app.ts, index.ts,
// vault-routes.ts): the same routes, request validation, error shape, live events and encryption vault.
//
// The desktop app mounts Server as an in-process http.Handler (no TCP port, so no token or Host check is needed).
// For development and tests, cmd/ck-server serves the same handler on a loopback port, guarded like the Node server:
// a per-launch bearer token and loopback Host headers only.
package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/reeper316099/Cipherkeep/go/internal/apperr"
	"github.com/reeper316099/Cipherkeep/go/internal/jsonstore"
	"github.com/reeper316099/Cipherkeep/go/internal/library"
	"github.com/reeper316099/Cipherkeep/go/internal/notionapi"
	"github.com/reeper316099/Cipherkeep/go/internal/vault"
)

// Options configure New.
type Options struct {
	DataDir string
	// Token, when set, is the bearer token every /api request must carry, and Host headers must be loopback (the
	// loopback listener). Leave it empty for the in-process handler, which no other program can reach.
	Token string
	// Watch turns on live reload of hand edits.
	Watch bool
	// WatchInBackground starts watching after the library is open (see library.Options).
	WatchInBackground bool
	// Static is the built web UI served at / (nil: API only).
	Static fs.FS
	// KDF is the scrypt cost for new vaults (tests lower it).
	KDF vault.KDF
	// Notion is a test hook for the optional Notion API import (replaces the network).
	Notion notionapi.ClientOptions
	// LaunchBackupDelay is passed to library.Open.
	LaunchBackupDelay time.Duration
	// Logger, when set, logs one line per request: method, path without the query, status. Never bodies.
	Logger *log.Logger
	// OnOpen, when set, is called each time a library opens (at start, or on unlock). The desktop shell uses it to
	// forward live events to the window; the library's event channels close when it is locked or closed.
	OnOpen func(*library.Library)
}

// Server owns a data folder: the instance lock, the vault, and whichever handler is current (open or locked).
type Server struct {
	o       Options
	dir     string
	lock    *jsonstore.InstanceLock
	vault   *vault.Vault
	limiter vault.Limiter
	want    [32]byte

	mu      sync.RWMutex
	lib     *library.Library
	open    http.Handler
	locked  http.Handler
	key     []byte // the master key, only while the library is open and encrypted
	busy    bool
	pending sync.WaitGroup // background work that must finish before Close
	closed  bool
}

// New opens the data folder. An encrypted library starts locked; anything else opens straight away.
func New(o Options) (*Server, error) {
	dir, err := filepath.Abs(o.DataDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	lock, err := jsonstore.AcquireInstanceLock(dir)
	if err != nil {
		return nil, err
	}
	s := &Server{o: o, dir: dir, lock: lock, vault: vault.New(dir, o.KDF), want: sha256.Sum256([]byte(o.Token))}
	if s.vault.Enabled() {
		s.locked = s.lockedHandler()
		return s, nil
	}
	if err := s.openLibrary(nil); err != nil {
		_ = lock.Release()
		return nil, err
	}
	return s, nil
}

// Locked reports whether an encrypted library is waiting to be unlocked.
func (s *Server) Locked() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lib == nil
}

// Library is the open library, or nil while locked.
func (s *Server) Library() *library.Library {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lib
}

// Close closes the library and releases the data folder.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	lib := s.lib
	s.lib, s.open, s.key = nil, nil, nil
	s.mu.Unlock()
	s.pending.Wait()
	var err error
	if lib != nil {
		err = lib.Close()
	}
	return errors.Join(err, s.lock.Release())
}

func (s *Server) openLibrary(codec jsonstore.Codec) error {
	lib, err := library.Open(library.Options{DataDir: s.dir, Watch: s.o.Watch, WatchInBackground: s.o.WatchInBackground,
		Codec: jsonstore.NewCodecHolder(codec),
		Lock:  s.lock, LaunchBackupDelay: s.o.LaunchBackupDelay})
	if err != nil {
		return err
	}
	h := s.openHandler(lib)
	s.mu.Lock()
	s.lib, s.open, s.locked = lib, h, nil
	s.mu.Unlock()
	if s.o.OnOpen != nil {
		s.o.OnOpen(lib)
	}
	if lib.Codec.Encrypting() {
		s.pending.Add(1)
		go func() {
			defer s.pending.Done()
			_ = normalizeEncryption(lib)
		}()
	}
	return nil
}

// ServeHTTP applies the guards every request shares, then hands the request to the current handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	id := newRequestID()
	r = r.WithContext(context.WithValue(r.Context(), reqIDKey, id))
	rec := &statusWriter{ResponseWriter: w, status: 200}
	defer func() {
		if s.o.Logger != nil {
			s.o.Logger.Printf("%s %s %d %s", r.Method, r.URL.Path, rec.status, time.Since(start).Round(time.Microsecond))
		}
	}()
	rec.Header().Set("x-request-id", id)
	rec.Header().Set("cache-control", "no-store")
	isAPI := strings.HasPrefix(r.URL.Path, "/api/")

	if s.o.Token != "" {
		// DNS-rebinding guard: only loopback hosts.
		host := r.Host
		if i := strings.LastIndexByte(host, ':'); i >= 0 && !strings.HasSuffix(host, "]") {
			host = host[:i]
		}
		if host != "127.0.0.1" && host != "localhost" && host != "[::1]" {
			writeError(rec, r, apperr.New(403, "BAD_HOST", "Forbidden host"))
			return
		}
		// The UI shell is public on loopback; everything under /api needs the per-launch token.
		if isAPI || s.o.Static == nil {
			tok, ok := strings.CutPrefix(r.Header.Get("authorization"), "Bearer ")
			got := sha256.Sum256([]byte(tok))
			if !ok || tok == "" || subtle.ConstantTimeCompare(got[:], s.want[:]) != 1 {
				writeError(rec, r, apperr.New(401, "UNAUTHORIZED", "Missing or invalid bearer token"))
				return
			}
		}
	}

	s.mu.RLock()
	h, busy, closed := s.open, s.busy, s.closed
	if h == nil {
		h = s.locked
	}
	s.mu.RUnlock()
	if closed || h == nil {
		rec.WriteHeader(503)
		return
	}
	// While the library is being (en/de)crypted, other requests wait it out rather than race the rewrite.
	if busy && isAPI && !strings.HasPrefix(r.URL.Path, "/api/v1/vault") {
		writeError(rec, r, apperr.New(503, "BUSY", "Encryption change in progress; try again in a moment"))
		return
	}
	h.ServeHTTP(rec, r)
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) { w.status = code; w.ResponseWriter.WriteHeader(code) }
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// router wraps a ServeMux so that an unknown route (or a known path with the wrong method) gets the JSON 404 the
// Node server sends, or the UI bundle, instead of Go's plain-text 404 and 405.
type router struct {
	mux    *http.ServeMux
	static fs.FS
}

func newRouter(static fs.FS) *router { return &router{mux: http.NewServeMux(), static: static} }

// handle registers fn; any error it returns is written in the shared error shape.
func (rt *router) handle(pattern string, fn func(w http.ResponseWriter, r *http.Request) error) {
	rt.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		if err := fn(w, r); err != nil {
			writeError(w, r, err)
		}
	})
}

func (rt *router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if _, pattern := rt.mux.Handler(r); pattern != "" {
		rt.mux.ServeHTTP(w, r)
		return
	}
	if rt.static != nil && (r.Method == http.MethodGet || r.Method == http.MethodHead) &&
		!strings.HasPrefix(r.URL.Path, "/api/") && serveStatic(rt.static, w, r) {
		return
	}
	writeError(w, r, apperr.New(404, "NOT_FOUND", "No such route"))
}

// lockedHandler is what is served while an encrypted library is locked: the UI shell, status, unlock, and 423 for
// every other API route.
func (s *Server) lockedHandler() http.Handler {
	rt := newRouter(s.o.Static)
	rt.handle("GET /api/v1/health", func(w http.ResponseWriter, r *http.Request) error {
		writeJSON(w, 200, map[string]any{"status": "ok", "locked": true})
		return nil
	})
	s.vaultRoutes(rt, true)
	rt.handle("/api/v1/", func(w http.ResponseWriter, r *http.Request) error {
		return apperr.New(423, "LOCKED", "Unlock your library to continue")
	})
	return rt
}

// UICSP is the content security policy of the UI bundle.
const UICSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

var mimeTypes = map[string]string{
	".html":  "text/html; charset=utf-8",
	".js":    "text/javascript; charset=utf-8",
	".mjs":   "text/javascript; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".json":  "application/json",
	".svg":   "image/svg+xml",
	".png":   "image/png",
	".ico":   "image/x-icon",
	".woff2": "font/woff2",
	".woff":  "font/woff",
	".txt":   "text/plain; charset=utf-8",
}

// serveStatic serves a file of the bundle (never outside it); extension-less paths fall back to index.html
// (client-side routes). It reports whether it answered.
func serveStatic(root fs.FS, w http.ResponseWriter, r *http.Request) bool {
	p := r.URL.Path
	if strings.ContainsRune(p, 0) || strings.ContainsRune(p, '\\') {
		return false
	}
	name := strings.TrimPrefix(path.Clean("/"+p), "/")
	ext := strings.ToLower(path.Ext(name))
	if name == "" || ext == "" || strings.HasSuffix(p, "/") {
		name, ext = "index.html", ".html"
	}
	if !fs.ValidPath(name) {
		return false
	}
	data, err := fs.ReadFile(root, name)
	if err != nil {
		return false
	}
	ct := mimeTypes[ext]
	if ct == "" {
		ct = "application/octet-stream"
	}
	h := w.Header()
	h.Set("content-type", ct)
	h.Set("content-security-policy", UICSP)
	h.Set("x-content-type-options", "nosniff")
	h.Set("referrer-policy", "no-referrer")
	if strings.HasPrefix(name, "assets/") || strings.Contains(name, "/assets/") {
		h.Set("cache-control", "public, max-age=31536000, immutable")
	} else {
		h.Set("cache-control", "no-store")
	}
	w.WriteHeader(200)
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
	return true
}
