package jsonstore

import (
	"bytes"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Compatibility with the TypeScript jsonstore. fixtures/compat/node was written by Node (scripts/gen-compat-fixtures.ts);
// fixtures/compat/go is written by this test with UPDATE_COMPAT=1 and read back by the Node test suite.

type cItem struct {
	ID   string   `json:"id"`
	N    float64  `json:"n"`
	Name string   `json:"name"`
	Tags []string `json:"tags"`
}
type cDoc struct {
	Items []cItem `json:"items"`
}

var compatData = cDoc{Items: []cItem{
	{ID: "c1", N: 1, Name: "ünïcode <tag> & \"quotes\" \u2028 end", Tags: []string{"a", "b"}},
	{ID: "c2", N: 2.5, Name: "second", Tags: []string{}}, // an empty list must stay [] (a nil slice would write null)
	{ID: "c3", N: 3, Name: "😀 emoji and \\ backslash\nnewline", Tags: []string{"z"}},
}}

func cOpts() Options[cDoc] {
	return Options[cDoc]{Version: 1, Defaults: func() cDoc { return cDoc{Items: []cItem{}} }}
}

const compatRoot = "../../../fixtures/compat"

func compatKey(t *testing.T) []byte {
	t.Helper()
	k, err := hex.DecodeString(strings.TrimSpace(read(t, filepath.Join(compatRoot, "KEY.hex"))))
	if err != nil || len(k) != 32 {
		t.Fatal("bad KEY.hex")
	}
	return k
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func checkItems(t *testing.T, got cDoc) {
	t.Helper()
	if len(got.Items) != len(compatData.Items) {
		t.Fatalf("%d items", len(got.Items))
	}
	for i, want := range compatData.Items {
		g := got.Items[i]
		if g.ID != want.ID || g.N != want.N || g.Name != want.Name || strings.Join(g.Tags, ",") != strings.Join(want.Tags, ",") {
			t.Errorf("item %d:\n got %+v\nwant %+v", i, g, want)
		}
	}
}

func TestCompatGoReadsAPlainNodeLibrary(t *testing.T) {
	dir := t.TempDir()
	copyTree(t, filepath.Join(compatRoot, "node", "plain"), dir)
	f := must(Open(NewStore(dir, nil, nil), "data/commands/a.json", cOpts()))
	checkItems(t, f.Data())
}

func TestCompatGoWritesTheSameBytesAsNode(t *testing.T) {
	dir := t.TempDir()
	f := must(Open(NewStore(dir, nil, nil), "data/commands/a.json", cOpts()))
	must(f.Update(func(d *cDoc) error { d.Items = append(d.Items, compatData.Items...); return nil }))
	want := read(t, filepath.Join(compatRoot, "node", "plain", "data", "commands", "a.json"))
	if got := read(t, filepath.Join(dir, "data/commands/a.json")); got != want {
		t.Fatalf("Go output differs from Node output:\n got %q\nwant %q", got, want)
	}
}

func TestCompatGoReadsAnEncryptedNodeLibrary(t *testing.T) {
	dir := t.TempDir()
	copyTree(t, filepath.Join(compatRoot, "node", "encrypted"), dir)
	codec := must(NewAESGCMCodec(compatKey(t)))
	f := must(Open(NewStore(dir, nil, NewCodecHolder(codec)), "data/commands/a.json", cOpts()))
	checkItems(t, f.Data())

	lines := strings.Split(strings.TrimSpace(read(t, filepath.Join(dir, "activity/2026-01.jsonl"))), "\n")
	if len(lines) != 3 {
		t.Fatalf("%d log lines", len(lines))
	}
	for i, l := range lines {
		plain := must(codec.DecodeLine(l))
		if !strings.Contains(plain, `"n":`+string(rune('1'+i))) {
			t.Errorf("line %d decrypted to %q", i, plain)
		}
	}
	// Without the key the Node-encrypted file is refused, never quarantined.
	if _, err := Open(NewStore(dir, nil, nil), "data/commands/a.json", cOpts()); !IsCode(err, CodeEncrypted) {
		t.Fatalf("locked: %v", err)
	}
}

func TestCompatGoRestoresFromANodeBackup(t *testing.T) {
	dir := t.TempDir()
	copyTree(t, filepath.Join(compatRoot, "node", "backups"), filepath.Join(dir, "backups"))
	backups := NewBackupManager(BackupOptions{DataDir: dir, BackupDir: filepath.Join(dir, "backups")})
	list := must(backups.List())
	if len(list) != 1 || list[0].Reason != "compat" {
		t.Fatalf("list: %+v", list)
	}
	write(t, filepath.Join(dir, "data/commands/a.json"), "corrupt{")
	f := must(Open(NewStore(dir, backups, nil), "data/commands/a.json", cOpts()))
	checkItems(t, f.Data())
}

func TestCompatWriteGoFixtures(t *testing.T) {
	out := filepath.Join(compatRoot, "go")
	update := os.Getenv("UPDATE_COMPAT") == "1"
	if update {
		_ = os.RemoveAll(out)
	}
	for _, kind := range []string{"plain", "encrypted"} {
		dir := filepath.Join(out, kind)
		if !update {
			dir = t.TempDir() // normal runs write to a scratch copy and only check the committed files below
		}
		s := NewStore(dir, nil, nil)
		var codec *AESGCMCodec
		if kind == "encrypted" {
			codec = must(NewAESGCMCodec(compatKey(t)))
			s.Codec.Set(codec)
		}
		f := must(Open(s, "data/commands/a.json", cOpts()))
		must(f.Update(func(d *cDoc) error { d.Items = append(d.Items, compatData.Items...); return nil }))
		if kind == "encrypted" {
			var lines []string
			for _, l := range []string{`{"n":1}`, `{"n":2}`, `{"n":3}`} {
				lines = append(lines, must(codec.EncodeLine(l)))
			}
			if err := os.MkdirAll(filepath.Join(dir, "activity"), 0o755); err != nil {
				t.Fatal(err)
			}
			write(t, filepath.Join(dir, "activity/2026-01.jsonl"), strings.Join(lines, "\n")+"\n")
		}
	}
	if update {
		// A Go-made backup of the plain library, with a fixed name.
		backups := NewBackupManager(BackupOptions{DataDir: filepath.Join(out, "plain"), BackupDir: filepath.Join(out, "backups")})
		b := must(backups.Create("compat"))
		if err := os.Rename(b.File, filepath.Join(out, "backups", "20260101T000000-compat.zip")); err != nil {
			t.Fatal(err)
		}
		return
	}
	// The committed Go-written files must still load, and the plain one must be byte-for-byte what Go writes now.
	dir := t.TempDir()
	copyTree(t, filepath.Join(out, "plain"), dir)
	checkItems(t, must(Open(NewStore(dir, nil, nil), "data/commands/a.json", cOpts())).Data())
	edir := t.TempDir()
	copyTree(t, filepath.Join(out, "encrypted"), edir)
	es := NewStore(edir, nil, NewCodecHolder(must(NewAESGCMCodec(compatKey(t)))))
	checkItems(t, must(Open(es, "data/commands/a.json", cOpts())).Data())
	if !bytes.Equal([]byte(read(t, filepath.Join(out, "plain", "data/commands/a.json"))), []byte(read(t, filepath.Join(compatRoot, "node", "plain", "data/commands/a.json")))) {
		t.Fatal("committed Go plain file differs from the Node one")
	}
}
