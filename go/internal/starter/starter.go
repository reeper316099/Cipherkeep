// Package starter is the starter pack that ships inside the app: 45 commands, 10 concepts, 4 notes and 3 labs, all
// original MIT-licensed content. Planning it produces an ordinary import plan, so loading it is idempotent, never
// overwrites edits and can be undone. The JSON is embedded in the binary; a test keeps it equal to ../../starter-pack.
package starter

import (
	"embed"
	"encoding/json"
	"strings"

	"github.com/reeper316099/Cipherkeep/go/internal/domain"
	"github.com/reeper316099/Cipherkeep/go/internal/importer"
	"github.com/reeper316099/Cipherkeep/go/internal/notion"
)

//go:embed data/*.json
var files embed.FS

// Tag marks everything the starter pack created, and RefPrefix starts every import reference it uses.
const (
	Tag       = "starter"
	RefPrefix = "starter:"
)

type rawCommand struct {
	Name        string     `json:"name"`
	Category    string     `json:"category"`
	Platforms   []string   `json:"platforms"`
	Risk        string     `json:"risk"`
	Syntax      string     `json:"syntax"`
	Explanation string     `json:"explanation"`
	WhatItDoes  string     `json:"whatItDoes"`
	Arguments   [][]string `json:"arguments"`
	Options     [][]string `json:"options"`
	Examples    [][]string `json:"examples"`
	Notes       string     `json:"notes"`
}

type rawConcept struct {
	Title           string           `json:"title"`
	Category        string           `json:"category"`
	Difficulty      string           `json:"difficulty"`
	Tags            []string         `json:"tags"`
	RelatedCommands []string         `json:"relatedCommands"`
	RelatedConcepts []string         `json:"relatedConcepts"`
	Sections        []map[string]any `json:"sections"`
}

type rawNote struct {
	Title    string   `json:"title"`
	Folder   string   `json:"folder"`
	Kind     string   `json:"kind"`
	Tags     []string `json:"tags"`
	Markdown string   `json:"markdown"`
}

type rawLab struct {
	Title      string   `json:"title"`
	Platform   string   `json:"platform"`
	Difficulty string   `json:"difficulty"`
	Domain     string   `json:"domain"`
	Commands   []string `json:"commands"`
	Concepts   []string `json:"concepts"`
	Writeup    string   `json:"writeup"`
}

func load(name string, into any) {
	b, err := files.ReadFile("data/" + name + ".json")
	if err == nil {
		err = json.Unmarshal(b, into)
	}
	if err != nil {
		panic("starter pack " + name + ": " + err.Error()) // the data is embedded at build time, so this is a build bug
	}
}

var (
	commands []rawCommand
	concepts []rawConcept
	notes    []rawNote
	labs     []rawLab
)

func init() {
	load("commands", &commands)
	load("concepts", &concepts)
	load("notes", &notes)
	load("labs", &labs)
}

// Counts reports what the pack contains.
func Counts() map[string]int {
	return map[string]int{"commands": len(commands), "concepts": len(concepts), "notes": len(notes), "labs": len(labs)}
}

func ref(c, key string) string { return RefPrefix + c + ":" + key }

func anys(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

func pairs(in [][]string, a, b string) []any {
	out := make([]any, len(in))
	for i, p := range in {
		x, y := "", ""
		if len(p) > 0 {
			x = p[0]
		}
		if len(p) > 1 {
			y = p[1]
		}
		out[i] = map[string]any{a: x, b: y}
	}
	return out
}

func targets(names []string, m map[string]string) []importer.RelTarget {
	out := []importer.RelTarget{}
	for _, n := range names {
		if r, ok := m[n]; ok {
			out = append(out, importer.RelTarget{Ref: r})
		}
	}
	return out
}

func rels(r ...importer.Relation) []importer.Relation {
	out := []importer.Relation{}
	for _, x := range r {
		if len(x.Targets) > 0 {
			out = append(out, x)
		}
	}
	return out
}

// Plan builds the import plan for the whole pack.
func Plan() *importer.Plan {
	plan := &importer.Plan{Kind: "json-backup", SourceName: "Cipherkeep starter pack", Items: []importer.PlanItem{},
		Templates: []importer.PlanTemplate{}, Attachments: []importer.PlanAttachment{}, Databases: []importer.PlanDatabase{}, Warnings: []importer.PlanWarning{}}
	cmdRefs, conceptRefs := map[string]string{}, map[string]string{}

	for _, c := range commands {
		r := ref("commands", c.Name)
		cmdRefs[c.Name] = r
		ph := []string{c.Syntax}
		for _, e := range c.Examples {
			ph = append(ph, e[0])
		}
		plan.Items = append(plan.Items, importer.PlanItem{Collection: "commands", Ref: r, Title: c.Name, Source: "starter-pack/commands.json",
			Warnings: []string{}, Relations: []importer.Relation{}, Input: map[string]any{
				"name": c.Name, "title": c.Name, "category": c.Category, "platforms": anys(c.Platforms), "risk": c.Risk, "syntax": c.Syntax,
				"explanation": c.Explanation, "whatItDoes": c.WhatItDoes, "arguments": pairs(c.Arguments, "name", "meaning"),
				"options": pairs(c.Options, "flag", "meaning"), "examples": pairs(c.Examples, "command", "description"),
				"placeholders": anys(notion.DetectPlaceholders(ph...)), "notes": c.Notes, "tags": []any{Tag},
				"extras": map[string]any{"source": "starter-pack"},
			}})
	}

	for _, k := range concepts {
		conceptRefs[k.Title] = ref("concepts", k.Title)
	}
	for _, k := range concepts {
		q := ""
		sections := make([]any, len(k.Sections))
		for i, s := range k.Sections {
			sections[i] = s
			if s["key"] == domain.QuestionsKey {
				q, _ = s["markdown"].(string)
			}
		}
		plan.Items = append(plan.Items, importer.PlanItem{Collection: "concepts", Ref: conceptRefs[k.Title], Title: k.Title, Source: "starter-pack/concepts.json",
			Warnings: []string{}, Input: map[string]any{
				"title": k.Title, "category": k.Category, "difficulty": k.Difficulty, "tags": anys(append([]string{Tag}, k.Tags...)),
				"sections": sections, "questionsOpen": domain.CountOpen(q), "extras": map[string]any{"source": "starter-pack"},
			},
			Relations: rels(
				importer.Relation{Field: "relatedCommandIds", Targets: targets(k.RelatedCommands, cmdRefs)},
				importer.Relation{Field: "relatedConceptIds", Targets: targets(k.RelatedConcepts, conceptRefs)})})
	}

	for _, n := range notes {
		segs := strings.Split(n.Folder, "/")
		for i, s := range segs {
			segs[i] = domain.Slugify(s)
		}
		tags := []string{Tag}
		for _, t := range n.Tags {
			if t != Tag {
				tags = append(tags, t)
			}
		}
		plan.Items = append(plan.Items, importer.PlanItem{Collection: "notes", Ref: ref("notes", n.Title), Title: n.Title, Source: "starter-pack/notes.json",
			Warnings: []string{}, Relations: []importer.Relation{}, Input: map[string]any{
				"title": n.Title, "folder": strings.Join(segs, "/"), "kind": n.Kind, "tags": anys(tags), "markdown": n.Markdown,
			}})
	}

	for _, l := range labs {
		plan.Items = append(plan.Items, importer.PlanItem{Collection: "labs", Ref: ref("labs", l.Title), Title: l.Title, Source: "starter-pack/labs.json",
			Warnings: []string{}, Input: map[string]any{
				"title": l.Title, "platform": l.Platform, "category": l.Platform, "difficulty": l.Difficulty, "domain": l.Domain,
				"status": "Not Started", "writeup": l.Writeup, "tags": []any{Tag},
			},
			Relations: rels(
				importer.Relation{Field: "commandIds", Targets: targets(l.Commands, cmdRefs)},
				importer.Relation{Field: "conceptIds", Targets: targets(l.Concepts, conceptRefs)})})
	}
	return plan
}
