package repo

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/reeper316099/Cipherkeep/go/internal/apperr"
	"github.com/reeper316099/Cipherkeep/go/internal/domain"
	"github.com/reeper316099/Cipherkeep/go/internal/jsonstore"
)

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func newJSON(t *testing.T, name string) (*JSONRepo, *jsonstore.Store, string) {
	t.Helper()
	dir := t.TempDir()
	st := jsonstore.NewStore(dir, nil, nil)
	r := NewJSONRepo(st, name)
	if err := r.Init(); err != nil {
		t.Fatal(err)
	}
	return r, st, dir
}

func titles(es []Entity) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Title()
	}
	return out
}

func TestJSONRepoCreatePatchRemoveAndShardPlacement(t *testing.T) {
	r, _, dir := newJSON(t, "commands")
	var upserts, removes int
	r.OnUpsert(func(Entity) { upserts++ })
	r.OnRemove(func(string) { removes++ })

	c := must(r.Create(map[string]any{"name": "ls", "category": "File System", "syntax": "ls [flags]"}, ""))
	if c.Title() != "ls" || c.Str("slug") != "ls" || len(c.ID()) != 26 || c.Str("createdAt") == "" {
		t.Fatalf("created: %+v", c)
	}
	if c.Num("mastery") != 0 || c.Str("risk") != "safe" {
		t.Fatal("defaults missing")
	}
	if _, err := os.Stat(filepath.Join(dir, "data/commands/file-system.json")); err != nil {
		t.Fatalf("shard not written: %v", err)
	}

	p := must(r.Patch(c.ID(), map[string]any{"category": "Networking", "mastery": 2.0}))
	if p.Str("category") != "Networking" || p.Num("mastery") != 2 || p.Str("createdAt") != c.Str("createdAt") || p.Str("updatedAt") == "" {
		t.Fatalf("patched: %+v", p)
	}
	if _, err := os.Stat(filepath.Join(dir, "data/commands/networking.json")); err != nil {
		t.Fatal("entity should move to the new category shard")
	}
	if got := must(os.ReadFile(filepath.Join(dir, "data/commands/file-system.json"))); bytes.Contains(got, []byte(c.ID())) {
		t.Fatal("entity still in the old shard")
	}
	if p2 := must(r.Patch(c.ID(), map[string]any{"title": "List files"})); p2.Str("slug") != "list-files" {
		t.Fatalf("a new title makes a new slug: %q", p2.Str("slug"))
	}

	if err := r.Remove(c.ID()); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Get(c.ID()); ok || len(r.All()) != 0 {
		t.Fatal("not removed")
	}
	if upserts != 3 || removes != 1 {
		t.Fatalf("events: %d upserts, %d removes", upserts, removes)
	}
	if _, err := r.Patch("nope", map[string]any{}); !isStatus(err, 404) {
		t.Fatalf("missing id: %v", err)
	}
	if err := r.Remove("nope"); !isStatus(err, 404) {
		t.Fatalf("missing id: %v", err)
	}
}

func isStatus(err error, status int) bool {
	e, ok := apperr.As(err)
	return ok && e.Status == status
}

func TestJSONRepoValidationAndTitleDefaults(t *testing.T) {
	r, _, _ := newJSON(t, "log")
	e := must(r.Create(map[string]any{"date": "2026-03-24"}, ""))
	if e.Title() != "2026-03-24" {
		t.Fatalf("a log entry's title defaults to its date: %q", e.Title())
	}
	if _, err := os.Stat(filepath.Join(r.store.Dir, "data/log/2026-03.json")); err != nil {
		t.Fatal("log entries are sharded by month")
	}
	if _, err := r.Create(map[string]any{"date": 5.0}, ""); err == nil {
		t.Fatal("a wrong type must be rejected")
	} else if _, ok := err.(*domain.ValidationError); !ok {
		t.Fatalf("want a ValidationError, got %T", err)
	}
	if _, err := r.Create(map[string]any{"date": "2026-03-24", "title": "   "}, ""); !isStatus(err, 400) {
		t.Fatalf("blank title: %v", err)
	}
	if _, err := r.Create(nil, ""); !isStatus(err, 400) {
		t.Fatalf("nil body: %v", err)
	}
	cmds, _, _ := newJSON(t, "commands")
	if _, err := cmds.Create(map[string]any{"category": "x"}, ""); err == nil {
		t.Fatal("a command needs a name")
	}
}

func TestJSONRepoRestorePutsAnEntityBackExactly(t *testing.T) {
	r, _, _ := newJSON(t, "commands")
	c := must(r.Create(map[string]any{"name": "ls", "category": "A"}, ""))
	before := c.Clone()
	must(r.Patch(c.ID(), map[string]any{"category": "B", "notes": "changed"}))
	if err := r.Restore(before); err != nil {
		t.Fatal(err)
	}
	got, _ := r.Get(c.ID())
	if got.Str("category") != "A" || got.Str("notes") != "" || got.Str("updatedAt") != before.Str("updatedAt") {
		t.Fatalf("restore changed the entity: %+v", got)
	}
	gone := before.Clone()
	gone["id"] = "01HZZZZZZZZZZZZZZZZZZZZZZZ"
	if err := r.Restore(gone); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Get(gone.ID()); !ok {
		t.Fatal("restore of a missing entity should add it")
	}
}

func TestJSONRepoReloadsHandEditedShardsAndSurvivesRestart(t *testing.T) {
	r, st, dir := newJSON(t, "commands")
	c := must(r.Create(map[string]any{"name": "rm", "category": "File System"}, ""))
	rebuilds := make(chan struct{}, 4)
	r.OnRebuild(func() { rebuilds <- struct{}{} })
	if err := st.Watch(); err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	file := filepath.Join(dir, "data/commands/file-system.json")
	text := strings.Replace(string(must(os.ReadFile(file))), `"explanation": ""`, `"explanation": "edited by hand"`, 1)
	if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-rebuilds:
	case <-time.After(10 * time.Second):
		t.Fatal("never happened: rebuild after a hand edit")
	}
	if got, _ := r.Get(c.ID()); got.Str("explanation") != "edited by hand" {
		t.Fatalf("hand edit not applied: %q", got.Str("explanation"))
	}
	// A new repository over the same folder sees the data.
	r2 := NewJSONRepo(jsonstore.NewStore(dir, nil, nil), "commands")
	if err := r2.Init(); err != nil {
		t.Fatal(err)
	}
	if got, ok := r2.Get(c.ID()); !ok || got.Str("name") != "rm" {
		t.Fatal("not persisted")
	}
}

func TestQueryFiltersSortsPagesAndOrdersByIDs(t *testing.T) {
	r, _, _ := newJSON(t, "commands")
	mk := func(name, cat string, fav bool, tags ...string) Entity {
		in := map[string]any{"name": name, "category": cat, "favorite": fav}
		var ts []any
		for _, x := range tags {
			ts = append(ts, x)
		}
		in["tags"] = ts
		return must(r.Create(in, ""))
	}
	a := mk("banana", "Fruit", false, "yellow")
	b := mk("Apple", "Fruit", true, "red", "round")
	mk("cherry", "Tree", false)
	arch := mk("dates", "Fruit", false)
	must(r.Patch(arch.ID(), map[string]any{"archived": true}))
	all := r.All()

	if got := titles(Query(all, ListQuery{}).Items); strings.Join(got, ",") != "Apple,banana,cherry" {
		t.Fatalf("default sort is by title, case-insensitively, archived hidden: %v", got)
	}
	if p := Query(all, ListQuery{Archived: "all"}); p.Total != 4 {
		t.Fatalf("archived=all: %d", p.Total)
	}
	if p := Query(all, ListQuery{Archived: "true"}); p.Total != 1 || p.Items[0].Title() != "dates" {
		t.Fatalf("archived=true: %+v", titles(p.Items))
	}
	if p := Query(all, ListQuery{Category: "Fruit"}); p.Total != 2 {
		t.Fatalf("category: %d", p.Total)
	}
	if p := Query(all, ListQuery{Tag: "round"}); p.Total != 1 || p.Items[0].ID() != b.ID() {
		t.Fatal("tag filter")
	}
	yes := true
	if p := Query(all, ListQuery{Favorite: &yes}); p.Total != 1 {
		t.Fatal("favorite filter")
	}
	if got := titles(Query(all, ListQuery{Sort: "name", Order: "desc"}).Items); strings.Join(got, ",") != "cherry,banana,Apple" {
		t.Fatalf("sort desc: %v", got)
	}
	if p := Query(all, ListQuery{Limit: 1, Offset: 1}); len(p.Items) != 1 || p.Total != 3 || p.Limit != 1 || p.Offset != 1 {
		t.Fatalf("paging: %+v", p)
	}
	if p := Query(all, ListQuery{Limit: 9999}); p.Limit != 500 {
		t.Fatalf("limit is capped at 500: %d", p.Limit)
	}
	if got := titles(Query(all, ListQuery{IDs: []string{b.ID(), "missing", a.ID()}}).Items); strings.Join(got, ",") != "Apple,banana" {
		t.Fatalf("ids keep the caller's order and skip unknown ids: %v", got)
	}
}

func TestShardOfPlacesEveryCollection(t *testing.T) {
	cases := []struct {
		coll string
		e    Entity
		want string
	}{
		{"commands", Entity{"category": "File System"}, "data/commands/file-system.json"},
		{"commands", Entity{}, "data/commands/uncategorized.json"},
		{"concepts", Entity{"category": "Web Security"}, "data/concepts/web-security.json"},
		{"labs", Entity{"platform": "picoCTF"}, "data/labs/picoctf.json"},
		{"log", Entity{"date": "2026-03-24"}, "data/log/2026-03.json"},
		{"projects", Entity{}, "data/projects/projects.json"},
		{"snippets", Entity{}, "data/snippets/snippets.json"},
	}
	for _, c := range cases {
		if got := ShardOf(c.coll, c.e); got != c.want {
			t.Errorf("%s: %s, want %s", c.coll, got, c.want)
		}
	}
}
