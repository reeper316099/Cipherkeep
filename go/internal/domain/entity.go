// Package domain holds the data model: entities as validated JSON objects (the same shapes and defaults as the Zod
// schemas in packages/shared), plus the small pure helpers that go with them (slugs, titles, wiki links, questions).
package domain

import (
	"crypto/rand"
	"encoding/binary"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

// Collections, in the order the rest of the app iterates them.
var Collections = []string{"commands", "concepts", "labs", "projects", "log", "notes", "snippets"}

// IsCollection reports whether name is one of the collections.
func IsCollection(name string) bool {
	for _, c := range Collections {
		if c == name {
			return true
		}
	}
	return false
}

// Entity is one stored item: a JSON object. Every entity has id, title, slug, category, tags, createdAt, updatedAt,
// favorite, archived, links and extras; the rest depends on the collection.
type Entity map[string]any

// ID returns the entity id.
func (e Entity) ID() string { return e.Str("id") }

// Str returns a string field, or "" when absent or not a string.
func (e Entity) Str(k string) string { s, _ := e[k].(string); return s }

// Bool returns a bool field.
func (e Entity) Bool(k string) bool { b, _ := e[k].(bool); return b }

// Num returns a numeric field, or 0.
func (e Entity) Num(k string) float64 {
	switch n := e[k].(type) {
	case float64:
		return n
	case int:
		return float64(n)
	}
	return 0
}

// Strs returns a list-of-strings field.
func (e Entity) Strs(k string) []string {
	switch v := e[k].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// Objs returns a list-of-objects field.
func (e Entity) Objs(k string) []map[string]any {
	v, _ := e[k].([]any)
	out := make([]map[string]any, 0, len(v))
	for _, x := range v {
		if m, ok := x.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// Obj returns an object field.
func (e Entity) Obj(k string) map[string]any { m, _ := e[k].(map[string]any); return m }

// Title is the entity title.
func (e Entity) Title() string { return e.Str("title") }

// Archived reports whether the entity is archived.
func (e Entity) Archived() bool { return e.Bool("archived") }

// Clone returns a deep copy.
func (e Entity) Clone() Entity { return DeepCopy(map[string]any(e)).(map[string]any) }

// DeepCopy copies maps and slices recursively (JSON-shaped values only).
func DeepCopy(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			out[k] = DeepCopy(x)
		}
		return out
	case Entity:
		return Entity(DeepCopy(map[string]any(t)).(map[string]any))
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = DeepCopy(x)
		}
		return out
	case []string:
		return append([]string(nil), t...)
	default:
		return v
	}
}

var (
	entropyMu sync.Mutex
	entropy   = ulid.Monotonic(rand.Reader, 0)
)

// NewID returns a new ULID.
func NewID() string {
	entropyMu.Lock()
	id, err := ulid.New(uint64(time.Now().UnixMilli()), entropy)
	entropyMu.Unlock()
	if err != nil {
		var b [8]byte
		_, _ = rand.Read(b[:])
		return ulid.MustNew(binary.BigEndian.Uint64(b[:])>>16, nil).String()
	}
	return id.String()
}

// NowISO is the current time as JavaScript's toISOString prints it.
func NowISO() string { return ISO(time.Now()) }

// ISO formats a time like JavaScript's Date.toISOString.
func ISO(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

// AppName is the single place the product name lives.
const AppName = "Cipherkeep"
