// Command app is the Cipherkeep desktop app: the Go API (internal/api) and the React UI (apps/web) in one Wails
// window. The UI's fetch('/api/v1/...') calls are answered by the API handler inside the process, through the Wails
// asset server: there is no network port.
//
// Build with the Wails CLI from this folder (`wails build`), or `go build -tags desktop,production`.
package main

import (
	"embed"
	"fmt"
	"html"
	"io/fs"
	"log"
	"os"
	"runtime"
	"strconv"
	"testing/fstest"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"

	"github.com/reeper316099/Cipherkeep/go/internal/api"
	"github.com/reeper316099/Cipherkeep/go/internal/desktop"
	"github.com/reeper316099/Cipherkeep/go/internal/jsonstore"
)

// version is set at build time (-ldflags "-X main.version=...").
var version = "dev"

// frontend holds the built UI (frontend/dist, made by `npm run build:wails`) and a placeholder page used when the
// UI has not been built, so `go vet` and `go test` work without a frontend build.
//
//go:embed all:frontend
var frontend embed.FS

func uiAssets() fs.FS {
	if sub, err := fs.Sub(frontend, "frontend/dist"); err == nil {
		if _, err := fs.Stat(sub, "index.html"); err == nil {
			return sub
		}
	}
	sub, _ := fs.Sub(frontend, "frontend/placeholder")
	return sub
}

// errorPage is what the window shows when the library cannot be opened (for example, another copy has it open).
func errorPage(msg string) fs.FS {
	page := `<!doctype html><meta charset="utf-8"><title>Cipherkeep</title>` +
		`<body style="font:15px system-ui;background:#090c12;color:#e6e9ef;padding:48px;max-width:640px">` +
		`<h1 style="font-size:20px">Cipherkeep could not start</h1><p>` + html.EscapeString(msg) + `</p></body>`
	return fstest.MapFS{"index.html": {Data: []byte(page)}}
}

func main() {
	waitForParent()
	home, _ := os.UserHomeDir()
	defaultDir := desktop.DefaultDataDir(os.Getenv, runtime.GOOS, home)
	dataDir := desktop.ResolveDataDir(defaultDir)

	a := &App{defaultDir: defaultDir, dataDir: dataDir}
	o := api.Options{DataDir: dataDir, Watch: true, WatchInBackground: true, OnOpen: a.onOpen}
	// For scripted smoke tests only: one line per API request (method, path without the query, status). Never content.
	if p := os.Getenv("CIPHERKEEP_API_LOG"); p != "" {
		if f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
			defer f.Close()
			o.Logger = log.New(f, "", log.Lmicroseconds)
			a.log = o.Logger
		}
	}
	srv, err := api.New(o)
	assets := uiAssets()
	var handler = any(nil)
	if err != nil {
		msg := "Its local data could not be opened: " + err.Error()
		if jsonstore.IsCode(err, jsonstore.CodeLocked) {
			msg = "Cipherkeep is already using this data folder: " + dataDir + ". Close the other copy and try again."
		}
		assets = errorPage(msg)
	} else {
		a.srv = srv
		handler = srv
	}
	as := &assetserver.Options{Assets: assets}
	if h, ok := handler.(*api.Server); ok {
		as.Handler = h
	}

	saved := desktop.LoadBounds(defaultDir)
	b := desktop.FitBounds(saved, nil, 1280, 820)
	if saved != nil && saved.Width >= 600 && saved.Height >= 400 {
		b.Width, b.Height, b.Maximized = saved.Width, saved.Height, saved.Maximized
		b.X, b.Y = saved.X, saved.Y // checked against the screens once the window exists
	}
	a.start = b
	state := options.Normal
	if b.Maximized {
		state = options.Maximised
	}

	err = wails.Run(&options.App{
		Title:            "Cipherkeep",
		Width:            b.Width,
		Height:           b.Height,
		MinWidth:         760,
		MinHeight:        520,
		WindowStartState: state,
		StartHidden:      os.Getenv("CIPHERKEEP_START_HIDDEN") == "1", // for scripted smoke tests only
		BackgroundColour: &options.RGBA{R: 9, G: 12, B: 18, A: 255},
		AssetServer:      as,
		Menu:             a.menu(),
		OnStartup:        a.startup,
		OnBeforeClose:    a.beforeClose,
		OnShutdown:       a.shutdown,
		Bind:             []any{a},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               "io.github.reeper316099.cipherkeep",
			OnSecondInstanceLaunch: a.secondInstance,
		},
		Mac: &mac.Options{
			About: &mac.AboutInfo{Title: "Cipherkeep " + version, Message: "Offline, local-first notes and practice for cybersecurity learning. MIT licensed."},
		},
		Windows: &windows.Options{
			// The web view's own profile lives with the shell's files, not in the library.
			WebviewUserDataPath: defaultDir + string(os.PathSeparator) + desktop.SessionDir + string(os.PathSeparator) + "webview2",
		},
		Linux: &linux.Options{ProgramName: "Cipherkeep"},
	})
	if a.srv != nil {
		_ = a.srv.Close()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "cipherkeep:", err)
		os.Exit(1)
	}
}

// waitForParent lets a relaunched copy (after moving the data folder) wait until the old one has exited, so the
// single-instance lock and the library lock are free.
func waitForParent() {
	pid, err := strconv.Atoi(os.Getenv("CIPHERKEEP_WAIT_PID"))
	_ = os.Unsetenv("CIPHERKEEP_WAIT_PID")
	if err != nil || pid <= 0 {
		return
	}
	deadline := time.Now().Add(15 * time.Second)
	for processAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
}
