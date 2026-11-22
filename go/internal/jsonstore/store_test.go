package jsonstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCreatesDefaultsAndSerialisesConcurrentWriters(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, nil, nil)
	f, err := Open(s, "data/a.json", opts())
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := f.Update(func(d *doc) error {
				d.Items = append(d.Items, item{ID: fmt.Sprintf("i%d", i), N: i})
				return nil
			}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if n := len(f.Data().Items); n != 25 {
		t.Fatalf("memory has %d items", n)
	}
	v, d := onDisk(t, filepath.Join(dir, "data/a.json"))
	if v != 1 || len(d.Items) != 25 {
		t.Fatalf("disk: version %d, %d items", v, len(d.Items))
	}
	ents, _ := os.ReadDir(filepath.Join(dir, "data"))
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

func TestRejectsInvalidUpdatesWithoutTouchingDiskOrMemory(t *testing.T) {
	dir := t.TempDir()
	f := must(Open(NewStore(dir, nil, nil), "a.json", opts()))
	before := read(t, filepath.Join(dir, "a.json"))
	_, err := f.Update(func(d *doc) error { d.Items = append(d.Items, item{ID: "x", N: -1}); return nil })
	if err == nil {
		t.Fatal("expected a validation error")
	}
	if len(f.Data().Items) != 0 || read(t, filepath.Join(dir, "a.json")) != before {
		t.Fatal("state changed after a rejected update")
	}
}

func TestMigrationsRunInOrderAndTakeAPreMigrationBackup(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.json"), `{"schemaVersion":1,"data":{"list":[{"id":"a","n":1}]}}`)
	backups := NewBackupManager(BackupOptions{DataDir: dir, BackupDir: filepath.Join(dir, "backups")})
	o := Options[doc]{
		Version: 2, Defaults: opts().Defaults,
		Migrations: map[int]func(raw json.RawMessage) (json.RawMessage, error){
			1: func(raw json.RawMessage) (json.RawMessage, error) {
				return []byte(strings.Replace(string(raw), `"list"`, `"items"`, 1)), nil
			},
		},
	}
	f, err := Open(NewStore(dir, backups, nil), "a.json", o)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.Data().Items; len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("items: %+v", got)
	}
	if v, _ := onDisk(t, filepath.Join(dir, "a.json")); v != 2 {
		t.Fatalf("schemaVersion on disk %d", v)
	}
	list, _ := backups.List()
	if len(list) == 0 || list[0].Reason != "pre-migration" {
		t.Fatalf("backups: %+v", list)
	}
}

func TestRefusesNewerSchemaWithoutClobbering(t *testing.T) {
	dir := t.TempDir()
	text := `{"schemaVersion":9,"data":{}}`
	write(t, filepath.Join(dir, "a.json"), text)
	_, err := Open(NewStore(dir, nil, nil), "a.json", opts())
	if !IsCode(err, CodeVersionTooNew) {
		t.Fatalf("err = %v", err)
	}
	if read(t, filepath.Join(dir, "a.json")) != text {
		t.Fatal("file was modified")
	}
}

func TestQuarantinesCorruptFileAndRestoresLastGoodBackup(t *testing.T) {
	dir := t.TempDir()
	backups := NewBackupManager(BackupOptions{DataDir: dir, BackupDir: filepath.Join(dir, "backups")})
	s1 := NewStore(dir, backups, nil)
	f1 := must(Open(s1, "a.json", opts()))
	must(f1.Update(func(d *doc) error { d.Items = append(d.Items, item{ID: "keep", N: 1}); return nil }))
	must(backups.Create("test"))
	write(t, filepath.Join(dir, "a.json"), `{ "schemaVersion": 1, "data": {oops`)

	s2 := NewStore(dir, backups, nil)
	var events []QuarantineInfo
	s2.OnQuarantine(func(q QuarantineInfo) { events = append(events, q) })
	f2 := must(Open(s2, "a.json", opts()))
	if got := f2.Data().Items; len(got) != 1 || got[0].ID != "keep" {
		t.Fatalf("not restored: %+v", got)
	}
	if len(events) != 1 || !events[0].Restored {
		t.Fatalf("events: %+v", events)
	}
	q, _ := os.ReadDir(filepath.Join(dir, ".quarantine"))
	if len(q) != 1 || !strings.Contains(read(t, filepath.Join(dir, ".quarantine", q[0].Name())), "oops") {
		t.Fatal("bad file not kept in .quarantine")
	}
}

func TestFallsBackToDefaultsWhenCorruptAndNoBackup(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.json"), "not json")
	f, err := Open(NewStore(dir, nil, nil), "a.json", opts())
	if err != nil || len(f.Data().Items) != 0 {
		t.Fatalf("err=%v items=%v", err, f.Data().Items)
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("never happened: %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestReloadsValidHandEditsAndKeepsMemoryOnInvalidOnes(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, nil, nil)
	f := must(Open(s, "a.json", opts()))
	reloads := make(chan string, 4)
	invalids := make(chan InvalidInfo, 4)
	s.OnReload(func(rel string) { reloads <- rel })
	s.OnInvalid(func(i InvalidInfo) { invalids <- i })
	if err := s.Watch(); err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	write(t, filepath.Join(dir, "a.json"), `{"schemaVersion":1,"data":{"items":[{"id":"hand","n":5}]}}`)
	select {
	case <-reloads:
	case <-time.After(10 * time.Second):
		t.Fatal("never happened: reload event after a valid hand edit")
	}
	if f.Data().Items[0].ID != "hand" {
		t.Fatal("hand edit not applied")
	}

	write(t, filepath.Join(dir, "a.json"), "{\n  \"schemaVersion\": 1,\n  \"data\": {\n  oops\n}")
	select {
	case info := <-invalids:
		if info.Rel != "a.json" || info.Line == 0 {
			t.Fatalf("invalid info: %+v", info)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("never happened: invalid event after a broken hand edit")
	}
	if f.Data().Items[0].ID != "hand" {
		t.Fatal("memory changed on an invalid edit")
	}
}

func TestReloadsFilesInNestedFolders(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, nil, nil)
	f := must(Open(s, "data/commands/file-system.json", opts()))
	reloads := make(chan string, 4)
	s.OnReload(func(rel string) { reloads <- rel })
	if err := s.Watch(); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	write(t, filepath.Join(dir, "data/commands/file-system.json"), `{"schemaVersion":1,"data":{"items":[{"id":"n","n":1}]}}`)
	select {
	case rel := <-reloads:
		if rel != "data/commands/file-system.json" {
			t.Fatalf("rel = %q", rel)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("never happened: reload of a nested file")
	}
	if f.Data().Items[0].ID != "n" {
		t.Fatal("not applied")
	}
}

func TestDoesNotTreatItsOwnWritesAsExternalChanges(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, nil, nil)
	f := must(Open(s, "a.json", opts()))
	var mu sync.Mutex
	reloads := 0
	s.OnReload(func(string) { mu.Lock(); reloads++; mu.Unlock() })
	if err := s.Watch(); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	must(f.Update(func(d *doc) error { d.Items = append(d.Items, item{ID: "x", N: 1}); return nil }))
	time.Sleep(400 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if reloads != 0 {
		t.Fatalf("%d reloads from our own write", reloads)
	}
}

func TestRollsBackATwoFileTransactionWhenTheSecondWriteFails(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, nil, nil)
	a := must(Open(s, "a.json", opts()))
	ob := opts()
	ob.Validate = func(d *doc) error {
		for _, it := range d.Items {
			if it.N == 99 {
				return errors.New("nope")
			}
		}
		return nil
	}
	b := must(Open(s, "b.json", ob))
	must(a.Update(func(d *doc) error { d.Items = append(d.Items, item{ID: "m", N: 1}); return nil }))

	err := Transaction([]*JsonFile[doc]{a, b}, func(d []*doc) error {
		d[0].Items = nil
		d[1].Items = append(d[1].Items, item{ID: "m", N: 99})
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("err = %v", err)
	}
	if len(a.Data().Items) != 1 {
		t.Fatal("memory not rolled back")
	}
	if _, d := onDisk(t, filepath.Join(dir, "a.json")); len(d.Items) != 1 {
		t.Fatal("disk not rolled back")
	}
	if err := Transaction([]*JsonFile[doc]{a, b}, func(d []*doc) error {
		d[1].Items = append(d[1].Items, d[0].Items...)
		d[0].Items = nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(a.Data().Items) != 0 || len(b.Data().Items) != 1 {
		t.Fatal("a successful transaction did not apply")
	}
}

func TestBlocksPathTraversal(t *testing.T) {
	_, err := Open(NewStore(t.TempDir(), nil, nil), "../evil.json", opts())
	if !IsCode(err, CodePathTraversal) {
		t.Fatalf("err = %v", err)
	}
}

func TestOpeningTheSamePathTwiceGivesTheSameHandle(t *testing.T) {
	s := NewStore(t.TempDir(), nil, nil)
	a := must(Open(s, "a.json", opts()))
	b := must(Open(s, "a.json", opts()))
	if a != b {
		t.Fatal("different handles")
	}
}
