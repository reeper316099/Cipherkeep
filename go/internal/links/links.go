// Package links computes how entities connect: explicit relation fields, typed links, and [[wiki links]] found in
// Markdown. It serves backlinks, outgoing links and the graph.
package links

import (
	"strings"
	"sync"

	"github.com/reeper316099/Cipherkeep/go/internal/domain"
	"github.com/reeper316099/Cipherkeep/go/internal/repo"
)

// Edge connects two entities. Kind is "relation" or "wikilink".
type Edge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Kind   string `json:"kind"`
}

// GraphNode is one entity in the graph.
type GraphNode struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Title    string `json:"title"`
	Category string `json:"category"`
	Degree   int    `json:"degree"`
}

// Ref is a linked entity as backlinks and outgoing links list it.
type Ref struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title"`
	Kind  string `json:"kind"`
}

type relation struct{ field, target string }

// relations lists, per collection, the id-list fields and the collection each points at.
var relations = map[string][]relation{
	"concepts": {{"relatedCommandIds", "commands"}, {"relatedConceptIds", "concepts"}},
	"labs":     {{"commandIds", "commands"}, {"conceptIds", "concepts"}, {"scriptIds", "snippets"}},
	"projects": {{"conceptIds", "concepts"}},
	"log":      {{"conceptIds", "concepts"}, {"commandIds", "commands"}, {"labIds", "labs"}},
}

// TextOf is the Markdown-bearing text of an entity, scanned for [[wiki links]].
func TextOf(typ string, e domain.Entity) string {
	switch typ {
	case "commands":
		return e.Str("whatItDoes") + "\n" + e.Str("notes")
	case "concepts":
		var parts []string
		for _, s := range e.Objs("sections") {
			if md, ok := s["markdown"].(string); ok {
				parts = append(parts, md)
			}
		}
		return strings.Join(parts, "\n")
	case "labs":
		return e.Str("writeup")
	case "projects":
		return e.Str("notes")
	case "log":
		return e.Str("keyTakeaway") + "\n" + e.Str("body")
	case "notes":
		return e.Str("markdown")
	}
	return e.Str("description")
}

// Index is the link index. It is rebuilt on demand and cached until a repository changes.
type Index struct {
	repos map[string]repo.Repo

	mu       sync.Mutex
	dirty    bool
	edges    []Edge
	nodes    map[string]*GraphNode
	order    []string // node ids in collection order
	incoming map[string][]Edge
}

// New creates an index over the repositories and watches them for changes.
func New(repos map[string]repo.Repo) *Index {
	ix := &Index{repos: repos, dirty: true}
	mark := func() { ix.mu.Lock(); ix.dirty = true; ix.mu.Unlock() }
	for _, c := range domain.Collections {
		if r := repos[c]; r != nil {
			r.OnUpsert(func(domain.Entity) { mark() })
			r.OnRemove(func(string) { mark() })
			r.OnRebuild(mark)
		}
	}
	return ix
}

func (ix *Index) build() {
	if !ix.dirty {
		return
	}
	ix.dirty = false
	ix.nodes = map[string]*GraphNode{}
	ix.order = nil
	var entries []domain.TitleIndexEntry
	for _, typ := range domain.Collections {
		r := ix.repos[typ]
		if r == nil {
			continue
		}
		for _, e := range r.All() {
			if e.Archived() {
				continue
			}
			ix.nodes[e.ID()] = &GraphNode{ID: e.ID(), Type: typ, Title: e.Title(), Category: e.Str("category")}
			ix.order = append(ix.order, e.ID())
			entry := domain.TitleIndexEntry{Type: typ, ID: e.ID(), Title: e.Title()}
			if typ == "commands" {
				entry.Name = e.Str("name")
			}
			entries = append(entries, entry)
		}
	}
	titles := domain.BuildTitleIndex(entries)
	edges := map[string]int{}
	var list []Edge
	add := func(source, target, kind string) {
		if source == target || ix.nodes[target] == nil || ix.nodes[source] == nil {
			return
		}
		key := source + ">" + target
		if i, ok := edges[key]; !ok {
			edges[key] = len(list)
			list = append(list, Edge{source, target, kind})
		} else if list[i].Kind == "wikilink" && kind == "relation" {
			list[i].Kind = kind
		}
	}
	for _, typ := range domain.Collections {
		r := ix.repos[typ]
		if r == nil {
			continue
		}
		for _, e := range r.All() {
			if e.Archived() {
				continue
			}
			for _, rel := range relations[typ] {
				for _, id := range e.Strs(rel.field) {
					add(e.ID(), id, "relation")
				}
			}
			for _, l := range e.Objs("links") {
				if t, ok := l["targetId"].(string); ok {
					add(e.ID(), t, "relation")
				}
			}
			for _, w := range domain.ExtractWikiLinks(TextOf(typ, e)) {
				if _, id, ok := domain.ResolveWikiLink(titles, w); ok {
					add(e.ID(), id, "wikilink")
				}
			}
		}
	}
	ix.edges = list
	ix.incoming = map[string][]Edge{}
	for _, ed := range list {
		ix.nodes[ed.Source].Degree++
		ix.nodes[ed.Target].Degree++
		ix.incoming[ed.Target] = append(ix.incoming[ed.Target], ed)
	}
}

// Backlinks lists what points at id.
func (ix *Index) Backlinks(id string) []Ref {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ix.build()
	out := []Ref{}
	for _, e := range ix.incoming[id] {
		n := ix.nodes[e.Source]
		out = append(out, Ref{ID: n.ID, Type: n.Type, Title: n.Title, Kind: e.Kind})
	}
	return out
}

// Outgoing lists what id points at.
func (ix *Index) Outgoing(id string) []Ref {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ix.build()
	out := []Ref{}
	for _, e := range ix.edges {
		if e.Source == id {
			n := ix.nodes[e.Target]
			out = append(out, Ref{ID: n.ID, Type: n.Type, Title: n.Title, Kind: e.Kind})
		}
	}
	return out
}

// Graph is the nodes and edges to draw.
type Graph struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []Edge      `json:"edges"`
}

// GraphOptions narrow the graph.
type GraphOptions struct {
	Types          []string
	IncludeOrphans bool
}

// Graph returns the graph; orphans (nodes with no links) are left out unless asked for.
func (ix *Index) Graph(o GraphOptions) Graph {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ix.build()
	nodes := []GraphNode{}
	ids := map[string]bool{}
	for _, id := range ix.order {
		n := ix.nodes[id]
		typeOK := len(o.Types) == 0
		for _, t := range o.Types {
			typeOK = typeOK || t == n.Type
		}
		if typeOK && (o.IncludeOrphans || n.Degree > 0) {
			nodes = append(nodes, *n)
			ids[n.ID] = true
		}
	}
	edges := []Edge{}
	for _, e := range ix.edges {
		if ids[e.Source] && ids[e.Target] {
			edges = append(edges, e)
		}
	}
	return Graph{Nodes: nodes, Edges: edges}
}
