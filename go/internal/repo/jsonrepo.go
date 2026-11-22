package repo

import (
	"os"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/reeper316099/Cipherkeep/go/internal/apperr"
	"github.com/reeper316099/Cipherkeep/go/internal/domain"
	"github.com/reeper316099/Cipherkeep/go/internal/jsonstore"
)

// Shard is the content of one JSON shard file.
type Shard struct {
	Items []Entity `json:"items"`
}

// cloneShard copies the list, not the entities: writers replace whole entities and never change one in place, so an
// entity that was read earlier stays valid, and a 600-command shard is not deep-copied on every edit.
func cloneShard(s Shard) Shard {
	return Shard{Items: append([]Entity(nil), s.Items...)}
}

type indexed struct {
	e   Entity
	rel string
	seq uint64
}

// JSONRepo keeps a collection in JSON shard files (one per category, platform or month, depending on the collection).
type JSONRepo struct {
	Events
	store  *jsonstore.Store
	name   string
	schema *domain.Schema

	wmu sync.Mutex // serialises writes (create, patch, remove, restore)

	mu     sync.RWMutex
	shards map[string]*jsonstore.JsonFile[Shard]
	ids    map[string][]string // shard -> ids, in file order
	index  map[string]indexed
	seq    uint64
	cache  []Entity
	fresh  bool
}

// NewJSONRepo creates the repository for one collection.
func NewJSONRepo(store *jsonstore.Store, name string) *JSONRepo {
	r := &JSONRepo{
		store: store, name: name, schema: domain.Schemas[name],
		shards: map[string]*jsonstore.JsonFile[Shard]{}, ids: map[string][]string{}, index: map[string]indexed{},
	}
	store.OnReload(func(rel string) {
		r.mu.RLock()
		_, mine := r.shards[rel]
		r.mu.RUnlock()
		if mine {
			r.reindex(rel)
			r.emitRebuild()
		}
	})
	return r
}

// Name is the collection name.
func (r *JSONRepo) Name() string { return r.name }

// Init opens every shard file that exists.
func (r *JSONRepo) Init() error {
	dir := path.Join("data", r.name)
	abs, err := r.store.Abs(dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return err
	}
	ents, err := os.ReadDir(abs)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(ents))
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, n := range names {
		if _, err := r.shard(path.Join(dir, n)); err != nil {
			return err
		}
	}
	return nil
}

// Close does nothing: the store owns the files.
func (r *JSONRepo) Close() error { return nil }

func (r *JSONRepo) shard(rel string) (*jsonstore.JsonFile[Shard], error) {
	r.mu.RLock()
	f := r.shards[rel]
	r.mu.RUnlock()
	if f != nil {
		return f, nil
	}
	f, err := jsonstore.Open(r.store, rel, jsonstore.Options[Shard]{
		Version:  1,
		Defaults: func() Shard { return Shard{Items: []Entity{}} },
		Clone:    cloneShard,
		Validate: func(s *Shard) error {
			if s.Items == nil {
				s.Items = []Entity{}
			}
			for i, raw := range s.Items {
				e, err := r.schema.Parse(raw, domain.ModeFull)
				if err != nil {
					return err
				}
				s.Items[i] = e
			}
			return nil
		},
	})
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.shards[rel] = f
	r.mu.Unlock()
	r.reindex(rel)
	return f, nil
}

// reindex rebuilds the index entries of one shard. Like the TypeScript version, a re-indexed shard's entities move
// to the end of the iteration order.
func (r *JSONRepo) reindex(rel string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, id := range r.ids[rel] {
		if cur, ok := r.index[id]; ok && cur.rel == rel {
			delete(r.index, id)
		}
	}
	f := r.shards[rel]
	if f == nil {
		return
	}
	items := f.Data().Items
	ids := make([]string, 0, len(items))
	for _, e := range items {
		r.seq++
		r.index[e.ID()] = indexed{e: e, rel: rel, seq: r.seq}
		ids = append(ids, e.ID())
	}
	r.ids[rel] = ids
	r.fresh = false
}

// All returns every entity in insertion order.
func (r *JSONRepo) All() []Entity {
	r.mu.RLock()
	if r.fresh {
		defer r.mu.RUnlock()
		return r.cache
	}
	r.mu.RUnlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.fresh {
		list := make([]indexed, 0, len(r.index))
		for _, v := range r.index {
			list = append(list, v)
		}
		sort.Slice(list, func(i, j int) bool { return list[i].seq < list[j].seq })
		r.cache = make([]Entity, len(list))
		for i, v := range list {
			r.cache[i] = v.e
		}
		r.fresh = true
	}
	return r.cache
}

// Get returns one entity.
func (r *JSONRepo) Get(id string) (Entity, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	v, ok := r.index[id]
	return v.e, ok
}

// ReloadAll re-reads every shard from disk (after a backup restore).
func (r *JSONRepo) ReloadAll() error {
	r.mu.RLock()
	files := make([]*jsonstore.JsonFile[Shard], 0, len(r.shards))
	rels := make([]string, 0, len(r.shards))
	for rel, f := range r.shards {
		files, rels = append(files, f), append(rels, rel)
	}
	r.mu.RUnlock()
	for _, f := range files {
		f.Reload()
	}
	for _, rel := range rels {
		r.reindex(rel)
	}
	r.emitRebuild()
	return nil
}

func finishCreate(parsed Entity, id string) (Entity, error) {
	title := parsed.Str("title")
	if strings.TrimSpace(title) == "" {
		return nil, apperr.Validation("title is required")
	}
	slug := parsed.Str("slug")
	if slug == "" {
		slug = domain.Slugify(title)
	}
	if id == "" {
		id = domain.NewID()
	}
	now := domain.NowISO()
	e := parsed.Clone()
	e["title"], e["slug"], e["id"], e["createdAt"], e["updatedAt"] = title, slug, id, now, now
	return e, nil
}

// Create validates input and stores a new entity. id may be empty (a new ULID is made).
func (r *JSONRepo) Create(input map[string]any, id string) (Entity, error) {
	if input == nil {
		return nil, apperr.Validation("Body must be an object")
	}
	parsed, err := r.schema.Parse(PrepareCreate(r.name, input), domain.ModeCreate)
	if err != nil {
		return nil, err
	}
	e, err := finishCreate(parsed, id)
	if err != nil {
		return nil, err
	}
	r.wmu.Lock()
	defer r.wmu.Unlock()
	f, err := r.shard(ShardOf(r.name, e))
	if err != nil {
		return nil, err
	}
	if _, err := f.Update(func(d *Shard) error { d.Items = append(d.Items, e); return nil }); err != nil {
		return nil, err
	}
	r.reindex(f.Rel())
	saved, _ := r.Get(e.ID())
	r.emitUpsert(saved)
	return saved, nil
}

// Patch applies a partial update.
func (r *JSONRepo) Patch(id string, input map[string]any) (Entity, error) {
	r.wmu.Lock()
	defer r.wmu.Unlock()
	r.mu.RLock()
	cur, ok := r.index[id]
	r.mu.RUnlock()
	if !ok {
		return nil, apperr.NotFound(r.name, id)
	}
	patch, err := r.schema.Parse(input, domain.ModePatch)
	if err != nil {
		return nil, err
	}
	merged := domain.Merge(cur.e, patch)
	merged["id"], merged["createdAt"] = id, cur.e["createdAt"]
	if t, ok := patch["title"].(string); ok && t != "" && patch.Str("slug") == "" {
		merged["slug"] = domain.Slugify(t)
	}
	merged["updatedAt"] = domain.NowISO()
	if err := r.replace(cur, merged); err != nil {
		return nil, err
	}
	saved, _ := r.Get(id)
	r.emitUpsert(saved)
	return saved, nil
}

func (r *JSONRepo) replace(cur indexed, merged Entity) error {
	id := merged.ID()
	newRel := ShardOf(r.name, merged)
	r.mu.RLock()
	oldFile := r.shards[cur.rel]
	r.mu.RUnlock()
	if newRel == cur.rel {
		if _, err := oldFile.Update(func(d *Shard) error {
			for i, e := range d.Items {
				if e.ID() == id {
					d.Items[i] = merged
				}
			}
			return nil
		}); err != nil {
			return err
		}
	} else {
		newFile, err := r.shard(newRel)
		if err != nil {
			return err
		}
		if err := jsonstore.Transaction([]*jsonstore.JsonFile[Shard]{oldFile, newFile}, func(d []*Shard) error {
			keep := d[0].Items[:0]
			for _, e := range d[0].Items {
				if e.ID() != id {
					keep = append(keep, e)
				}
			}
			d[0].Items = keep
			d[1].Items = append(d[1].Items, merged)
			return nil
		}); err != nil {
			return err
		}
		r.reindex(cur.rel)
	}
	r.reindex(newRel)
	return nil
}

// Restore puts an entity back exactly as given.
func (r *JSONRepo) Restore(e Entity) error {
	r.wmu.Lock()
	defer r.wmu.Unlock()
	r.mu.RLock()
	cur, ok := r.index[e.ID()]
	r.mu.RUnlock()
	if ok {
		if err := r.replace(cur, e); err != nil {
			return err
		}
	} else {
		f, err := r.shard(ShardOf(r.name, e))
		if err != nil {
			return err
		}
		if _, err := f.Update(func(d *Shard) error { d.Items = append(d.Items, e); return nil }); err != nil {
			return err
		}
		r.reindex(f.Rel())
	}
	saved, _ := r.Get(e.ID())
	r.emitUpsert(saved)
	return nil
}

// Remove deletes an entity.
func (r *JSONRepo) Remove(id string) error {
	r.wmu.Lock()
	defer r.wmu.Unlock()
	r.mu.RLock()
	cur, ok := r.index[id]
	f := r.shards[cur.rel]
	r.mu.RUnlock()
	if !ok {
		return apperr.NotFound(r.name, id)
	}
	if _, err := f.Update(func(d *Shard) error {
		keep := d.Items[:0]
		for _, e := range d.Items {
			if e.ID() != id {
				keep = append(keep, e)
			}
		}
		d.Items = keep
		return nil
	}); err != nil {
		return err
	}
	r.reindex(cur.rel)
	r.emitRemove(id)
	return nil
}
