package repo

import (
	"crypto/sha1"
	"encoding/hex"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"gopkg.in/yaml.v3"

	"github.com/reeper316099/Cipherkeep/go/internal/apperr"
	"github.com/reeper316099/Cipherkeep/go/internal/domain"
	"github.com/reeper316099/Cipherkeep/go/internal/jsonstore"
)

var frontmatterRe = regexp.MustCompile(`(?s)^---\r?\n(.*?)\r?\n---\r?\n?(.*)$`)

// ParseFrontmatter splits a Markdown file into its YAML frontmatter (as a plain map) and the body.
func ParseFrontmatter(text string) (map[string]any, string) {
	m := frontmatterRe.FindStringSubmatch(text)
	if m == nil {
		return map[string]any{}, text
	}
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(m[1]), &root); err != nil || len(root.Content) == 0 {
		return map[string]any{}, m[2]
	}
	if v, ok := yamlToAny(root.Content[0]).(map[string]any); ok {
		return v, m[2]
	}
	return map[string]any{}, m[2]
}

// yamlToAny converts a YAML node the way the JavaScript yaml library's core schema does: timestamps stay strings.
func yamlToAny(n *yaml.Node) any {
	switch n.Kind {
	case yaml.MappingNode:
		out := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			out[n.Content[i].Value] = yamlToAny(n.Content[i+1])
		}
		return out
	case yaml.SequenceNode:
		out := make([]any, len(n.Content))
		for i, c := range n.Content {
			out[i] = yamlToAny(c)
		}
		return out
	case yaml.AliasNode:
		return yamlToAny(n.Alias)
	case yaml.ScalarNode:
		switch n.Tag {
		case "!!null":
			return nil
		case "!!bool":
			var b bool
			_ = n.Decode(&b)
			return b
		case "!!int", "!!float":
			var f float64
			if err := n.Decode(&f); err == nil {
				return f
			}
		}
		return n.Value // strings, and timestamps (kept as the text that was written)
	}
	return nil
}

// frontmatterOrder puts the familiar keys first; anything else follows alphabetically.
var frontmatterOrder = []string{"id", "title", "category", "tags", "kind", "favorite", "archived", "createdAt", "updatedAt", "importRef", "links", "extras", "folder", "slug"}

// SerializeNote renders a note as a Markdown file with YAML frontmatter. Plain notes get their folder and slug from
// the path; encrypted notes live at _enc/<id>.md and carry both inside the encrypted frontmatter (keepPath).
func SerializeNote(n Entity, keepPath bool) ([]byte, error) {
	skip := map[string]bool{"markdown": true}
	if !keepPath {
		skip["folder"], skip["slug"] = true, true
	}
	var keys []string
	seen := map[string]bool{}
	for _, k := range frontmatterOrder {
		if _, ok := n[k]; ok && !skip[k] {
			keys, seen[k] = append(keys, k), true
		}
	}
	var rest []string
	for k := range n {
		if !seen[k] && !skip[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	keys = append(keys, rest...)
	doc := &yaml.Node{Kind: yaml.MappingNode}
	for _, k := range keys {
		v := &yaml.Node{}
		if err := v.Encode(n[k]); err != nil {
			return nil, err
		}
		doc.Content = append(doc.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: k}, v)
	}
	y, err := yaml.Marshal(doc)
	if err != nil {
		return nil, err
	}
	return []byte("---\n" + strings.TrimRight(string(y), "\n") + "\n---\n" + n.Str("markdown")), nil
}

// SafeFolder normalises a user-supplied folder to safe, slugged forward-slash segments.
func SafeFolder(folder string) (string, error) {
	var segs []string
	for _, s := range regexp.MustCompile(`[\\/]+`).Split(folder, -1) {
		s = strings.TrimSpace(s)
		if s == "" || s == "." {
			continue
		}
		if s == ".." {
			return "", apperr.Validation("Invalid folder")
		}
		segs = append(segs, domain.Slugify(s))
	}
	return strings.Join(segs, "/"), nil
}

func sha(b []byte) string { h := sha1.Sum(b); return hex.EncodeToString(h[:]) }

type noteEntry struct {
	e   Entity
	rel string
	seq uint64
}

// NoteRepo keeps notes as real Markdown files with YAML frontmatter under data/notes/<folder>/<slug>.md. While
// encryption is on they are encrypted files at data/notes/_enc/<id>.md, so even titles and folders are hidden.
type NoteRepo struct {
	Events
	dataDir string
	watch   bool
	codec   jsonstore.Codec
	schema  *domain.Schema
	// Readers is how many files are read at once during the start-up scan.
	Readers int

	wmu sync.Mutex // serialises writes

	mu        sync.RWMutex
	byID      map[string]noteEntry
	byRel     map[string]string
	written   map[string]string // abs path -> hash of our last write
	seq       uint64
	cache     []Entity
	fresh     bool
	plainSeen int

	wstate noteWatch
}

// NewNoteRepo creates the notes repository. codec may be nil (plain files).
func NewNoteRepo(dataDir string, watch bool, codec jsonstore.Codec) *NoteRepo {
	if codec == nil {
		codec = jsonstore.PlainCodec{}
	}
	return &NoteRepo{
		dataDir: dataDir, watch: watch, codec: codec, schema: domain.Schemas["notes"], Readers: 16,
		byID: map[string]noteEntry{}, byRel: map[string]string{}, written: map[string]string{},
	}
}

// Name is "notes".
func (r *NoteRepo) Name() string { return "notes" }

func (r *NoteRepo) root() string { return filepath.Join(r.dataDir, "data", "notes") }

// PlainSeen counts plain files found while encryption is on (an interrupted switch or a restored older backup).
func (r *NoteRepo) PlainSeen() int { r.mu.RLock(); defer r.mu.RUnlock(); return r.plainSeen }

// Init scans the notes folder and starts watching it.
func (r *NoteRepo) Init() error {
	if err := os.MkdirAll(r.root(), 0o755); err != nil {
		return err
	}
	if err := r.scan(); err != nil {
		return err
	}
	if r.watch {
		return r.startWatch()
	}
	return nil
}

// Close stops the watcher.
func (r *NoteRepo) Close() error { return r.stopWatch() }

func listMarkdown(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsPermission(err) {
				return filepath.SkipDir
			}
			return err
		}
		if p != dir && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".md") {
			out = append(out, p)
		}
		return nil
	})
	return out, err
}

type parsedNote struct {
	abs, rel  string
	e         Entity
	needsID   bool
	plain     bool
	unchanged bool
}

// scan reads every note with bounded parallelism and indexes them in path order.
func (r *NoteRepo) scan() error {
	files, err := listMarkdown(r.root())
	if err != nil {
		return err
	}
	sort.Strings(files)
	results := make([]*parsedNote, len(files))
	var wg sync.WaitGroup
	work := make(chan int)
	for w := 0; w < max(1, r.Readers); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range work {
				if p, err := r.parse(files[i]); err == nil {
					results[i] = p
				}
			}
		}()
	}
	for i := range files {
		work <- i
	}
	close(work)
	wg.Wait()
	for _, p := range results {
		if p != nil {
			if err := r.commit(p, true); err != nil {
				return err
			}
		}
	}
	return nil
}

// parse reads and validates one file; a nil result with a nil error means "ignore it" (locked, invalid or our own write).
func (r *NoteRepo) parse(abs string) (*parsedNote, error) {
	raw, err := jsonstore.ReadFileRetry(abs)
	if err != nil {
		return nil, err
	}
	r.mu.RLock()
	own := r.written[abs] == sha(raw)
	r.mu.RUnlock()
	if own {
		return &parsedNote{unchanged: true}, nil
	}
	plainText, err := r.codec.Decode(raw)
	if err != nil {
		if jsonstore.IsCode(err, jsonstore.CodeEncrypted) || jsonstore.IsCode(err, jsonstore.CodeDecryptFailed) {
			return nil, nil // locked, or encrypted under another key: leave it alone
		}
		return nil, err
	}
	sealed := jsonstore.IsEncrypted(raw)
	relNative, _ := filepath.Rel(r.root(), abs)
	rel := filepath.ToSlash(relNative)
	data, body := ParseFrontmatter(string(plainText))
	pathFolder := path.Dir(rel)
	if pathFolder == "." {
		pathFolder = ""
	}
	folder := pathFolder
	if f, ok := data["folder"].(string); sealed && ok {
		folder = f
	}
	base := strings.TrimSuffix(path.Base(rel), ".md")
	if s, ok := data["slug"].(string); sealed && ok && s != "" {
		base = s
	}
	now := domain.NowISO()
	cand := map[string]any{}
	for k, v := range data {
		cand[k] = v
	}
	needsID := false
	if id, _ := data["id"].(string); id == "" {
		needsID = true
		cand["id"] = domain.NewID()
	}
	if cand["title"] == nil {
		cand["title"] = base
	}
	cand["slug"], cand["folder"], cand["markdown"] = base, folder, body
	if cand["createdAt"] == nil {
		cand["createdAt"] = now
	}
	if cand["updatedAt"] == nil {
		cand["updatedAt"] = now
	}
	e, perr := r.schema.Parse(cand, domain.ModeFull)
	if perr != nil {
		return nil, nil // an invalid hand edit: keep the previous state
	}
	return &parsedNote{abs: abs, rel: rel, e: e, needsID: needsID, plain: !sealed}, nil
}

// commit indexes a parsed note (and writes it back when it had no id yet).
func (r *NoteRepo) commit(p *parsedNote, quiet bool) error {
	if p.unchanged {
		return nil
	}
	r.mu.Lock()
	if r.codec.Encrypting() && p.plain {
		r.plainSeen++
	}
	// drop any stale entry for this path
	if old, ok := r.byRel[p.rel]; ok && old != p.e.ID() {
		delete(r.byID, old)
	}
	r.seq++
	if prev, ok := r.byID[p.e.ID()]; ok {
		delete(r.byRel, prev.rel)
	}
	r.byID[p.e.ID()] = noteEntry{e: p.e, rel: p.rel, seq: r.seq}
	r.byRel[p.rel] = p.e.ID()
	r.fresh = false
	r.mu.Unlock()
	if p.needsID {
		if err := r.writeFile(p.e, p.rel); err != nil {
			return err
		}
	}
	if !quiet {
		r.emitUpsert(p.e)
	}
	return nil
}

func (r *NoteRepo) dropFile(abs string) {
	relNative, err := filepath.Rel(r.root(), abs)
	if err != nil {
		return
	}
	rel := filepath.ToSlash(relNative)
	r.mu.Lock()
	id, ok := r.byRel[rel]
	if ok {
		delete(r.byRel, rel)
		delete(r.byID, id)
		r.fresh = false
	}
	r.mu.Unlock()
	if ok {
		r.emitRemove(id)
	}
}

func (r *NoteRepo) writeFile(n Entity, rel string) error {
	text, err := SerializeNote(n, r.codec.Encrypting())
	if err != nil {
		return err
	}
	raw, err := r.codec.Encode(text)
	if err != nil {
		return err
	}
	abs := filepath.Join(r.root(), filepath.FromSlash(rel))
	r.mu.Lock()
	r.written[abs] = sha(raw)
	r.mu.Unlock()
	return jsonstore.AtomicWriteFile(abs, raw)
}

// relFor says where a note lives right now: a path-free file when encrypting, otherwise folder/slug.md.
func (r *NoteRepo) relFor(folder, slug, id string) string {
	if r.codec.Encrypting() {
		return "_enc/" + id + ".md"
	}
	return r.uniqueRel(folder, slug, id)
}

func (r *NoteRepo) uniqueRel(folder, slug, selfID string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for n := 1; ; n++ {
		name := slug
		if n > 1 {
			name = slug + "-" + itoa(n)
		}
		rel := path.Join(folder, name+".md")
		if owner, taken := r.byRel[rel]; !taken || owner == selfID {
			return rel
		}
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// All returns the notes in index order (path order after a scan, then the order they were added).
func (r *NoteRepo) All() []Entity {
	r.mu.RLock()
	if r.fresh {
		defer r.mu.RUnlock()
		return r.cache
	}
	r.mu.RUnlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.fresh {
		list := make([]noteEntry, 0, len(r.byID))
		for _, v := range r.byID {
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

// Get returns one note.
func (r *NoteRepo) Get(id string) (Entity, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	v, ok := r.byID[id]
	return v.e, ok
}

// ReloadAll rescans the folder.
func (r *NoteRepo) ReloadAll() error {
	r.mu.Lock()
	r.byID, r.byRel, r.fresh = map[string]noteEntry{}, map[string]string{}, false
	r.mu.Unlock()
	if err := r.scan(); err != nil {
		return err
	}
	r.emitRebuild()
	return nil
}

func (r *NoteRepo) set(e Entity, rel string) {
	r.mu.Lock()
	if prev, ok := r.byID[e.ID()]; ok {
		delete(r.byRel, prev.rel)
		r.seq++
		r.byID[e.ID()] = noteEntry{e: e, rel: rel, seq: prev.seq}
	} else {
		r.seq++
		r.byID[e.ID()] = noteEntry{e: e, rel: rel, seq: r.seq}
	}
	r.byRel[rel] = e.ID()
	r.fresh = false
	r.mu.Unlock()
}

// Create validates input and writes a new note.
func (r *NoteRepo) Create(input map[string]any, id string) (Entity, error) {
	if input == nil {
		return nil, apperr.Validation("Body must be an object")
	}
	parsed, err := r.schema.Parse(PrepareCreate("notes", input), domain.ModeCreate)
	if err != nil {
		return nil, err
	}
	title := strings.TrimSpace(parsed.Str("title"))
	if title == "" {
		return nil, apperr.Validation("title is required")
	}
	folder, err := SafeFolder(parsed.Str("folder"))
	if err != nil {
		return nil, err
	}
	r.wmu.Lock()
	defer r.wmu.Unlock()
	if id == "" {
		id = domain.NewID()
	}
	rel := r.relFor(folder, domain.Slugify(title), id)
	slug := strings.TrimSuffix(path.Base(rel), ".md")
	if r.codec.Encrypting() {
		slug = domain.Slugify(title)
	}
	now := domain.NowISO()
	note := parsed.Clone()
	note["title"], note["folder"], note["slug"], note["id"], note["createdAt"], note["updatedAt"] = title, folder, slug, id, now, now
	if err := r.writeFile(note, rel); err != nil {
		return nil, err
	}
	r.set(note, rel)
	r.emitUpsert(note)
	return note, nil
}

// Patch applies a partial update; a new title or folder moves the file.
func (r *NoteRepo) Patch(id string, input map[string]any) (Entity, error) {
	r.wmu.Lock()
	defer r.wmu.Unlock()
	r.mu.RLock()
	cur, ok := r.byID[id]
	r.mu.RUnlock()
	if !ok {
		return nil, apperr.NotFound("notes", id)
	}
	patch, err := r.schema.Parse(input, domain.ModePatch)
	if err != nil {
		return nil, err
	}
	merged := domain.Merge(cur.e, patch)
	merged["id"], merged["createdAt"] = id, cur.e["createdAt"]
	if f, ok := patch["folder"].(string); ok {
		sf, err := SafeFolder(f)
		if err != nil {
			return nil, err
		}
		merged["folder"] = sf
	}
	merged["updatedAt"] = domain.NowISO()
	wantSlug := cur.e.Str("slug")
	if t, ok := patch["title"].(string); ok && t != "" && patch.Str("slug") == "" {
		wantSlug = domain.Slugify(t)
	}
	rel := r.relFor(merged.Str("folder"), wantSlug, id)
	if r.codec.Encrypting() {
		merged["slug"] = wantSlug
	} else {
		merged["slug"] = strings.TrimSuffix(path.Base(rel), ".md")
	}
	if err := r.writeFile(merged, rel); err != nil {
		return nil, err
	}
	r.set(merged, rel)
	if rel != cur.rel {
		r.removeFile(cur.rel)
	}
	r.emitUpsert(merged)
	return merged, nil
}

func (r *NoteRepo) removeFile(rel string) {
	abs := filepath.Join(r.root(), filepath.FromSlash(rel))
	r.mu.Lock()
	delete(r.written, abs)
	r.mu.Unlock()
	_ = os.Remove(abs)
}

// Restore puts a note back exactly as given.
func (r *NoteRepo) Restore(e Entity) error {
	r.wmu.Lock()
	defer r.wmu.Unlock()
	r.mu.RLock()
	cur, had := r.byID[e.ID()]
	r.mu.RUnlock()
	rel := r.relFor(e.Str("folder"), e.Str("slug"), e.ID())
	if err := r.writeFile(e, rel); err != nil {
		return err
	}
	r.set(e, rel)
	if had && cur.rel != rel {
		r.removeFile(cur.rel)
	}
	r.emitUpsert(e)
	return nil
}

// Remove deletes a note.
func (r *NoteRepo) Remove(id string) error {
	r.wmu.Lock()
	defer r.wmu.Unlock()
	r.mu.Lock()
	cur, ok := r.byID[id]
	if ok {
		delete(r.byID, id)
		delete(r.byRel, cur.rel)
		r.fresh = false
	}
	r.mu.Unlock()
	if !ok {
		return apperr.NotFound("notes", id)
	}
	r.removeFile(cur.rel)
	r.emitRemove(id)
	return nil
}

// Recode moves every note to the layout the current codec calls for (call after the codec was switched): encrypted
// notes to _enc/<id>.md, plain notes back to folder/slug.md.
func (r *NoteRepo) Recode() error {
	r.wmu.Lock()
	defer r.wmu.Unlock()
	r.mu.RLock()
	snapshot := make([]noteEntry, 0, len(r.byID))
	for _, v := range r.byID {
		snapshot = append(snapshot, v)
	}
	r.mu.RUnlock()
	sort.Slice(snapshot, func(i, j int) bool { return snapshot[i].seq < snapshot[j].seq })
	for _, cur := range snapshot {
		id := cur.e.ID()
		rel := r.relFor(cur.e.Str("folder"), cur.e.Str("slug"), id)
		next := cur.e
		if !r.codec.Encrypting() {
			next = cur.e.Clone()
			next["slug"] = strings.TrimSuffix(path.Base(rel), ".md")
		}
		if err := r.writeFile(next, rel); err != nil {
			return err
		}
		r.set(next, rel)
		if rel != cur.rel {
			r.removeFile(cur.rel)
		}
	}
	r.mu.Lock()
	r.plainSeen = 0
	r.mu.Unlock()
	return pruneEmptyDirs(r.root(), true)
}

func pruneEmptyDirs(dir string, isRoot bool) error {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	empty := true
	for _, e := range ents {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			if err := pruneEmptyDirs(filepath.Join(dir, e.Name()), false); err != nil {
				return err
			}
			if _, err := os.Stat(filepath.Join(dir, e.Name())); err == nil {
				empty = false
			}
		} else {
			empty = false
		}
	}
	if empty && !isRoot {
		_ = os.Remove(dir)
	}
	return nil
}

// ---- watcher ----

type noteWatch struct {
	mu     sync.Mutex
	w      *fsnotify.Watcher
	timers map[string]*time.Timer
	done   chan struct{}
}

func (r *NoteRepo) startWatch() error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	r.wstate.mu.Lock()
	r.wstate.w, r.wstate.timers, r.wstate.done = w, map[string]*time.Timer{}, make(chan struct{})
	r.wstate.mu.Unlock()
	addNoteDirs(w, r.root())
	go func(done chan struct{}) {
		for {
			select {
			case <-done:
				return
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				r.onEvent(w, ev)
			case <-w.Errors:
			}
		}
	}(r.wstate.done)
	return nil
}

func addNoteDirs(w *fsnotify.Watcher, root string) {
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			_ = w.Add(p)
		}
		return nil
	})
}

func (r *NoteRepo) onEvent(w *fsnotify.Watcher, ev fsnotify.Event) {
	if strings.HasPrefix(filepath.Base(ev.Name), ".") {
		return
	}
	if ev.Has(fsnotify.Create) {
		if st, err := os.Stat(ev.Name); err == nil && st.IsDir() {
			addNoteDirs(w, ev.Name)
			files, _ := listMarkdown(ev.Name)
			for _, f := range files {
				r.schedule(f)
			}
			return
		}
	}
	if strings.HasSuffix(ev.Name, ".md") {
		r.schedule(ev.Name)
	}
}

func (r *NoteRepo) schedule(abs string) {
	r.wstate.mu.Lock()
	defer r.wstate.mu.Unlock()
	if r.wstate.timers == nil {
		return
	}
	if t, ok := r.wstate.timers[abs]; ok {
		t.Reset(80 * time.Millisecond)
		return
	}
	r.wstate.timers[abs] = time.AfterFunc(80*time.Millisecond, func() {
		r.wstate.mu.Lock()
		delete(r.wstate.timers, abs)
		r.wstate.mu.Unlock()
		r.changed(abs)
	})
}

// changed handles a settled file event: reload the file if it exists, otherwise drop the note.
func (r *NoteRepo) changed(abs string) {
	if _, err := os.Stat(abs); err != nil {
		r.dropFile(abs)
		return
	}
	p, err := r.parse(abs)
	if err != nil || p == nil {
		return
	}
	_ = r.commit(p, false)
}

func (r *NoteRepo) stopWatch() error {
	r.wstate.mu.Lock()
	defer r.wstate.mu.Unlock()
	if r.wstate.w == nil {
		return nil
	}
	close(r.wstate.done)
	for _, t := range r.wstate.timers {
		t.Stop()
	}
	r.wstate.timers = nil
	err := r.wstate.w.Close()
	r.wstate.w = nil
	return err
}
