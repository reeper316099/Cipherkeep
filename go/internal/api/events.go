package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/reeper316099/Cipherkeep/go/internal/library"
)

// pingEvery keeps idle event streams open through proxies and web views.
var pingEvery = 25 * time.Second

// eventData is what the TypeScript server sends for each kind of event.
func eventData(e library.Event) any {
	if e.Kind != "change" {
		return e.Data
	}
	m := map[string]any{"collection": e.Collection, "kind": e.Change}
	if e.ID != "" {
		m["id"] = e.ID
	}
	return m
}

// serveEvents streams live events (Server-Sent Events) until the client goes away or the library closes (locking).
func serveEvents(w http.ResponseWriter, r *http.Request, lib *library.Library) {
	ch, stop := lib.Subscribe()
	defer stop()
	h := w.Header()
	h.Set("content-type", "text/event-stream")
	h.Set("cache-control", "no-store")
	h.Set("connection", "keep-alive")
	w.WriteHeader(200)
	flush := func() {
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
	send := func(event string, data any) bool {
		b, err := marshal(data)
		if err != nil {
			return true
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b); err != nil {
			return false
		}
		flush()
		return true
	}
	if !send("hello", map[string]any{"ok": true}) {
		return
	}
	ping := time.NewTicker(pingEvery)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case e, open := <-ch:
			if !open {
				return // the library is locking or closing
			}
			if !send(e.Kind, eventData(e)) {
				return
			}
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flush()
		}
	}
}
