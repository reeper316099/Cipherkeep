package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/reeper316099/Cipherkeep/go/internal/apperr"
	"github.com/reeper316099/Cipherkeep/go/internal/domain"
	"github.com/reeper316099/Cipherkeep/go/internal/jsonstore"
	"github.com/reeper316099/Cipherkeep/go/internal/library"
	"github.com/reeper316099/Cipherkeep/go/internal/links"
	"github.com/reeper316099/Cipherkeep/go/internal/repo"
	"github.com/reeper316099/Cipherkeep/go/internal/sanitize"
	"github.com/reeper316099/Cipherkeep/go/internal/search"
	"github.com/reeper316099/Cipherkeep/go/internal/study"
)

// omittable are the bulky fields a list response may leave out (the UI fetches them with GET /:id).
var omittable = map[string]bool{"markdown": true, "sections": true, "writeup": true, "body": true, "notes": true,
	"code": true, "whatItDoes": true}

var (
	omitRe     = regexp.MustCompile(`^[A-Za-z]+(,[A-Za-z]+)*$`)
	sortRe     = regexp.MustCompile(`^[A-Za-z]+$`)
	backupRe   = regexp.MustCompile(`^[A-Za-z0-9_.-]+\.zip$`)
	strategies = []string{"skip", "update-empty-only", "overwrite", "keep-both"}
)

func collectionParam(v string) (string, error) {
	if domain.IsCollection(v) {
		return v, nil
	}
	return "", apperr.New(404, "NOT_FOUND", "Unknown collection: %s", v)
}

func collectionList(v string) ([]string, error) {
	var out []string
	for _, c := range strings.Split(v, ",") {
		name, err := collectionParam(c)
		if err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, nil
}

func tzOffset(r *http.Request) (int, error) {
	v, _, err := queryInt(r, "tzOffset", -840, 840, 0, true)
	return v, err
}

// openHandler builds the full API for an open library.
func (s *Server) openHandler(lib *library.Library) http.Handler {
	rt := newRouter(s.o.Static)
	api := func(pattern string, fn func(w http.ResponseWriter, r *http.Request) error) {
		method, p, _ := strings.Cut(pattern, " ")
		rt.handle(method+" /api/v1"+p, fn)
	}
	ok := func(w http.ResponseWriter, v any) error { writeJSON(w, 200, v); return nil }

	api("GET /health", func(w http.ResponseWriter, r *http.Request) error {
		return ok(w, map[string]any{"status": "ok"})
	})
	s.vaultRoutes(rt, false)
	api("GET /meta", func(w http.ResponseWriter, r *http.Request) error {
		counts := map[string]int{}
		for _, c := range domain.Collections {
			counts[c] = len(lib.Repos[c].All())
		}
		return ok(w, map[string]any{"appName": domain.AppName, "apiVersion": 1, "collections": domain.Collections,
			"counts": counts})
	})

	// ---- collections ----
	for _, name := range domain.Collections {
		name, rp := name, lib.Repos[name]
		act := func(typ, id string) { _ = lib.Activity.Record(typ, name, id) }

		api("GET /"+name, func(w http.ResponseWriter, r *http.Request) error {
			q, omit, text, err := listQuery(r)
			if err != nil {
				return err
			}
			if text != "" {
				hits := lib.Search.Search(text, search.Options{Types: []string{name}, Limit: 500})
				q.IDs = make([]string, len(hits))
				for i, h := range hits {
					q.IDs[i] = h.ID
				}
			}
			page := repo.Query(rp.All(), q)
			if omit != nil {
				items := make([]domain.Entity, len(page.Items))
				for i, e := range page.Items {
					cp := make(domain.Entity, len(e))
					for k, v := range e {
						if !omit[k] {
							cp[k] = v
						}
					}
					items[i] = cp
				}
				page.Items = items
			}
			if page.Items == nil {
				page.Items = []domain.Entity{}
			}
			return ok(w, page)
		})
		api("GET /"+name+"/{id}", func(w http.ResponseWriter, r *http.Request) error {
			id := r.PathValue("id")
			e, found := rp.Get(id)
			if !found {
				return apperr.NotFound(name, id)
			}
			return ok(w, e)
		})
		api("POST /"+name, func(w http.ResponseWriter, r *http.Request) error {
			b, err := readBody(r)
			if err != nil {
				return err
			}
			in, _ := b.(map[string]any)
			e, err := rp.Create(in, "")
			if err != nil {
				return err
			}
			act("create", e.ID())
			writeJSON(w, 201, e)
			return nil
		})
		api("PATCH /"+name+"/{id}", func(w http.ResponseWriter, r *http.Request) error {
			id := r.PathValue("id")
			b, err := readBody(r)
			if err != nil {
				return err
			}
			in, _ := b.(map[string]any)
			if in == nil {
				return invalid("", "Expected object, received %s", jsType(b))
			}
			e, err := rp.Patch(id, in)
			if err != nil {
				return err
			}
			act("update", id)
			return ok(w, e)
		})
		api("DELETE /"+name+"/{id}", func(w http.ResponseWriter, r *http.Request) error {
			id := r.PathValue("id")
			if err := rp.Remove(id); err != nil {
				return err
			}
			act("delete", id)
			w.WriteHeader(204)
			return nil
		})
		api("POST /"+name+"/bulk", func(w http.ResponseWriter, r *http.Request) error {
			b, err := object(r)
			if err != nil {
				return err
			}
			op, err := optEnum(b, "op", "delete", "patch")
			if err != nil {
				return err
			}
			if op == "" {
				return invalid("op", "Invalid discriminator value. Expected 'delete' | 'patch'")
			}
			ids, err := stringList(b, "ids", 1, 1000)
			if err != nil {
				return err
			}
			var patch map[string]any
			if op == "patch" {
				p, isObj := b["patch"].(map[string]any)
				if !isObj {
					return invalid("patch", "Expected object, received %s", jsType(b["patch"]))
				}
				patch = p
			}
			var missing []string
			for _, id := range ids {
				if _, found := rp.Get(id); !found {
					missing = append(missing, id)
				}
			}
			if len(missing) > 0 {
				return &apperr.Error{Status: 404, Code: "NOT_FOUND", Message: "Some ids do not exist", Details: missing}
			}
			if op == "delete" {
				if _, err := lib.Backups.Create("pre-bulk-delete"); err != nil {
					return err
				}
			}
			for _, id := range ids {
				if op == "delete" {
					err = rp.Remove(id)
					act("delete", id)
				} else {
					_, err = rp.Patch(id, patch)
					act("update", id)
				}
				if err != nil {
					return err
				}
			}
			return ok(w, map[string]any{"affected": len(ids)})
		})
	}

	// ---- search ----
	api("GET /search", func(w http.ResponseWriter, r *http.Request) error {
		limit, _, err := queryInt(r, "limit", 1, 1000, 50, true)
		if err != nil {
			return err
		}
		var types []string
		if t := r.URL.Query().Get("type"); r.URL.Query().Has("type") {
			if types, err = collectionList(t); err != nil {
				return err
			}
		}
		hits := lib.Search.Search(r.URL.Query().Get("q"), search.Options{Limit: limit, Types: types})
		if hits == nil {
			hits = []search.Hit{}
		}
		facets := map[string]int{}
		for _, h := range hits {
			facets[h.Type]++
		}
		return ok(w, map[string]any{"hits": hits, "facets": facets})
	})

	// ---- taxonomy ----
	api("GET /categories", func(w http.ResponseWriter, r *http.Request) error { return ok(w, lib.Categories.Data()) })
	api("PUT /categories", func(w http.ResponseWriter, r *http.Request) error {
		b, err := object(r)
		if err != nil {
			return err
		}
		next, err := parseCategories(b)
		if err != nil {
			return err
		}
		if _, err := lib.Categories.Update(func(c *domain.Categories) error { *c = next; return nil }); err != nil {
			return err
		}
		return ok(w, next)
	})
	api("GET /skills", func(w http.ResponseWriter, r *http.Request) error { return ok(w, lib.Skills.Data()) })
	api("PUT /skills", func(w http.ResponseWriter, r *http.Request) error {
		b, err := readBody(r)
		if err != nil {
			return err
		}
		next, err := parseSkills(b)
		if err != nil {
			return err
		}
		if _, err := lib.Skills.Update(func(s *[]domain.SkillDomain) error { *s = next; return nil }); err != nil {
			return err
		}
		return ok(w, next)
	})
	api("GET /tags", func(w http.ResponseWriter, r *http.Request) error {
		counts := map[string]int{}
		for _, c := range domain.Collections {
			for _, e := range lib.Repos[c].All() {
				for _, t := range e.Strs("tags") {
					counts[t]++
				}
			}
		}
		type tag struct {
			Name  string `json:"name"`
			Count int    `json:"count"`
		}
		out := make([]tag, 0, len(counts))
		for n, c := range counts {
			out = append(out, tag{n, c})
		}
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].Count != out[j].Count {
				return out[i].Count > out[j].Count
			}
			if c := bytes.Compare(repo.TitleKey(out[i].Name), repo.TitleKey(out[j].Name)); c != 0 {
				return c < 0
			}
			return out[i].Name < out[j].Name
		})
		return ok(w, out)
	})
	retag := func(from []string, to string) (int, error) {
		in := map[string]bool{}
		for _, f := range from {
			in[f] = true
		}
		changed := 0
		for _, c := range domain.Collections {
			for _, e := range lib.Repos[c].All() {
				tags := e.Strs("tags")
				hit := false
				for _, t := range tags {
					hit = hit || in[t]
				}
				if !hit {
					continue
				}
				seen := map[string]bool{}
				next := []any{}
				for _, t := range tags {
					if in[t] {
						t = to
					}
					if !seen[t] {
						seen[t] = true
						next = append(next, t)
					}
				}
				if _, err := lib.Repos[c].Patch(e.ID(), map[string]any{"tags": next}); err != nil {
					return changed, err
				}
				changed++
			}
		}
		return changed, nil
	}
	api("POST /tags/rename", func(w http.ResponseWriter, r *http.Request) error {
		b, err := object(r)
		if err != nil {
			return err
		}
		from, err := reqString(b, "from", 1, 0)
		if err != nil {
			return err
		}
		to, err := reqString(b, "to", 1, 0)
		if err != nil {
			return err
		}
		n, err := retag([]string{from}, to)
		if err != nil {
			return err
		}
		return ok(w, map[string]any{"changed": n})
	})
	api("POST /tags/merge", func(w http.ResponseWriter, r *http.Request) error {
		b, err := object(r)
		if err != nil {
			return err
		}
		from, err := stringList(b, "from", 1, 0)
		if err != nil {
			return err
		}
		for i, f := range from {
			if f == "" {
				return invalid("from", "String at %d must contain at least 1 character(s)", i)
			}
		}
		to, err := reqString(b, "to", 1, 0)
		if err != nil {
			return err
		}
		n, err := retag(from, to)
		if err != nil {
			return err
		}
		return ok(w, map[string]any{"changed": n})
	})

	// ---- templates ----
	templatePath := func(c string) (string, error) { return lib.Store.Abs("config/templates/" + c + ".json") }
	api("GET /templates/{collection}", func(w http.ResponseWriter, r *http.Request) error {
		c, err := collectionParam(r.PathValue("collection"))
		if err != nil {
			return err
		}
		if p, err := templatePath(c); err == nil {
			if raw, err := jsonstore.ReadDecoded(lib.Codec, p); err == nil {
				var v any
				if json.Unmarshal(raw, &v) == nil {
					if m, isObj := v.(map[string]any); isObj {
						if t, err := parseTemplate(m); err == nil {
							return ok(w, t)
						}
					}
				}
			}
		}
		d, has := domain.DefaultTemplates()[c]
		if !has {
			return apperr.NotFound("template", c)
		}
		return ok(w, d)
	})
	api("PUT /templates/{collection}", func(w http.ResponseWriter, r *http.Request) error {
		c, err := collectionParam(r.PathValue("collection"))
		if err != nil {
			return err
		}
		b, err := object(r)
		if err != nil {
			return err
		}
		t, err := parseTemplate(b)
		if err != nil {
			return err
		}
		p, err := templatePath(c)
		if err != nil {
			return err
		}
		if err := jsonstore.WriteJSONEncoded(lib.Codec, p, t); err != nil {
			return err
		}
		return ok(w, t)
	})
	api("DELETE /templates/{collection}", func(w http.ResponseWriter, r *http.Request) error {
		c, err := collectionParam(r.PathValue("collection"))
		if err != nil {
			return err
		}
		p, err := templatePath(c)
		if err != nil {
			return err
		}
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
		w.WriteHeader(204)
		return nil
	})

	// ---- settings & backups ----
	api("GET /settings", func(w http.ResponseWriter, r *http.Request) error { return ok(w, lib.Settings.Data()) })
	api("PUT /settings", func(w http.ResponseWriter, r *http.Request) error {
		b, err := readBody(r)
		if err != nil {
			return err
		}
		if _, isObj := b.(map[string]any); !isObj {
			return invalid("", "Expected object, received %s", jsType(b))
		}
		raw, _ := json.Marshal(b)
		next, err := domain.ParseSettings(raw)
		if err != nil {
			return invalid("", "%s", err.Error())
		}
		if _, err := lib.Settings.Update(func(s *domain.Settings) error { *s = next; return nil }); err != nil {
			return err
		}
		return ok(w, next)
	})
	api("GET /backups", func(w http.ResponseWriter, r *http.Request) error {
		list, err := lib.Backups.List()
		if err != nil {
			return err
		}
		out := make([]map[string]any, len(list))
		for i, b := range list {
			out[i] = backupJSON(b)
		}
		return ok(w, out)
	})
	api("POST /backups", func(w http.ResponseWriter, r *http.Request) error {
		b, err := optionalObject(r)
		if err != nil {
			return err
		}
		reason := "manual"
		if v, has, err := optString(b, "reason"); err != nil {
			return err
		} else if has {
			if err := checkLen("reason", v, 0, 40); err != nil {
				return err
			}
			reason = v
		}
		info, err := lib.Backups.Create(reason)
		if err != nil {
			return err
		}
		writeJSON(w, 201, backupJSON(info))
		return nil
	})
	api("POST /backup/restore", func(w http.ResponseWriter, r *http.Request) error {
		b, err := object(r)
		if err != nil {
			return err
		}
		name, err := reqString(b, "name", 0, 0)
		if err != nil {
			return err
		}
		if !backupRe.MatchString(name) {
			return invalid("name", "Invalid")
		}
		list, err := lib.Backups.List()
		if err != nil {
			return err
		}
		found := false
		for _, x := range list {
			found = found || x.Name == name
		}
		if !found {
			return apperr.NotFound("backup", name)
		}
		if _, err := lib.Backups.Create("pre-restore"); err != nil {
			return err
		}
		if err := lib.Backups.Restore(name); err != nil {
			return err
		}
		if err := lib.ReloadAll(); err != nil {
			return err
		}
		// An older plaintext backup must not leave plaintext behind.
		if err := normalizeEncryption(lib); err != nil {
			return err
		}
		return ok(w, map[string]any{"restored": name})
	})

	s.importRoutes(api, lib)

	// ---- links, backlinks, graph ----
	api("GET /entities/{id}/backlinks", func(w http.ResponseWriter, r *http.Request) error {
		return ok(w, nonNil(lib.Links.Backlinks(r.PathValue("id"))))
	})
	api("GET /entities/{id}/links", func(w http.ResponseWriter, r *http.Request) error {
		return ok(w, nonNil(lib.Links.Outgoing(r.PathValue("id"))))
	})
	api("GET /graph", func(w http.ResponseWriter, r *http.Request) error {
		o := links.GraphOptions{}
		var err error
		if r.URL.Query().Has("types") {
			if o.Types, err = collectionList(r.URL.Query().Get("types")); err != nil {
				return err
			}
		}
		if o.IncludeOrphans, err = queryBool(r, "includeOrphans"); err != nil {
			return err
		}
		return ok(w, lib.Links.Graph(o))
	})

	// ---- sanitized writeup export ----
	api("POST /labs/{id}/sanitized-writeup", func(w http.ResponseWriter, r *http.Request) error {
		id := r.PathValue("id")
		lab, found := lib.Repos["labs"].Get(id)
		if !found {
			return apperr.NotFound("labs", id)
		}
		return ok(w, sanitize.Writeup(lab.Str("writeup"), lib.Settings.Data().SanitizePatterns))
	})

	s.attachmentRoutes(api, lib)

	// ---- study & stats ----
	studyCollection := func(v string) (string, error) {
		if v == "" || v == "commands" || v == "concepts" {
			return v, nil
		}
		return "", invalid("collection", "Invalid enum value. Expected 'commands' | 'concepts', received '%s'", v)
	}
	api("GET /study/queue", func(w http.ResponseWriter, r *http.Request) error {
		tz, err := tzOffset(r)
		if err != nil {
			return err
		}
		c, err := queryEnum(r, "collection", "commands", "concepts")
		if err != nil {
			return err
		}
		limit, _, err := queryInt(r, "limit", 1, 200, 20, true)
		if err != nil {
			return err
		}
		q, err := lib.Study.Queue(library.QueueOptions{Collection: c, Limit: limit,
			OnlyID: r.URL.Query().Get("onlyId"), Now: time.Now(), TzOffset: tz})
		if err != nil {
			return err
		}
		return ok(w, q)
	})
	api("POST /study/answer", func(w http.ResponseWriter, r *http.Request) error {
		b, err := object(r)
		if err != nil {
			return err
		}
		id, err := reqString(b, "id", 1, 0)
		if err != nil {
			return err
		}
		rating, err := optEnum(b, "rating", "again", "hard", "good", "easy")
		if err != nil {
			return err
		}
		if rating == "" {
			return invalid("rating", "Required")
		}
		cs, _, err := optString(b, "collection")
		if err != nil {
			return err
		}
		c, err := studyCollection(cs)
		if err != nil {
			return err
		}
		ms := 0
		if v, has := b["ms"]; has && v != nil {
			f, isNum := v.(float64)
			if !isNum || f != float64(int64(f)) || f < 0 || f > 3_600_000 {
				return invalid("ms", "Expected an integer from 0 to 3600000")
			}
			ms = int(f)
		}
		ct, _, err := optString(b, "cardType")
		if err != nil {
			return err
		}
		if err := checkLen("cardType", ct, 0, 20); err != nil {
			return err
		}
		res, err := lib.Study.Answer(library.AnswerInput{ID: id, Rating: study.Rating(rating), Collection: c, Ms: ms,
			CardType: ct, Now: time.Now()})
		if err != nil {
			return err
		}
		return ok(w, res)
	})
	api("GET /study/stats", func(w http.ResponseWriter, r *http.Request) error {
		tz, err := tzOffset(r)
		if err != nil {
			return err
		}
		st, err := lib.Study.Stats(time.Now(), tz)
		if err != nil {
			return err
		}
		return ok(w, st)
	})
	api("GET /stats/overview", func(w http.ResponseWriter, r *http.Request) error {
		tz, err := tzOffset(r)
		if err != nil {
			return err
		}
		o, err := lib.BuildOverview(library.OverviewOptions{Now: time.Now(), TzOffset: tz})
		if err != nil {
			return err
		}
		return ok(w, o)
	})
	api("GET /questions", func(w http.ResponseWriter, r *http.Request) error {
		inc, err := queryBool(r, "includeDone")
		if err != nil {
			return err
		}
		return ok(w, nonNil(lib.Study.Questions(inc)))
	})

	// ---- activity ----
	api("GET /activity/{month}", func(w http.ResponseWriter, r *http.Request) error {
		return ok(w, nonNil(lib.Activity.Month(r.PathValue("month"))))
	})

	// ---- live events (SSE) ----
	api("GET /events", func(w http.ResponseWriter, r *http.Request) error {
		serveEvents(w, r, lib)
		return nil
	})
	return rt
}

// nonNil turns a nil slice into an empty one so it encodes as [] like the TypeScript version.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func backupJSON(b jsonstore.BackupInfo) map[string]any {
	return map[string]any{"name": b.Name, "createdAt": domain.ISO(b.CreatedAt), "reason": b.Reason, "size": b.Size}
}

// listQuery reads the list parameters. omit is nil when nothing is left out; text is the free-text filter.
func listQuery(r *http.Request) (repo.ListQuery, map[string]bool, string, error) {
	q := repo.ListQuery{}
	v := r.URL.Query()
	var omit map[string]bool
	if v.Has("omit") {
		o := v.Get("omit")
		if !omitRe.MatchString(o) {
			return q, nil, "", invalid("omit", "Invalid")
		}
		omit = map[string]bool{}
		for _, f := range strings.Split(o, ",") {
			if !omittable[f] {
				return q, nil, "", invalid("omit", "unsupported field in omit")
			}
			omit[f] = true
		}
	}
	q.Category, q.Tag = v.Get("category"), v.Get("tag")
	if v.Has("favorite") {
		f, err := queryBool(r, "favorite")
		if err != nil {
			return q, nil, "", err
		}
		q.Favorite = &f
	}
	var err error
	if q.Archived, err = queryEnum(r, "archived", "true", "false", "all"); err != nil {
		return q, nil, "", err
	}
	if v.Has("sort") {
		if !sortRe.MatchString(v.Get("sort")) {
			return q, nil, "", invalid("sort", "Invalid")
		}
		q.Sort = v.Get("sort")
	}
	if q.Order, err = queryEnum(r, "order", "asc", "desc"); err != nil {
		return q, nil, "", err
	}
	if q.Limit, _, err = queryInt(r, "limit", 1, 500, 0, false); err != nil {
		return q, nil, "", err
	}
	if q.Offset, _, err = queryInt(r, "offset", 0, 1<<31-1, 0, false); err != nil {
		return q, nil, "", err
	}
	return q, omit, v.Get("q"), nil
}

func stringList(m map[string]any, k string, min, max int) ([]string, error) {
	raw, isArr := m[k].([]any)
	if !isArr {
		if _, has := m[k]; !has || m[k] == nil {
			return nil, invalid(k, "Required")
		}
		return nil, invalid(k, "Expected array, received %s", jsType(m[k]))
	}
	if len(raw) < min {
		return nil, invalid(k, "Array must contain at least %d element(s)", min)
	}
	if max > 0 && len(raw) > max {
		return nil, invalid(k, "Array must contain at most %d element(s)", max)
	}
	out := make([]string, len(raw))
	for i, x := range raw {
		s, isStr := x.(string)
		if !isStr {
			return nil, invalid(k, "Expected string at %d, received %s", i, jsType(x))
		}
		out[i] = s
	}
	return out, nil
}

// parseCategories checks a categories object: every collection maps to a list of {name, color?}.
func parseCategories(m map[string]any) (domain.Categories, error) {
	out := domain.Categories{}
	for k, v := range m {
		list, isArr := v.([]any)
		if !isArr {
			return nil, invalid(k, "Expected array, received %s", jsType(v))
		}
		opts := make([]domain.Option, 0, len(list))
		for _, x := range list {
			o, isObj := x.(map[string]any)
			if !isObj {
				return nil, invalid(k, "Expected object, received %s", jsType(x))
			}
			name, err := reqString(o, "name", 1, 0)
			if err != nil {
				return nil, err
			}
			color, _, err := optString(o, "color")
			if err != nil {
				return nil, err
			}
			opts = append(opts, domain.Option{Name: name, Color: color})
		}
		out[k] = opts
	}
	return out, nil
}

// parseSkills checks a skills list, filling the defaults (weight 1, no aliases).
func parseSkills(b any) ([]domain.SkillDomain, error) {
	list, isArr := b.([]any)
	if !isArr {
		return nil, invalid("", "Expected array, received %s", jsType(b))
	}
	out := make([]domain.SkillDomain, 0, len(list))
	for _, x := range list {
		o, isObj := x.(map[string]any)
		if !isObj {
			return nil, invalid("", "Expected object, received %s", jsType(x))
		}
		name, err := reqString(o, "name", 1, 0)
		if err != nil {
			return nil, err
		}
		d := domain.SkillDomain{Name: name, Weight: 1, Aliases: []string{}}
		if v, has := o["weight"]; has && v != nil {
			f, isNum := v.(float64)
			if !isNum || f < 0 || f > 10 {
				return nil, invalid("weight", "Expected a number from 0 to 10")
			}
			d.Weight = f
		}
		if _, has := o["aliases"]; has && o["aliases"] != nil {
			if d.Aliases, err = stringList(o, "aliases", 0, 0); err != nil {
				return nil, err
			}
		}
		out = append(out, d)
	}
	return out, nil
}

type templateSection struct {
	Key   string  `json:"key"`
	Title string  `json:"title"`
	Hint  *string `json:"hint,omitempty"`
}

type template struct {
	Name     string            `json:"name"`
	Sections []templateSection `json:"sections"`
}

// parseTemplate checks a template: {name, sections: [{key, title, hint?}]}; other keys are dropped.
func parseTemplate(m map[string]any) (template, error) {
	name, err := reqString(m, "name", 0, 0)
	if err != nil {
		return template{}, err
	}
	list, isArr := m["sections"].([]any)
	if !isArr {
		return template{}, invalid("sections", "Expected array, received %s", jsType(m["sections"]))
	}
	t := template{Name: name, Sections: make([]templateSection, 0, len(list))}
	for _, x := range list {
		o, isObj := x.(map[string]any)
		if !isObj {
			return template{}, invalid("sections", "Expected object, received %s", jsType(x))
		}
		key, err := reqString(o, "key", 0, 0)
		if err != nil {
			return template{}, err
		}
		title, err := reqString(o, "title", 0, 0)
		if err != nil {
			return template{}, err
		}
		sec := templateSection{Key: key, Title: title}
		if h, has, err := optString(o, "hint"); err != nil {
			return template{}, err
		} else if has {
			sec.Hint = &h
		}
		t.Sections = append(t.Sections, sec)
	}
	return t, nil
}
