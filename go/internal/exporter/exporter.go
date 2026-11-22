// Package exporter writes a library out: an Obsidian-style Markdown vault (zip), CSV per collection, and a full JSON
// backup. It is the Go port of packages/server/src/export.ts. Lab writeups flagged private are left out unless asked
// for.
package exporter

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/reeper316099/Cipherkeep/go/internal/domain"
	"github.com/reeper316099/Cipherkeep/go/internal/importer"
	"github.com/reeper316099/Cipherkeep/go/internal/jsonstore"
	"github.com/reeper316099/Cipherkeep/go/internal/library"
	"github.com/reeper316099/Cipherkeep/go/internal/repo"
)

// Options choose what an export includes.
type Options struct {
	// IncludePrivate includes lab writeups flagged writeupPrivate (left out by default).
	IncludePrivate bool
}

const privateNote = "_Private writeup omitted from this export._"

func str(e map[string]any, k string) string {
	if s, ok := e[k].(string); ok {
		return s
	}
	return ""
}

func list(v any) []any {
	l, _ := v.([]any)
	return l
}

func objs(v any) []map[string]any {
	var out []map[string]any
	for _, x := range list(v) {
		if m, ok := x.(map[string]any); ok {
			out = append(out, m)
		} else {
			out = append(out, map[string]any{})
		}
	}
	return out
}

func withheld(e repo.Entity, o Options) bool { return e["writeupPrivate"] == true && !o.IncludePrivate }

var (
	unsafeName = regexp.MustCompile(`[\\/:*?"<>|#^\[\]]`)
	spaceRun   = regexp.MustCompile(`[\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]+`)
)

func safeName(s string) string {
	s = domain.JSTrim(spaceRun.ReplaceAllString(unsafeName.ReplaceAllString(s, "-"), " "))
	if s == "" {
		return "untitled"
	}
	return s
}

// missing marks a field the entity does not have (JavaScript's undefined), which frontmatter leaves out.
type missing struct{}

type kv struct {
	k string
	v any
}

func field(e map[string]any, k string) any {
	if v, ok := e[k]; ok {
		return v
	}
	return missing{}
}

// fm writes YAML frontmatter, leaving out missing fields, empty strings and empty lists. Key order is the order given.
func fm(pairs ...kv) string {
	m := &yaml.Node{Kind: yaml.MappingNode}
	for _, p := range pairs {
		switch v := p.v.(type) {
		case missing:
			continue
		case string:
			if v == "" {
				continue
			}
		case []any:
			if len(v) == 0 {
				continue
			}
		case []string:
			if len(v) == 0 {
				continue
			}
		}
		var val yaml.Node
		if err := val.Encode(normalise(p.v)); err != nil {
			continue
		}
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: p.k}, &val)
	}
	if len(m.Content) == 0 {
		return "---\n{}\n---\n\n"
	}
	out, _ := yaml.Marshal(m)
	return "---\n" + strings.TrimRight(string(out), "\n") + "\n---\n\n"
}

// normalise turns whole-number floats into ints so YAML prints 3, not 3.
func normalise(v any) any {
	if f, ok := v.(float64); ok && f == float64(int64(f)) {
		return int64(f)
	}
	return v
}

func (w *writer) wiki(c string, ids any) []any {
	out := []any{}
	for _, id := range list(ids) {
		s, _ := id.(string)
		if e, ok := w.lib.Repos[c].Get(s); ok {
			out = append(out, "[["+str(e, "title")+"]]")
		}
	}
	return out
}

var (
	pipe    = regexp.MustCompile(`\|`)
	newline = regexp.MustCompile(`\n`)
)

func cell(s string) string { return newline.ReplaceAllString(pipe.ReplaceAllString(s, `\|`), " ") }

func commandMd(e repo.Entity) string {
	opts, args, exs := objs(e["options"]), objs(e["arguments"]), objs(e["examples"])
	parts := []string{
		fm(kv{"ck_collection", "commands"}, kv{"ck_id", field(e, "id")}, kv{"title", field(e, "title")}, kv{"category", field(e, "category")},
			kv{"tags", field(e, "tags")}, kv{"risk", field(e, "risk")}, kv{"platforms", field(e, "platforms")}, kv{"syntax", field(e, "syntax")},
			kv{"explanation", field(e, "explanation")}),
		"## \U0001F4CC What it does", "", str(e, "whatItDoes"), "", "---", "", "## ⚙️ Basic Syntax", "", str(e, "syntax"), "",
	}
	if len(args) > 0 {
		parts = append(parts, "---", "", "## \U0001F9E9 Arguments", "")
		for _, a := range args {
			parts = append(parts, "- "+str(a, "name")+" → "+str(a, "meaning"))
		}
		parts = append(parts, "")
	}
	if len(opts) > 0 {
		parts = append(parts, "---", "", "## \U0001F527 Common Options (Flags)", "", "| Option | Meaning |", "| --- | --- |")
		for _, o := range opts {
			parts = append(parts, "| "+cell(str(o, "flag"))+" | "+cell(str(o, "meaning"))+" |")
		}
		parts = append(parts, "")
	}
	if len(exs) > 0 {
		parts = append(parts, "---", "", "## \U0001F4A1 Examples", "", "```bash")
		for _, x := range exs {
			if d := str(x, "description"); d != "" {
				parts = append(parts, str(x, "command")+" = \""+d+"\"")
			} else {
				parts = append(parts, str(x, "command"))
			}
		}
		parts = append(parts, "```", "")
	}
	if n := str(e, "notes"); n != "" {
		parts = append(parts, "", n, "")
	}
	return strings.Join(parts, "\n")
}

func (w *writer) conceptMd(e repo.Entity) string {
	var secs []string
	for _, s := range objs(e["sections"]) {
		secs = append(secs, "## "+str(s, "title")+"\n\n"+str(s, "markdown"))
	}
	return fm(kv{"ck_collection", "concepts"}, kv{"ck_id", field(e, "id")}, kv{"title", field(e, "title")}, kv{"category", field(e, "category")},
		kv{"difficulty", field(e, "difficulty")}, kv{"tags", field(e, "tags")},
		kv{"related_commands", w.wiki("commands", e["relatedCommandIds"])}, kv{"related_concepts", w.wiki("concepts", e["relatedConceptIds"])}) +
		strings.Join(secs, "\n\n---\n\n") + "\n"
}

type writer struct {
	lib *library.Library
	o   Options
}

func (w *writer) entityFile(c string, e repo.Entity) (rel, text string) {
	name := safeName(str(e, "title"))
	switch c {
	case "commands":
		n := str(e, "name")
		if n == "" {
			n = str(e, "title")
		}
		return "commands/" + safeName(n) + ".md", commandMd(e)
	case "concepts":
		return "concepts/" + name + ".md", w.conceptMd(e)
	case "labs":
		platform := str(e, "platform")
		if platform == "" {
			platform = "Other"
		}
		body := str(e, "writeup")
		if withheld(e, w.o) {
			body = privateNote
		}
		return "labs/" + safeName(platform) + "/" + name + ".md",
			fm(kv{"ck_collection", "labs"}, kv{"ck_id", field(e, "id")}, kv{"title", field(e, "title")}, kv{"platform", field(e, "platform")},
				kv{"status", field(e, "status")}, kv{"difficulty", field(e, "difficulty")}, kv{"domain", field(e, "domain")}, kv{"points", field(e, "points")},
				kv{"solvedAt", field(e, "solvedAt")}, kv{"timeSpentMin", field(e, "timeSpentMin")}, kv{"hintsUsed", field(e, "hintsUsed")},
				kv{"tags", field(e, "tags")}, kv{"commands", w.wiki("commands", e["commandIds"])}, kv{"concepts", w.wiki("concepts", e["conceptIds"])}) +
				body + "\n"
	case "projects":
		return "projects/" + name + ".md",
			fm(kv{"ck_collection", "projects"}, kv{"ck_id", field(e, "id")}, kv{"title", field(e, "title")}, kv{"type", field(e, "type")},
				kv{"status", field(e, "status")}, kv{"skills", field(e, "skillsUsed")}, kv{"repo", field(e, "repoUrl")}, kv{"tags", field(e, "tags")},
				kv{"concepts", w.wiki("concepts", e["conceptIds"])}) + str(e, "notes") + "\n"
	case "log":
		d := str(e, "date")
		if d == "" {
			d = name
		}
		return "log/" + d + ".md",
			fm(kv{"ck_collection", "log"}, kv{"ck_id", field(e, "id")}, kv{"title", field(e, "title")}, kv{"date", field(e, "date")},
				kv{"timeSpentMin", field(e, "timeSpentMin")}, kv{"timeSpent", field(e, "timeSpentRaw")}, kv{"topics", field(e, "topics")},
				kv{"takeaway", field(e, "keyTakeaway")}, kv{"commands", w.wiki("commands", e["commandIds"])}, kv{"concepts", w.wiki("concepts", e["conceptIds"])}) +
				str(e, "body") + "\n"
	case "notes":
		folder := ""
		if f := str(e, "folder"); f != "" {
			folder = f + "/"
		}
		slug := str(e, "slug")
		if slug == "" {
			slug = name
		}
		return "notes/" + folder + slug + ".md",
			fm(kv{"ck_collection", "notes"}, kv{"ck_id", field(e, "id")}, kv{"title", field(e, "title")}, kv{"category", field(e, "category")},
				kv{"tags", field(e, "tags")}, kv{"kind", field(e, "kind")}) + str(e, "markdown")
	}
	text = fm(kv{"ck_collection", "snippets"}, kv{"ck_id", field(e, "id")}, kv{"title", field(e, "title")}, kv{"language", field(e, "language")},
		kv{"file", field(e, "filePath")}, kv{"tags", field(e, "tags")})
	if d := str(e, "description"); d != "" {
		text += d + "\n\n"
	}
	if code := str(e, "code"); code != "" {
		text += "```" + str(e, "language") + "\n" + code + "\n```\n"
	}
	return "snippets/" + name + ".md", text
}

// relinkAttachments turns ck://attachments/x/y into a path relative to the Markdown file, so it works in Obsidian.
func relinkAttachments(text, fileRel string) string {
	depth := strings.Count(fileRel, "/")
	up := "./"
	if depth > 0 {
		up = strings.Repeat("../", depth)
	}
	return strings.ReplaceAll(text, "ck://attachments/", up+"attachments/")
}

// Markdown builds the vault zip.
func Markdown(lib *library.Library, o Options) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name string, data []byte) error {
		f, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = f.Write(data)
		return err
	}
	w := &writer{lib: lib, o: o}
	used := map[string]bool{}
	for _, c := range domain.Collections {
		for _, e := range lib.Repos[c].All() {
			rel, text := w.entityFile(c, e)
			final := rel
			for n := 2; used[strings.ToLower(final)]; n++ {
				final = strings.TrimSuffix(rel, ".md") + " (" + strconv.Itoa(n) + ").md"
			}
			used[strings.ToLower(final)] = true
			if err := add(final, []byte(relinkAttachments(text, final))); err != nil {
				return nil, err
			}
		}
	}
	root := filepath.Join(lib.Dir, "attachments")
	var files []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)
	for _, p := range files {
		data, err := jsonstore.ReadDecoded(lib.Codec, p)
		if err != nil {
			return nil, err
		}
		rel, _ := filepath.Rel(root, p)
		if err := add("attachments/"+filepath.ToSlash(rel), data); err != nil {
			return nil, err
		}
	}
	readme := "# " + domain.AppName + " export\n\nObsidian-compatible vault. Files carry `ck_collection`/`ck_id` frontmatter so " + domain.AppName + " can re-import them.\n"
	if err := add("README.md", []byte(readme)); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

var formulaStart = regexp.MustCompile(`^[=+\-@\t\r]`)

// GuardCell prefixes a cell that a spreadsheet could read as a formula with `'`; the CSV importer strips it again.
func GuardCell(s string) string {
	if formulaStart.MatchString(s) {
		return "'" + s
	}
	return s
}

// CSVColumns are the columns of a collection's CSV: its schema fields in order.
func CSVColumns(c string) []string {
	s := domain.Schemas[c]
	out := make([]string, len(s.Fields))
	for i, f := range s.Fields {
		out[i] = f.Name
	}
	return out
}

// jsonOf is JSON.stringify for an entity value: object keys follow the schema's field order, as Zod keeps them.
func jsonOf(v any, f *domain.Field) string {
	var b bytes.Buffer
	writeJSON(&b, v, f)
	return b.String()
}

func writeJSON(b *bytes.Buffer, v any, f *domain.Field) {
	switch x := v.(type) {
	case map[string]any:
		b.WriteByte('{')
		var keys []string
		if f != nil && f.Kind == domain.KObject {
			for _, sf := range f.Fields {
				if _, ok := x[sf.Name]; ok {
					keys = append(keys, sf.Name)
				}
			}
		}
		known := map[string]bool{}
		for _, k := range keys {
			known[k] = true
		}
		var rest []string
		for k := range x {
			if !known[k] {
				rest = append(rest, k)
			}
		}
		sort.Strings(rest)
		keys = append(keys, rest...)
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(jsQuote(k))
			b.WriteByte(':')
			var sub *domain.Field
			if f != nil && f.Kind == domain.KObject {
				for j := range f.Fields {
					if f.Fields[j].Name == k {
						sub = &f.Fields[j]
					}
				}
			}
			writeJSON(b, x[k], sub)
		}
		b.WriteByte('}')
	case []any:
		b.WriteByte('[')
		var item *domain.Field
		if f != nil && f.Kind == domain.KList {
			item = f.Item
		}
		for i, e := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJSON(b, e, item)
		}
		b.WriteByte(']')
	case nil:
		b.WriteString("null")
	case string:
		b.WriteString(jsQuote(x))
	case bool:
		b.WriteString(strconv.FormatBool(x))
	case float64, int, int64:
		b.WriteString(importer.JSString(x))
	default:
		raw, _ := json.Marshal(v)
		b.Write(raw)
	}
}

// jsQuote is JSON.stringify for a string: only quotes, backslashes and control characters are escaped (Go's encoder
// also escapes <, > and &, which JavaScript does not).
func jsQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\f':
			b.WriteString(`\f`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20:
			b.WriteString(`\u00`)
			b.WriteByte("0123456789abcdef"[r>>4])
			b.WriteByte("0123456789abcdef"[r&0xf])
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// CSV exports one collection as CSV with a header row. Cells that could be read as formulas are guarded.
func CSV(lib *library.Library, c string, o Options) string {
	schema := domain.Schemas[c]
	cols := CSVColumns(c)
	var rows [][]string
	for _, e := range lib.Repos[c].All() {
		row := make([]string, len(schema.Fields))
		for i := range schema.Fields {
			f := &schema.Fields[i]
			v := e[f.Name]
			if c == "labs" && f.Name == "writeup" && withheld(e, o) {
				v = ""
			}
			if v == nil {
				continue
			}
			var s string
			switch importer.KindOf(*f) {
			case importer.KindStrings:
				parts := make([]string, 0)
				for _, x := range list(v) {
					parts = append(parts, importer.JSString(x))
				}
				s = strings.Join(parts, "; ")
			case importer.KindJSON:
				s = jsonOf(v, f)
			default:
				s = importer.JSString(v)
			}
			row[i] = GuardCell(s)
		}
		rows = append(rows, row)
	}
	return csvUnparse(cols, rows) + "\n"
}

// CSVTemplate is an empty CSV with the columns a user fills in (no id, timestamps or extras).
func CSVTemplate(c string) string {
	var cols []string
	for _, k := range CSVColumns(c) {
		switch k {
		case "id", "createdAt", "updatedAt", "extras":
		default:
			cols = append(cols, k)
		}
	}
	return csvUnparse(cols, nil) + "\n"
}

// JSON builds the full backup document (format 1).
func JSON(lib *library.Library, o Options) map[string]any {
	colls := map[string]any{}
	for _, c := range domain.Collections {
		items := []any{}
		for _, e := range lib.Repos[c].All() {
			if c == "labs" && withheld(e, o) {
				cp := domain.DeepCopy(map[string]any(e)).(map[string]any)
				cp["writeup"] = ""
				items = append(items, cp)
			} else {
				items = append(items, map[string]any(e))
			}
		}
		colls[c] = items
	}
	return map[string]any{
		"app": domain.AppName, "format": 1, "exportedAt": domain.NowISO(), "includesPrivateWriteups": o.IncludePrivate,
		"collections": colls,
		"config":      map[string]any{"settings": lib.Settings.Data(), "categories": lib.Categories.Data(), "skills": lib.Skills.Data()},
	}
}
