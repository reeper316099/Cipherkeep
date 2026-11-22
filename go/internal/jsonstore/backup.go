package jsonstore

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// BackupOptions configures a BackupManager.
type BackupOptions struct {
	DataDir   string
	BackupDir string
	Keep      int // newest zips kept; default 20
	// Exclude lists extra top-level names (relative to DataDir) left out of backups.
	Exclude []string
}

// BackupInfo describes one backup zip.
type BackupInfo struct {
	Name      string
	File      string
	CreatedAt time.Time
	Reason    string
	Size      int64

	mod time.Time // modification time, used to order backups made in the same second
}

var defaultExclude = []string{"backups", "exports", "logs", "imports", ".quarantine", ".lock", ".electron-session"}

// BackupManager makes rolling zip backups of a whole data directory.
type BackupManager struct {
	opts    BackupOptions
	keep    int
	exclude map[string]bool
}

// NewBackupManager returns a manager for the given directories.
func NewBackupManager(o BackupOptions) *BackupManager {
	k := o.Keep
	if k <= 0 {
		k = 20
	}
	ex := map[string]bool{}
	for _, n := range append(append([]string{}, defaultExclude...), o.Exclude...) {
		ex[n] = true
	}
	return &BackupManager{opts: o, keep: k, exclude: ex}
}

// Keep returns how many backups are retained.
func (m *BackupManager) Keep() int { return m.keep }

type entry struct{ abs, name string }

var unsafeReason = regexp.MustCompile(`[^a-zA-Z0-9-]`)

// Create streams the data folder into a zip written to a temp file and moved into place atomically. A file that
// vanishes mid-backup fails this backup with an error; it never crashes the process.
func (m *BackupManager) Create(reason string) (BackupInfo, error) {
	files, err := m.collect(m.opts.DataDir, "", nil)
	if err != nil {
		return BackupInfo{}, err
	}
	stamp := time.Now().UTC().Format("20060102T150405")
	safe := unsafeReason.ReplaceAllString(reason, "-")
	if len(safe) > 40 {
		safe = safe[:40]
	}
	name := fmt.Sprintf("%s-%s.zip", stamp, safe)
	for n := 1; exists(filepath.Join(m.opts.BackupDir, name)); n++ {
		name = fmt.Sprintf("%s-%s-%d.zip", stamp, safe, n)
	}
	if err := os.MkdirAll(m.opts.BackupDir, 0o755); err != nil {
		return BackupInfo{}, err
	}
	final := filepath.Join(m.opts.BackupDir, name)
	tmp := final + ".tmp"
	if err := writeZip(tmp, files); err != nil {
		_ = os.Remove(tmp)
		return BackupInfo{}, err
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return BackupInfo{}, err
	}
	if err := m.prune(); err != nil {
		return BackupInfo{}, err
	}
	return m.info(name)
}

func writeZip(dst string, files []entry) (err error) {
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := out.Close(); err == nil {
			err = cerr
		}
	}()
	zw := zip.NewWriter(out)
	for _, f := range files {
		if err := addFile(zw, f); err != nil {
			return err
		}
	}
	return zw.Close()
}

func addFile(zw *zip.Writer, f entry) error {
	src, err := os.Open(f.abs)
	if err != nil {
		return err
	}
	defer src.Close()
	st, err := src.Stat()
	if err != nil {
		return err
	}
	h, err := zip.FileInfoHeader(st)
	if err != nil {
		return err
	}
	h.Name = f.name
	h.Method = zip.Deflate
	w, err := zw.CreateHeader(h)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, src)
	return err
}

// CreateIfStale makes a backup only when the newest one is older than hours.
func (m *BackupManager) CreateIfStale(hours float64, reason string) (*BackupInfo, error) {
	list, err := m.List()
	if err != nil {
		return nil, err
	}
	if len(list) > 0 && time.Since(list[0].CreatedAt) < time.Duration(hours*float64(time.Hour)) {
		return nil, nil
	}
	b, err := m.Create(reason)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// List returns the backups, newest first.
func (m *BackupManager) List() ([]BackupInfo, error) {
	ents, err := os.ReadDir(m.opts.BackupDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []BackupInfo
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".zip") {
			if b, err := m.info(e.Name()); err == nil {
				out = append(out, b)
			}
		}
	}
	// Newest first. Names alone are not enough: two backups in the same second get a "-1" suffix, which sorts
	// as older, so ties on the timestamp are broken by the file's modification time.
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if sa, sb := stampOf(a.Name), stampOf(b.Name); sa != sb {
			return sa > sb
		}
		if !a.mod.Equal(b.mod) {
			return a.mod.After(b.mod)
		}
		return a.Name > b.Name
	})
	return out, nil
}

// ReadFromLatest returns the raw bytes of rel from the newest backup that contains it.
func (m *BackupManager) ReadFromLatest(rel string) ([]byte, bool) {
	want := filepath.ToSlash(rel)
	list, _ := m.List()
	for _, b := range list {
		zr, err := zip.OpenReader(b.File)
		if err != nil {
			continue
		}
		for _, f := range zr.File {
			if f.Name != want {
				continue
			}
			rc, err := f.Open()
			if err == nil {
				data, rerr := io.ReadAll(rc)
				rc.Close()
				if rerr == nil {
					zr.Close()
					return data, true
				}
			}
		}
		zr.Close()
	}
	return nil, false
}

// Restore writes a whole backup over the data dir (the caller should snapshot first).
func (m *BackupManager) Restore(name string) error {
	zr, err := zip.OpenReader(filepath.Join(m.opts.BackupDir, filepath.Base(name)))
	if err != nil {
		return err
	}
	defer zr.Close()
	root, err := filepath.Abs(m.opts.DataDir)
	if err != nil {
		return err
	}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		target := filepath.Join(root, filepath.FromSlash(f.Name))
		if !strings.HasPrefix(target, root+string(filepath.Separator)) {
			continue // zip-slip guard
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return err
		}
		if err := AtomicWriteFile(target, data); err != nil {
			return err
		}
	}
	return nil
}

func (m *BackupManager) prune() error {
	all, err := m.List()
	if err != nil {
		return err
	}
	for i := m.keep; i < len(all); i++ {
		if err := os.Remove(all[i].File); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

var nameRe = regexp.MustCompile(`^(\d{4})(\d{2})(\d{2})T(\d{2})(\d{2})(\d{2})-(.*?)(?:-\d+)?\.zip$`)

// stampOf is the leading timestamp of a backup name ("20261121T123456").
func stampOf(name string) string {
	if len(name) >= 15 {
		return name[:15]
	}
	return name
}

func (m *BackupManager) info(name string) (BackupInfo, error) {
	file := filepath.Join(m.opts.BackupDir, name)
	st, err := os.Stat(file)
	if err != nil {
		return BackupInfo{}, err
	}
	created, reason := st.ModTime(), ""
	if g := nameRe.FindStringSubmatch(name); g != nil {
		if t, err := time.Parse("20060102T150405", g[1]+g[2]+g[3]+"T"+g[4]+g[5]+g[6]); err == nil {
			created = t
		}
		reason = g[7]
	}
	return BackupInfo{Name: name, File: file, CreatedAt: created, Reason: reason, Size: st.Size(), mod: st.ModTime()}, nil
}

func (m *BackupManager) collect(abs, rel string, out []entry) ([]entry, error) {
	ents, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}
	for _, e := range ents {
		if rel == "" && m.exclude[e.Name()] {
			continue
		}
		if strings.HasSuffix(e.Name(), ".tmp") {
			continue
		}
		childRel := e.Name()
		if rel != "" {
			childRel = path.Join(rel, e.Name())
		}
		childAbs := filepath.Join(abs, e.Name())
		switch {
		case e.IsDir():
			if out, err = m.collect(childAbs, childRel, out); err != nil {
				return nil, err
			}
		case e.Type().IsRegular():
			out = append(out, entry{abs: childAbs, name: childRel})
		}
	}
	return out, nil
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return !errors.Is(err, os.ErrNotExist)
}
