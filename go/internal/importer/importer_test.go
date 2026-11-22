package importer_test

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

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

func planFixture(t *testing.T, lib *library.Library) (*importer.Plan, *importer.VirtualFS) {
	t.Helper()
	vfs, name, err := importer.FromPath(fixture)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := importer.BuildPlan(lib, vfs, importer.BuildOptions{SourceName: name})
	if err != nil {
		t.Fatal(err)
	}
	return plan, vfs
}

func counts(p *importer.Plan) map[string]int {
	m := map[string]int{}
	for _, i := range p.Items {
		m[i.Collection]++
	}
	return m
}

func list(lib *library.Library, c string) []map[string]any {
	var out []map[string]any
	for _, e := range lib.Repos[c].All() {
		out = append(out, e)
	}
	return out
}

func byField(items []map[string]any, k, v string) map[string]any {
	for _, i := range items {
		if i[k] == v {
			return i
		}
	}
	return nil
}

func strsOf(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

func sub(v any, k string) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(map[string]any)[k].(string))
	}
	return out
}

func eq(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v\nwant %#v", got, want)
	}
}

func TestSyntheticNotionExport(t *testing.T) {
	lib := open(t)
	plan, vfs := planFixture(t, lib)
	eq(t, plan.Kind, "notion")
	eq(t, counts(plan), map[string]int{"commands": 15, "concepts": 2, "projects": 1, "log": 1, "notes": 12, "snippets": 1}) // 12 incl. the hub page
	eq(t, len(plan.Templates), 4)
	eq(t, len(plan.Attachments), 1)
	for _, i := range plan.Items {
		if i.Title == "Lab Dashboard" && !strings.Contains(i.SkipByDefault, "hub") {
			t.Fatalf("hub page not skipped by default: %q", i.SkipByDefault)
		}
	}
	eq(t, len(lib.Repos["commands"].All()), 0) // planning writes nothing

	rep, err := importer.Commit(lib, plan, importer.Options{Strategy: importer.StrategyUpdateEmptyOnly}, &importer.Source{VFS: vfs})
	if err != nil {
		t.Fatal(err)
	}
	eq(t, rep.Created, map[string]int{"commands": 15, "concepts": 2, "projects": 1, "log": 1, "notes": 11, "snippets": 1})
	eq(t, len(rep.Skipped), 1)
	if !strings.Contains(*rep.Snapshot, "pre-import") {
		t.Fatalf("snapshot %q", *rep.Snapshot)
	}
	warnFor := func(title string) string {
		for _, w := range rep.ItemWarnings {
			if w.Title == title {
				return strings.Join(w.Warnings, ",")
			}
		}
		return ""
	}
	if !strings.Contains(warnFor("ls"), "Ghost Lab") || !strings.Contains(warnFor("Directory Tree"), "awk") {
		t.Fatalf("item warnings: %#v", rep.ItemWarnings)
	}

	cmds := list(lib, "commands")
	by := func(n string) map[string]any { return byField(cmds, "name", n) }
	nCat := func(c string) int {
		n := 0
		for _, x := range cmds {
			if x["category"] == c {
				n++
			}
		}
		return n
	}
	eq(t, nCat("File System"), 10)
	eq(t, nCat("General"), 5)
	eq(t, by("ls")["syntax"], "ls [flags] [directory]")
	eq(t, sub(by("ls")["options"], "flag"), []string{"-a", "-l", "-h", "-t"})
	eq(t, sub(by("ls")["examples"], "command"), []string{"ls -al", "ls /var", "ls --color=auto", "ls -lh ~"})
	eq(t, by("ls")["examples"].([]any)[2].(map[string]any)["description"], "colourised output")
	eq(t, strsOf(by("ls")["placeholders"]), []string{"flags", "directory"})
	eq(t, by("rm")["risk"], "destructive")
	eq(t, by("rm")["extras"].(map[string]any)["droppedTemplateHints"], []any{"(Inputs the command takes.)"})
	eq(t, by("less")["extras"].(map[string]any)["optionsHeading"], "Commands")
	eq(t, len(by("less")["options"].([]any)), 4)
	eq(t, len(by("cp")["options"].([]any)), 3) // -u row repaired
	eq(t, sub(by("cp")["examples"], "command"), []string{"cp a.txt b.txt", "cp one two", "cp -r d1 d2", "cp -u big small"})
	eq(t, sub(by("mv")["examples"], "command"), []string{"mv a.txt dir", "mv one", "mv -i one"})
	eq(t, by("mv")["examples"].([]any)[1].(map[string]any)["description"], "Runs mv on one.")
	eq(t, len(by("cd")["options"].([]any)), 3)
	eq(t, len(by("ls")["links"].([]any)), 0) // unresolved Used In link is a warning, not a link

	concepts := list(lib, "concepts")
	tree, globs := byField(concepts, "title", "Directory Tree"), byField(concepts, "title", "Globs")
	eq(t, len(tree["sections"].([]any)), 13)
	eq(t, tree["questionsOpen"], float64(2))
	eq(t, globs["questionsOpen"], float64(0))
	name := func(id any) string {
		for _, c := range cmds {
			if c["id"] == id {
				return c["name"].(string)
			}
		}
		return ""
	}
	var treeCmds, globCmds []string
	for _, id := range tree["relatedCommandIds"].([]any) {
		treeCmds = append(treeCmds, name(id))
	}
	sort.Strings(treeCmds)
	for _, id := range globs["relatedCommandIds"].([]any) {
		globCmds = append(globCmds, name(id))
	}
	eq(t, treeCmds, []string{"cd", "pwd"})
	eq(t, globCmds, []string{"cp", "ls"})
	eq(t, strsOf(tree["tags"]), []string{"Foundations"})

	l := list(lib, "log")[0]
	eq(t, l["date"], "2026-06-03")
	eq(t, l["timeSpentMin"], float64(90))
	eq(t, l["timeSpentRaw"], "~1.5 hours")
	eq(t, len(l["commandIds"].([]any)), 1)
	eq(t, len(l["conceptIds"].([]any)), 1)
	p := list(lib, "projects")[0]
	eq(t, p["status"], "In progress")
	eq(t, p["type"], "Script")
	eq(t, strsOf(p["skillsUsed"]), []string{"Bash", "Globs"})

	notes := list(lib, "notes")
	n := 0
	for _, x := range notes {
		if x["title"] == "Notes" {
			n++
		}
	}
	eq(t, n, 2) // duplicate titles kept
	cheat := byField(notes, "title", "Linux Cheat Sheet")
	eq(t, cheat["kind"], "cheatsheet")
	md := cheat["markdown"].(string)
	for _, want := range []string{"> [!TIP]", "> **▸ Hidden tips**", "ck://attachments/", "`ls \\[flags\\]`"} {
		if !strings.Contains(md, want) {
			t.Errorf("cheat sheet missing %q", want)
		}
	}
	eq(t, byField(notes, "title", "Web Notes")["folder"], "study-club/ctf-notes")
	eq(t, byField(notes, "title", "SSH Configuration")["kind"], "guide")
	sn := list(lib, "snippets")[0]
	eq(t, sn["title"], "Helper Scripts")
	eq(t, sn["language"], "bash")
	for _, rel := range []string{"attachments", "config/templates/concepts.json", "imports/" + rep.ID + "/manifest.json"} {
		if _, err := os.Stat(filepath.Join(lib.Dir, rel)); err != nil {
			t.Errorf("missing %s", rel)
		}
	}

	// idempotent and never overwrites user edits
	cd := by("cd")
	if _, err := lib.Repos["commands"].Patch(cd["id"].(string), map[string]any{"explanation": "my own words", "whatItDoes": ""}); err != nil {
		t.Fatal(err)
	}
	commitAgain := func(strategy importer.Strategy) *importer.Report {
		pl, vf := planFixture(t, lib)
		r, err := importer.Commit(lib, pl, importer.Options{Strategy: strategy}, &importer.Source{VFS: vf})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	again := commitAgain(importer.StrategyUpdateEmptyOnly)
	eq(t, again.Created, map[string]int{})
	eq(t, again.Updated, map[string]int{"commands": 1}) // only the field I emptied is refilled
	after, _ := lib.Repos["commands"].Get(cd["id"].(string))
	eq(t, after["explanation"], "my own words")
	if after["whatItDoes"] == "" {
		t.Fatal("whatItDoes was not refilled")
	}
	third := commitAgain(importer.StrategyUpdateEmptyOnly)
	eq(t, third.Updated, map[string]int{})
	sum := 0
	for _, v := range third.Unchanged {
		sum += v
	}
	eq(t, sum, 31)

	// overwrite replaces fields and undo restores them
	over := commitAgain(importer.StrategyOverwrite)
	eq(t, over.Updated["commands"], 1)
	got, _ := lib.Repos["commands"].Get(cd["id"].(string))
	eq(t, got["explanation"], "change directory")
	u, err := importer.Undo(lib, over.ID)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, *u, importer.UndoResult{Removed: 0, Restored: 1})
	got, _ = lib.Repos["commands"].Get(cd["id"].(string))
	eq(t, got["explanation"], "my own words")
	if _, err := importer.Undo(lib, over.ID); err == nil || !strings.Contains(err.Error(), "already undone") {
		t.Fatalf("second undo: %v", err)
	}

	// undoing the first import removes everything it created
	u, err = importer.Undo(lib, rep.ID)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, u.Removed, 31)
	for _, c := range []string{"commands", "concepts", "projects", "log", "notes", "snippets", "labs"} {
		eq(t, len(lib.Repos[c].All()), 0)
	}
	if _, err := os.Stat(filepath.Join(lib.Dir, "config/templates/concepts.json")); err == nil {
		t.Fatal("template survived undo")
	}
	if len(importer.List(lib)) == 0 {
		t.Fatal("import history is empty")
	}
}

func TestReclassifyLoosePages(t *testing.T) {
	lib := open(t)
	plan, vfs := planFixture(t, lib)
	ref := func(title string) string {
		for _, i := range plan.Items {
			if i.Title == title {
				return i.Ref
			}
		}
		t.Fatalf("no item %q", title)
		return ""
	}
	rep, err := importer.Commit(lib, plan, importer.Options{Strategy: importer.StrategyUpdateEmptyOnly, Overrides: map[string]string{
		ref("Helper Scripts"): "note", ref("Network Cheat Sheet"): "snippet", ref("Lab Dashboard"): "include", ref("Windows Cheat Sheet"): "skip",
	}}, &importer.Source{VFS: vfs})
	if err != nil {
		t.Fatal(err)
	}
	eq(t, rep.Created["snippets"], 1)
	eq(t, rep.Created["notes"], 11)
	eq(t, list(lib, "snippets")[0]["title"], "Network Cheat Sheet")
	var titles []string
	for _, n := range list(lib, "notes") {
		titles = append(titles, n["title"].(string))
	}
	if !contains(titles, "Lab Dashboard") || contains(titles, "Windows Cheat Sheet") {
		t.Fatalf("notes: %v", titles)
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for n, c := range files {
		w, _ := zw.Create(n)
		_, _ = w.Write([]byte(c))
	}
	_ = zw.Close()
	return buf.Bytes()
}

func TestZipUploadOfTheSameExport(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	err := filepath.WalkDir(fixture, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(fixture, p)
		data, _ := os.ReadFile(p)
		w, _ := zw.Create(filepath.ToSlash(rel))
		_, _ = w.Write(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = zw.Close()
	lib := open(t)
	vfs, err := importer.FromUpload("export.zip", buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := importer.BuildPlan(lib, vfs, importer.BuildOptions{SourceName: "export.zip"})
	if err != nil {
		t.Fatal(err)
	}
	eq(t, counts(plan)["commands"], 15)
	withCollection := 0
	for _, d := range plan.Databases {
		if d.Collection != nil {
			withCollection++
		}
	}
	eq(t, withCollection, 5)
	rep, err := importer.Commit(lib, plan, importer.Options{Strategy: importer.StrategyUpdateEmptyOnly}, &importer.Source{VFS: vfs, Zip: buf.Bytes(), ZipName: "export.zip"})
	if err != nil {
		t.Fatal(err)
	}
	eq(t, rep.Created["commands"], 15)
	if _, err := os.Stat(filepath.Join(lib.Dir, "imports", rep.ID, "export.zip")); err != nil {
		t.Fatal("original upload was not kept")
	}
}

func TestZipSlipAndBadSources(t *testing.T) {
	vfs, err := importer.FromUpload("x.zip", zipOf(t, map[string]string{"../evil.md": "# evil", "safe/ok.md": "# ok\n\nhello", "a/../../b.md": "x", "__MACOSX/x.md": "x", "d/.DS_Store": "x"}))
	if err != nil {
		t.Fatal(err)
	}
	eq(t, vfs.Paths(), []string{"safe/ok.md"}) // nothing can escape the virtual fs; metadata is dropped
	lib := open(t)
	plan, err := importer.BuildPlan(lib, vfs, importer.BuildOptions{SourceName: "x.zip"})
	if err != nil {
		t.Fatal(err)
	}
	eq(t, plan.Items[0].Title, "ok")
	if _, _, err := importer.FromPath("/definitely/not/here"); err == nil {
		t.Fatal("expected an error for a missing path")
	}
	if _, err := importer.NewPreviewStore().Get("nope"); err == nil {
		t.Fatal("expected PREVIEW_EXPIRED")
	}
	if _, err := importer.DetectKind(importer.NewVirtualFS()); err == nil {
		t.Fatal("an empty source is not importable")
	}
	if _, err := importer.FromUpload("bad.zip", []byte("PK not really")); err == nil {
		t.Fatal("expected an error for a corrupt zip")
	}
	// a CSV with no hint cannot be placed
	v2, _ := importer.FromUpload("x.csv", []byte("a,b\n1,2\n"))
	if _, err := importer.BuildPlan(lib, v2, importer.BuildOptions{SourceName: "x.csv"}); err == nil || !strings.Contains(err.Error(), "Choose which collection") {
		t.Fatalf("got %v", err)
	}
}

func TestPreviewExpires(t *testing.T) {
	s := importer.NewPreviewStore()
	id := s.Put(&importer.Plan{}, nil, nil, "")
	if _, err := s.Get(id); err != nil {
		t.Fatal(err)
	}
	s.Drop(id)
	if _, err := s.Get(id); err == nil {
		t.Fatal("dropped preview should be gone")
	}
}

// Local only: runs when private-fixtures/ holds the author's real export (it is gitignored, so CI skips this). Only
// counts are asserted; nothing from the export is copied into the repository.
func TestRealExportLocalOnly(t *testing.T) {
	const real = "../../../private-fixtures/Cyber Security Dashboard.zip"
	if _, err := os.Stat(real); err != nil {
		t.Skip("no private export on this machine")
	}
	lib := open(t)
	vfs, name, err := importer.FromPath(real)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := importer.BuildPlan(lib, vfs, importer.BuildOptions{SourceName: name})
	if err != nil {
		t.Fatal(err)
	}
	c := counts(plan)
	if c["commands"] != 15 || c["concepts"] != 2 || c["projects"] != 1 || c["log"] != 1 || c["labs"] != 0 || len(plan.Templates) != 4 {
		t.Fatalf("counts %v, %d templates", c, len(plan.Templates))
	}
	rep, err := importer.Commit(lib, plan, importer.Options{Strategy: importer.StrategyUpdateEmptyOnly}, &importer.Source{VFS: vfs})
	if err != nil {
		t.Fatal(err)
	}
	eq(t, rep.Created["commands"], 15)
	nFS, nGen := 0, 0
	for _, e := range lib.Repos["commands"].All() {
		switch e["category"] {
		case "File System":
			nFS++
		case "General":
			nGen++
		}
	}
	eq(t, nFS, 10)
	eq(t, nGen, 5)
	plan2, _ := importer.BuildPlan(lib, vfs, importer.BuildOptions{SourceName: name})
	again, err := importer.Commit(lib, plan2, importer.Options{Strategy: importer.StrategyUpdateEmptyOnly}, &importer.Source{VFS: vfs})
	if err != nil {
		t.Fatal(err)
	}
	eq(t, len(again.Created), 0)
	eq(t, len(again.Updated), 0)
}
