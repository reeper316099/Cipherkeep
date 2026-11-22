package repo

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/reeper316099/Cipherkeep/go/internal/jsonstore"
)

func newNotes(t *testing.T, watch bool, codec jsonstore.Codec) (*NoteRepo, string) {
	t.Helper()
	dir := t.TempDir()
	r := NewNoteRepo(dir, watch, codec)
	if err := r.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r, dir
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestNotesAreRealMarkdownFilesWithFrontmatter(t *testing.T) {
	r, dir := newNotes(t, false, nil)
	n := must(r.Create(map[string]any{"title": "SSH Guide", "folder": "Guides/Remote Access", "markdown": "# SSH\n\nBody\n", "tags": []any{"ssh"}}, ""))
	if n.Str("folder") != "guides/remote-access" || n.Str("slug") != "ssh-guide" {
		t.Fatalf("%+v", n)
	}
	file := filepath.Join(dir, "data/notes/guides/remote-access/ssh-guide.md")
	text := readFile(t, file)
	if !strings.HasPrefix(text, "---\nid: "+n.ID()+"\ntitle: SSH Guide\n") || !strings.HasSuffix(text, "---\n# SSH\n\nBody\n") {
		t.Fatalf("file:\n%s", text)
	}
	if strings.Contains(text, "folder:") || strings.Contains(text, "slug:") || strings.Contains(text, "markdown:") {
		t.Fatal("folder, slug and body come from the path and the file body, not the frontmatter")
	}
	// A second note with the same title gets a unique file name.
	m := must(r.Create(map[string]any{"title": "SSH Guide", "folder": "Guides/Remote Access"}, ""))
	if m.Str("slug") != "ssh-guide-2" {
		t.Fatalf("slug %q", m.Str("slug"))
	}
	// A new instance reads them back from disk.
	r2 := NewNoteRepo(dir, false, nil)
	if err := r2.Init(); err != nil {
		t.Fatal(err)
	}
	if got, ok := r2.Get(n.ID()); !ok || got.Str("markdown") != "# SSH\n\nBody\n" || got.Str("folder") != "guides/remote-access" || got.Strs("tags")[0] != "ssh" {
		t.Fatalf("reloaded: %+v", got)
	}
}

func TestPatchingATitleOrFolderMovesTheFile(t *testing.T) {
	r, dir := newNotes(t, false, nil)
	n := must(r.Create(map[string]any{"title": "Old", "folder": "a"}, ""))
	p := must(r.Patch(n.ID(), map[string]any{"title": "New Name", "folder": "b/c"}))
	if p.Str("slug") != "new-name" || p.Str("folder") != "b/c" {
		t.Fatalf("%+v", p)
	}
	if _, err := os.Stat(filepath.Join(dir, "data/notes/a/old.md")); err == nil {
		t.Fatal("the old file should be gone")
	}
	if _, err := os.Stat(filepath.Join(dir, "data/notes/b/c/new-name.md")); err != nil {
		t.Fatal("the new file should exist")
	}
	if _, err := r.Patch(n.ID(), map[string]any{"folder": "../escape"}); !isStatus(err, 400) {
		t.Fatalf("path traversal: %v", err)
	}
	if err := r.Remove(n.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "data/notes/b/c/new-name.md")); err == nil {
		t.Fatal("remove must delete the file")
	}
}

func TestHandEditedNotesReloadAndInvalidEditsAreIgnored(t *testing.T) {
	r, dir := newNotes(t, true, nil)
	n := must(r.Create(map[string]any{"title": "Live", "markdown": "one"}, ""))
	ups := make(chan Entity, 8)
	removes := make(chan string, 8)
	r.OnUpsert(func(e Entity) { ups <- e })
	r.OnRemove(func(id string) { removes <- id })
	file := filepath.Join(dir, "data/notes/live.md")

	text := readFile(t, file)
	if err := os.WriteFile(file, []byte(strings.Replace(text, "one", "two", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	waitUpsert := func(want string) {
		t.Helper()
		deadline := time.After(10 * time.Second)
		for {
			select {
			case e := <-ups:
				if e.Str("markdown") == want {
					return
				}
			case <-deadline:
				t.Fatalf("never happened: upsert with markdown %q", want)
			}
		}
	}
	waitUpsert("two")
	if got, _ := r.Get(n.ID()); got.Str("markdown") != "two" {
		t.Fatal("not applied")
	}

	// A brand-new file dropped into a new folder is picked up and gets an id written back.
	if err := os.MkdirAll(filepath.Join(dir, "data/notes/fresh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "data/notes/fresh/dropped.md"), []byte("---\ntitle: Dropped\ncreatedAt: 2026-01-01\n---\nhello"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitUpsert("hello")
	var dropped Entity
	for _, e := range r.All() {
		if e.Title() == "Dropped" {
			dropped = e
		}
	}
	if dropped == nil || dropped.Str("folder") != "fresh" || dropped.Str("createdAt") != "2026-01-01" || len(dropped.ID()) != 26 {
		t.Fatalf("dropped note: %+v", dropped)
	}
	if !strings.Contains(readFile(t, filepath.Join(dir, "data/notes/fresh/dropped.md")), "id: "+dropped.ID()) {
		t.Fatal("a file without an id gets one written back")
	}

	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	select {
	case id := <-removes:
		if id != n.ID() {
			t.Fatalf("removed %s", id)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("never happened: remove event after deleting the file")
	}
}

func TestNotesDoNotTreatTheirOwnWritesAsEdits(t *testing.T) {
	r, _ := newNotes(t, true, nil)
	n := must(r.Create(map[string]any{"title": "Mine", "markdown": "x"}, ""))
	count := 0
	r.OnUpsert(func(Entity) { count++ })
	must(r.Patch(n.ID(), map[string]any{"markdown": "y"}))
	time.Sleep(500 * time.Millisecond)
	if count != 1 {
		t.Fatalf("%d upserts for one patch", count)
	}
}

func TestEncryptedNotesLiveAtPathFreeFilesAndHideTitlesAndFolders(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	codec := must(jsonstore.NewAESGCMCodec(key))
	r, dir := newNotes(t, false, codec)
	n := must(r.Create(map[string]any{"title": "Secret ZEBRA title", "folder": "Hidden Folder", "markdown": "ZEBRA body"}, ""))
	ents := must(os.ReadDir(filepath.Join(dir, "data/notes")))
	if len(ents) != 1 || ents[0].Name() != "_enc" {
		t.Fatalf("only _enc should exist: %v", ents)
	}
	raw := must(os.ReadFile(filepath.Join(dir, "data/notes/_enc", n.ID()+".md")))
	if !jsonstore.IsEncrypted(raw) || bytes.Contains(raw, []byte("ZEBRA")) {
		t.Fatal("note file must be ciphertext")
	}
	// Reopening with the key restores folder and slug from the encrypted frontmatter.
	r2 := NewNoteRepo(dir, false, codec)
	if err := r2.Init(); err != nil {
		t.Fatal(err)
	}
	got, ok := r2.Get(n.ID())
	if !ok || got.Str("folder") != "hidden-folder" || got.Str("slug") != "secret-zebra-title" || got.Str("markdown") != "ZEBRA body" {
		t.Fatalf("%+v", got)
	}
	// Without the key the file is ignored, not quarantined, not rewritten.
	locked := NewNoteRepo(dir, false, nil)
	if err := locked.Init(); err != nil {
		t.Fatal(err)
	}
	if len(locked.All()) != 0 {
		t.Fatal("a locked repository must not load encrypted notes")
	}
	after := must(os.ReadFile(filepath.Join(dir, "data/notes/_enc", n.ID()+".md")))
	if !bytes.Equal(raw, after) {
		t.Fatal("the encrypted file was touched")
	}
}

func TestRecodeSwitchesTheWholeLibraryBetweenLayouts(t *testing.T) {
	holder := jsonstore.NewCodecHolder(nil)
	r, dir := newNotes(t, false, holder)
	a := must(r.Create(map[string]any{"title": "Alpha ZEBRA", "folder": "one", "markdown": "ZEBRA"}, ""))
	must(r.Create(map[string]any{"title": "Beta", "folder": "one/two", "markdown": "b"}, ""))
	key := make([]byte, 32)
	holder.Set(must(jsonstore.NewAESGCMCodec(key)))
	if err := r.Recode(); err != nil {
		t.Fatal(err)
	}
	ents := must(os.ReadDir(filepath.Join(dir, "data/notes")))
	if len(ents) != 1 || ents[0].Name() != "_enc" {
		t.Fatalf("after encrypting: %v", ents)
	}
	if got, _ := r.Get(a.ID()); got.Str("folder") != "one" {
		t.Fatal("folder lost")
	}

	holder.Set(jsonstore.PlainCodec{})
	// Decoding back needs the key while reading, but the repository holds entities in memory and only writes.
	if err := r.Recode(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readFile(t, filepath.Join(dir, "data/notes/one/alpha-zebra.md")), "ZEBRA") {
		t.Fatal("notes should be back at folder/slug.md in plain text")
	}
	if _, err := os.Stat(filepath.Join(dir, "data/notes/_enc")); err == nil {
		t.Fatal("the _enc folder should be removed once empty")
	}
}

func TestFrontmatterKeepsTimestampsAsStringsLikeTheTypeScriptVersion(t *testing.T) {
	data, body := ParseFrontmatter("---\ntitle: T\ncreatedAt: 2026-03-24T10:00:00.000Z\nn: 3\nflag: true\ntags: [a, b]\nnothing:\n---\nbody here")
	if data["createdAt"] != "2026-03-24T10:00:00.000Z" || data["n"] != 3.0 || data["flag"] != true || body != "body here" {
		t.Fatalf("%#v %q", data, body)
	}
	if tags, _ := data["tags"].([]any); len(tags) != 2 || data["nothing"] != nil {
		t.Fatalf("%#v", data)
	}
	if d, b := ParseFrontmatter("no frontmatter"); len(d) != 0 || b != "no frontmatter" {
		t.Fatal("plain files have no frontmatter")
	}
}

func TestSafeFolder(t *testing.T) {
	for in, want := range map[string]string{"": "", "A B/C d": "a-b/c-d", "/x//y/": "x/y", "./z": "z", `win\path`: "win/path"} {
		if got, err := SafeFolder(in); err != nil || got != want {
			t.Errorf("SafeFolder(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := SafeFolder("a/../b"); err == nil {
		t.Fatal(".. must be rejected")
	}
}
