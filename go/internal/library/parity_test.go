package library

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/reeper316099/Cipherkeep/go/internal/domain"
	"github.com/reeper316099/Cipherkeep/go/internal/links"
	"github.com/reeper316099/Cipherkeep/go/internal/repo"
	"github.com/reeper316099/Cipherkeep/go/internal/search"
	"github.com/reeper316099/Cipherkeep/go/internal/study"
)

// Parity with the TypeScript implementation. fixtures/compat/parity/library is a realistic library (the starter pack plus
// labs, logs, notes, an archived item and a deterministic review history) and expected.json records what the TypeScript
// code returns for it (scripts/gen-parity-fixtures.ts). The Go code must return the same.

const parityRoot = "../../../fixtures/compat/parity"

type diffs struct{ list []string }

func (d *diffs) add(format string, args ...any) {
	if len(d.list) < 40 {
		d.list = append(d.list, fmt.Sprintf(format, args...))
	}
}

// compare walks two decoded JSON values. Numbers match within a relative tolerance; objects ignore key order.
func compare(d *diffs, path string, want, got any) {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			d.add("%s: want an object, got %T", path, got)
			return
		}
		for k, wv := range w {
			gv, present := g[k]
			if !present {
				if wv != nil {
					d.add("%s.%s: missing (want %v)", path, k, short(wv))
				}
				continue
			}
			compare(d, path+"."+k, wv, gv)
		}
		for k, gv := range g {
			if _, present := w[k]; !present && gv != nil {
				d.add("%s.%s: unexpected %v", path, k, short(gv))
			}
		}
	case []any:
		g, ok := got.([]any)
		if !ok {
			d.add("%s: want an array, got %T (%v)", path, got, short(got))
			return
		}
		if len(w) != len(g) {
			d.add("%s: want %d items, got %d", path, len(w), len(g))
		}
		for i := 0; i < len(w) && i < len(g); i++ {
			compare(d, fmt.Sprintf("%s[%d]", path, i), w[i], g[i])
		}
	case float64:
		g, ok := got.(float64)
		if !ok || math.Abs(w-g) > 1e-6*math.Max(1, math.Abs(w)) {
			d.add("%s: want %v, got %v", path, w, short(got))
		}
	default:
		if fmt.Sprint(want) != fmt.Sprint(got) {
			d.add("%s: want %v, got %v", path, short(want), short(got))
		}
	}
}

func short(v any) string {
	s := strings.ReplaceAll(fmt.Sprintf("%v", v), "\n", "⏎")
	if len(s) > 120 {
		s = s[:120] + "…"
	}
	return s
}

func generic(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func loadExpected(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(parityRoot, "expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func openParity(t *testing.T) *Library {
	t.Helper()
	dir := t.TempDir()
	copyTree(t, filepath.Join(parityRoot, "library"), dir)
	return open(t, dir)
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func report(t *testing.T, what string, d *diffs) {
	t.Helper()
	if len(d.list) > 0 {
		t.Errorf("%s differs from the TypeScript output:\n  %s", what, strings.Join(d.list, "\n  "))
	}
}

func TestParityEntitiesLoadAndListLikeTypeScript(t *testing.T) {
	lib := openParity(t)
	want := loadExpected(t)
	got := map[string]any{}
	titles := map[string]any{}
	for _, c := range domain.Collections {
		page := repo.Query(lib.Repos[c].All(), repo.ListQuery{Limit: 500, Archived: "all"})
		ids := make([]string, len(page.Items))
		for i, e := range page.Items {
			ids[i] = e.ID()
		}
		got[c] = ids
		def := repo.Query(lib.Repos[c].All(), repo.ListQuery{Limit: 500})
		ts := make([]string, len(def.Items))
		for i, e := range def.Items {
			ts[i] = e.Title()
		}
		titles[c] = ts
	}
	d := &diffs{}
	// Entities with the same title may list in either order; compare as sets within a title.
	compare(d, "lists", sortedWithin(want["lists"]), sortedWithin(generic(t, got)))
	compare(d, "defaultLists", want["defaultLists"], generic(t, titles))
	report(t, "listing", d)
	total := 0
	for _, c := range domain.Collections {
		total += len(lib.Repos[c].All())
	}
	if total < 80 {
		t.Fatalf("the parity library should be realistic, it has %d entities", total)
	}
}

// sortedWithin leaves list order alone (the order is part of the contract) but makes the structure comparable.
func sortedWithin(v any) any { return v }

func TestParitySearchMatchesTypeScript(t *testing.T) {
	lib := openParity(t)
	want := loadExpected(t)["search"].(map[string]any)
	var queries []string
	for q := range want {
		queries = append(queries, q)
	}
	sort.Strings(queries)
	for _, q := range queries {
		hits := lib.Search.Search(q, search.Options{Limit: 50})
		got := make([]any, len(hits))
		for i, h := range hits {
			got[i] = map[string]any{"type": h.Type, "id": h.ID, "title": h.Title, "score": h.Score, "highlight": h.Highlight}
		}
		d := &diffs{}
		wl := want[q].([]any)
		gl := generic(t, got).([]any)
		// Equal scores may come out in a different order; compare sets of (id, score) by score band first.
		if sameUnordered(wl, gl) {
			continue
		}
		compare(d, q, wl, gl)
		report(t, fmt.Sprintf("search %q", q), d)
	}
}

// sameUnordered reports whether two hit lists hold the same hits with the same scores, allowing different order only
// among hits whose scores are equal (the order of ties is not specified).
func sameUnordered(want, got []any) bool {
	if len(want) != len(got) {
		return false
	}
	key := func(h any) string {
		m := h.(map[string]any)
		return fmt.Sprintf("%v|%.5f|%v", m["id"], m["score"], m["highlight"])
	}
	wk, gk := make([]string, len(want)), make([]string, len(got))
	for i := range want {
		wk[i], gk[i] = key(want[i]), key(got[i])
	}
	scoreAt := func(l []any, i int) float64 { return l[i].(map[string]any)["score"].(float64) }
	for i := range want {
		if math.Abs(scoreAt(want, i)-scoreAt(got, i)) > 1e-5*math.Max(1, scoreAt(want, i)) {
			return false // the score sequence itself must agree
		}
	}
	sort.Strings(wk)
	sort.Strings(gk)
	for i := range wk {
		if wk[i] != gk[i] {
			return false
		}
	}
	return true
}

func TestParityGraphAndLinksMatchTypeScript(t *testing.T) {
	lib := openParity(t)
	want := loadExpected(t)
	g := lib.Links.Graph(links.GraphOptions{IncludeOrphans: true})
	d := &diffs{}
	wg := want["graph"].(map[string]any)
	// Nodes follow collection then entity order; compare as sets by id, edges as sets by source>target.
	nodes := func(v any) map[string]any {
		out := map[string]any{}
		for _, n := range v.([]any) {
			m := n.(map[string]any)
			out[m["id"].(string)] = m
		}
		return out
	}
	edges := func(v any) map[string]any {
		out := map[string]any{}
		for _, n := range v.([]any) {
			m := n.(map[string]any)
			out[m["source"].(string)+">"+m["target"].(string)] = m
		}
		return out
	}
	gg := generic(t, g).(map[string]any)
	compare(d, "graph.nodes", nodes(wg["nodes"]), nodes(gg["nodes"]))
	compare(d, "graph.edges", edges(wg["edges"]), edges(gg["edges"]))
	report(t, "graph", d)

	d = &diffs{}
	for id, w := range want["links"].(map[string]any) {
		wm := w.(map[string]any)
		gotBack := generic(t, lib.Links.Backlinks(id))
		gotOut := generic(t, lib.Links.Outgoing(id))
		refSet := func(v any) map[string]any {
			out := map[string]any{}
			for _, r := range v.([]any) {
				m := r.(map[string]any)
				out[m["id"].(string)] = m
			}
			return out
		}
		compare(d, "back."+id, refSet(wm["back"]), refSet(gotBack))
		compare(d, "out."+id, refSet(wm["out"]), refSet(gotOut))
	}
	report(t, "backlinks and outgoing links", d)
}

func TestParityStudyQueueStatsAndQuestionsMatchTypeScript(t *testing.T) {
	lib := openParity(t)
	want := loadExpected(t)
	now, _ := time.Parse(time.RFC3339Nano, want["now"].(string))
	for _, tc := range []struct {
		key string
		off int
	}{{"queue", 0}, {"queue300", 300}} {
		q, err := lib.Study.Queue(QueueOptions{Now: now, Limit: 80, TzOffset: tc.off})
		if err != nil {
			t.Fatal(err)
		}
		d := &diffs{}
		compare(d, tc.key, want[tc.key], generic(t, q))
		report(t, "study "+tc.key, d)
	}
	st, err := lib.Study.Stats(now, 0)
	if err != nil {
		t.Fatal(err)
	}
	d := &diffs{}
	compare(d, "stats", want["stats"], generic(t, st))
	compare(d, "questions", want["questions"], generic(t, lib.Study.Questions(true)))
	report(t, "study stats and questions", d)
}

func TestParityOverviewMatchesTypeScript(t *testing.T) {
	want := loadExpected(t)
	now, _ := time.Parse(time.RFC3339Nano, want["now"].(string))
	for _, tc := range []struct {
		key string
		off int
	}{{"overview", 0}, {"overview300", 300}} {
		lib := openParity(t) // a fresh copy each time: the dashboard writes a skill-history snapshot
		o, err := lib.BuildOverview(OverviewOptions{Now: now, TzOffset: tc.off})
		if err != nil {
			t.Fatal(err)
		}
		d := &diffs{}
		compare(d, tc.key, want[tc.key], generic(t, o))
		report(t, "dashboard "+tc.key, d)
	}
}

func TestParityScheduleSequencesMatchTypeScript(t *testing.T) {
	want := loadExpected(t)["scheduleSample"].([]any)
	now, _ := time.Parse(time.RFC3339Nano, loadExpected(t)["now"].(string))
	ratings := []study.Rating{"good", "good", "easy", "hard", "again", "good", "easy"}
	for n, seq := range want {
		s := study.NewSrs()
		var got []any
		for _, r := range ratings[n:] {
			s = study.Schedule(s, r, now)
			got = append(got, generic(t, s))
		}
		d := &diffs{}
		compare(d, fmt.Sprintf("sequence%d", n), seq, got)
		report(t, "SM-2 schedule", d)
	}
}
