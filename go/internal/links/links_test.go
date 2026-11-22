package links

import (
	"reflect"
	"sort"
	"testing"

	"github.com/reeper316099/Cipherkeep/go/internal/domain"
	"github.com/reeper316099/Cipherkeep/go/internal/jsonstore"
	"github.com/reeper316099/Cipherkeep/go/internal/repo"
)

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

type env struct {
	repos map[string]repo.Repo
	ix    *Index
}

func setup(t *testing.T) env {
	t.Helper()
	dir := t.TempDir()
	st := jsonstore.NewStore(dir, nil, nil)
	repos := map[string]repo.Repo{}
	for _, c := range domain.Collections {
		var r repo.Repo
		if c == "notes" {
			r = repo.NewNoteRepo(dir, false, nil)
		} else {
			r = repo.NewJSONRepo(st, c)
		}
		if err := r.Init(); err != nil {
			t.Fatal(err)
		}
		repos[c] = r
	}
	return env{repos, New(repos)}
}

func refs(rs []Ref) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Type + ":" + r.Title + ":" + r.Kind
	}
	sort.Strings(out)
	return out
}

func TestBacklinksCombineRelationsAndResolvedWikiLinksIgnoringCodeAndGlobClasses(t *testing.T) {
	e := setup(t)
	ls := must(e.repos["commands"].Create(map[string]any{"name": "ls", "category": "File System"}, ""))
	tree := must(e.repos["concepts"].Create(map[string]any{
		"title":             "Directory Tree",
		"sections":          []any{map[string]any{"key": "a", "title": "A", "markdown": "see [[cmd:ls]] and [[:upper:]]"}},
		"relatedCommandIds": []any{ls.ID()},
	}, ""))
	note := must(e.repos["notes"].Create(map[string]any{"title": "Cheat", "markdown": "uses [[Directory Tree]] and [[Nope]] and `[[ls]]`"}, ""))
	must(e.repos["labs"].Create(map[string]any{"title": "Bandit 1", "platform": "Bandit", "commandIds": []any{ls.ID()}}, ""))

	if got := refs(e.ix.Backlinks(ls.ID())); !reflect.DeepEqual(got, []string{"concepts:Directory Tree:relation", "labs:Bandit 1:relation"}) {
		t.Fatalf("%v", got)
	}
	if got := refs(e.ix.Backlinks(tree.ID())); !reflect.DeepEqual(got, []string{"notes:Cheat:wikilink"}) {
		t.Fatalf("%v", got)
	}
	if got := refs(e.ix.Outgoing(note.ID())); !reflect.DeepEqual(got, []string{"concepts:Directory Tree:wikilink"}) {
		t.Fatalf("[[Nope]] is unresolved and `[[ls]]` is code: %v", got)
	}

	g := e.ix.Graph(GraphOptions{})
	var titles []string
	for _, n := range g.Nodes {
		titles = append(titles, n.Title)
	}
	sort.Strings(titles)
	if !reflect.DeepEqual(titles, []string{"Bandit 1", "Cheat", "Directory Tree", "ls"}) {
		t.Fatalf("%v", titles)
	}
	if len(g.Edges) != 3 { // concept -> ls counted once although linked both ways
		t.Fatalf("%d edges: %+v", len(g.Edges), g.Edges)
	}
	if g2 := e.ix.Graph(GraphOptions{Types: []string{"commands", "labs"}}); len(g2.Edges) != 1 {
		t.Fatalf("typed graph: %+v", g2.Edges)
	}

	// Links stay fresh after edits.
	if err := e.repos["concepts"].Remove(tree.ID()); err != nil {
		t.Fatal(err)
	}
	if len(e.ix.Backlinks(ls.ID())) != 1 {
		t.Fatal("backlinks after a delete")
	}
	hasCheat := func(g Graph) bool {
		for _, n := range g.Nodes {
			if n.Title == "Cheat" {
				return true
			}
		}
		return false
	}
	if hasCheat(e.ix.Graph(GraphOptions{})) || !hasCheat(e.ix.Graph(GraphOptions{IncludeOrphans: true})) {
		t.Fatal("orphans are hidden unless asked for")
	}
}

func TestRelationBeatsWikilinkAndArchivedEntitiesAreSkipped(t *testing.T) {
	e := setup(t)
	a := must(e.repos["commands"].Create(map[string]any{"name": "alpha"}, ""))
	b := must(e.repos["concepts"].Create(map[string]any{
		"title": "B", "relatedCommandIds": []any{a.ID()},
		"sections": []any{map[string]any{"key": "k", "title": "K", "markdown": "[[cmd:alpha]]"}},
	}, ""))
	if got := refs(e.ix.Outgoing(b.ID())); !reflect.DeepEqual(got, []string{"commands:alpha:relation"}) {
		t.Fatalf("a relation wins over a wiki link to the same target: %v", got)
	}
	must(e.repos["commands"].Patch(a.ID(), map[string]any{"archived": true}))
	if len(e.ix.Outgoing(b.ID())) != 0 {
		t.Fatal("links to archived entities are dropped")
	}
}

func TestTextOfPicksTheMarkdownFieldsOfEachCollection(t *testing.T) {
	if TextOf("commands", domain.Entity{"whatItDoes": "a", "notes": "b"}) != "a\nb" ||
		TextOf("notes", domain.Entity{"markdown": "m"}) != "m" ||
		TextOf("snippets", domain.Entity{"description": "d"}) != "d" ||
		TextOf("log", domain.Entity{"keyTakeaway": "k", "body": "b"}) != "k\nb" {
		t.Fatal("TextOf")
	}
}
