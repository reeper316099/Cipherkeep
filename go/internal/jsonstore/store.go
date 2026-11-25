package jsonstore

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// InvalidInfo describes an external edit that was rejected; in-memory state was kept.
type InvalidInfo struct {
	Rel     string
	Message string
	Line    int
}

// QuarantineInfo describes a corrupt file that was moved aside.
type QuarantineInfo struct {
	Rel      string
	MovedTo  string
	Restored bool
}

type anyFile interface {
	relPath() string
	reloadExternal()
	rewrite() error
}

// Store owns a data directory: the files opened through it, the codec, the backups and the watcher.
type Store struct {
	Dir     string
	Backups *BackupManager
	Codec   *CodecHolder

	mutex KeyedMutex

	mu    sync.Mutex
	files map[string]anyFile

	hmu         sync.RWMutex
	onReload    []func(rel string)
	onInvalid   []func(InvalidInfo)
	onQuarantne []func(QuarantineInfo)

	w watcherState
}

// NewStore creates a store for dir. backups and codec may be nil.
func NewStore(dir string, backups *BackupManager, codec *CodecHolder) *Store {
	if codec == nil {
		codec = NewCodecHolder(nil)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	return &Store{Dir: abs, Backups: backups, Codec: codec, files: map[string]anyFile{}}
}

// OnReload registers a handler for files changed on disk by someone else and reloaded.
func (s *Store) OnReload(fn func(rel string)) {
	s.hmu.Lock()
	s.onReload = append(s.onReload, fn)
	s.hmu.Unlock()
}

// OnInvalid registers a handler for external edits that were invalid (memory was kept).
func (s *Store) OnInvalid(fn func(InvalidInfo)) {
	s.hmu.Lock()
	s.onInvalid = append(s.onInvalid, fn)
	s.hmu.Unlock()
}

// OnQuarantine registers a handler for corrupt files moved aside.
func (s *Store) OnQuarantine(fn func(QuarantineInfo)) {
	s.hmu.Lock()
	s.onQuarantne = append(s.onQuarantne, fn)
	s.hmu.Unlock()
}

func (s *Store) emitReload(rel string) {
	s.hmu.RLock()
	hs := append([]func(string){}, s.onReload...)
	s.hmu.RUnlock()
	for _, h := range hs {
		h(rel)
	}
}

func (s *Store) emitInvalid(i InvalidInfo) {
	s.hmu.RLock()
	hs := append([]func(InvalidInfo){}, s.onInvalid...)
	s.hmu.RUnlock()
	for _, h := range hs {
		h(i)
	}
}

func (s *Store) emitQuarantine(q QuarantineInfo) {
	s.hmu.RLock()
	hs := append([]func(QuarantineInfo){}, s.onQuarantne...)
	s.hmu.RUnlock()
	for _, h := range hs {
		h(q)
	}
}

// Abs resolves rel inside the data directory and refuses anything that escapes it.
func (s *Store) Abs(rel string) (string, error) {
	full := filepath.Join(s.Dir, filepath.FromSlash(rel))
	if full != s.Dir && !strings.HasPrefix(full, s.Dir+string(filepath.Separator)) {
		return "", newErr(CodePathTraversal, "Path escapes data directory", rel)
	}
	return full, nil
}

// Lock serialises work on a key (usually a relative file path) and returns the release function.
func (s *Store) Lock(key string) func() { return s.mutex.Lock(key) }

// Forget drops a file handle (for example an emptied shard) without deleting the file.
func (s *Store) Forget(rel string) { s.mu.Lock(); delete(s.files, rel); s.mu.Unlock() }

// Rels lists the relative paths of the open files.
func (s *Store) Rels() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.files))
	for k := range s.files {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// RewriteAll rewrites every open file through the current codec (used when encryption is switched on or off).
func (s *Store) RewriteAll() error {
	s.mu.Lock()
	files := make([]anyFile, 0, len(s.files))
	for _, f := range s.files {
		files = append(files, f)
	}
	s.mu.Unlock()
	for _, f := range files {
		if err := f.rewrite(); err != nil {
			return err
		}
	}
	return nil
}

// OpenPaths returns the absolute paths of every open file.
func (s *Store) OpenPaths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.files))
	for rel := range s.files {
		if p, err := s.Abs(rel); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// RewritePlaintext rewrites, through the current codec, every open file that is not encrypted on disk (used after a
// plaintext backup was restored into an encrypted library, so nothing readable lingers).
func (s *Store) RewritePlaintext() error {
	s.mu.Lock()
	files := make([]anyFile, 0, len(s.files))
	for _, f := range s.files {
		files = append(files, f)
	}
	s.mu.Unlock()
	for _, f := range files {
		p, err := s.Abs(f.relPath())
		if err != nil {
			continue
		}
		raw, err := os.ReadFile(p)
		if err != nil || IsEncrypted(raw) {
			continue
		}
		if err := f.rewrite(); err != nil {
			return err
		}
	}
	return nil
}

// Options describes a JSON file kept by the store.
type Options[T any] struct {
	Version  int
	Defaults func() T
	// Validate may normalise the value in place. It runs on load, reload and every update.
	Validate func(*T) error
	// Migrations[n] upgrades the raw data from version n to n+1.
	Migrations map[int]func(json.RawMessage) (json.RawMessage, error)
	// Clone optionally copies a value faster than the default JSON round trip.
	Clone func(T) T
}

// JsonFile is one versioned JSON file. Reads return immutable snapshots: every write swaps in a new value, so a
// value returned by Data stays valid (and unchanged) while other goroutines update the file.
type JsonFile[T any] struct {
	store *Store
	rel   string
	opts  Options[T]

	mu       sync.RWMutex
	data     T
	lastText []byte
	lastHash string
}

var _ anyFile = (*JsonFile[struct{}])(nil)

func sum(b []byte) string { h := sha1.Sum(b); return hex.EncodeToString(h[:]) }

// Open loads (or creates) the file at rel. Opening the same path twice returns the same handle.
func Open[T any](s *Store, rel string, opts Options[T]) (*JsonFile[T], error) {
	s.mu.Lock()
	if existing, ok := s.files[rel]; ok {
		s.mu.Unlock()
		if f, ok := existing.(*JsonFile[T]); ok {
			return f, nil
		}
		return nil, fmt.Errorf("jsonstore: %s was opened with a different type", rel)
	}
	s.mu.Unlock()
	if _, err := s.Abs(rel); err != nil {
		return nil, err
	}
	f := &JsonFile[T]{store: s, rel: rel, opts: opts}
	if err := f.load(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.files[rel] = f
	s.mu.Unlock()
	return f, nil
}

func (f *JsonFile[T]) relPath() string { return f.rel }

// Reload re-reads the file from disk as if the watcher had noticed an edit (used after a backup is restored).
func (f *JsonFile[T]) Reload() { f.reloadExternal() }

// Rel is the path relative to the data directory, with forward slashes.
func (f *JsonFile[T]) Rel() string { return f.rel }

// Path is the absolute path.
func (f *JsonFile[T]) Path() string { p, _ := f.store.Abs(f.rel); return p }

// Data returns the current value. Treat it as read-only; use Update to change it.
func (f *JsonFile[T]) Data() T { f.mu.RLock(); defer f.mu.RUnlock(); return f.data }

// Text returns the last plain JSON text written or read.
func (f *JsonFile[T]) Text() []byte { f.mu.RLock(); defer f.mu.RUnlock(); return f.lastText }

func (f *JsonFile[T]) setState(v T, text, raw []byte) {
	f.mu.Lock()
	f.data, f.lastText, f.lastHash = v, text, sum(raw)
	f.mu.Unlock()
}

func (f *JsonFile[T]) check(v *T) error {
	if f.opts.Validate != nil {
		return f.opts.Validate(v)
	}
	return nil
}

func (f *JsonFile[T]) clone(v T) (T, error) {
	if f.opts.Clone != nil {
		return f.opts.Clone(v), nil
	}
	var out T
	b, err := json.Marshal(v)
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(b, &out)
	return out, err
}

func (f *JsonFile[T]) fileErr(err error) error {
	if e, ok := err.(*Error); ok && e.File == "" {
		c := *e
		c.File = f.rel
		return &c
	}
	return err
}

// load handles a missing file (create defaults), a corrupt one (quarantine and restore) and migrations.
func (f *JsonFile[T]) load() error {
	path := f.Path()
	raw, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		return f.withLock(func() error {
			v := f.opts.Defaults()
			if err := f.check(&v); err != nil {
				return err
			}
			return f.writeLocked(v)
		})
	}
	// A locked or wrongly keyed file is never "corrupt": do not quarantine it or replace it with defaults.
	text, err := f.store.Codec.Decode(raw)
	if err != nil {
		return f.fileErr(err)
	}
	if err := f.ingest(text, raw); err != nil {
		if IsCode(err, CodeVersionTooNew) {
			return err
		}
		return f.quarantineAndRecover(raw)
	}
	return nil
}

func (f *JsonFile[T]) withLock(fn func() error) error {
	defer f.store.mutex.Lock(f.rel)()
	return fn()
}

// ingest parses, migrates and validates text; on success it replaces the in-memory state.
func (f *JsonFile[T]) ingest(text, raw []byte) error {
	// Fast path: a file already at the current version decodes in one pass. Anything unusual (older version,
	// damage, a missing key) falls through to the careful path below, which produces the precise errors.
	var quick struct {
		SchemaVersion *float64 `json:"schemaVersion"`
		Data          *T       `json:"data"`
	}
	if json.Unmarshal(text, &quick) == nil && quick.SchemaVersion != nil && quick.Data != nil &&
		int(*quick.SchemaVersion) == f.opts.Version {
		val := *quick.Data
		if err := f.check(&val); err != nil {
			return err
		}
		f.setState(val, text, raw)
		return nil
	}
	var env map[string]json.RawMessage
	if err := json.Unmarshal(text, &env); err != nil {
		return &Error{Code: CodeInvalidJSON, Msg: "Invalid JSON: " + err.Error(), File: f.rel, Line: errorLine(text, err)}
	}
	vraw, hasV := env["schemaVersion"]
	data, hasD := env["data"]
	var ver float64
	if !hasV || !hasD || json.Unmarshal(vraw, &ver) != nil {
		return newErr(CodeInvalidEnvelope, "Missing schemaVersion/data envelope", f.rel)
	}
	from := int(ver)
	if from > f.opts.Version {
		return newErr(CodeVersionTooNew, fmt.Sprintf("File schema v%d is newer than supported v%d", from, f.opts.Version), f.rel)
	}
	for v := from; v < f.opts.Version; v++ {
		step := f.opts.Migrations[v]
		if step == nil {
			return newErr(CodeMissingMigrate, fmt.Sprintf("No migration from v%d", v), f.rel)
		}
		var err error
		if data, err = step(data); err != nil {
			return err
		}
	}
	var val T
	if err := json.Unmarshal(data, &val); err != nil {
		return newErr(CodeInvalidEnvelope, "Data does not match the expected shape: "+err.Error(), f.rel)
	}
	if err := f.check(&val); err != nil {
		return err
	}
	if from < f.opts.Version {
		if f.store.Backups != nil {
			if _, err := f.store.Backups.Create("pre-migration"); err != nil {
				return err
			}
		}
		return f.writeLocked(val)
	}
	f.setState(val, text, raw)
	return nil
}

// reloadExternal is called by the watcher. Invalid hand edits never replace in-memory state.
func (f *JsonFile[T]) reloadExternal() {
	defer f.store.mutex.Lock(f.rel)()
	raw, err := ReadFileRetry(f.Path())
	if err != nil {
		return
	}
	f.mu.RLock()
	same := sum(raw) == f.lastHash
	f.mu.RUnlock()
	if same {
		return // our own write
	}
	text, err := f.store.Codec.Decode(raw)
	if err != nil {
		f.store.emitInvalid(InvalidInfo{Rel: f.rel, Message: err.Error()})
		return
	}
	if err := f.ingest(text, raw); err != nil {
		info := InvalidInfo{Rel: f.rel, Message: err.Error()}
		if e, ok := err.(*Error); ok {
			info.Line = e.Line
		}
		f.store.emitInvalid(info)
		return
	}
	f.store.emitReload(f.rel)
}

// Update applies fn to a copy of the data, validates it, writes it, then swaps it in. Concurrent updates to the
// same file run one at a time.
func (f *JsonFile[T]) Update(fn func(*T) error) (T, error) {
	var out T
	err := f.withLock(func() error {
		draft, err := f.clone(f.Data())
		if err != nil {
			return err
		}
		if err := fn(&draft); err != nil {
			return err
		}
		if err := f.check(&draft); err != nil {
			return err
		}
		if err := f.writeLocked(draft); err != nil {
			return err
		}
		out = draft
		return nil
	})
	return out, err
}

func (f *JsonFile[T]) rewrite() error {
	return f.withLock(func() error { return f.writeLocked(f.Data()) })
}

// writeLocked writes valid through the codec and stores it. The caller holds the file's lock.
func (f *JsonFile[T]) writeLocked(valid T) error {
	text, err := StableStringify(map[string]any{"schemaVersion": f.opts.Version, "data": valid})
	if err != nil {
		return err
	}
	raw, err := f.store.Codec.Encode(text)
	if err != nil {
		return err
	}
	path := f.Path()
	if err := AtomicWriteFile(path, raw); err != nil {
		return err
	}
	f.setState(valid, text, raw)
	return nil
}

// quarantineAndRecover moves a corrupt file aside, then restores the newest backup of it, or defaults.
func (f *JsonFile[T]) quarantineAndRecover(badRaw []byte) error {
	qdir := filepath.Join(f.store.Dir, ".quarantine")
	if err := os.MkdirAll(qdir, 0o755); err != nil {
		return err
	}
	moved := filepath.Join(qdir, fmt.Sprintf("%s.%d.bad", strings.ReplaceAll(f.rel, "/", "__"), time.Now().UnixMilli()))
	if err := os.WriteFile(moved, badRaw, 0o644); err != nil {
		return err
	}
	if err := os.Remove(f.Path()); err != nil && !os.IsNotExist(err) {
		return err
	}
	restored := false
	if f.store.Backups != nil {
		if b, ok := f.store.Backups.ReadFromLatest(f.rel); ok {
			if text, err := f.store.Codec.Decode(b); err == nil && f.ingest(text, b) == nil {
				if err := f.withLock(func() error { return f.writeLocked(f.Data()) }); err == nil {
					restored = true
				}
			}
		}
	}
	if !restored {
		if err := f.withLock(func() error {
			v := f.opts.Defaults()
			if err := f.check(&v); err != nil {
				return err
			}
			return f.writeLocked(v)
		}); err != nil {
			return err
		}
	}
	f.store.emitQuarantine(QuarantineInfo{Rel: f.rel, MovedTo: moved, Restored: restored})
	return nil
}

// Transaction applies fn across several files. All writes succeed, or the files already written are rolled back.
func Transaction[T any](files []*JsonFile[T], fn func(drafts []*T) error) error {
	if len(files) == 0 {
		return nil
	}
	unique := make([]*JsonFile[T], 0, len(files))
	seen := map[*JsonFile[T]]bool{}
	for _, f := range files {
		if !seen[f] {
			seen[f] = true
			unique = append(unique, f)
		}
	}
	sort.Slice(unique, func(i, j int) bool { return unique[i].rel < unique[j].rel })
	for _, f := range unique {
		defer f.store.mutex.Lock(f.rel)() // taken in sorted order, so two transactions cannot deadlock
	}
	drafts := make([]*T, len(files))
	for i, f := range files {
		d, err := f.clone(f.Data())
		if err != nil {
			return err
		}
		drafts[i] = &d
	}
	if err := fn(drafts); err != nil {
		return err
	}
	for i, f := range files {
		if err := f.check(drafts[i]); err != nil {
			return err
		}
	}
	type prev struct {
		f    *JsonFile[T]
		text []byte
		data T
	}
	var written []prev
	for i, f := range files {
		written = append(written, prev{f: f, text: f.Text(), data: f.Data()})
		if err := f.writeLocked(*drafts[i]); err != nil {
			for j := len(written) - 1; j >= 0; j-- {
				w := written[j]
				if enc, eerr := w.f.store.Codec.Encode(w.text); eerr == nil {
					path, _ := w.f.store.Abs(w.f.rel)
					_ = AtomicWriteFile(path, enc)
					w.f.setState(w.data, w.text, enc)
				}
			}
			return err
		}
	}
	return nil
}
