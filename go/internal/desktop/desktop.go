// Package desktop is the platform-independent part of the desktop shell: where the data lives, which links may leave
// the app, where a window may be restored, and whether a folder may become the new data folder. It is the Go port of
// apps/desktop/src/security.ts and the data-folder logic in apps/desktop/src/main.ts; the Wails app in go/app uses it.
package desktop

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

// AppName is the folder name under the OS app-data directory.
const AppName = "Cipherkeep"

// DataDirEnv overrides the data folder (portable use, tests).
const DataDirEnv = "CIPHERKEEP_DATA_DIR"

// SessionDir holds what the shell itself writes (window state, the data-folder pointer, the web view's profile on
// Windows) inside the default data folder. Backups leave it out.
const SessionDir = ".desktop-session"

// DefaultDataDir is the standard per-OS app-data folder, or the folder named by CIPHERKEEP_DATA_DIR:
// macOS ~/Library/Application Support/Cipherkeep, Windows %APPDATA%\Cipherkeep, Linux $XDG_CONFIG_HOME/Cipherkeep
// (or ~/.config/Cipherkeep). The same rules as the Node app, so both open the same library.
func DefaultDataDir(getenv func(string) string, goos, home string) string {
	if v := getenv(DataDirEnv); v != "" {
		if abs, err := filepath.Abs(v); err == nil {
			return abs
		}
		return v
	}
	switch goos {
	case "windows":
		base := getenv("APPDATA")
		if base == "" {
			base = home + `\AppData\Roaming`
		}
		return strings.TrimRight(base, `\`) + `\` + AppName
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", AppName)
	}
	base := getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, AppName)
}

func pointerFile(defaultDir string) string {
	return filepath.Join(defaultDir, SessionDir, "location.json")
}

// ResolveDataDir follows the pointer a "move data folder" left behind, if it still points at an existing folder.
func ResolveDataDir(defaultDir string) string {
	raw, err := os.ReadFile(pointerFile(defaultDir))
	if err != nil {
		return defaultDir
	}
	var p struct {
		DataDir string `json:"dataDir"`
	}
	if json.Unmarshal(raw, &p) != nil || p.DataDir == "" || !filepath.IsAbs(p.DataDir) {
		return defaultDir
	}
	if st, err := os.Stat(p.DataDir); err != nil || !st.IsDir() {
		return defaultDir
	}
	return p.DataDir
}

// WritePointer records the folder to open on the next start.
func WritePointer(defaultDir, target string) error {
	f := pointerFile(defaultDir)
	if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
		return err
	}
	b, _ := json.Marshal(map[string]string{"dataDir": target})
	return os.WriteFile(f, b, 0o644)
}

// IsSafeExternalURL allows only web and mail links, never with embedded credentials.
func IsSafeExternalURL(raw string) bool {
	if raw == "" || len(utf16.Encode([]rune(raw))) > 2048 {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return u.Host != ""
	case "mailto":
		return true
	}
	return false
}

// IsRevealablePath accepts an absolute path that can be shown in the file manager (never opened or run).
func IsRevealablePath(p string) bool {
	return p != "" && len(p) < 4096 && !strings.ContainsRune(p, 0) && filepath.IsAbs(p)
}

func inside(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) &&
		!filepath.IsAbs(rel)
}

// ValidateNewDataDir checks a new data folder: absolute, different from the current one, not nested with it, and
// either missing, empty, or already holding Cipherkeep data (meta.json). It returns the reason it is refused.
func ValidateNewDataDir(target, current string) error {
	if !IsRevealablePath(target) {
		return errors.New("Choose an absolute folder path.")
	}
	t, c := filepath.Clean(target), filepath.Clean(current)
	if t == c {
		return errors.New("That is already the data folder.")
	}
	if inside(t, c) || inside(c, t) {
		return errors.New("The new folder cannot be inside the current one (or contain it).")
	}
	st, err := os.Stat(t)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return errors.New("That folder cannot be read.")
	}
	if !st.IsDir() {
		return errors.New("That path is a file, not a folder.")
	}
	ents, err := os.ReadDir(t)
	if err != nil {
		return errors.New("That folder cannot be read.")
	}
	n := 0
	for _, e := range ents {
		if e.Name() == "meta.json" {
			return nil
		}
		if e.Name() != ".DS_Store" {
			n++
		}
	}
	if n == 0 {
		return nil
	}
	return errors.New("That folder is not empty and does not look like Cipherkeep data.")
}

// CopyData copies a library to a new folder, leaving out the instance lock and the shell's own files.
func CopyData(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if rel == ".lock" || rel == SessionDir || rel == ".electron-session" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
}

// Bounds is a saved window placement.
type Bounds struct {
	X         *int `json:"x,omitempty"`
	Y         *int `json:"y,omitempty"`
	Width     int  `json:"width"`
	Height    int  `json:"height"`
	Maximized bool `json:"maximized,omitempty"`
}

// Area is a display's work area.
type Area struct{ X, Y, Width, Height int }

// FitBounds restores saved bounds only if enough of the window is still on a connected display.
func FitBounds(saved *Bounds, displays []Area, width, height int) Bounds {
	dflt := Bounds{Width: width, Height: height}
	if saved == nil || saved.Width < 600 || saved.Height < 400 {
		return dflt
	}
	if saved.X == nil || saved.Y == nil {
		dflt.Maximized = saved.Maximized
		return dflt
	}
	for _, d := range displays {
		w := min(*saved.X+saved.Width, d.X+d.Width) - max(*saved.X, d.X)
		h := min(*saved.Y+saved.Height, d.Y+d.Height) - max(*saved.Y, d.Y)
		if w >= 200 && h >= 120 {
			return *saved
		}
	}
	dflt.Maximized = saved.Maximized
	return dflt
}

func stateFile(defaultDir string) string {
	return filepath.Join(defaultDir, SessionDir, "window-state.json")
}

// LoadBounds reads the saved window placement.
func LoadBounds(defaultDir string) *Bounds {
	raw, err := os.ReadFile(stateFile(defaultDir))
	if err != nil {
		return nil
	}
	var b Bounds
	if json.Unmarshal(raw, &b) != nil {
		return nil
	}
	return &b
}

// SaveBounds writes the window placement (best effort).
func SaveBounds(defaultDir string, b Bounds) {
	f := stateFile(defaultDir)
	if os.MkdirAll(filepath.Dir(f), 0o755) != nil {
		return
	}
	raw, _ := json.Marshal(b)
	_ = os.WriteFile(f, raw, 0o644)
}
