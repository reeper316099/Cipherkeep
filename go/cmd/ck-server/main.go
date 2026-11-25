// Command ck-server serves the Go API on a loopback port, guarded like the Node server (a per-launch bearer token,
// loopback Host headers only). It is for development and for running the TypeScript API tests against the Go
// version; the desktop app uses the same handler in process, with no port at all.
//
// On start it prints one JSON line, {"url": ..., "token": ...}, to stdout. It shuts down cleanly when stdin closes
// (so a parent process can stop it on every platform) or on SIGINT/SIGTERM.
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/reeper316099/Cipherkeep/go/internal/api"
	"github.com/reeper316099/Cipherkeep/go/internal/vault"
)

func main() {
	data := flag.String("data", "", "data folder (required)")
	port := flag.Int("port", 0, "port on 127.0.0.1 (0: any free port)")
	token := flag.String("token", "", "bearer token (default: random)")
	watch := flag.Bool("watch", true, "reload hand edits")
	static := flag.String("static", "", "built web UI to serve at /")
	kdfN := flag.Int("kdf-n", 0, "scrypt N for new vaults (tests lower it)")
	verbose := flag.Bool("log", false, "log one line per request (method, path, status)")
	launchBackup := flag.Bool("launch-backup", true, "take the background launch backup 3 s after opening (tests turn it off)")
	flag.Parse()
	if *data == "" {
		fmt.Fprintln(os.Stderr, "ck-server: -data is required")
		os.Exit(2)
	}
	if *token == "" {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			log.Fatal(err)
		}
		*token = base64.RawURLEncoding.EncodeToString(b)
	}
	o := api.Options{DataDir: *data, Token: *token, Watch: *watch}
	if !*launchBackup {
		o.LaunchBackupDelay = -1
	}
	if *static != "" {
		o.Static = os.DirFS(*static)
	}
	if *kdfN > 0 {
		o.KDF = vault.KDF{Algo: "scrypt", N: *kdfN, R: 8, P: 1}
	}
	if *verbose {
		o.Logger = log.New(os.Stderr, "", log.LstdFlags)
	}
	srv, err := api.New(o)
	if err != nil {
		out, _ := json.Marshal(map[string]string{"error": err.Error()})
		fmt.Println(string(out))
		os.Exit(1)
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		_ = srv.Close()
		log.Fatal(err)
	}
	hs := &http.Server{Handler: srv, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = hs.Serve(ln) }()
	out, _ := json.Marshal(map[string]string{"url": "http://" + ln.Addr().String(), "token": *token})
	fmt.Println(string(out))

	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(io.Discard, os.Stdin); done <- struct{}{} }()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() { <-sig; done <- struct{}{} }()
	<-done

	// Finish requests in flight; open event streams never finish on their own, so they are cut after a moment.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = hs.Shutdown(ctx)
	_ = hs.Close()
	if err := srv.Close(); err != nil {
		log.Fatal(err)
	}
}
