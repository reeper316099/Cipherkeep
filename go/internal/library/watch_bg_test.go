package library

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatchingInTheBackgroundStillSeesHandEdits(t *testing.T) {
	dir := t.TempDir()
	lib, err := Open(Options{DataDir: dir, Watch: true, WatchInBackground: true, LaunchBackupDelay: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	lib.watchMu.Lock() // wait for the background start
	lib.watchMu.Unlock()
	ch, stop := lib.Subscribe()
	defer stop()
	note := "---\nid: 01JABCDEFGHJKMNPQRSTVWXYZ0\ntitle: Hand edit\ncreatedAt: 2026-11-24T00:00:00.000Z\nupdatedAt: 2026-11-24T00:00:00.000Z\n---\nwritten on disk\n"
	if err := os.MkdirAll(filepath.Join(dir, "data/notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "data/notes/hand-edit.md"), []byte(note), 0o644); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(10 * time.Second)
	for {
		select {
		case e := <-ch:
			if e.Kind == "change" && e.Collection == "notes" && e.ID == "01JABCDEFGHJKMNPQRSTVWXYZ0" {
				return
			}
		case <-deadline:
			t.Fatal("the hand-written note was never picked up")
		}
	}
}

func TestClosingWhileTheWatchIsStartingIsSafe(t *testing.T) {
	lib, err := Open(Options{DataDir: t.TempDir(), Watch: true, WatchInBackground: true, LaunchBackupDelay: -1})
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.Close(); err != nil {
		t.Fatal(err)
	}
}
