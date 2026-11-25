package library

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/reeper316099/Cipherkeep/go/internal/domain"
	"github.com/reeper316099/Cipherkeep/go/internal/jsonstore"
	"github.com/reeper316099/Cipherkeep/go/internal/links"
	"github.com/reeper316099/Cipherkeep/go/internal/repo"
	"github.com/reeper316099/Cipherkeep/go/internal/search"
)

// DataVersion is the version of the data folder layout.
const DataVersion = 1

// Options configure Open.
type Options struct {
	DataDir string
	// Watch turns on live reload of hand edits.
	Watch bool
	// Codec opens an encrypted library (nil: plain).
	Codec *jsonstore.CodecHolder
	// Lock is an instance lock the caller already holds and will release itself.
	Lock *jsonstore.InstanceLock
	// LaunchBackupDelay is how long after opening the "launch" backup runs (default 3 s; negative: never).
	LaunchBackupDelay time.Duration
	// WatchInBackground starts watching (when Watch is on) after Open returns instead of before. Setting up the
	// watchers costs seconds on a large library on macOS, where every watched file needs its own handle; the desktop
	// app shows the library first and starts watching right after. Hand edits made in those seconds are not seen.
	WatchInBackground bool
}

// Event is something the live-event stream reports.
type Event struct {
	Kind       string // change, invalid, quarantine, reload
	Collection string
	ID         string
	Change     string // upsert, remove, rebuild (for kind change)
	Data       any
}

// Library is an open library.
type Library struct {
	Dir     string
	Store   *jsonstore.Store
	Backups *jsonstore.BackupManager
	Codec   *jsonstore.CodecHolder
	Repos   map[string]repo.Repo
	Search  *search.Index
	Links   *links.Index

	Settings   *jsonstore.JsonFile[domain.Settings]
	Categories *jsonstore.JsonFile[domain.Categories]
	Skills     *jsonstore.JsonFile[[]domain.SkillDomain]

	Activity *ActivityLog
	Study    *StudyService

	subMu sync.Mutex
	subs  map[int]chan Event
	next  int

	ownsLock bool
	lock     *jsonstore.InstanceLock
	timer    *time.Timer

	watchMu sync.Mutex // held while background watching starts, so Close waits for it
	closed  bool
}

// Open opens (or creates) a library.
func Open(o Options) (*Library, error) {
	dir, err := filepath.Abs(o.DataDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	lib := &Library{Dir: dir, subs: map[int]chan Event{}, lock: o.Lock}
	if o.Lock == nil {
		if lib.lock, err = jsonstore.AcquireInstanceLock(dir); err != nil {
			return nil, err
		}
		lib.ownsLock = true
	}
	fail := func(err error) (*Library, error) {
		_ = lib.Close()
		return nil, err
	}

	lib.Backups = jsonstore.NewBackupManager(jsonstore.BackupOptions{DataDir: dir, BackupDir: filepath.Join(dir, "backups")})
	lib.Codec = o.Codec
	if lib.Codec == nil {
		lib.Codec = jsonstore.NewCodecHolder(nil)
	}
	lib.Store = jsonstore.NewStore(dir, lib.Backups, lib.Codec)
	lib.Store.OnInvalid(func(i jsonstore.InvalidInfo) { lib.emit(Event{Kind: "invalid", Data: i}) })
	lib.Store.OnQuarantine(func(q jsonstore.QuarantineInfo) { lib.emit(Event{Kind: "quarantine", Data: q}) })
	lib.Store.OnReload(func(rel string) { lib.emit(Event{Kind: "reload", Data: rel}) })

	meta := filepath.Join(dir, "meta.json")
	if _, err := os.Stat(meta); os.IsNotExist(err) {
		if err := jsonstore.AtomicWriteJSON(meta, map[string]any{
			"appName": domain.AppName, "dataVersion": DataVersion, "createdAt": domain.NowISO(),
		}); err != nil {
			return fail(err)
		}
	}
	// The launch backup runs in the background shortly after start-up so a big library opens immediately.
	if delay := o.LaunchBackupDelay; delay >= 0 {
		if delay == 0 {
			delay = 3 * time.Second
		}
		lib.timer = time.AfterFunc(delay, func() { _, _ = lib.Backups.CreateIfStale(6, "launch") })
	}

	if lib.Settings, err = jsonstore.Open(lib.Store, "config/settings.json", jsonstore.Options[domain.Settings]{
		Version: 1, Defaults: domain.DefaultSettings, Validate: func(s *domain.Settings) error { return s.Validate() },
	}); err != nil {
		return fail(err)
	}
	if lib.Categories, err = jsonstore.Open(lib.Store, "config/categories.json", jsonstore.Options[domain.Categories]{
		Version: 1, Defaults: domain.DefaultCategories, Validate: func(c *domain.Categories) error { return c.Validate() },
	}); err != nil {
		return fail(err)
	}
	if lib.Skills, err = jsonstore.Open(lib.Store, "config/skills.json", jsonstore.Options[[]domain.SkillDomain]{
		Version: 2, Defaults: domain.DefaultSkills, Validate: domain.ValidateSkills,
		Migrations: map[int]func(json.RawMessage) (json.RawMessage, error){1: domain.MigrateSkillsV1},
	}); err != nil {
		return fail(err)
	}

	lib.Repos = map[string]repo.Repo{}
	for _, c := range domain.Collections {
		var r repo.Repo
		if c == "notes" {
			r = repo.NewNoteRepo(dir, o.Watch && !o.WatchInBackground, lib.Codec)
		} else {
			r = repo.NewJSONRepo(lib.Store, c)
		}
		if err := r.Init(); err != nil {
			return fail(err)
		}
		lib.Repos[c] = r
	}
	lib.Search = search.NewIndex()
	for _, c := range domain.Collections {
		r, name := lib.Repos[c], c
		lib.Search.Attach(r)
		r.OnUpsert(func(e domain.Entity) { lib.emit(Event{Kind: "change", Collection: name, ID: e.ID(), Change: "upsert"}) })
		r.OnRemove(func(id string) { lib.emit(Event{Kind: "change", Collection: name, ID: id, Change: "remove"}) })
		r.OnRebuild(func() { lib.emit(Event{Kind: "change", Collection: name, Change: "rebuild"}) })
	}
	if o.Watch && o.WatchInBackground {
		lib.watchMu.Lock()
		go func() {
			defer lib.watchMu.Unlock()
			if n, ok := lib.Repos["notes"].(*repo.NoteRepo); ok {
				_ = n.StartWatch()
			}
			_ = lib.Store.Watch()
		}()
	} else if o.Watch {
		if err := lib.Store.Watch(); err != nil {
			return fail(err)
		}
	}
	lib.Links = links.New(lib.Repos)
	lib.Activity = NewActivityLog(lib.Store)
	lib.Study = NewStudyService(lib)
	return lib, nil
}

// Subscribe returns a channel of live events and a function that stops it. A slow reader drops events.
func (l *Library) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 64)
	l.subMu.Lock()
	id := l.next
	l.next++
	l.subs[id] = ch
	l.subMu.Unlock()
	return ch, func() {
		l.subMu.Lock()
		if c, ok := l.subs[id]; ok {
			delete(l.subs, id)
			close(c)
		}
		l.subMu.Unlock()
	}
}

func (l *Library) emit(e Event) {
	l.subMu.Lock()
	defer l.subMu.Unlock()
	for _, ch := range l.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

// ReloadAll re-reads every collection from disk.
func (l *Library) ReloadAll() error {
	for _, c := range domain.Collections {
		if err := l.Repos[c].ReloadAll(); err != nil {
			return err
		}
	}
	return nil
}

// Close stops watchers and releases the instance lock (when this library took it).
func (l *Library) Close() error {
	l.watchMu.Lock() // let a background watch start finish, so Close stops it
	l.closed = true
	l.watchMu.Unlock()
	if l.timer != nil {
		l.timer.Stop()
	}
	if l.Store != nil {
		_ = l.Store.Close()
	}
	for _, r := range l.Repos {
		_ = r.Close()
	}
	l.subMu.Lock()
	for id, ch := range l.subs {
		close(ch)
		delete(l.subs, id)
	}
	l.subMu.Unlock()
	if l.ownsLock && l.lock != nil {
		return l.lock.Release()
	}
	return nil
}
