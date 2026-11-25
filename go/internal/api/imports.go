package api

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/reeper316099/Cipherkeep/go/internal/apperr"
	"github.com/reeper316099/Cipherkeep/go/internal/domain"
	"github.com/reeper316099/Cipherkeep/go/internal/exporter"
	"github.com/reeper316099/Cipherkeep/go/internal/importer"
	"github.com/reeper316099/Cipherkeep/go/internal/jsonstore"
	"github.com/reeper316099/Cipherkeep/go/internal/library"
	"github.com/reeper316099/Cipherkeep/go/internal/notionapi"
	"github.com/reeper316099/Cipherkeep/go/internal/starter"
)

type routeFn = func(pattern string, fn func(w http.ResponseWriter, r *http.Request) error)

// previewResponse is what the UI sees of a plan before committing it.
func previewResponse(p *importer.Plan, previewID string) map[string]any {
	counts := map[string]int{}
	for _, it := range p.Items {
		counts[it.Collection]++
	}
	templates := make([]map[string]any, len(p.Templates))
	for i, t := range p.Templates {
		templates[i] = map[string]any{"collection": t.Collection, "name": t.Template.Name, "sections": len(t.Template.Sections)}
	}
	items := make([]map[string]any, len(p.Items))
	for i, it := range p.Items {
		m := map[string]any{"ref": it.Ref, "collection": it.Collection, "title": it.Title, "source": it.Source,
			"warnings": nonNil(it.Warnings)}
		if it.SkipByDefault != "" {
			m["skipByDefault"] = it.SkipByDefault
		}
		if it.Alternatives != nil {
			keys := make([]string, 0, len(it.Alternatives))
			for k := range it.Alternatives {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			m["alternatives"] = keys
		}
		items[i] = m
	}
	return map[string]any{"previewId": previewID, "kind": p.Kind, "sourceName": p.SourceName, "counts": counts,
		"databases": nonNil(p.Databases), "templates": templates, "attachments": len(p.Attachments),
		"warnings": nonNil(p.Warnings), "items": items}
}

// mapping reads {page: collection | "skip"}.
func parseMapping(v any) (map[string]string, error) {
	if v == nil {
		return nil, nil
	}
	m, isObj := v.(map[string]any)
	if !isObj {
		return nil, invalid("mapping", "Expected object, received %s", jsType(v))
	}
	out := map[string]string{}
	for k, x := range m {
		s, isStr := x.(string)
		if !isStr || s != "skip" && !domain.IsCollection(s) {
			return nil, invalid("mapping", "Invalid value for %s", k)
		}
		out[k] = s
	}
	return out, nil
}

func strategy(b map[string]any) (importer.Strategy, error) {
	v, err := optEnum(b, "strategy", strategies...)
	if v == "" {
		v = string(importer.StrategyUpdateEmptyOnly)
	}
	return importer.Strategy(v), err
}

// upload is the first file of a multipart request, and the plain fields sent before it.
type upload struct {
	name   string
	data   []byte
	fields map[string]string
}

var errTooLarge = errors.New("file too large")

// readUpload reads the first file part of a multipart body, at most limit bytes.
func readUpload(r *http.Request, limit int64) (*upload, error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, apperr.New(406, "FST_INVALID_MULTIPART_CONTENT_TYPE", "the request is not multipart")
	}
	up := &upload{fields: map[string]string{}}
	for nFields := 0; ; {
		part, err := mr.NextPart()
		if err == io.EOF {
			return nil, apperr.New(400, "VALIDATION", "No file uploaded")
		}
		if err != nil {
			return nil, apperr.New(400, "BAD_REQUEST", "Malformed multipart body")
		}
		if part.FileName() == "" {
			nFields++
			if nFields > 10 {
				return nil, apperr.New(413, "FST_FIELDS_LIMIT", "reach fields limit")
			}
			v, err := io.ReadAll(io.LimitReader(part, 1<<20))
			if err != nil {
				return nil, apperr.New(400, "BAD_REQUEST", "Malformed multipart body")
			}
			up.fields[part.FormName()] = string(v)
			continue
		}
		up.name = part.FileName()
		up.data, err = io.ReadAll(io.LimitReader(part, limit+1))
		if err != nil {
			return nil, apperr.New(400, "BAD_REQUEST", "Malformed multipart body")
		}
		if int64(len(up.data)) > limit {
			return up, errTooLarge
		}
		return up, nil
	}
}

func isMultipart(r *http.Request) bool {
	mt, _, _ := mime.ParseMediaType(r.Header.Get("content-type"))
	return mt == "multipart/form-data"
}

func (s *Server) importRoutes(api routeFn, lib *library.Library) {
	ok := func(w http.ResponseWriter, v any) error { writeJSON(w, 200, v); return nil }

	// ---- starter pack (original MIT content bundled with the app) ----
	api("GET /starter-pack", func(w http.ResponseWriter, r *http.Request) error {
		loaded := 0
		for _, c := range domain.Collections {
			for _, e := range lib.Repos[c].All() {
				if ref, _ := e["importRef"].(string); strings.HasPrefix(ref, starter.RefPrefix) {
					loaded++
				}
			}
		}
		return ok(w, map[string]any{"license": "MIT", "counts": starter.Counts(), "loaded": loaded})
	})
	api("POST /starter-pack/load", func(w http.ResponseWriter, r *http.Request) error {
		b, err := optionalObject(r)
		if err != nil {
			return err
		}
		st, err := strategy(b)
		if err != nil {
			return err
		}
		rep, err := importer.Commit(lib, starter.Plan(), importer.Options{Strategy: st}, nil)
		if err != nil {
			return err
		}
		return ok(w, rep)
	})

	// ---- import ----
	previews := importer.NewPreviewStore()
	api("POST /import/preview", func(w http.ResponseWriter, r *http.Request) error {
		var (
			vfs  *importer.VirtualFS
			name string
			zip  []byte
			o    importer.BuildOptions
			err  error
		)
		if isMultipart(r) {
			up, err := readUpload(r, 500<<20)
			if errors.Is(err, errTooLarge) {
				return apperr.New(413, "FST_REQ_FILE_TOO_LARGE", "request file too large")
			}
			if err != nil {
				return err
			}
			if c := up.fields["collection"]; c != "" {
				if o.Collection, err = collectionParam(c); err != nil {
					return err
				}
			}
			if m := up.fields["mapping"]; m != "" {
				var v any
				if err := json.Unmarshal([]byte(m), &v); err != nil {
					return apperr.New(400, "BAD_REQUEST", "mapping is not valid JSON")
				}
				if o.Mapping, err = parseMapping(v); err != nil {
					return err
				}
			}
			name = up.name
			if vfs, err = importer.FromUpload(name, up.data); err != nil {
				return err
			}
			if strings.HasSuffix(strings.ToLower(name), ".zip") {
				zip = up.data
			}
		} else {
			b, err := object(r)
			if err != nil {
				return err
			}
			p, err := reqString(b, "path", 1, 0)
			if err != nil {
				return err
			}
			if o.Collection, err = optEnum(b, "collection", domain.Collections...); err != nil {
				return err
			}
			if o.Mapping, err = parseMapping(b["mapping"]); err != nil {
				return err
			}
			if vfs, name, err = importer.FromPath(p); err != nil {
				return err
			}
		}
		o.SourceName = name
		plan, err := importer.BuildPlan(lib, vfs, o)
		if err != nil {
			return err
		}
		return ok(w, previewResponse(plan, previews.Put(plan, vfs, zip, name)))
	})

	// ---- optional: import straight from the Notion API (off by default; see Settings, Privacy) ----
	jobs := notionapi.NewJobs()
	var npMu sync.Mutex
	notionPreviews := map[string]map[string]any{}
	api("POST /import/notion-api/start", func(w http.ResponseWriter, r *http.Request) error {
		if !lib.Settings.Data().NotionAPIEnabled {
			return apperr.New(403, "NOTION_API_DISABLED", `Turn on "Import from the Notion API" in Settings first`)
		}
		b, err := object(r)
		if err != nil {
			return err
		}
		tok, err := reqString(b, "token", 20, 500)
		if err != nil {
			return err
		}
		job, err := jobs.Start(strings.TrimSpace(tok), s.o.Notion)
		if err != nil {
			var ne *notionapi.Error
			if errors.As(err, &ne) {
				return apperr.New(409, "BUSY", "%s", ne.Message)
			}
			return err
		}
		return ok(w, map[string]any{"jobId": job.ID})
	})
	api("GET /import/notion-api/{id}", func(w http.ResponseWriter, r *http.Request) error {
		id := r.PathValue("id")
		job, found := jobs.Get(id)
		if !found {
			return apperr.NotFound("notion import", id)
		}
		npMu.Lock()
		defer npMu.Unlock()
		if _, done := notionPreviews[id]; job.State == "done" && job.VFS != nil && !done {
			plan, err := importer.BuildPlan(lib, job.VFS, importer.BuildOptions{SourceName: "Notion (API)"})
			if err != nil {
				return err
			}
			notionPreviews[id] = previewResponse(plan, previews.Put(plan, job.VFS, nil, "Notion (API)"))
			jobs.DropVFS(id)
		}
		out := map[string]any{"state": job.State, "progress": job.Progress}
		if job.Message != "" {
			out["message"] = job.Message
		}
		if job.Result != nil {
			out["stats"] = job.Result
		}
		if p, has := notionPreviews[id]; has {
			out["preview"] = p
		}
		return ok(w, out)
	})
	api("POST /import/notion-api/{id}/cancel", func(w http.ResponseWriter, r *http.Request) error {
		jobs.Cancel(r.PathValue("id"))
		return ok(w, map[string]any{"ok": true})
	})
	api("POST /import/commit", func(w http.ResponseWriter, r *http.Request) error {
		b, err := object(r)
		if err != nil {
			return err
		}
		id, err := reqString(b, "previewId", 0, 0)
		if err != nil {
			return err
		}
		st, err := strategy(b)
		if err != nil {
			return err
		}
		var overrides map[string]string
		if v, has := b["overrides"]; has && v != nil {
			m, isObj := v.(map[string]any)
			if !isObj {
				return invalid("overrides", "Expected object, received %s", jsType(v))
			}
			overrides = map[string]string{}
			for k, x := range m {
				sv, isStr := x.(string)
				if !isStr {
					return invalid("overrides", "Expected string, received %s", jsType(x))
				}
				overrides[k] = sv
			}
		}
		p, err := previews.Get(id)
		if err != nil {
			return err
		}
		rep, err := importer.Commit(lib, p.Plan, importer.Options{Strategy: st, Overrides: overrides},
			&importer.Source{VFS: p.VFS, Zip: p.Zip, ZipName: p.ZipName})
		if err != nil {
			return err
		}
		previews.Drop(id)
		return ok(w, rep)
	})
	api("POST /import/undo/{id}", func(w http.ResponseWriter, r *http.Request) error {
		res, err := importer.Undo(lib, r.PathValue("id"))
		if err != nil {
			return err
		}
		return ok(w, res)
	})
	api("GET /imports", func(w http.ResponseWriter, r *http.Request) error {
		return ok(w, nonNil(importer.List(lib)))
	})

	// ---- export ----
	download := func(w http.ResponseWriter, ct, filename string, body []byte) error {
		w.Header().Set("content-type", ct)
		w.Header().Set("content-disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
		w.WriteHeader(200)
		_, _ = w.Write(body)
		return nil
	}
	stamp := func() string { return time.Now().UTC().Format("2006-01-02") }
	app := strings.ToLower(domain.AppName)
	exportOpts := func(r *http.Request) (exporter.Options, error) {
		inc, err := queryBool(r, "includePrivate")
		return exporter.Options{IncludePrivate: inc}, err
	}
	queryCollection := func(r *http.Request) (string, error) {
		c, err := queryEnum(r, "collection", domain.Collections...)
		if err == nil && c == "" {
			err = invalid("collection", "Required")
		}
		return c, err
	}
	api("GET /export/markdown", func(w http.ResponseWriter, r *http.Request) error {
		o, err := exportOpts(r)
		if err != nil {
			return err
		}
		zip, err := exporter.Markdown(lib, o)
		if err != nil {
			return err
		}
		return download(w, "application/zip", app+"-vault-"+stamp()+".zip", zip)
	})
	api("GET /export/csv", func(w http.ResponseWriter, r *http.Request) error {
		o, err := exportOpts(r)
		if err != nil {
			return err
		}
		c, err := queryCollection(r)
		if err != nil {
			return err
		}
		return download(w, "text/csv; charset=utf-8", c+"-"+stamp()+".csv", []byte(exporter.CSV(lib, c, o)))
	})
	api("GET /export/csv-template", func(w http.ResponseWriter, r *http.Request) error {
		c, err := queryCollection(r)
		if err != nil {
			return err
		}
		return download(w, "text/csv; charset=utf-8", c+"-template.csv", []byte(exporter.CSVTemplate(c)))
	})
	api("GET /export/json", func(w http.ResponseWriter, r *http.Request) error {
		o, err := exportOpts(r)
		if err != nil {
			return err
		}
		body, err := marshalIndent(exporter.JSON(lib, o))
		if err != nil {
			return err
		}
		return download(w, "application/json", app+"-backup-"+stamp()+".json", body)
	})
}

// marshalIndent is JSON.stringify(v, null, 2).
func marshalIndent(v any) ([]byte, error) {
	b, err := marshal(v)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, b, "", "  "); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// ---- attachments (images pasted into notes or imported from Notion) ----

var (
	hashRe      = regexp.MustCompile(`^[0-9a-f]{8}$`)
	fileRe      = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_. -]{0,120}$`)
	leadJunkRe  = regexp.MustCompile(`^[._ -]+`)
	attachTypes = map[string]string{"png": "image/png", "jpg": "image/jpeg", "jpeg": "image/jpeg", "gif": "image/gif",
		"webp": "image/webp", "pdf": "application/pdf"}
)

func magicOK(ext string, b []byte) bool {
	switch ext {
	case "png":
		return len(b) >= 4 && string(b[1:4]) == "PNG"
	case "jpg", "jpeg":
		return len(b) >= 2 && b[0] == 0xff && b[1] == 0xd8
	case "gif":
		return len(b) >= 3 && string(b[:3]) == "GIF"
	case "webp":
		return len(b) >= 12 && string(b[8:12]) == "WEBP"
	}
	return false
}

func lastExt(name string) string {
	i := strings.LastIndexByte(name, '.')
	return strings.ToLower(name[i+1:])
}

// safeAttachmentName keeps word characters, dots, spaces and dashes, as the TypeScript version does: every other
// UTF-16 unit becomes "_", leading dots, spaces and dashes go, and only the last 80 units are kept.
func safeAttachmentName(name, ext string) string {
	units := utf16.Encode([]rune(name))
	out := make([]byte, 0, len(units))
	for _, u := range units {
		c := rune(u)
		if c < 128 && (c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("_. -", c)) {
			out = append(out, byte(c))
		} else {
			out = append(out, '_')
		}
	}
	s := leadJunkRe.ReplaceAllString(string(out), "")
	if len(s) > 80 {
		s = s[len(s)-80:]
	}
	if s == "" {
		s = "image." + ext
	}
	return s
}

func (s *Server) attachmentRoutes(api routeFn, lib *library.Library) {
	api("GET /attachments/{hash}/{file}", func(w http.ResponseWriter, r *http.Request) error {
		hash, file := r.PathValue("hash"), r.PathValue("file")
		if !hashRe.MatchString(hash) || !fileRe.MatchString(file) || strings.Contains(file, "..") {
			return apperr.NotFound("attachment", file)
		}
		p, err := lib.Store.Abs("attachments/" + hash + "/" + file)
		if err != nil {
			return apperr.NotFound("attachment", file)
		}
		data, err := jsonstore.ReadDecoded(lib.Codec, p)
		if err != nil {
			return apperr.NotFound("attachment", file)
		}
		ct := attachTypes[lastExt(file)]
		if ct == "" {
			ct = "application/octet-stream"
		}
		h := w.Header()
		h.Set("content-type", ct)
		h.Set("x-content-type-options", "nosniff")
		h.Set("content-security-policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
		h.Set("cache-control", "private, max-age=3600")
		w.WriteHeader(200)
		_, _ = w.Write(data)
		return nil
	})
	api("POST /attachments", func(w http.ResponseWriter, r *http.Request) error {
		if !isMultipart(r) {
			return apperr.New(406, "FST_INVALID_MULTIPART_CONTENT_TYPE", "the request is not multipart")
		}
		up, err := readUpload(r, 10<<20)
		if errors.Is(err, errTooLarge) {
			return apperr.New(413, "TOO_LARGE", "Image larger than 10 MB")
		}
		if err != nil {
			return err
		}
		ext := lastExt(up.name)
		switch ext {
		case "png", "jpg", "jpeg", "gif", "webp":
		default:
			return apperr.New(415, "UNSUPPORTED", "Only PNG, JPEG, GIF and WebP images can be attached")
		}
		if !magicOK(ext, up.data) {
			return apperr.New(415, "UNSUPPORTED", "Only PNG, JPEG, GIF and WebP images can be attached")
		}
		name := safeAttachmentName(up.name, ext)
		sum := sha1.Sum(up.data)
		hash := hex.EncodeToString(sum[:])[:8]
		p, err := lib.Store.Abs("attachments/" + hash + "/" + name)
		if err != nil {
			return err
		}
		if err := jsonstore.WriteEncoded(lib.Codec, p, up.data); err != nil {
			return err
		}
		writeJSON(w, 201, map[string]any{"url": "ck://attachments/" + hash + "/" + strings.ReplaceAll(name, " ", "%20")})
		return nil
	})
}
