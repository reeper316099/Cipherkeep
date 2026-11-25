package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
	"github.com/wailsapp/wails/v2/pkg/options"
	wr "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/reeper316099/Cipherkeep/go/internal/api"
	"github.com/reeper316099/Cipherkeep/go/internal/desktop"
	"github.com/reeper316099/Cipherkeep/go/internal/library"
)

// App is the shell. Its exported methods are what the UI can call (window.go.main.App.*): the same small surface the
// Electron preload offered (window.cipherkeep), and nothing more.
type App struct {
	defaultDir string
	dataDir    string
	srv        *api.Server
	start      desktop.Bounds

	mu         sync.Mutex
	ctx        context.Context
	forwarding map[*library.Library]bool
}

// Boot is what the UI reads once at start.
type Boot struct {
	Platform string `json:"platform"`
	Version  string `json:"version"`
	DataDir  string `json:"dataDir"`
}

// MoveResult answers SetDataFolder.
type MoveResult struct {
	OK     bool   `json:"ok"`
	Reason string `json:"reason,omitempty"`
}

func (a *App) startup(ctx context.Context) {
	a.mu.Lock()
	a.ctx = ctx
	a.mu.Unlock()
	if a.start.X != nil && a.start.Y != nil && a.onScreen(ctx) {
		wr.WindowSetPosition(ctx, *a.start.X, *a.start.Y)
	}
	if a.srv != nil {
		if lib := a.srv.Library(); lib != nil {
			a.onOpen(lib)
		}
	}
}

// onScreen checks the saved size still fits a connected screen (Wails reports sizes, not positions).
func (a *App) onScreen(ctx context.Context) bool {
	screens, err := wr.ScreenGetAll(ctx)
	if err != nil {
		return false
	}
	for _, s := range screens {
		if *a.start.X+200 <= s.Size.Width && *a.start.Y+120 <= s.Size.Height && *a.start.X > -a.start.Width+200 && *a.start.Y >= 0 {
			return true
		}
	}
	return false
}

func (a *App) beforeClose(ctx context.Context) bool {
	b := desktop.Bounds{Maximized: wr.WindowIsMaximised(ctx)}
	if prev := desktop.LoadBounds(a.defaultDir); b.Maximized && prev != nil {
		b.X, b.Y, b.Width, b.Height = prev.X, prev.Y, prev.Width, prev.Height
	} else {
		x, y := wr.WindowGetPosition(ctx)
		b.X, b.Y = &x, &y
		b.Width, b.Height = wr.WindowGetSize(ctx)
	}
	if b.Width < 600 || b.Height < 400 {
		b.Width, b.Height = 1280, 820
	}
	desktop.SaveBounds(a.defaultDir, b)
	return false
}

func (a *App) shutdown(context.Context) {}

func (a *App) secondInstance(options.SecondInstanceData) {
	a.mu.Lock()
	ctx := a.ctx
	a.mu.Unlock()
	if ctx != nil {
		wr.WindowUnminimise(ctx)
		wr.WindowShow(ctx)
	}
}

// onOpen forwards the library's live events to the window as Wails events ("ck:event"), the in-process stand-in for
// the server-sent event stream. The loop ends when the library is locked or closed.
func (a *App) onOpen(lib *library.Library) {
	a.mu.Lock()
	ctx := a.ctx
	if ctx == nil || a.forwarding[lib] {
		a.mu.Unlock()
		return
	}
	if a.forwarding == nil {
		a.forwarding = map[*library.Library]bool{}
	}
	a.forwarding[lib] = true
	a.mu.Unlock()
	ch, stop := lib.Subscribe()
	go func() {
		defer stop()
		for e := range ch {
			wr.EventsEmit(ctx, "ck:event", e.Kind)
		}
		a.mu.Lock()
		delete(a.forwarding, lib)
		a.mu.Unlock()
	}()
}

func (a *App) send(action string) {
	a.mu.Lock()
	ctx := a.ctx
	a.mu.Unlock()
	if ctx != nil {
		wr.WindowShow(ctx)
		wr.EventsEmit(ctx, "ck:menu", action)
	}
}

// ---- what the UI may call ----

// Boot returns the platform, version and data folder.
func (a *App) Boot() Boot { return Boot{Platform: runtime.GOOS, Version: version, DataDir: a.dataDir} }

// OpenExternal opens a web or mail link in the default browser or mail app. Anything else is refused.
func (a *App) OpenExternal(url string) bool {
	if !desktop.IsSafeExternalURL(url) || a.ctx == nil {
		return false
	}
	wr.BrowserOpenURL(a.ctx, url)
	return true
}

// RevealInFolder shows a file in the file manager. It never opens or runs it.
func (a *App) RevealInFolder(p string) bool {
	if !desktop.IsRevealablePath(p) {
		return false
	}
	if _, err := os.Stat(p); err != nil {
		return false
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", "-R", p)
	case "windows":
		cmd = exec.Command("explorer", "/select,"+p)
	default:
		cmd = exec.Command("xdg-open", filepath.Dir(p))
	}
	return cmd.Start() == nil
}

// OpenDataFolder opens the data folder in the file manager.
func (a *App) OpenDataFolder() {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", a.dataDir)
	case "windows":
		cmd = exec.Command("explorer", a.dataDir)
	default:
		cmd = exec.Command("xdg-open", a.dataDir)
	}
	_ = cmd.Start()
}

// PickFolder asks for a folder; "" when cancelled.
func (a *App) PickFolder() string {
	if a.ctx == nil {
		return ""
	}
	p, err := wr.OpenDirectoryDialog(a.ctx, wr.OpenDialogOptions{Title: "Choose a data folder", CanCreateDirectories: true})
	if err != nil {
		return ""
	}
	return p
}

// SetDataFolder validates a new data folder, optionally copies the current data there, then restarts the app on it.
func (a *App) SetDataFolder(target string, copyData bool) MoveResult {
	if err := desktop.ValidateNewDataDir(target, a.dataDir); err != nil {
		return MoveResult{Reason: err.Error()}
	}
	t := filepath.Clean(target)
	fail := MoveResult{Reason: "Could not move the data. Nothing was changed."}
	if err := os.MkdirAll(t, 0o755); err != nil {
		return fail
	}
	if _, err := os.Stat(filepath.Join(t, "meta.json")); copyData && os.IsNotExist(err) {
		if a.srv != nil {
			_ = a.srv.Close() // release the lock and finish writes before copying
		}
		if err := desktop.CopyData(a.dataDir, t); err != nil {
			return fail
		}
	}
	if err := desktop.WritePointer(a.defaultDir, t); err != nil {
		return fail
	}
	if exe, err := os.Executable(); err == nil {
		cmd := exec.Command(exe, os.Args[1:]...)
		cmd.Env = append(os.Environ(), "CIPHERKEEP_WAIT_PID="+itoa(os.Getpid()))
		_ = cmd.Start()
	}
	if a.ctx != nil {
		wr.Quit(a.ctx)
	}
	return MoveResult{OK: true}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

// ---- menu (the same entries and shortcuts as the Electron app) ----

var goMenu = [][3]string{
	{"Home", "/", "1"}, {"Review", "/study", "2"}, {"Commands", "/commands", "3"}, {"Concepts", "/concepts", "4"},
	{"Labs", "/labs", "5"}, {"Projects", "/projects", "6"}, {"Learning Log", "/log", "7"},
	{"Notes & Guides", "/notes", "8"}, {"Snippets", "/snippets", "9"}, {"Knowledge Graph", "/graph", ""},
	{"Open Questions", "/questions", ""},
}

func (a *App) menu() *menu.Menu {
	isMac := runtime.GOOS == "darwin"
	m := menu.NewMenu()
	on := func(action string) func(*menu.CallbackData) { return func(*menu.CallbackData) { a.send(action) } }
	if isMac {
		// The standard application menu (About, Hide, Quit). Wails v2 cannot add items to it, so Settings is in File.
		m.Append(menu.AppMenu())
	}
	file := m.AddSubmenu("File")
	file.AddText("New Command", keys.CmdOrCtrl("n"), on("new-command"))
	file.AddText("Import / Export…", keys.Combo("i", keys.CmdOrCtrlKey, keys.ShiftKey), on("nav:/import"))
	file.AddSeparator()
	file.AddText("Settings…", keys.CmdOrCtrl(","), on("nav:/settings"))
	if !isMac {
		file.AddSeparator()
		file.AddText("Quit", keys.CmdOrCtrl("q"), func(*menu.CallbackData) {
			if a.ctx != nil {
				wr.Quit(a.ctx)
			}
		})
	}
	m.Append(menu.EditMenu())
	view := m.AddSubmenu("View")
	// Ctrl/⌘+K is handled by the page itself (registering it here too would toggle the palette twice).
	view.AddText("Command Palette (Ctrl/⌘ + K)", nil, on("palette"))
	g := m.AddSubmenu("Go")
	for _, item := range goMenu {
		var acc *keys.Accelerator
		if item[2] != "" {
			acc = keys.CmdOrCtrl(item[2])
		}
		g.AddText(item[0], acc, on("nav:"+item[1]))
	}
	if isMac {
		m.Append(menu.WindowMenu())
	}
	help := m.AddSubmenu("Help")
	help.AddText("Keyboard Shortcuts", nil, on("help"))
	help.AddText("Show Data Folder", nil, func(*menu.CallbackData) { a.OpenDataFolder() })
	return m
}
