// Package repo holds the collections: one repository per collection, backed by JSON shards (jsonstore) or, for
// notes, real Markdown files. Repositories keep every entity in memory, serve reads from there, and tell listeners
// (search, links, live events) what changed.
package repo

import (
	"bytes"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"

	"github.com/reeper316099/Cipherkeep/go/internal/domain"
)

// Entity is one stored item.
type Entity = domain.Entity

// Repo is the interface every collection implements.
type Repo interface {
	Name() string
	Init() error
	// All returns the entities in a stable order. Entities are shared and must be treated as read-only.
	All() []Entity
	Get(id string) (Entity, bool)
	Create(input map[string]any, id string) (Entity, error)
	Patch(id string, input map[string]any) (Entity, error)
	Remove(id string) error
	// Restore puts an entity back exactly as given (undo); timestamps are not touched.
	Restore(e Entity) error
	ReloadAll() error
	Close() error
	OnUpsert(func(Entity))
	OnRemove(func(id string))
	OnRebuild(func())
}

// Events is the listener plumbing both repositories embed.
type Events struct {
	mu       sync.RWMutex
	upserts  []func(Entity)
	removes  []func(string)
	rebuilds []func()
}

func (e *Events) OnUpsert(fn func(Entity)) {
	e.mu.Lock()
	e.upserts = append(e.upserts, fn)
	e.mu.Unlock()
}
func (e *Events) OnRemove(fn func(string)) {
	e.mu.Lock()
	e.removes = append(e.removes, fn)
	e.mu.Unlock()
}
func (e *Events) OnRebuild(fn func()) {
	e.mu.Lock()
	e.rebuilds = append(e.rebuilds, fn)
	e.mu.Unlock()
}

func (e *Events) emitUpsert(x Entity) {
	e.mu.RLock()
	fs := append([]func(Entity){}, e.upserts...)
	e.mu.RUnlock()
	for _, f := range fs {
		f(x)
	}
}

func (e *Events) emitRemove(id string) {
	e.mu.RLock()
	fs := append([]func(string){}, e.removes...)
	e.mu.RUnlock()
	for _, f := range fs {
		f(id)
	}
}

func (e *Events) emitRebuild() {
	e.mu.RLock()
	fs := append([]func(){}, e.rebuilds...)
	e.mu.RUnlock()
	for _, f := range fs {
		f()
	}
}

// ListQuery filters, sorts and pages a list.
type ListQuery struct {
	Category string
	Tag      string
	Favorite *bool
	Archived string // "true", "false" (default) or "all"
	Sort     string
	Order    string // "asc" (default) or "desc"
	Limit    int    // default 50, at most 500
	Offset   int
	// IDs, when set, restricts to (and orders by) these ids.
	IDs []string
}

// Page is one page of a list.
type Page struct {
	Items  []Entity `json:"items"`
	Total  int      `json:"total"`
	Limit  int      `json:"limit"`
	Offset int      `json:"offset"`
}

var (
	collator  = collate.New(language.Und)
	keyMu     sync.Mutex
	keyBuf    collate.Buffer
	titleKeys sync.Map // title -> collation key
	keyCount  atomic.Int64
)

// TitleKey is the collation key of a title. Comparing keys is a byte comparison, far cheaper than collating two strings
// on every step of a sort of thousands of titles; keys are cached per title.
func TitleKey(title string) []byte {
	if k, ok := titleKeys.Load(title); ok {
		return k.([]byte)
	}
	keyMu.Lock()
	key := append([]byte(nil), collator.KeyFromString(&keyBuf, title)...)
	keyMu.Unlock()
	if keyCount.Add(1) > 200_000 { // bound the cache
		titleKeys.Range(func(k, _ any) bool { titleKeys.Delete(k); return true })
		keyCount.Store(0)
	}
	titleKeys.Store(title, key)
	return key
}

func less(x, y any, dir int) (decided, lessThan bool) {
	if x == nil && y == nil {
		return false, false
	}
	if x == nil {
		return true, false // undefined values sort last
	}
	if y == nil {
		return true, true
	}
	switch a := x.(type) {
	case string:
		if b, ok := y.(string); ok && a != b {
			return true, (a < b) == (dir == 1)
		}
	case float64:
		if b, ok := y.(float64); ok && a != b {
			return true, (a < b) == (dir == 1)
		}
	case bool:
		if b, ok := y.(bool); ok && a != b {
			return true, (!a && b) == (dir == 1)
		}
	}
	return false, false
}

// Query applies a ListQuery to a list of entities.
func Query(all []Entity, q ListQuery) Page {
	var items []Entity
	if q.IDs != nil {
		byID := make(map[string]Entity, len(all))
		for _, e := range all {
			byID[e.ID()] = e
		}
		for _, id := range q.IDs {
			if e, ok := byID[id]; ok {
				items = append(items, e)
			}
		}
	} else {
		items = append([]Entity(nil), all...)
	}
	arch := q.Archived
	if arch == "" {
		arch = "false"
	}
	keep := items[:0:0]
	for _, e := range items {
		if arch != "all" && e.Archived() != (arch == "true") {
			continue
		}
		if q.Category != "" && e.Str("category") != q.Category {
			continue
		}
		if q.Tag != "" && !containsStr(e.Strs("tags"), q.Tag) {
			continue
		}
		if q.Favorite != nil && e.Bool("favorite") != *q.Favorite {
			continue
		}
		keep = append(keep, e)
	}
	items = keep
	switch {
	case q.Sort != "":
		dir := 1
		if q.Order == "desc" {
			dir = -1
		}
		sort.SliceStable(items, func(i, j int) bool {
			if decided, lt := less(items[i][q.Sort], items[j][q.Sort], dir); decided {
				return lt
			}
			return false
		})
	case q.IDs == nil:
		keys := make([][]byte, len(items))
		for i, e := range items {
			keys[i] = TitleKey(e.Title())
		}
		idx := make([]int, len(items))
		for i := range idx {
			idx[i] = i
		}
		sort.SliceStable(idx, func(a, b int) bool { return bytes.Compare(keys[idx[a]], keys[idx[b]]) < 0 })
		sorted := make([]Entity, len(items))
		for i, j := range idx {
			sorted[i] = items[j]
		}
		items = sorted
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	limit = min(limit, 500)
	total := len(items)
	lo := min(q.Offset, total)
	hi := min(lo+limit, total)
	return Page{Items: items[lo:hi], Total: total, Limit: limit, Offset: q.Offset}
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// PrepareCreate fills in the title from name (or, for the log, the date) when it is missing.
func PrepareCreate(name string, input map[string]any) map[string]any {
	o := make(map[string]any, len(input)+1)
	for k, v := range input {
		o[k] = v
	}
	if o["title"] == nil {
		if n, ok := o["name"].(string); ok {
			o["title"] = n
		} else if name == "log" {
			if d, ok := o["date"].(string); ok {
				o["title"] = d
			}
		}
	}
	return o
}

// ShardOf says which JSON shard file an entity lives in.
func ShardOf(name string, e Entity) string {
	switch name {
	case "commands", "concepts":
		cat := e.Str("category")
		if cat == "" {
			cat = "uncategorized"
		}
		return "data/" + name + "/" + domain.Slugify(cat) + ".json"
	case "labs":
		p := e.Str("platform")
		if p == "" {
			p = "other"
		}
		return "data/labs/" + domain.Slugify(p) + ".json"
	case "log":
		d := e.Str("date")
		if len(d) > 7 {
			d = d[:7]
		}
		return "data/log/" + d + ".json"
	case "projects":
		return "data/projects/projects.json"
	}
	return "data/" + name + "/" + name + ".json"
}

func trimLower(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
