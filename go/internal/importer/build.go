package importer

import (
	"crypto/rand"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/reeper316099/Cipherkeep/go/internal/apperr"
	"github.com/reeper316099/Cipherkeep/go/internal/library"
)

var notionFile = regexp.MustCompile(`(?i)\s[0-9a-f]{32}(?:_all)?\.(?:md|csv)$`)

// DetectKind decides what kind of source this is: notion, markdown, csv or json-backup.
func DetectKind(vfs *VirtualFS) (string, error) {
	paths := vfs.Paths()
	has := func(ext string) bool {
		for _, p := range paths {
			if strings.HasSuffix(strings.ToLower(p), ext) {
				return true
			}
		}
		return false
	}
	for _, p := range paths {
		if notionFile.MatchString(p) {
			return "notion", nil
		}
	}
	switch {
	case has(".md"):
		return "markdown", nil
	case has(".csv"):
		return "csv", nil
	case has(".json"):
		return "json-backup", nil
	}
	return "", apperr.New(400, "BAD_SOURCE", "Nothing importable found (expected a Notion export, CSV, Markdown folder or JSON backup)")
}

// BuildOptions choose how a source is planned.
type BuildOptions struct {
	SourceName string
	Collection string
	Mapping    map[string]string
}

// BuildPlan detects the kind of source and plans its import. It never writes.
func BuildPlan(lib *library.Library, vfs *VirtualFS, opts BuildOptions) (*Plan, error) {
	lookup := ExistingLookup(lib)
	kind, err := DetectKind(vfs)
	if err != nil {
		return nil, err
	}
	switch kind {
	case "notion":
		return PlanNotion(vfs, NotionOptions{SourceName: opts.SourceName, Lookup: lookup, Mapping: opts.Mapping}), nil
	case "markdown":
		return PlanMarkdownFolder(vfs, opts.SourceName), nil
	case "csv":
		return PlanCSV(vfs, CSVOptions{SourceName: opts.SourceName, Collection: opts.Collection, Lookup: lookup, Mapping: opts.Mapping})
	}
	return PlanJSONBackup(vfs, opts.SourceName)
}

// Preview is a plan waiting to be committed, with the source it was made from.
type Preview struct {
	Plan    *Plan
	VFS     *VirtualFS
	Zip     []byte
	ZipName string
	expires time.Time
}

// PreviewStore holds previews between "show me what would happen" and "do it". Previews never write anything and
// expire after 30 minutes.
type PreviewStore struct {
	mu sync.Mutex
	m  map[string]*Preview
}

// NewPreviewStore creates an empty store.
func NewPreviewStore() *PreviewStore { return &PreviewStore{m: map[string]*Preview{}} }

func uuid() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// Put stores a preview and returns its id. Expired previews are dropped on the way.
func (s *PreviewStore) Put(plan *Plan, vfs *VirtualFS, zip []byte, zipName string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := Now()
	for k, v := range s.m {
		if v.expires.Before(now) {
			delete(s.m, k)
		}
	}
	id := uuid()
	s.m[id] = &Preview{Plan: plan, VFS: vfs, Zip: zip, ZipName: zipName, expires: now.Add(30 * time.Minute)}
	return id
}

// Get returns a live preview or a 404 PREVIEW_EXPIRED error.
func (s *PreviewStore) Get(id string) (*Preview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[id]
	if !ok || v.expires.Before(Now()) {
		return nil, apperr.New(404, "PREVIEW_EXPIRED", "Preview not found or expired; run the preview again")
	}
	return v, nil
}

// Drop forgets a preview.
func (s *PreviewStore) Drop(id string) {
	s.mu.Lock()
	delete(s.m, id)
	s.mu.Unlock()
}
