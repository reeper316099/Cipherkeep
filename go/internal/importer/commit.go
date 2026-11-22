package importer

import (
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/reeper316099/Cipherkeep/go/internal/apperr"
	"github.com/reeper316099/Cipherkeep/go/internal/domain"
	"github.com/reeper316099/Cipherkeep/go/internal/jsonstore"
	"github.com/reeper316099/Cipherkeep/go/internal/library"
	"github.com/reeper316099/Cipherkeep/go/internal/repo"
)

// Now is the clock; tests replace it.
var Now = time.Now

var skipKeys = map[string]bool{"id": true, "createdAt": true, "updatedAt": true, "importRef": true, "slug": true}

// relationLink maps a relation field to the type of the typed link that mirrors it.
var relationLink = map[string]string{
	"relatedCommandIds": "command", "commandIds": "command", "conceptIds": "concept", "relatedConceptIds": "concept",
	"labIds": "lab", "scriptIds": "snippet",
}

func isEmpty(v any) bool {
	if v == nil {
		return true
	}
	if s, ok := v.(string); ok {
		return s == ""
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Map:
		return rv.Len() == 0
	}
	return false
}

// same compares two values by their JSON form.
func same(a, b any) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

func asList(v any) []any {
	switch x := v.(type) {
	case []any:
		return x
	case []string:
		return toAny(x)
	}
	return nil
}

// mergePatch is the set of fields to write for an existing entity under the chosen strategy.
func mergePatch(existing repo.Entity, incoming map[string]any, strategy Strategy) map[string]any {
	patch := map[string]any{}
	for k, v := range incoming {
		if skipKeys[k] {
			continue
		}
		cur := existing[k]
		switch {
		case strategy == StrategyOverwrite:
			if !same(cur, v) {
				patch[k] = v
			}
		case k == "tags" || k == "links" || relationLink[k] != "":
			// collections: union, never remove what the user has
			curList := asList(cur)
			union := append([]any{}, curList...)
			for _, x := range asList(v) {
				dup := false
				for _, y := range union {
					if same(x, y) {
						dup = true
						break
					}
				}
				if !dup {
					union = append(union, x)
				}
			}
			if len(union) != len(curList) {
				patch[k] = union
			}
		case k == "extras":
			merged := map[string]any{}
			if vm, ok := v.(map[string]any); ok {
				for kk, vv := range vm {
					merged[kk] = vv
				}
			}
			if cm, ok := cur.(map[string]any); ok {
				for kk, vv := range cm {
					merged[kk] = vv
				}
			}
			if !same(merged, cur) {
				patch[k] = merged
			}
		case isEmpty(cur) && !isEmpty(v):
			patch[k] = v
		}
	}
	return patch
}

func newImportID() string {
	stamp := Now().UTC().Format("20060102T150405")
	id := strings.ToLower(domain.NewID())
	return stamp + "-" + id[len(id)-6:]
}

// ExistingLookup finds entities already in the library by normalised title.
func ExistingLookup(lib *library.Library) Lookup {
	maps := map[string]map[string]string{}
	for _, c := range domain.Collections {
		m := map[string]string{}
		for _, e := range lib.Repos[c].All() {
			title := e.Str("title")
			if c == "commands" {
				if n, ok := e["name"].(string); ok {
					title = n
				}
			}
			k := domain.NormalizeTitle(title)
			if _, ok := m[k]; !ok {
				m[k] = e.ID()
			}
		}
		maps[c] = m
	}
	return lookupFunc(func(c, k string) (string, bool) { id, ok := maps[c][k]; return id, ok })
}

type lookupFunc func(collection, normTitle string) (string, bool)

func (f lookupFunc) Find(c, k string) (string, bool) { return f(c, k) }

// Source is what a commit may copy from: attachment bytes and the original upload.
type Source struct {
	VFS     *VirtualFS
	Zip     []byte
	ZipName string
}

func titleKey(c string, title string, folder, name any) string {
	t := title
	if c == "commands" && name != nil {
		t = jsStr(name)
	}
	prefix := ""
	if c == "notes" {
		if folder != nil {
			prefix = jsStr(folder)
		}
		prefix += "/"
	}
	return c + "|" + prefix + domain.NormalizeTitle(t)
}

var safeZipName = regexp.MustCompile(`[^\w. -]`)

type planned struct {
	it       PlanItem
	existing repo.Entity
	id       string
}

// Commit applies a plan: it takes a pre-import backup, creates or merges the items, writes templates and attachments,
// and records a manifest so Undo can reverse it.
func Commit(lib *library.Library, plan *Plan, opts Options, src *Source) (*Report, error) {
	id := newImportID()
	dir := filepath.Join(lib.Dir, "imports", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	bi, err := lib.Backups.Create("pre-import")
	if err != nil {
		return nil, err
	}
	snapshot := bi.Name

	// effective items after overrides
	skipped := []SkippedItem{}
	var effective []PlanItem
	for _, it := range plan.Items {
		o := opts.Overrides[it.Ref]
		if o == "skip" {
			skipped = append(skipped, SkippedItem{it.Ref, it.Title, "skipped by you"})
			continue
		}
		item := it
		if (o == "note" || o == "snippet" || o == "include") && it.Alternatives != nil {
			c := "notes"
			if o == "snippet" {
				c = "snippets"
			}
			item.Collection = c
			if alt, ok := it.Alternatives[c]; ok {
				item.Input = alt
			}
		} else if it.SkipByDefault != "" && o != "include" {
			skipped = append(skipped, SkippedItem{it.Ref, it.Title, it.SkipByDefault})
			continue
		}
		effective = append(effective, item)
	}

	// match against what is already stored
	byRef := map[string]repo.Entity{}
	byTitle := map[string]repo.Entity{}
	for _, c := range domain.Collections {
		for _, e := range lib.Repos[c].All() {
			if r, ok := e["importRef"]; ok && r != nil && jsStr(r) != "" {
				byRef[c+"|"+jsStr(r)] = e
			}
			byTitle[titleKey(c, e.Str("title"), e["folder"], e["name"])] = e
		}
	}
	findExisting := func(it PlanItem) repo.Entity {
		if opts.Strategy == StrategyKeepBoth {
			return nil
		}
		if it.FixedID != "" {
			if hit, ok := lib.Repos[it.Collection].Get(it.FixedID); ok {
				return hit
			}
		}
		if e, ok := byRef[it.Collection+"|"+it.Ref]; ok {
			return e
		}
		return byTitle[titleKey(it.Collection, it.Title, it.Input["folder"], it.Input["name"])]
	}

	refToID := map[string]string{}
	plans := make([]planned, len(effective))
	for i, it := range effective {
		existing := findExisting(it)
		newID := it.FixedID
		if existing != nil {
			newID = existing.ID()
		} else if newID == "" {
			newID = domain.NewID()
		}
		refToID[it.Ref] = newID
		plans[i] = planned{it, existing, newID}
	}

	created, updated, unchanged := map[string]int{}, map[string]int{}, map[string]int{}
	manifest := Manifest{ID: id, CreatedAt: domain.ISO(Now()), Created: []ManifestCreated{}, Updated: []ManifestUpdated{},
		Templates: []ManifestTemplate{}, Attachments: []string{}}
	resolveTargets := func(ts []RelTarget) []any {
		var out []any
		for _, t := range ts {
			target := t.ID
			if target == "" {
				target = refToID[t.Ref]
			}
			if target != "" {
				out = append(out, target)
			}
		}
		return out
	}
	appendLinks := func(input map[string]any, typ string, ids []any) {
		links := append([]any{}, asList(input["links"])...)
		for _, tid := range ids {
			links = append(links, map[string]any{"type": typ, "targetId": tid})
		}
		input["links"] = links
	}

	for _, p := range plans {
		it, existing := p.it, p.existing
		input := make(map[string]any, len(it.Input)+2)
		for k, v := range it.Input {
			input[k] = v
		}
		for _, rel := range it.Relations {
			ids := resolveTargets(rel.Targets)
			if len(ids) == 0 {
				continue
			}
			if strings.HasPrefix(rel.Field, "links:") {
				appendLinks(input, rel.Field[6:], ids)
			} else {
				input[rel.Field] = ids
				if lt := relationLink[rel.Field]; lt != "" {
					appendLinks(input, lt, ids)
				}
			}
		}
		if opts.Strategy != StrategyKeepBoth {
			input["importRef"] = it.Ref
		}
		r := lib.Repos[it.Collection]
		if opts.Strategy == StrategySkip && existing != nil {
			skipped = append(skipped, SkippedItem{it.Ref, it.Title, "already exists"})
			continue
		}
		if existing == nil {
			if it.FixedID != "" && it.Input["createdAt"] != nil && jsStr(it.Input["createdAt"]) != "" {
				withID := map[string]any{}
				for k, v := range input {
					withID[k] = v
				}
				withID["id"] = p.id
				full, err := domain.Schemas[it.Collection].Parse(withID, domain.ModeFull)
				if err != nil {
					return nil, err
				}
				if err := r.Restore(full); err != nil {
					return nil, err
				}
			} else if _, err := r.Create(input, p.id); err != nil {
				return nil, err
			}
			manifest.Created = append(manifest.Created, ManifestCreated{it.Collection, p.id})
			created[it.Collection]++
			continue
		}
		patch := mergePatch(existing, input, opts.Strategy)
		if len(patch) == 0 {
			unchanged[it.Collection]++
			continue
		}
		manifest.Updated = append(manifest.Updated, ManifestUpdated{it.Collection, p.id, domain.DeepCopy(map[string]any(existing)).(map[string]any)})
		if _, err := r.Patch(p.id, patch); err != nil {
			return nil, err
		}
		updated[it.Collection]++
	}

	// templates
	written := []string{}
	for _, t := range plan.Templates {
		file, err := lib.Store.Abs("config/templates/" + t.Collection + ".json")
		if err != nil {
			return nil, err
		}
		var before *string
		if b, err := jsonstore.ReadDecoded(lib.Codec, file); err == nil && len(b) > 0 {
			s := string(b)
			before = &s
		}
		if before != nil && opts.Strategy != StrategyOverwrite {
			continue
		}
		manifest.Templates = append(manifest.Templates, ManifestTemplate{t.Collection, before})
		if err := jsonstore.WriteJSONEncoded(lib.Codec, file, t.Template); err != nil {
			return nil, err
		}
		written = append(written, t.Collection)
	}

	// attachments
	copied := 0
	if src != nil && src.VFS != nil {
		for _, a := range plan.Attachments {
			data, ok := src.VFS.Files[a.From]
			if !ok {
				continue
			}
			rel := path.Join("attachments", a.To)
			target, err := lib.Store.Abs(rel)
			if err != nil {
				return nil, err
			}
			if _, err := os.Stat(target); err == nil {
				continue
			}
			if err := jsonstore.WriteEncoded(lib.Codec, target, data); err != nil {
				return nil, err
			}
			manifest.Attachments = append(manifest.Attachments, rel)
			copied++
		}
	}

	seen := map[string]bool{}
	for _, m := range []map[string]int{created, updated} {
		names := make([]string, 0, len(m))
		for c := range m {
			names = append(names, c)
		}
		sort.Strings(names)
		for _, c := range names {
			if !seen[c] {
				seen[c] = true
				_ = lib.Activity.Record("import", c, "")
			}
		}
	}

	itemWarnings := []ItemWarning{}
	for _, i := range effective {
		if len(i.Warnings) > 0 {
			itemWarnings = append(itemWarnings, ItemWarning{i.Collection, i.Title, i.Warnings})
		}
	}
	report := &Report{ID: id, Kind: plan.Kind, SourceName: plan.SourceName, Strategy: opts.Strategy, CreatedAt: manifest.CreatedAt,
		Created: created, Updated: updated, Unchanged: unchanged, Skipped: skipped, Templates: written, Attachments: copied,
		Warnings: plan.Warnings, ItemWarnings: itemWarnings, Snapshot: &snapshot}
	if err := jsonstore.WriteJSONEncoded(lib.Codec, filepath.Join(dir, "manifest.json"), manifest); err != nil {
		return nil, err
	}
	if err := jsonstore.WriteJSONEncoded(lib.Codec, filepath.Join(dir, "report.json"), report); err != nil {
		return nil, err
	}
	if src != nil && src.Zip != nil {
		name := safeZipName.ReplaceAllString(src.ZipName, "_")
		if name == "" {
			name = "source.zip"
		}
		if err := jsonstore.WriteEncoded(lib.Codec, filepath.Join(dir, name), src.Zip); err != nil {
			return nil, err
		}
	}
	return report, nil
}

var importIDRe = regexp.MustCompile(`^[\w-]+$`)

// UndoResult counts what an undo did.
type UndoResult struct {
	Removed  int `json:"removed"`
	Restored int `json:"restored"`
}

// Undo reverses an import: it removes what was created, restores what was changed, and restores templates and
// attachments. A backup is taken first.
func Undo(lib *library.Library, importID string) (*UndoResult, error) {
	if !importIDRe.MatchString(importID) {
		return nil, apperr.Validation("Bad import id")
	}
	file := filepath.Join(lib.Dir, "imports", importID, "manifest.json")
	var manifest Manifest
	raw, err := jsonstore.ReadDecoded(lib.Codec, file)
	if err == nil {
		err = json.Unmarshal(raw, &manifest)
	}
	if err != nil {
		return nil, apperr.NotFound("import", importID)
	}
	if manifest.Undone != "" {
		return nil, apperr.New(409, "ALREADY_UNDONE", "This import was already undone")
	}
	if _, err := lib.Backups.Create("pre-undo"); err != nil {
		return nil, err
	}
	res := &UndoResult{}
	for i := len(manifest.Created) - 1; i >= 0; i-- {
		c := manifest.Created[i]
		if _, ok := lib.Repos[c.Collection].Get(c.ID); ok {
			if err := lib.Repos[c.Collection].Remove(c.ID); err != nil {
				return nil, err
			}
			res.Removed++
		}
	}
	for _, u := range manifest.Updated {
		if err := lib.Repos[u.Collection].Restore(u.Before); err != nil {
			return nil, err
		}
		res.Restored++
	}
	for _, t := range manifest.Templates {
		f, err := lib.Store.Abs("config/templates/" + t.Collection + ".json")
		if err != nil {
			return nil, err
		}
		if t.Before == nil {
			if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
		} else if err := jsonstore.WriteEncoded(lib.Codec, f, []byte(*t.Before)); err != nil {
			return nil, err
		}
	}
	for _, a := range manifest.Attachments {
		f, err := lib.Store.Abs(a)
		if err != nil {
			return nil, err
		}
		if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	manifest.Undone = domain.ISO(Now())
	if err := jsonstore.WriteJSONEncoded(lib.Codec, file, manifest); err != nil {
		return nil, err
	}
	return res, nil
}

// List returns the reports of past imports, newest first.
func List(lib *library.Library) []Report {
	dir := filepath.Join(lib.Dir, "imports")
	ents, err := os.ReadDir(dir)
	out := []Report{}
	if err != nil {
		return out
	}
	names := make([]string, 0, len(ents))
	for _, e := range ents {
		names = append(names, e.Name())
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for _, n := range names {
		raw, err := jsonstore.ReadDecoded(lib.Codec, filepath.Join(dir, n, "report.json"))
		if err != nil {
			continue
		}
		var r Report
		if json.Unmarshal(raw, &r) == nil {
			out = append(out, r)
		}
	}
	return out
}
