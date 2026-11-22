// Package parity holds tests that compare the Go import and export code with what the TypeScript code returns for the
// same inputs (fixtures/compat/importers/expected.json, written by scripts/gen-importer-fixtures.ts).
package parity

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/reeper316099/Cipherkeep/go/internal/domain"
	"github.com/reeper316099/Cipherkeep/go/internal/exporter"
	"github.com/reeper316099/Cipherkeep/go/internal/importer"
	"github.com/reeper316099/Cipherkeep/go/internal/library"
	"github.com/reeper316099/Cipherkeep/go/internal/notion"
	"github.com/reeper316099/Cipherkeep/go/internal/repo"
	"github.com/reeper316099/Cipherkeep/go/internal/sanitize"
	"github.com/reeper316099/Cipherkeep/go/internal/starter"
)

const root = "../../../fixtures/compat/importers"

var expected map[string]any

func TestMain(m *testing.M) {
	b, err := os.ReadFile(filepath.Join(root, "expected.json"))
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	if err := json.Unmarshal(b, &expected); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// norm round-trips a value through JSON so Go structs, maps and numbers compare with what the TypeScript produced.
func norm(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func diff(path string, got, want any) string {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return fmt.Sprintf("%s: got %T %v, want object", path, got, got)
		}
		keys := map[string]bool{}
		for k := range w {
			keys[k] = true
		}
		for k := range g {
			keys[k] = true
		}
		names := make([]string, 0, len(keys))
		for k := range keys {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			gv, gok := g[k]
			wv, wok := w[k]
			if gok != wok {
				return fmt.Sprintf("%s.%s: present in Go=%v, in TS=%v (Go %v, TS %v)", path, k, gok, wok, short(gv), short(wv))
			}
			if d := diff(path+"."+k, gv, wv); d != "" {
				return d
			}
		}
	case []any:
		g, ok := got.([]any)
		if !ok {
			return fmt.Sprintf("%s: got %s, want array", path, short(got))
		}
		if len(g) != len(w) {
			return fmt.Sprintf("%s: %d items in Go, %d in TS\nGo: %s\nTS: %s", path, len(g), len(w), short(g), short(w))
		}
		for i := range w {
			if d := diff(fmt.Sprintf("%s[%d]", path, i), g[i], w[i]); d != "" {
				return d
			}
		}
	default:
		if !reflect.DeepEqual(got, want) {
			return fmt.Sprintf("%s:\nGo: %s\nTS: %s", path, short(got), short(want))
		}
	}
	return ""
}

func short(v any) string {
	b, _ := json.Marshal(v)
	if len(b) > 600 {
		return string(b[:600]) + "..."
	}
	return string(b)
}

func check(t *testing.T, name string, got, want any) {
	t.Helper()
	if d := diff(name, norm(t, got), want); d != "" {
		t.Error(d)
	}
}

func at(path ...string) any {
	var cur any = expected
	for _, p := range path {
		cur = cur.(map[string]any)[p]
	}
	return cur
}

func list(v any) []any { return v.([]any) }

// ---- parsers ----

func mapEach[T any, R any](in []T, f func(T) R) []R {
	out := make([]R, len(in))
	for i, x := range in {
		out[i] = f(x)
	}
	return out
}

func strsOf(v any) []string {
	var out []string
	for _, x := range list(v) {
		out = append(out, x.(string))
	}
	return out
}

func nilStr(s string, ok bool) any {
	if !ok {
		return nil
	}
	return s
}

func TestTextParsersMatchTypeScript(t *testing.T) {
	p := func(k string) []any { return list(at("parsers", k)) }

	// inputs come from the expected file's own generator lists, re-declared here so a change to either side shows up
	times := []string{"~1.5 hours", "1 hr", "45 mins", "2h 30m", "1:30", "25:61", "half an hour", "an hour and 15 minutes", "about 2 hrs",
		"approx. 3 hrs", "roughly an hour", "1.25h", "90", "a while", "", "2 hours 15 minutes", "1 hr 30 mins", "45m", "3 MINS", "\u2248 2 h",
		"1\u00a0hr", "around half an hour", "an hour", "0.5 hours", "10 minutes and 5 minutes"}
	check(t, "times", mapEach(times, func(s string) any {
		if n, ok := notion.ParseTimeSpent(s); ok {
			return n
		}
		return nil
	}), at("parsers", "times"))

	dates := []string{"March 24, 2026", "2026-03-24", "24 Mar 2026", "03/24/2026", "@March 24, 2026 \u2192 March 25, 2026", "Sept. 3rd, 2026",
		"Sept 3, 2026", "February 31, 2026", "2026-02-30", "0050-01-01", "Foo 3, 2026", "Ma 3, 2026", "", "soon", "2026-13-01",
		"12/31/2026", "1/2/2026", "1st Jan 2026", "  March 5 2026  ", "2026-03-24T10:00:00", "2024-02-29", "2023-02-29",
		"December 1, 2025 12:00 PM", "5 may 2026", "5 Mayday 2026"}
	check(t, "dates", mapEach(dates, func(s string) any { return nilStr(notion.ParseNotionDate(s)) }), at("parsers", "dates"))

	relations := []string{
		"pwd (Dash/%F0%9F%92%BB%20Commands/pwd%2032df3e3018aa804783b1f8a9de1e33c1.md), a, b (x/b%2032df3e3018aa803c957dd5c1468647de.md)",
		"ls, cd", "", "   ", "Docs (https://example.com/x)", "(dir/Name%2032df3e3018aa804783b1f8a9de1e33c1.md)",
		"A (a.md), B (b.md)", "Weird (title) here (x/y%2032DF3E3018AA804783B1F8A9DE1E33C1.md)", "trailing, (a.md)",
		"one (one.md) two (two.md)", "x (https://a.b/c d)", "T (%E0%A4%A.md)"}
	check(t, "relations", mapEach(relations, func(s string) any {
		out := []any{}
		for _, r := range notion.ParseRelationCell(s) {
			out = append(out, map[string]any{"title": r.Title, "path": nilStr(r.Path, r.Path != ""), "id": nilStr(r.ID, r.ID != "")})
		}
		return out
	}), at("parsers", "relations"))

	names := []string{"ls 32df3e3018aa80d89e90dddec81b898b.md", "\U0001F4DA Cyber Concepts 32cf3e3018aa8051999ed048cba85e91_all.csv", "No id here.md",
		"dir/sub/ls 32DF3E3018AA80D89E90DDDEC81B898B.md", "plain.csv", "  spaced   .md", "x_all.csv", "weird 32df3e3018aa80d89e90dddec81b898.md"}
	check(t, "names", mapEach(names, func(s string) any {
		n := notion.SplitName(s)
		return map[string]any{"title": n.Title, "id": nilStr(n.ID, n.ID != ""), "ext": n.Ext, "all": n.All}
	}), at("parsers", "names"))

	headings := []string{"\U0001F9E0 Intuition (VERY IMPORTANT)", "\U0001F527 Common Options (Flags)", "  --x--  ", "123 abc", "\u2753 Questions I still have", "!!!", "Tools / Commands Related"}
	check(t, "headings", mapEach(headings, notion.HeadingKey), at("parsers", "headings"))
	paths := []string{"Dash/%F0%9F%92%BB%20Commands/pwd.md", "a\\b%zz", "100%", "caf%C3%A9.md", "cafe\u0301.md", "%E0%A4%A"}
	check(t, "paths", mapEach(paths, notion.DecodeNotionPath), at("parsers", "paths"))
	check(t, "lists", mapEach([]string{"a, b; c ,,", "", " ; ", "one"}, func(s string) any {
		if l := notion.SplitList(s); l != nil {
			return l
		}
		return []string{}
	}), at("parsers", "lists"))

	pages := []struct {
		text  string
		props bool
	}{
		{"# ls\n\nCategory: File System\nSyntax: ls \\[flags\\]\n\n## \U0001F4CC What it does\n\nlists\n", true},
		{"# Page\n\nNote: not a property\n\ntext", false},
		{"\ufeff# T\r\n\r\nA: 1\r\nnot a prop\r\n\r\nbody", true},
		{"# T\n\n" + strings.Repeat("k", 70) + ": v\n\nbody", true},
		{"# T\n\nKey:\nOther: x y\n\n", true},
		{"no heading\n\nKey: v\n\nbody", true},
		{"\n\n#   Spaced title  \n\nCategory: A\nCategory: B\n\nbody", true},
		{"# T\n\nKey:value\n\nbody", true},
		{"# T\n\n- dash: no\n\nbody", true},
	}
	check(t, "pages", mapEach(pages, func(c struct {
		text  string
		props bool
	}) any {
		pg := notion.ParsePage(c.text, c.props)
		return map[string]any{"title": pg.Title, "props": pg.Props, "body": pg.Body}
	}), at("parsers", "pages"))

	bodies := []string{
		"use \\[flags\\] here\n\n```\necho \\[x\\]\n```", "a `\\[x\\]` and \\[y\\]",
		"<aside>\n\U0001F4A1 Remember this\n</aside>\n\n<details><summary>More</summary>\nhidden text\n</details>",
		"<aside>\n\u26a0\ufe0f careful\n\nmore\n</aside>", "<aside>plain</aside>", "| a | b\nc |\n| d | e |", "```\n| not\ntable\n```",
		"x  \t\n\n\n\n\ny   ", "~~~\n```\n~~~\n\\[ok\\]", "| h | i |\n| --- | --- |\n| a\n  b | c |"}
	check(t, "bodies", mapEach(bodies, notion.NormalizeBody), at("parsers", "bodies"))
	check(t, "repaired", mapEach(bodies, notion.RepairTables), at("parsers", "repaired"))
	check(t, "unescaped", mapEach([]string{"ls \\[flags\\] \\*", "a\\\\b \\_x\\_ \\`t\\`", "no escapes"}, notion.UnescapeAll), at("parsers", "unescaped"))

	sections := []string{
		"intro\n\n## A\n\none\n\n---\n\n## \U0001F9E0 B\n\n```\n## not heading\n```", "## Only\n\ntext\n\n---\n\n",
		"---\n\nlead\n\n## A\n\n---\n\nx\n\n---", "## A\n### B\n##C\n## D  \ntext"}
	check(t, "sections", mapEach(sections, func(s string) any {
		pre, secs := notion.SplitSections(s)
		out := []any{}
		for _, x := range secs {
			out = append(out, map[string]any{"title": x.Title, "key": x.Key, "markdown": x.Markdown})
		}
		return map[string]any{"preamble": pre, "sections": out}
	}), at("parsers", "sections"))

	tables := []string{"| a | b |\n| --- | --- |\n| `x\\|y` | z |", "| only |\n| :--: |\n| 1 |\n\ntext\n\n| a | b |\n|--|--|\n| 1 | 2 |",
		"```\n| a |\n```\n| real | row |", "|a|b|\n|-|-|\n|x|y|"}
	check(t, "tables", mapEach(tables, func(s string) any {
		out := []any{}
		for _, tb := range notion.ParseTables(s) {
			out = append(out, tb.Rows)
		}
		return out
	}), at("parsers", "tables"))

	cells := []string{"`ls` **bold** *it* _u_ a<br/>b \\[x\\]", "2*3*4 snake_case_name", "**a** and *b*", "_x_y_ and _z_", "a<BR >b", "  \\*esc\\*  "}
	check(t, "cells", mapEach(cells, notion.CellText), at("parsers", "cells"))

	fences := []string{"x\n```bash\nls -l\n```\ntext\n~~~\nraw\n~~~", "```\nunclosed", "```js\na\n```\n```\nb\n```", "```\n````\n```", "a\n~~~\n```\n~~~"}
	check(t, "fences", mapEach(fences, func(s string) any {
		out := []any{}
		for _, b := range notion.FencedBlocks(s) {
			out = append(out, map[string]any{"lang": b.Lang, "code": b.Code})
		}
		return out
	}), at("parsers", "fences"))

	lines := []string{"ls -al ~ = \"lists all\"", "echo a = \"b\" = \"c\"", "plain command", "", "# comment", " = \"no command\"", "x = \"trailing\"  "}
	check(t, "exampleLines", mapEach(lines, func(s string) any {
		if ex, ok := notion.SplitExampleLine(s); ok {
			return map[string]any{"command": ex.Command, "description": ex.Description}
		}
		return nil
	}), at("parsers", "exampleLines"))

	phs := []string{"ls [flags] <target> {ip} [[:upper:]]* [!abc]", "<a-b> {c_d} [e f] <1x>", "no tokens"}
	check(t, "placeholders", mapEach(phs, func(s string) any { return notion.DetectPlaceholders(s) }), at("parsers", "placeholders"))
	check(t, "filled", mapEach(phs, func(s string) any {
		return notion.FillPlaceholders(s, map[string]string{"flags": "-a", "target": "host", "ip": ""})
	}), at("parsers", "filled"))

	type risk struct {
		n string
		e []string
	}
	risks := []risk{{"rm", nil}, {"mv", nil}, {"ls", nil}, {"find", []string{"find . -exec rm -rf {} +"}}, {"  RM  -rf", nil}, {"x", []string{"dd if=a of=/dev/sda"}},
		{"", nil}, {"y", []string{":(){ :|:& };:"}}, {"z", []string{"mkfs.ext4 /dev/sdb"}}}
	check(t, "risks", mapEach(risks, func(r risk) any { return notion.ClassifyRisk(r.n, r.e) }), at("parsers", "risks"))
	check(t, "bullets", mapEach([]string{"- Step 1\n- real\n1. numbered\n-\n", "* a\n+ b\n  - nested\n10. ten\n", "plain\n- ok"}, notion.BulletItems), at("parsers", "bullets"))
	check(t, "difficulties", mapEach([]string{"Beginner", "advanced", "Wizard", "", " Hard ", "MEDIUM"}, notion.MapDifficulty), at("parsers", "difficulties"))
	_ = p
}

func TestPageMappersMatchTypeScript(t *testing.T) {
	cat := "General"
	body1 := strings.Join([]string{
		"## \U0001F4CC What it does", "", "Shows things", "", "---", "", "## \U0001F9E9 Arguments", "", "(Inputs the command takes.)", "", "- target \u2192 thing to show", "- `-x`: flag", "- bare", "",
		"---", "", "## \U0001F527 Common Options (Flags)", "", "| Option | Meaning |", "| --- | --- |", "| -a | all |", "| -l | long |", "", "---", "",
		"## \U0001F4A1 Examples", "", "```bash", "show -a x = \"all of x\"", "```", "", "| `show -l y` | Long y |", "| --- | --- |", "| `show -a z` | All z |", "",
		"## \U0001F9EA Extra", "", "kept"}, "\n")
	r1 := notion.ParseCommandPage(notion.CommandSource{Name: "show", Category: &cat, Body: body1,
		Props: map[string]string{"Syntax": "show \\[flags\\] \\[target\\]", "Example": "show -a", "Explanation": "shows"}})
	r2 := notion.ParseCommandPage(notion.CommandSource{Name: "less", Props: map[string]string{"Category": "Files"},
		Body: "## \U0001F4CC What it does\n\nPager\n\n## Commands\n\n| Key | Action |\n| --- | --- |\n| q | quit |\n| /x | search |\n\n## \U0001F3AF Basic Syntax\n\n```bash\nless <file>\n```\n"})
	check(t, "commands", []any{
		map[string]any{"warnings": r1.Warnings, "input": r1.Input},
		map[string]any{"warnings": r2.Warnings, "input": r2.Input}}, at("parsers", "commands"))

	c := notion.ParseConceptPage(notion.ConceptSource{Name: "X", Difficulty: "Beginner", Tags: []string{"a", "b"},
		Body: "## \U0001F9E0 What is it?\n\ntext\n\n---\n\n## \U0001F6E0\ufe0f Tools / Commands Related\n\n- cp\n- find\n\n---\n\n## \u2753 Questions I still have\n\n- one?\n- \n- two?\n\n## \U0001F381 Custom\n\nmine\n\n## Difficulty Level\n\nadvanced\n\n## Related Concepts\n\n- Y\n- Component 1"})
	check(t, "concept", map[string]any{"warnings": c.Warnings, "commandMentions": c.CommandMentions, "conceptMentions": c.ConceptMentions, "input": c.Input}, at("parsers", "concept"))

	logs := []notion.LogSource{
		{Name: "March 24, 2026", Date: notion.S("March 24, 2026"), TimeSpent: notion.S("~1.5 hours"), Topics: notion.S("A, B"), KeyTakeaway: notion.S("tk"), Body: "## \U0001F4DA What I learned\n\n- less\n- df"},
		{Name: "Notes", TimeSpent: notion.S("a while")},
		{Name: "Mar 3 2026", Date: notion.S("")},
		{Name: "x", Date: notion.S("nonsense"), Topics: notion.S(",,"), Body: "## What I learned\n\n-\n- one"},
	}
	check(t, "logs", mapEach(logs, func(l notion.LogSource) any {
		r := notion.ParseLogEntry(l)
		return map[string]any{"warnings": r.Warnings, "learned": r.Learned, "input": r.Input}
	}), at("parsers", "logs"))

	cases := []struct {
		text  string
		extra []string
	}{
		{"flag: picoCTF{s3cr3t_v4lue} at 192.168.1.20 and mail a.b@ex.org; token=abc123 Bearer abcdefgh12345678", nil},
		{"-----BEGIN RSA PRIVATE KEY-----\nabc\n-----END RSA PRIVATE KEY-----\nhost lab-7.internal", []string{`lab-\d+\.internal`, "(x+)+y", "["}},
		{"Used nmap 7.94 on port 443 (version 1.2.3)", nil},
		{"AKIA" + "ABCDEFGHIJKLMNOP" + " and 999.1.1.1 and 10.0.0.255 password: hunter2", nil},
		{"Host LAB-9.Internal", []string{`lab-\d+\.internal`}},
		{"api_key = abc and API-KEY:xyz and secret=\u00a0q", nil},
		{"x@y.z, e@f.g.h and flag{ab} and FLAG{abcd}", nil},
	}
	check(t, "sanitize", mapEach(cases, func(c struct {
		text  string
		extra []string
	}) any {
		return sanitize.Writeup(c.text, c.extra)
	}), at("parsers", "sanitize"))
}

// ---- planners ----

func TestPlansMatchTypeScript(t *testing.T) {
	vfs, _, err := importer.FromPath("../../../fixtures/synthetic/notion-export")
	if err != nil {
		t.Fatal(err)
	}
	check(t, "notion plan", importer.PlanNotion(vfs, importer.NotionOptions{SourceName: "notion-export"}), at("plans", "notion"))
	check(t, "starter plan", starter.Plan(), at("plans", "starter"))

	samples := []struct{ collection, text string }{
		{"commands", strings.Join([]string{
			"Name,Category,Syntax,Risk,Tags,Options,Placeholders,id,createdAt,Extra Column",
			`"'=cmd",General,'=cmd,safe,a|b,"[{""flag"":""-x"",""meaning"":""x""}]",,SHOULD_BE_DROPPED,2020-01-01,keep me`,
			`ls,File System,"ls [flags]",caution,"x; y",not json,"[""flags""]",,,`,
			",General,nameless,,,,,,,", "", "cd,General,cd <dir>,SAFE,,,,,,"}, "\r\n")},
		{"labs", strings.Join([]string{"Title,Platform,Status,Points,Hints Used,Writeup Private,Tags", `Lab A,Bandit,Completed,10,2,yes,"one, two"`, "Lab B,,Not Started,abc,,no,"}, "\n")},
		{"notes", "title,folder,markdown,kind\nN,\"a/b\",\"# h\n\nbody\",guide\n"},
	}
	var csvPlans []any
	for _, s := range samples {
		v := importer.NewVirtualFS()
		v.Add("in.csv", []byte(s.text))
		p, err := importer.PlanCSV(v, importer.CSVOptions{SourceName: "in.csv", Collection: s.collection})
		if err != nil {
			t.Fatal(err)
		}
		csvPlans = append(csvPlans, p)
	}
	check(t, "csv plans", csvPlans, at("plans", "csv"))

	md := importer.NewVirtualFS()
	for name, text := range map[string]string{
		"top.md":               "# Top Note\n\nbody text\n",
		"sub/with-front.md":    "---\ntitle: Front Title\ntags: [x, y]\ncategory: Cat\ncustom: 1\nother:\n  nested: true\n---\n\n# Ignored H1\n\ncontent\n",
		"sub/tags-string.md":   "---\ntags: \"a, b\"\n---\nx",
		"sub/deep/no-title.md": "just text",
		"cmd.md":               "---\nck_collection: commands\ntitle: mycmd\ncategory: General\nsyntax: mycmd [x]\nexplanation: does it\nexample: mycmd -x\nrisk: caution\ntags: [t1]\n---\n\n## \U0001F4CC What it does\n\nStuff\n\n## \U0001F527 Common Options (Flags)\n\n| Option | Meaning |\n| --- | --- |\n| -x | ex |\n",
		"con.md":               "---\nck_collection: concepts\ntitle: MyConcept\ndifficulty: Intermediate\ntags: [c]\n---\n\n## One\n\nfirst\n\n---\n\n## Two\n\nsecond",
		"readme.txt":           "ignored",
	} {
		md.Add(name, []byte(text))
	}
	check(t, "markdown plan", importer.PlanMarkdownFolder(md, "folder"), at("plans", "markdown"))

	bk := importer.NewVirtualFS()
	bk.Add("backup.json", []byte(`{"app":"Cipherkeep","format":1,"collections":{"notes":[{"id":"01HZZZZZZZZZZZZZZZZZZZZZZ1","title":"Backed up","createdAt":"2025-01-01T00:00:00.000Z","updatedAt":"2025-01-02T00:00:00.000Z","folder":"f","markdown":"m","slug":"backed-up"}],"commands":[{"id":"01HZZZZZZZZZZZZZZZZZZZZZZ2","name":"bk","title":"bk","category":"General","createdAt":"2025-01-01T00:00:00.000Z","updatedAt":"2025-01-02T00:00:00.000Z","slug":"bk"},{"title":"invalid: no id"}]}}`))
	jp, err := importer.PlanJSONBackup(bk, "backup.json")
	if err != nil {
		t.Fatal(err)
	}
	check(t, "json backup plan", jp, at("plans", "jsonBackup"))
}

// ---- exports ----

func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestExportsMatchTypeScript(t *testing.T) {
	dir := t.TempDir()
	copyDir(t, filepath.Join(root, "library"), dir)
	lib, err := library.Open(library.Options{DataDir: dir, LaunchBackupDelay: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()

	csv := map[string]any{}
	tpl := map[string]any{}
	for _, c := range domain.Collections {
		csv[c] = exporter.CSV(lib, c, exporter.Options{})
		tpl[c] = exporter.CSVTemplate(c)
	}
	check(t, "csv", csv, at("exports", "csv"))
	check(t, "csvPrivate", exporter.CSV(lib, "labs", exporter.Options{IncludePrivate: true}), at("exports", "csvPrivate"))
	check(t, "templates", tpl, at("exports", "templates"))

	read := func(b []byte) []*zip.File {
		zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
		if err != nil {
			t.Fatal(err)
		}
		return zr.File
	}
	text := func(f *zip.File) string {
		rc, _ := f.Open()
		defer rc.Close()
		b, _ := io.ReadAll(rc)
		return string(b)
	}
	zb, err := exporter.Markdown(lib, exporter.Options{})
	if err != nil {
		t.Fatal(err)
	}
	var vault []any
	for _, f := range read(zb) {
		tx := text(f)
		if !strings.HasSuffix(f.Name, ".md") || !strings.HasPrefix(tx, "---\n") {
			vault = append(vault, map[string]any{"name": f.Name, "text": tx})
			continue
		}
		fm, body := repo.ParseFrontmatter(tx)
		vault = append(vault, map[string]any{"name": f.Name, "frontmatter": fm, "body": body})
	}
	sort.Slice(vault, func(i, j int) bool {
		return vault[i].(map[string]any)["name"].(string) < vault[j].(map[string]any)["name"].(string)
	})
	check(t, "vault", vault, at("exports", "vault"))

	pb, _ := exporter.Markdown(lib, exporter.Options{IncludePrivate: true})
	labs := map[string]bool{}
	var labNames []string
	for _, f := range read(pb) {
		if strings.HasPrefix(f.Name, "labs/") {
			labs[f.Name] = strings.Contains(text(f), "flag is here")
			labNames = append(labNames, f.Name)
		}
	}
	sort.Strings(labNames) // adm-zip lists entries by name
	var leaks []any
	for _, n := range labNames {
		leaks = append(leaks, labs[n])
	}
	check(t, "vaultPrivateLab", leaks, at("exports", "vaultPrivateLab"))

	doc := exporter.JSON(lib, exporter.Options{})
	check(t, "json", map[string]any{"app": doc["app"], "format": doc["format"], "includesPrivateWriteups": doc["includesPrivateWriteups"], "collections": doc["collections"]}, at("exports", "json"))
}
