package exporter_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/reeper316099/Cipherkeep/go/internal/exporter"
	"github.com/reeper316099/Cipherkeep/go/internal/importer"
	"github.com/reeper316099/Cipherkeep/go/internal/library"
)

const fixture = "../../../fixtures/synthetic/notion-export"

func open(t *testing.T) *library.Library {
	t.Helper()
	lib, err := library.Open(library.Options{DataDir: t.TempDir(), LaunchBackupDelay: -1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
	return lib
}

func importVFS(t *testing.T, lib *library.Library, vfs *importer.VirtualFS, name, collection string) {
	t.Helper()
	plan, err := importer.BuildPlan(lib, vfs, importer.BuildOptions{SourceName: name, Collection: collection})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := importer.Commit(lib, plan, importer.Options{Strategy: importer.StrategyUpdateEmptyOnly}, &importer.Source{VFS: vfs}); err != nil {
		t.Fatal(err)
	}
}

func source(t *testing.T) *library.Library {
	t.Helper()
	lib := open(t)
	vfs, name, err := importer.FromPath(fixture)
	if err != nil {
		t.Fatal(err)
	}
	importVFS(t, lib, vfs, name, "")
	mk := func(c string, in map[string]any) {
		if _, err := lib.Repos[c].Create(in, ""); err != nil {
			t.Fatal(err)
		}
	}
	mk("labs", map[string]any{"title": "Secret Lab", "platform": "Bandit", "writeup": "the flag is here"})
	mk("labs", map[string]any{"title": "Open Lab", "platform": "Bandit", "writeup": "public notes", "writeupPrivate": false})
	mk("commands", map[string]any{"name": "=cmd", "category": "General", "syntax": "=cmd", "options": []any{map[string]any{"flag": "-x", "meaning": "x"}}})
	return lib
}

func readZip(t *testing.T, b []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		data, _ := io.ReadAll(rc)
		_ = rc.Close()
		out[f.Name] = string(data)
	}
	return out
}

func list(lib *library.Library, c string) []map[string]any {
	var out []map[string]any
	for _, e := range lib.Repos[c].All() {
		out = append(out, e)
	}
	return out
}

func find(items []map[string]any, k, v string) map[string]any {
	for _, i := range items {
		if i[k] == v {
			return i
		}
	}
	return nil
}

func TestMarkdownVault(t *testing.T) {
	src := source(t)
	b, err := exporter.Markdown(src, exporter.Options{})
	if err != nil {
		t.Fatal(err)
	}
	files := readZip(t, b)
	ls := files["commands/ls.md"]
	if !strings.HasPrefix(ls, "---\nck_collection: commands") {
		t.Fatalf("frontmatter: %q", ls[:60])
	}
	for _, want := range []string{"| -a | include dotfiles |", `ls --color=auto = "colourised output"`} {
		if !strings.Contains(ls, want) {
			t.Errorf("ls.md missing %q", want)
		}
	}
	if !strings.Contains(files["concepts/Directory Tree.md"], "[[pwd]]") {
		t.Error("wiki links missing")
	}
	if !strings.Contains(files["notes/study-club/linux-cheat-sheet.md"], "](../../attachments/") {
		t.Error("attachments are not relative")
	}
	hasDiagram := false
	for n := range files {
		if strings.HasPrefix(n, "attachments/") && strings.HasSuffix(n, "diagram.png") {
			hasDiagram = true
		}
	}
	if !hasDiagram {
		t.Error("attachment missing from vault")
	}
	if !strings.Contains(files["labs/Bandit/Secret Lab.md"], "Private writeup omitted") || strings.Contains(files["labs/Bandit/Secret Lab.md"], "flag is here") {
		t.Error("private writeup leaked")
	}
	if !strings.Contains(files["labs/Bandit/Open Lab.md"], "public notes") {
		t.Error("public writeup missing")
	}
	pb, _ := exporter.Markdown(src, exporter.Options{IncludePrivate: true})
	if !strings.Contains(readZip(t, pb)["labs/Bandit/Secret Lab.md"], "flag is here") {
		t.Error("includePrivate ignored")
	}
}

func TestVaultRoundTripsThroughTheMarkdownImporter(t *testing.T) {
	src := source(t)
	b, _ := exporter.Markdown(src, exporter.Options{})
	dst := open(t)
	vfs, err := importer.FromUpload("vault.zip", b)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := importer.BuildPlan(dst, vfs, importer.BuildOptions{SourceName: "vault.zip"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Kind != "markdown" {
		t.Fatalf("kind %s", plan.Kind)
	}
	importVFS(t, dst, vfs, "vault.zip", "")
	a, bb := list(src, "commands"), list(dst, "commands")
	for _, cmd := range a {
		if cmd["name"] == "=cmd" {
			continue
		}
		cp := find(bb, "name", cmd["name"].(string))
		if cp == nil {
			t.Fatalf("command %v was lost", cmd["name"])
		}
		for _, k := range []string{"options", "syntax", "risk"} {
			if !reflect.DeepEqual(cp[k], cmd[k]) {
				t.Errorf("%v.%s: got %#v want %#v", cmd["name"], k, cp[k], cmd[k])
			}
		}
		var ac, bc []string
		for _, e := range cmd["examples"].([]any) {
			ac = append(ac, e.(map[string]any)["command"].(string))
		}
		for _, e := range cp["examples"].([]any) {
			bc = append(bc, e.(map[string]any)["command"].(string))
		}
		if !reflect.DeepEqual(ac, bc) {
			t.Errorf("%v examples: %v vs %v", cmd["name"], bc, ac)
		}
	}
	if n := len(find(list(dst, "concepts"), "title", "Directory Tree")["sections"].([]any)); n != 13 {
		t.Fatalf("sections: %d", n)
	}
}

func TestCSVGuardAndReimport(t *testing.T) {
	src := source(t)
	csv := exporter.CSV(src, "commands", exporter.Options{})
	if !strings.Contains(csv, "'=cmd") {
		t.Error("formula guard missing")
	}
	if !strings.Contains(strings.SplitN(csv, "\n", 2)[0], "name") {
		t.Error("no header")
	}
	dst := open(t)
	vfs, _ := importer.FromUpload("commands.csv", []byte(csv))
	plan, err := importer.BuildPlan(dst, vfs, importer.BuildOptions{SourceName: "commands.csv", Collection: "commands"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Kind != "csv" || len(plan.Items) != 16 {
		t.Fatalf("kind %s, %d items", plan.Kind, len(plan.Items))
	}
	importVFS(t, dst, vfs, "commands.csv", "commands")
	b := list(dst, "commands")
	if find(b, "name", "=cmd")["syntax"] != "=cmd" {
		t.Error("guarded cell was not restored")
	}
	ls := find(b, "name", "ls")
	if len(ls["options"].([]any)) != 4 || len(ls["tags"].([]any)) != 0 {
		t.Errorf("ls: %#v", ls)
	}
	tpl := exporter.CSVTemplate("labs")
	if !strings.Contains(strings.SplitN(tpl, "\n", 2)[0], "platform") || strings.Contains(tpl, "createdAt") {
		t.Errorf("template: %q", tpl)
	}
}

func TestJSONBackupRestoresIdsAndRelations(t *testing.T) {
	src := source(t)
	raw, _ := json.Marshal(exporter.JSON(src, exporter.Options{}))
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["app"] != "Cipherkeep" || doc["format"] != float64(1) {
		t.Fatalf("header: %v %v", doc["app"], doc["format"])
	}
	if strings.Contains(string(raw), "flag is here") {
		t.Fatal("private writeup in JSON export")
	}
	dst := open(t)
	vfs, _ := importer.FromUpload("backup.json", raw)
	plan, err := importer.BuildPlan(dst, vfs, importer.BuildOptions{SourceName: "backup.json"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Kind != "json-backup" {
		t.Fatalf("kind %s", plan.Kind)
	}
	importVFS(t, dst, vfs, "backup.json", "")
	ids := func(l []map[string]any) []string {
		var out []string
		for _, x := range l {
			out = append(out, x["id"].(string))
		}
		sort.Strings(out)
		return out
	}
	a, b := list(src, "concepts"), list(dst, "concepts")
	if !reflect.DeepEqual(ids(a), ids(b)) {
		t.Fatalf("ids differ: %v vs %v", ids(a), ids(b))
	}
	tree := find(b, "title", "Directory Tree")
	if len(tree["relatedCommandIds"].([]any)) != 2 {
		t.Fatal("relations lost")
	}
	if tree["createdAt"] != find(a, "title", "Directory Tree")["createdAt"] {
		t.Fatal("createdAt changed")
	}
	if _, ok := dst.Repos["commands"].Get(tree["relatedCommandIds"].([]any)[0].(string)); !ok {
		t.Fatal("related command does not exist")
	}
}
