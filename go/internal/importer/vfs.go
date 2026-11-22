package importer

import (
	"archive/zip"
	"bytes"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/text/unicode/norm"

	"github.com/reeper316099/Cipherkeep/go/internal/apperr"
	"github.com/reeper316099/Cipherkeep/go/internal/notion"
)

// Limits on what one import may read.
const (
	MaxBytes = 500 * 1024 * 1024
	MaxFiles = 50_000
)

// VirtualFS is an in-memory view of an import source: normalised slash-separated paths to bytes.
type VirtualFS struct {
	Files map[string][]byte
}

// NewVirtualFS makes an empty source.
func NewVirtualFS() *VirtualFS { return &VirtualFS{Files: map[string][]byte{}} }

// Add stores a file. Paths are normalised to NFC with forward slashes; paths that escape upward (zip-slip) and macOS
// metadata are ignored.
func (v *VirtualFS) Add(p string, data []byte) {
	clean := strings.TrimLeft(strings.ReplaceAll(norm.NFC.String(p), `\`, "/"), "/")
	if clean == "" {
		return
	}
	segs := strings.Split(clean, "/")
	for _, s := range segs {
		if s == ".." {
			return
		}
	}
	if strings.HasPrefix(clean, "__MACOSX/") || segs[len(segs)-1] == ".DS_Store" {
		return
	}
	v.Files[clean] = data
}

// Text returns a file as text without a byte-order mark. A missing file is an empty string.
func (v *VirtualFS) Text(p string) string { return notion.StripBOM(string(v.Files[p])) }

// Paths lists the paths in JavaScript's string order.
func (v *VirtualFS) Paths() []string {
	out := make([]string, 0, len(v.Files))
	for p := range v.Files {
		out = append(out, p)
	}
	sortUTF16(out)
	return out
}

var zipExt = regexp.MustCompile(`(?i)\.zip$`)

// FromZip reads a zip archive. Zips nested inside it (Notion sometimes wraps parts that way) are opened up to two
// levels deep. The total size actually read, not the size the archive claims, is what the limit counts.
func FromZip(buf []byte) (*VirtualFS, error) {
	v := NewVirtualFS()
	var total int64
	return v, readZip(buf, v, 0, &total)
}

func readZip(buf []byte, v *VirtualFS, depth int, total *int64) error {
	zr, err := zip.NewReader(bytes.NewReader(buf), int64(len(buf)))
	if err != nil {
		return apperr.New(400, "BAD_SOURCE", "Could not read the zip archive: %v", err)
	}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || strings.HasSuffix(f.Name, "/") {
			continue
		}
		if len(v.Files) > MaxFiles {
			return apperr.New(413, "TOO_LARGE", "Too many files in archive")
		}
		if int64(f.UncompressedSize64) > MaxBytes-*total {
			return apperr.New(413, "TOO_LARGE", "Archive too large")
		}
		rc, err := f.Open()
		if err != nil {
			return apperr.New(400, "BAD_SOURCE", "Could not read %s in the archive", f.Name)
		}
		data, err := io.ReadAll(io.LimitReader(rc, MaxBytes-*total+1))
		_ = rc.Close()
		if err != nil {
			return apperr.New(400, "BAD_SOURCE", "Could not read %s in the archive", f.Name)
		}
		*total += int64(len(data))
		if *total > MaxBytes {
			return apperr.New(413, "TOO_LARGE", "Archive too large")
		}
		if depth < 2 && zipExt.MatchString(f.Name) {
			if err := readZip(data, v, depth+1, total); err != nil {
				return err
			}
		} else {
			v.Add(f.Name, data)
		}
	}
	return nil
}

// FromFolder reads every regular file under dir; symbolic links are not followed.
func FromFolder(dir string) (*VirtualFS, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	v := NewVirtualFS()
	var total int64
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		total += int64(len(data))
		if total > MaxBytes {
			return apperr.New(413, "TOO_LARGE", "Folder too large")
		}
		rel, _ := filepath.Rel(root, p)
		v.Add(filepath.ToSlash(rel), data)
		return nil
	})
	return v, err
}

// FromPath reads a folder, a zip file or a single file, and returns the name to show for it.
func FromPath(p string) (*VirtualFS, string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return nil, "", err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, "", apperr.New(400, "BAD_SOURCE", "Source path does not exist")
	}
	name := filepath.Base(abs)
	if st.IsDir() {
		v, err := FromFolder(abs)
		return v, name, err
	}
	buf, err := os.ReadFile(abs)
	if err != nil {
		return nil, "", err
	}
	if zipExt.MatchString(abs) {
		v, err := FromZip(buf)
		return v, name, err
	}
	v := NewVirtualFS()
	v.Add(name, buf)
	return v, name, nil
}

// FromUpload reads uploaded bytes: a zip (by name or by its PK signature) or a single file.
func FromUpload(name string, buf []byte) (*VirtualFS, error) {
	if zipExt.MatchString(name) || (len(buf) > 1 && buf[0] == 0x50 && buf[1] == 0x4b) {
		return FromZip(buf)
	}
	v := NewVirtualFS()
	v.Add(name, buf)
	return v, nil
}
