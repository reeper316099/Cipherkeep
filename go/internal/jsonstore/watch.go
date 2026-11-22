package jsonstore

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// writeStability is how long a file must stay quiet before a change is processed, so an editor that writes in
// several steps is read once, when it is done.
const writeStability = 80 * time.Millisecond

type watcherState struct {
	mu     sync.Mutex
	w      *fsnotify.Watcher
	timers map[string]*time.Timer
	done   chan struct{}
}

// Watch starts watching the data directory for hand edits of the files opened through the store. Hidden files
// and folders (temp files, .quarantine, .lock) are ignored. It returns once the watches are in place.
func (s *Store) Watch() error {
	s.w.mu.Lock()
	defer s.w.mu.Unlock()
	if s.w.w != nil {
		return nil
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	s.w.w, s.w.timers, s.w.done = w, map[string]*time.Timer{}, make(chan struct{})
	if err := addDirs(w, s.Dir); err != nil {
		_ = w.Close()
		s.w.w = nil
		return err
	}
	go s.watchLoop(w, s.w.done)
	return nil
}

func addDirs(w *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsPermission(err) {
				return filepath.SkipDir // an unreadable folder must never stall start-up
			}
			return nil
		}
		if d.IsDir() {
			if p != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			_ = w.Add(p) // best effort: a folder that cannot be watched is skipped
		}
		return nil
	})
}

func (s *Store) watchLoop(w *fsnotify.Watcher, done chan struct{}) {
	for {
		select {
		case <-done:
			return
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			s.onFsEvent(w, ev)
		case <-w.Errors: // unwatchable paths must not crash or stall anything
		}
	}
}

func (s *Store) onFsEvent(w *fsnotify.Watcher, ev fsnotify.Event) {
	if strings.HasPrefix(filepath.Base(ev.Name), ".") {
		return
	}
	if ev.Has(fsnotify.Create) {
		if st, err := os.Stat(ev.Name); err == nil && st.IsDir() {
			_ = addDirs(w, ev.Name)
			// Files that landed before the watch was added.
			_ = filepath.WalkDir(ev.Name, func(p string, d os.DirEntry, err error) error {
				if err == nil && !d.IsDir() && !strings.HasPrefix(d.Name(), ".") {
					s.schedule(p)
				}
				return nil
			})
			return
		}
	}
	if ev.Has(fsnotify.Write) || ev.Has(fsnotify.Create) || ev.Has(fsnotify.Rename) {
		s.schedule(ev.Name)
	}
}

func (s *Store) schedule(path string) {
	s.w.mu.Lock()
	defer s.w.mu.Unlock()
	if s.w.timers == nil {
		return
	}
	if t, ok := s.w.timers[path]; ok {
		t.Reset(writeStability)
		return
	}
	s.w.timers[path] = time.AfterFunc(writeStability, func() {
		s.w.mu.Lock()
		delete(s.w.timers, path)
		s.w.mu.Unlock()
		s.fileChanged(path)
	})
}

func (s *Store) fileChanged(path string) {
	rel, err := filepath.Rel(s.Dir, path)
	if err != nil {
		return
	}
	// Files are registered under forward-slash names; Windows reports backslashes.
	rel = filepath.ToSlash(rel)
	s.mu.Lock()
	f := s.files[rel]
	s.mu.Unlock()
	if f != nil {
		f.reloadExternal()
	}
}

// Close stops the watcher.
func (s *Store) Close() error {
	s.w.mu.Lock()
	defer s.w.mu.Unlock()
	if s.w.w == nil {
		return nil
	}
	close(s.w.done)
	for _, t := range s.w.timers {
		t.Stop()
	}
	s.w.timers = nil
	err := s.w.w.Close()
	s.w.w = nil
	return err
}
