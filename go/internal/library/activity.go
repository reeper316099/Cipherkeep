// Package library ties the pieces together into one open library: the store, the repositories, search, links and
// the services that compute over them (activity log, study, dashboard). It is the Go counterpart of the TypeScript
// server's Context.
package library

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/reeper316099/Cipherkeep/go/internal/jsonstore"
)

// ActivityEvent is one thing that happened: what kind, to which item. It never records content.
type ActivityEvent struct {
	Ts         string `json:"ts"`
	Type       string `json:"type"` // create, update, delete, study, lab, import
	Collection string `json:"collection,omitempty"`
	ID         string `json:"id,omitempty"`
}

// ActivityLog is an append-only monthly JSONL log.
type ActivityLog struct{ store *jsonstore.Store }

// NewActivityLog creates the log.
func NewActivityLog(s *jsonstore.Store) *ActivityLog { return &ActivityLog{store: s} }

func (a *ActivityLog) rel(ts string) string { return "activity/" + ts[:7] + ".jsonl" }

// Record appends an event stamped with the current time.
func (a *ActivityLog) Record(typ, collection, id string) error {
	return a.RecordAt(time.Now(), typ, collection, id)
}

// RecordAt appends an event with the given time (tests use this).
func (a *ActivityLog) RecordAt(t time.Time, typ, collection, id string) error {
	ts := t.UTC().Format("2006-01-02T15:04:05.000Z")
	b, err := json.Marshal(ActivityEvent{Ts: ts, Type: typ, Collection: collection, ID: id})
	if err != nil {
		return err
	}
	line, err := a.store.Codec.EncodeLine(string(b))
	if err != nil {
		return err
	}
	rel := a.rel(ts)
	defer a.store.Lock(rel)()
	return appendLine(a.store, rel, line)
}

func appendLine(s *jsonstore.Store, rel, line string) error {
	abs, err := s.Abs(rel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(abs, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.WriteString(line + "\n")
	cerr := f.Close()
	if werr != nil {
		return werr
	}
	return cerr
}

// readLines returns the decoded, non-empty lines of a JSONL file (none when it does not exist).
func readLines(s *jsonstore.Store, rel string) []string {
	abs, err := s.Abs(rel)
	if err != nil {
		return nil
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if l == "" {
			continue
		}
		if plain, err := s.Codec.DecodeLine(l); err == nil {
			out = append(out, plain)
		}
	}
	return out
}

// Month returns one month's events ("2026-03"). A torn last line is ignored.
func (a *ActivityLog) Month(yyyyMm string) []ActivityEvent {
	if len(yyyyMm) != 7 || yyyyMm[4] != '-' {
		return nil
	}
	var out []ActivityEvent
	for _, l := range readLines(a.store, "activity/"+yyyyMm+".jsonl") {
		var e ActivityEvent
		if json.Unmarshal([]byte(l), &e) == nil {
			out = append(out, e)
		}
	}
	return out
}

// Range returns the events whose UTC month falls between the two YYYY-MM-DD days (inclusive); callers bucket by their
// own timezone.
func (a *ActivityLog) Range(fromDay, toDay string) []ActivityEvent {
	y, m := atoi(fromDay[:4]), atoi(fromDay[5:7])
	ty, tm := atoi(toDay[:4]), atoi(toDay[5:7])
	var out []ActivityEvent
	for y < ty || (y == ty && m <= tm) {
		out = append(out, a.Month(fmt.Sprintf("%04d-%02d", y, m))...)
		if m++; m > 12 {
			m, y = 1, y+1
		}
	}
	return out
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }
