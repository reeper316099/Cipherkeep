package notionapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/reeper316099/Cipherkeep/go/internal/importer"
	"github.com/reeper316099/Cipherkeep/go/internal/library"
)

const token = "ntn_FAKE_test_token_0123456789abcdef"

func id(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }
func rt(t string, a Obj) []any {
	return []any{Obj{"plain_text": t, "annotations": a, "href": nil}}
}
func title(t string) Obj { return Obj{"type": "title", "title": rt(t, nil)} }

var (
	dsCmd, dsCon = id(1), id(2)
	sources      = []any{
		Obj{"object": "data_source", "id": dsCmd, "title": rt("Commands", nil), "parent": Obj{"database_id": id(11)},
			"properties": Obj{"Name": Obj{"type": "title"}, "Category": Obj{"type": "select"}, "Syntax": Obj{"type": "rich_text"}, "Explanation": Obj{"type": "rich_text"}}},
		Obj{"object": "data_source", "id": dsCon, "title": rt("Concepts", nil), "parent": Obj{"database_id": id(12)},
			"properties": Obj{"Name": Obj{"type": "title"}, "Related Commands": Obj{"type": "relation"}}},
	}
)

func cmd(n int, name, syntax string) Obj {
	return Obj{"object": "page", "id": id(n), "parent": Obj{"type": "data_source_id", "data_source_id": dsCmd, "database_id": id(11)},
		"properties": Obj{"Name": title(name), "Category": Obj{"type": "select", "select": Obj{"name": "File System"}},
			"Syntax": Obj{"type": "rich_text", "rich_text": rt(syntax, nil)}, "Explanation": Obj{"type": "rich_text", "rich_text": rt(name+" explained", nil)}}}
}

var pages = []any{
	cmd(21, "zlist", "zlist [-a] <dir>"),
	cmd(22, "zcopy", "zcopy <src> <dst>"),
	Obj{"object": "page", "id": id(23), "parent": Obj{"type": "data_source_id", "data_source_id": dsCon},
		"properties": Obj{"Name": title("Zebra Filesystems"), "Related Commands": Obj{"type": "relation", "relation": []any{Obj{"id": id(21)}}}}},
	Obj{"object": "page", "id": id(24), "parent": Obj{"type": "workspace"}, "properties": Obj{"title": title("Loose Guide")}},
	Obj{"object": "page", "id": id(25), "in_trash": true, "parent": Obj{"type": "workspace"}, "properties": Obj{"title": title("Trashed")}},
}

var blocks = map[string][]any{
	id(21): {
		Obj{"id": id(31), "type": "heading_2", "heading_2": Obj{"rich_text": rt("Usage", nil)}},
		Obj{"id": id(32), "type": "paragraph", "paragraph": Obj{"rich_text": rt("Lists a directory.", Obj{"bold": false})}},
		Obj{"id": id(33), "type": "bulleted_list_item", "has_children": true, "bulleted_list_item": Obj{"rich_text": rt("first", nil)}},
		Obj{"id": id(34), "type": "code", "code": Obj{"language": "bash", "rich_text": rt("zlist -a /tmp", nil)}},
		Obj{"id": id(35), "type": "image", "image": Obj{"type": "file", "file": Obj{"url": "https://s3.example/secret.png"}, "caption": []any{}}},
	},
	id(33): {Obj{"id": id(36), "type": "bulleted_list_item", "bulleted_list_item": Obj{"rich_text": rt("nested", nil)}}},
	id(24): {Obj{"id": id(900), "type": "paragraph", "paragraph": Obj{"rich_text": rt("Hello from the API.", nil)}}},
}

type recorded struct {
	url     string
	headers http.Header
}

type fake struct {
	mu    sync.Mutex
	calls []recorded
	limit bool // answer the first request with 429
	done  bool
}

func (f *fake) Do(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.calls = append(f.calls, recorded{r.URL.String(), r.Header.Clone()})
	first := !f.done
	f.done = true
	f.mu.Unlock()
	resp := func(code int, body any, h http.Header) (*http.Response, error) {
		b, _ := json.Marshal(body)
		if h == nil {
			h = http.Header{}
		}
		return &http.Response{StatusCode: code, Body: io.NopCloser(bytes.NewReader(b)), Header: h}, nil
	}
	if f.limit && first {
		return resp(429, Obj{}, http.Header{"Retry-After": {"0"}})
	}
	list := func(results []any) (*http.Response, error) {
		return resp(200, Obj{"results": results, "has_more": false, "next_cursor": nil}, nil)
	}
	if r.URL.Path == "/v1/search" {
		var body struct {
			Filter struct{ Value string }
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		if body.Filter.Value == "data_source" {
			return list(sources)
		}
		return list(pages)
	}
	if m := regexp.MustCompile(`^/v1/blocks/([^/]+)/children$`).FindStringSubmatch(r.URL.Path); m != nil {
		return list(blocks[m[1]])
	}
	return resp(404, Obj{}, nil)
}

func newClient(f *fake) *Client {
	return NewClient(token, ClientOptions{Do: f.Do, MinInterval: time.Nanosecond, Sleep: func(context.Context, time.Duration) error { return nil }})
}

func TestFetchBuildsAnExportTheNormalImporterReads(t *testing.T) {
	f := &fake{limit: true}
	got, err := FetchExport(context.Background(), newClient(f), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Stats.Databases != 2 || got.Stats.Pages != 4 || got.Stats.Images != 1 { // the trashed page is skipped
		t.Fatalf("stats: %+v", got.Stats)
	}
	lib, err := library.Open(library.Options{DataDir: t.TempDir(), LaunchBackupDelay: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	plan, err := importer.BuildPlan(lib, got.VFS, importer.BuildOptions{SourceName: "Notion"})
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	titles := []string{}
	for _, i := range plan.Items {
		counts[i.Collection]++
		titles = append(titles, i.Title)
	}
	if plan.Kind != "notion" || counts["commands"] != 2 || counts["concepts"] != 1 || !strings.Contains(strings.Join(titles, "|"), "Loose Guide") {
		t.Fatalf("plan: %s %v %v", plan.Kind, counts, titles)
	}
	rep, err := importer.Commit(lib, plan, importer.Options{Strategy: importer.StrategyUpdateEmptyOnly}, &importer.Source{VFS: got.VFS})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Created["commands"] != 2 || rep.Created["concepts"] != 1 {
		t.Fatalf("created %v", rep.Created)
	}
	var zlist map[string]any
	for _, e := range lib.Repos["commands"].All() {
		if e["name"] == "zlist" {
			zlist = e
		}
	}
	if zlist["category"] != "File System" || zlist["syntax"] != "zlist [-a] <dir>" {
		t.Fatalf("zlist: %v", zlist)
	}
	concept := lib.Repos["concepts"].All()[0]
	if ids := concept["relatedCommandIds"].([]any); len(ids) != 1 || ids[0] != zlist["id"] {
		t.Fatalf("relation: %v", concept["relatedCommandIds"])
	}

	// Only api.notion.com, always the pinned version, always the token as a bearer header, and it backed off on 429.
	if len(f.calls) <= 5 {
		t.Fatalf("only %d calls", len(f.calls))
	}
	for _, c := range f.calls {
		if !strings.HasPrefix(c.url, "https://api.notion.com/v1/") || c.headers.Get("Notion-Version") != "2026-03-11" ||
			c.headers.Get("Authorization") != "Bearer "+token || strings.Contains(c.url, token) {
			t.Fatalf("bad call: %+v", c)
		}
	}
	// The token is nowhere on disk (data, backups, import records).
	_ = filepath.WalkDir(lib.Dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if b, _ := os.ReadFile(p); bytes.Contains(b, []byte(token)) {
				t.Errorf("token found in %s", p)
			}
		}
		return nil
	})
}

func TestRejectedTokenFailsCleanlyWithoutLeakingIt(t *testing.T) {
	jobs := NewJobs()
	job, err := jobs.Start(token, ClientOptions{MinInterval: time.Nanosecond, Do: func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader("{}")), Header: http.Header{}}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	var done Job
	for i := 0; i < 200; i++ {
		done, _ = jobs.Get(job.ID)
		if done.State != "running" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if done.State != "failed" || !strings.Contains(done.Message, "rejected the token") {
		t.Fatalf("job: %+v", done)
	}
	b, _ := json.Marshal(done)
	if strings.Contains(string(b), token) {
		t.Fatal("token leaked into the job")
	}
}

func TestOnlyOneJobAtATimeAndCancel(t *testing.T) {
	jobs := NewJobs()
	block := make(chan struct{})
	slow := ClientOptions{MinInterval: time.Nanosecond, Do: func(r *http.Request) (*http.Response, error) {
		select {
		case <-block:
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
		return nil, fmt.Errorf("closed")
	}}
	j, err := jobs.Start(token, slow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.Start(token, slow); err == nil {
		t.Fatal("a second job should be refused")
	}
	jobs.Cancel(j.ID)
	for i := 0; i < 200; i++ {
		if got, _ := jobs.Get(j.ID); got.State != "running" {
			if got.State != "cancelled" {
				t.Fatalf("state %s", got.State)
			}
			close(block)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("job did not stop")
}

func TestRetriesAndBackoff(t *testing.T) {
	var sleeps []time.Duration
	calls := 0
	c := NewClient(token, ClientOptions{MinInterval: time.Nanosecond, Sleep: func(_ context.Context, d time.Duration) error {
		if d > time.Microsecond {
			sleeps = append(sleeps, d)
		}
		return nil
	}, Do: func(*http.Request) (*http.Response, error) {
		calls++
		switch calls {
		case 1:
			return &http.Response{StatusCode: 429, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{"Retry-After": {"3"}}}, nil
		case 2:
			return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Header: http.Header{}}, nil
	}})
	var out Obj
	if err := c.Request(context.Background(), "GET", "/users/me", nil, &out); err != nil {
		t.Fatal(err)
	}
	if out["ok"] != true || calls != 3 || len(sleeps) != 2 || sleeps[0] != 3*time.Second || sleeps[1] != 2*time.Second {
		t.Fatalf("calls=%d sleeps=%v out=%v", calls, sleeps, out)
	}
}

func TestRichTextToMarkdown(t *testing.T) {
	got := RichTextToMarkdown([]any{
		Obj{"plain_text": "bold ", "annotations": Obj{"bold": true}},
		Obj{"plain_text": "code", "annotations": Obj{"code": true}},
		Obj{"plain_text": " link", "href": "https://example.com"},
	})
	if got != "**bold** `code` [link](https://example.com)" {
		t.Fatalf("got %q", got)
	}
}

func TestConvertsAMixedPage(t *testing.T) {
	kids := map[string][]Obj{"t": {
		{"id": "r1", "type": "table_row", "table_row": Obj{"cells": []any{[]any{Obj{"plain_text": "A"}}, []any{Obj{"plain_text": "B"}}}}},
		{"id": "r2", "type": "table_row", "table_row": Obj{"cells": []any{[]any{Obj{"plain_text": "1"}}, []any{Obj{"plain_text": "2"}}}}},
	}}
	one := func(s string) []any { return []any{Obj{"plain_text": s}} }
	md, err := BlocksToMarkdown(context.Background(), []Obj{
		{"id": "h", "type": "heading_2", "heading_2": Obj{"rich_text": one("Sec")}},
		{"id": "n1", "type": "numbered_list_item", "numbered_list_item": Obj{"rich_text": one("one")}},
		{"id": "n2", "type": "numbered_list_item", "numbered_list_item": Obj{"rich_text": one("two")}},
		{"id": "c", "type": "code", "code": Obj{"language": "plain text", "rich_text": one("a ``` b")}},
		{"id": "k", "type": "callout", "callout": Obj{"icon": Obj{"emoji": "\U0001F4A1"}, "rich_text": one("tip")}},
		{"id": "td", "type": "to_do", "to_do": Obj{"checked": true, "rich_text": one("done")}},
		{"id": "t", "type": "table", "has_children": true, "table": Obj{}},
	}, func(_ context.Context, id string) ([]Obj, error) { return kids[id], nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"## Sec", "1. one\n2. two", "````\na ``` b\n````", "> \U0001F4A1 tip", "- [x] done", "| A | B |\n| --- | --- |\n| 1 | 2 |"} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q in\n%s", want, md)
		}
	}
}

func TestPropertiesAreWrittenTheWayNotionExportsThem(t *testing.T) {
	none := func(string) (string, bool) { return "", false }
	check := func(p Obj, titleOf func(string) (string, bool), want string) {
		t.Helper()
		if got := PropertyToCell(p, titleOf); got != want {
			t.Errorf("%v: got %q want %q", p, got, want)
		}
	}
	check(Obj{"type": "date", "date": Obj{"start": "2026-03-24"}}, none, "March 24, 2026")
	check(Obj{"type": "date", "date": Obj{"start": "2026-03-24", "end": "2026-03-25"}}, none, "March 24, 2026 → March 25, 2026")
	check(Obj{"type": "checkbox", "checkbox": true}, none, "Yes")
	check(Obj{"type": "multi_select", "multi_select": []any{Obj{"name": "a"}, Obj{"name": "b"}}}, none, "a, b")
	check(Obj{"type": "number", "number": 3.0}, none, "3")
	check(Obj{"type": "unique_id", "unique_id": Obj{"prefix": "T", "number": 7.0}}, none, "T-7")
	check(Obj{"type": "rollup"}, none, "")
	check(Obj{"type": "relation", "relation": []any{Obj{"id": id(21)}, Obj{"id": id(99)}}},
		func(x string) (string, bool) { return "zlist", x == id(21) }, "zlist (https://www.notion.so/00000000000040008000000000000021)")
}
