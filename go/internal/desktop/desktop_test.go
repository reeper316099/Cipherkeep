package desktop

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestDefaultDataDirFollowsTheOverrideThenOSConventions(t *testing.T) {
	abs, _ := filepath.Abs("/x/y")
	cases := []struct {
		env  map[string]string
		goos string
		home string
		want string
	}{
		{map[string]string{DataDirEnv: "/x/y"}, "linux", "/home/a", abs},
		{nil, "darwin", "/Users/a", filepath.Join("/Users/a", "Library", "Application Support", "Cipherkeep")},
		{nil, "linux", "/home/a", filepath.Join("/home/a", ".config", "Cipherkeep")},
		{map[string]string{"XDG_CONFIG_HOME": "/cfg"}, "linux", "/home/a", filepath.Join("/cfg", "Cipherkeep")},
		{map[string]string{"APPDATA": `C:\Users\a\AppData\Roaming`}, "windows", `C:\Users\a`, `C:\Users\a\AppData\Roaming\Cipherkeep`},
		{nil, "windows", `C:\Users\a`, `C:\Users\a\AppData\Roaming\Cipherkeep`},
	}
	for _, c := range cases {
		if got := DefaultDataDir(env(c.env), c.goos, c.home); got != c.want {
			t.Errorf("%s %v: got %q, want %q", c.goos, c.env, got, c.want)
		}
	}
}

func TestPointerIsFollowedOnlyWhenItLeadsSomewhereReal(t *testing.T) {
	def := t.TempDir()
	if ResolveDataDir(def) != def {
		t.Fatal("no pointer: the default folder")
	}
	moved := t.TempDir()
	if err := WritePointer(def, moved); err != nil {
		t.Fatal(err)
	}
	if ResolveDataDir(def) != moved {
		t.Fatal("pointer to an existing folder is followed")
	}
	_ = WritePointer(def, filepath.Join(moved, "gone"))
	if ResolveDataDir(def) != def {
		t.Fatal("a pointer to a missing folder is ignored")
	}
	_ = WritePointer(def, "relative/path")
	if ResolveDataDir(def) != def {
		t.Fatal("a relative pointer is ignored")
	}
}

func TestOnlyWebAndMailLinksLeaveTheApp(t *testing.T) {
	for _, ok := range []string{"https://example.com/a?b", "http://example.com", "mailto:a@example.com"} {
		if !IsSafeExternalURL(ok) {
			t.Errorf("%s should be allowed", ok)
		}
	}
	for _, bad := range []string{"", "file:///etc/passwd", "javascript:alert(1)", "https://user:pw@example.com",
		"ck://attachments/x", "smb://host/share", "https://" + strings.Repeat("a", 2100), "vscode://x"} {
		if IsSafeExternalURL(bad) {
			t.Errorf("%.40s should be refused", bad)
		}
	}
}

func TestNewDataFolderRules(t *testing.T) {
	cur := t.TempDir()
	other := t.TempDir()
	if err := ValidateNewDataDir(other, cur); err != nil {
		t.Fatalf("an empty folder is fine: %v", err)
	}
	if err := ValidateNewDataDir(filepath.Join(other, "new"), cur); err != nil {
		t.Fatalf("a missing folder is fine: %v", err)
	}
	if ValidateNewDataDir(cur, cur) == nil || ValidateNewDataDir("relative", cur) == nil {
		t.Fatal("the same folder and relative paths are refused")
	}
	if ValidateNewDataDir(filepath.Join(cur, "sub"), cur) == nil || ValidateNewDataDir(filepath.Dir(cur), cur) == nil {
		t.Fatal("nested folders are refused both ways")
	}
	_ = os.WriteFile(filepath.Join(other, "random.txt"), []byte("x"), 0o644)
	if ValidateNewDataDir(other, cur) == nil {
		t.Fatal("a folder with other files is refused")
	}
	_ = os.WriteFile(filepath.Join(other, "meta.json"), []byte("{}"), 0o644)
	if err := ValidateNewDataDir(other, cur); err != nil {
		t.Fatalf("an existing library is fine: %v", err)
	}
	if ValidateNewDataDir(filepath.Join(other, "random.txt"), cur) == nil {
		t.Fatal("a file is refused")
	}
}

func TestCopyDataLeavesOutTheLockAndShellFiles(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	for _, f := range []string{"meta.json", ".lock", "data/notes/a.md", SessionDir + "/window-state.json"} {
		p := filepath.Join(src, f)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte(f), 0o644)
	}
	if err := CopyData(src, dst); err != nil {
		t.Fatal(err)
	}
	for f, want := range map[string]bool{"meta.json": true, "data/notes/a.md": true, ".lock": false, SessionDir: false} {
		if _, err := os.Stat(filepath.Join(dst, f)); (err == nil) != want {
			t.Errorf("%s copied = %v, want %v", f, err == nil, want)
		}
	}
}

func ip(v int) *int { return &v }

func TestWindowBoundsAreRestoredOnlyOnAConnectedDisplay(t *testing.T) {
	displays := []Area{{0, 0, 1440, 900}}
	if b := FitBounds(nil, displays, 1280, 820); b.Width != 1280 || b.X != nil {
		t.Fatal("nothing saved: the default size")
	}
	saved := &Bounds{X: ip(100), Y: ip(50), Width: 1000, Height: 700}
	if b := FitBounds(saved, displays, 1280, 820); *b.X != 100 {
		t.Fatal("visible: restored")
	}
	off := &Bounds{X: ip(3000), Y: ip(50), Width: 1000, Height: 700, Maximized: true}
	if b := FitBounds(off, displays, 1280, 820); b.X != nil || !b.Maximized {
		t.Fatal("off screen: default size, maximised state kept")
	}
	if b := FitBounds(&Bounds{Width: 100, Height: 100}, displays, 1280, 820); b.Width != 1280 {
		t.Fatal("too small: default")
	}
	def := t.TempDir()
	SaveBounds(def, *saved)
	if got := LoadBounds(def); got == nil || *got.X != 100 || got.Height != 700 {
		t.Fatalf("round trip: %+v", got)
	}
	_ = runtime.GOOS
}
