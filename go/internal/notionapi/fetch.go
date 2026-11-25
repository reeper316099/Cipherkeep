package notionapi

import (
	"context"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/reeper316099/Cipherkeep/go/internal/importer"
)

// Progress says how far a fetch has got.
type Progress struct {
	Phase string `json:"phase"` // listing, reading, building
	Done  int    `json:"done"`
	Total int    `json:"total"`
}

// Stats summarises a fetch.
type Stats struct {
	Databases    int `json:"databases"`
	Pages        int `json:"pages"`
	SkippedPages int `json:"skippedPages"`
	Images       int `json:"images"`
	Unsupported  int `json:"unsupported"`
}

// Fetched is an in-memory Notion export and what it took to make it.
type Fetched struct {
	VFS   *importer.VirtualFS
	Stats Stats
}

func listAll(ctx context.Context, c *Client, filterValue string) ([]Obj, error) {
	const cap = 20_000
	var out []Obj
	cursor := ""
	for {
		body := map[string]any{"filter": map[string]any{"property": "object", "value": filterValue}, "page_size": 100}
		if cursor != "" {
			body["start_cursor"] = cursor
		}
		var res Obj
		if err := c.Request(ctx, "POST", "/search", body, &res); err != nil {
			return nil, err
		}
		for _, r := range arr(res["results"]) {
			out = append(out, obj(r))
		}
		cursor = ""
		if res["has_more"] == true {
			cursor = s(res["next_cursor"])
		}
		if cursor == "" || len(out) >= cap {
			return out, nil
		}
	}
}

func isTrashed(p Obj) bool {
	return p["in_trash"] == true || p["is_archived"] == true || p["archived"] == true
}

// FetchExport reads the whole workspace the token can see and builds the same files a Notion export would hold.
func FetchExport(ctx context.Context, c *Client, onProgress func(Progress)) (*Fetched, error) {
	if onProgress == nil {
		onProgress = func(Progress) {}
	}
	onProgress(Progress{Phase: "listing"})
	sources, err := listAll(ctx, c, "data_source")
	if err != nil {
		return nil, err
	}
	rawPages, err := listAll(ctx, c, "page")
	if err != nil {
		return nil, err
	}
	var pages []Obj
	for _, p := range rawPages {
		if !isTrashed(p) {
			pages = append(pages, p)
		}
	}
	dsByDB := map[string]string{}
	dsByID := map[string]Obj{}
	for _, ds := range sources {
		dsByID[s(ds["id"])] = ds
		if db := s(obj(ds["parent"])["database_id"]); db != "" {
			dsByDB[db] = s(ds["id"])
		}
	}
	dsOf := func(p Obj) string {
		par := obj(p["parent"])
		if id := s(par["data_source_id"]); id != "" {
			return id
		}
		if db := s(par["database_id"]); db != "" {
			return dsByDB[db]
		}
		return ""
	}
	titleKey := func(props Obj) string {
		keys := make([]string, 0, len(props))
		for k := range props {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if s(obj(props[k])["type"]) == "title" {
				return k
			}
		}
		return ""
	}
	pageTitle := func(p Obj) string {
		props := obj(p["properties"])
		if k := titleKey(props); k != "" {
			prop := obj(props[k])
			if t := plain(prop[s(prop["type"])]); t != "" {
				return t
			}
		}
		return "Untitled"
	}
	byID := map[string]Obj{}
	titles := map[string]string{}
	for _, p := range pages {
		byID[s(p["id"])] = p
		titles[s(p["id"])] = pageTitle(p)
	}
	titleOf := func(id string) (string, bool) { t, ok := titles[id]; return t, ok }

	vfs := importer.NewVirtualFS()
	var stats Stats
	var convert ConvertStats
	cache := map[string][]Obj{}
	getChildren := func(ctx context.Context, id string) ([]Obj, error) {
		if cached, ok := cache[id]; ok {
			return cached, nil
		}
		var all []Obj
		cursor := ""
		for {
			path := "/blocks/" + url.PathEscape(id) + "/children?page_size=100"
			if cursor != "" {
				path += "&start_cursor=" + url.QueryEscape(cursor)
			}
			var res Obj
			if err := c.Request(ctx, "GET", path, nil, &res); err != nil {
				return nil, err
			}
			for _, b := range arr(res["results"]) {
				all = append(all, obj(b))
			}
			if res["has_more"] != true || s(res["next_cursor"]) == "" {
				break
			}
			cursor = s(res["next_cursor"])
		}
		cache[id] = all
		return all, nil
	}

	var pathOf func(p Obj) string
	pathOf = func(p Obj) string {
		if ds := dsOf(p); ds != "" {
			if name := safeName(plain(obj(dsByID[ds])["title"])); name != "Untitled" {
				return name
			}
			return "Database"
		}
		if parent, ok := byID[s(obj(p["parent"])["page_id"])]; ok {
			return pathOf(parent) + "/" + safeName(pageTitle(parent)) + " " + hex32(s(parent["id"]))
		}
		return ""
	}

	for i, p := range pages {
		onProgress(Progress{Phase: "reading", Done: i, Total: len(pages)})
		blocks, err := getChildren(ctx, s(p["id"]))
		body := ""
		if err == nil {
			body, err = BlocksToMarkdown(ctx, blocks, getChildren, &convert)
		}
		if err != nil {
			if IsCode(err, BadToken) || IsCode(err, Cancelled) {
				return nil, err
			}
			stats.SkippedPages++ // no access to the content, or a transient failure: import the properties only
			body = ""
		} else {
			stats.Pages++
		}
		cache = map[string][]Obj{}
		title := pageTitle(p)
		dir := pathOf(p)
		if dir != "" {
			dir += "/"
		}
		vfs.Add(dir+safeName(title)+" "+hex32(s(p["id"]))+".md", []byte("# "+title+"\n\n"+body+"\n"))
	}

	onProgress(Progress{Phase: "building", Done: len(pages), Total: len(pages)})
	for _, ds := range sources {
		props := obj(ds["properties"])
		tKey := titleKey(props)
		if tKey == "" {
			continue
		}
		others := make([]string, 0, len(props))
		for k := range props {
			if k != tKey {
				others = append(others, k)
			}
		}
		sort.Strings(others) // the API's own column order is not kept; the title column always comes first
		cols := append([]string{tKey}, others...)
		esc := make([]string, len(cols))
		for i, k := range cols {
			esc[i] = csvEscape(k)
		}
		lines := []string{strings.Join(esc, ",")}
		for _, r := range pages {
			if dsOf(r) != s(ds["id"]) {
				continue
			}
			row := make([]string, len(cols))
			for i, k := range cols {
				if prop := obj(obj(r["properties"])[k]); prop != nil {
					row[i] = csvEscape(PropertyToCell(prop, titleOf))
				}
			}
			lines = append(lines, strings.Join(row, ","))
		}
		name := safeName(plain(ds["title"]))
		if name == "Untitled" {
			name = "Database"
		}
		vfs.Add(name+" "+hex32(s(ds["id"]))+".csv", []byte("\xef\xbb\xbf"+strings.Join(lines, "\n")+"\n"))
		stats.Databases++
	}
	stats.Images, stats.Unsupported = convert.Images, convert.Unsupported
	return &Fetched{VFS: vfs, Stats: stats}, nil
}

// Job is one background import from Notion.
type Job struct {
	ID       string
	State    string // running, done, failed, cancelled
	Progress Progress
	Message  string
	Result   *Stats
	VFS      *importer.VirtualFS
}

type jobEntry struct {
	job    Job
	cancel context.CancelFunc
}

// Jobs keeps the in-memory jobs. The token is captured by the running goroutine and stored nowhere else.
type Jobs struct {
	mu   sync.Mutex
	jobs map[string]*jobEntry
	seq  []string
}

// NewJobs creates an empty job list.
func NewJobs() *Jobs { return &Jobs{jobs: map[string]*jobEntry{}} }

// Start begins a fetch in the background. Only one may run at a time.
func (j *Jobs) Start(token string, o ClientOptions) (Job, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, id := range j.seq {
		if e := j.jobs[id]; e != nil && e.job.State == "running" {
			return Job{}, fail(Failed, "An import from Notion is already running")
		}
	}
	for len(j.seq) > 5 { // drop the oldest finished jobs
		delete(j.jobs, j.seq[0])
		j.seq = j.seq[1:]
	}
	id := newID()
	ctx, cancel := context.WithCancel(context.Background())
	entry := &jobEntry{job: Job{ID: id, State: "running", Progress: Progress{Phase: "listing"}}, cancel: cancel}
	j.jobs[id] = entry
	j.seq = append(j.seq, id)
	client := NewClient(token, o)
	go func() {
		res, err := FetchExport(ctx, client, func(p Progress) {
			j.mu.Lock()
			entry.job.Progress = p
			j.mu.Unlock()
		})
		j.mu.Lock()
		defer j.mu.Unlock()
		cancel()
		if err != nil {
			entry.job.State = "failed"
			if IsCode(err, Cancelled) {
				entry.job.State = "cancelled"
			}
			entry.job.Message = "Something went wrong while reading from Notion"
			if e, ok := err.(*Error); ok {
				entry.job.Message = e.Message
			}
			return
		}
		entry.job.VFS, entry.job.Result, entry.job.State = res.VFS, &res.Stats, "done"
	}()
	return entry.job, nil
}

// Get returns a snapshot of a job.
func (j *Jobs) Get(id string) (Job, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if e := j.jobs[id]; e != nil {
		return e.job, true
	}
	return Job{}, false
}

// DropVFS forgets a finished job's files once they have been turned into a preview.
func (j *Jobs) DropVFS(id string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if e := j.jobs[id]; e != nil {
		e.job.VFS = nil
	}
}

// Cancel stops a running job.
func (j *Jobs) Cancel(id string) {
	j.mu.Lock()
	e := j.jobs[id]
	j.mu.Unlock()
	if e != nil {
		e.cancel()
	}
}
