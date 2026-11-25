package api

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/reeper316099/Cipherkeep/go/internal/jsonstore"
	"github.com/reeper316099/Cipherkeep/go/internal/library"
	"github.com/reeper316099/Cipherkeep/go/internal/repo"
	"github.com/reeper316099/Cipherkeep/go/internal/vault"
)

// skipTop are top-level names never rewritten: key material, plain metadata, backups (already raw copies) and scratch
// space.
var skipTop = map[string]bool{vault.File: true, "meta.json": true, "backups": true, "exports": true, "logs": true}

func looseFiles(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == dir {
			return nil
		}
		name := d.Name()
		top := filepath.Dir(p) == dir
		if strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".tmp") || top && skipTop[name] {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			out = append(out, p)
		}
		return nil
	})
	return out, err
}

// recodeLooseFiles brings every file that is not a JSON shard or a note (templates, attachments, import records,
// append-only logs) into the state `to` calls for. `from` decodes what is there now; plain files pass through any
// codec. Safe to repeat: files already in the target state are left untouched.
func recodeLooseFiles(lib *library.Library, from, to jsonstore.Codec) (int, error) {
	notesRoot := filepath.Join(lib.Dir, "data", "notes") + string(filepath.Separator)
	open := map[string]bool{}
	for _, p := range lib.Store.OpenPaths() {
		open[p] = true
	}
	files, err := looseFiles(lib.Dir)
	if err != nil {
		return 0, err
	}
	changed := 0
	for _, abs := range files {
		if strings.HasPrefix(abs, notesRoot) || open[abs] {
			continue
		}
		rel, err := filepath.Rel(lib.Dir, abs)
		if err != nil {
			return changed, err
		}
		rel = filepath.ToSlash(rel)
		did, err := func() (bool, error) {
			defer lib.Store.Lock(rel)()
			raw, err := os.ReadFile(abs)
			if err != nil {
				if os.IsNotExist(err) {
					return false, nil
				}
				return false, err
			}
			if strings.HasSuffix(abs, ".jsonl") {
				// Append-only logs: convert line by line under the same lock the writers use.
				lines := strings.Split(string(raw), "\n")
				dirty := false
				for i, l := range lines {
					if l == "" || jsonstore.IsEncryptedLine(l) == to.Encrypting() {
						continue
					}
					plain, err := from.DecodeLine(l)
					if err != nil {
						return false, err
					}
					if lines[i], err = to.EncodeLine(plain); err != nil {
						return false, err
					}
					dirty = true
				}
				if !dirty {
					return false, nil
				}
				return true, jsonstore.AtomicWriteFile(abs, []byte(strings.Join(lines, "\n")))
			}
			if jsonstore.IsEncrypted(raw) == to.Encrypting() {
				return false, nil
			}
			plain, err := from.Decode(raw)
			if err != nil {
				return false, err
			}
			enc, err := to.Encode(plain)
			if err != nil {
				return false, err
			}
			return true, jsonstore.AtomicWriteFile(abs, enc)
		}()
		if err != nil {
			return changed, err
		}
		if did {
			changed++
		}
	}
	return changed, nil
}

func notesOf(lib *library.Library) *repo.NoteRepo {
	n, _ := lib.Repos["notes"].(*repo.NoteRepo)
	return n
}

// recodeLibrary switches the whole library from the codec it uses now to `to` (shards, notes, everything else).
func recodeLibrary(lib *library.Library, from, to jsonstore.Codec) error {
	lib.Codec.Set(to)
	if err := lib.Store.RewriteAll(); err != nil {
		return err
	}
	if err := notesOf(lib).Recode(); err != nil {
		return err
	}
	_, err := recodeLooseFiles(lib, from, to)
	return err
}

// normalizeEncryption finishes, after opening an encrypted library, any switch an earlier run (or a restored older
// backup) left half-done.
func normalizeEncryption(lib *library.Library) error {
	if !lib.Codec.Encrypting() {
		return nil
	}
	if n := notesOf(lib); n.PlainSeen() > 0 {
		if err := n.Recode(); err != nil {
			return err
		}
	}
	if _, err := recodeLooseFiles(lib, lib.Codec, lib.Codec); err != nil {
		return err
	}
	// JSON shards restored from an old plaintext backup would be re-encoded the next time they are written; do it
	// now so nothing readable lingers.
	return lib.Store.RewritePlaintext()
}
