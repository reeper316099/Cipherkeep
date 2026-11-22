package domain

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func parse(t *testing.T, coll string, mode Mode, in map[string]any) Entity {
	t.Helper()
	e, err := Schemas[coll].Parse(in, mode)
	if err != nil {
		t.Fatalf("parse %s: %v", coll, err)
	}
	return e
}

var base = map[string]any{
	"id": "01HZZZZZZZZZZZZZZZZZZZZZZZ", "title": "ls", "slug": "ls",
	"createdAt": "2026-01-01T00:00:00.000Z", "updatedAt": "2026-01-01T00:00:00.000Z",
}

func with(extra map[string]any) map[string]any {
	m := map[string]any{}
	for k, v := range base {
		m[k] = v
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func TestEveryCollectionHasASchema(t *testing.T) {
	for _, c := range Collections {
		if Schemas[c] == nil {
			t.Errorf("no schema for %s", c)
		}
	}
}

func TestAppliesDefaultsForAMinimalCommand(t *testing.T) {
	e := parse(t, "commands", ModeFull, with(map[string]any{"name": "ls"}))
	if e.Str("risk") != "safe" || e.Num("mastery") != 0 || len(e.Objs("options")) != 0 {
		t.Fatalf("defaults: %+v", e)
	}
	if srs := e.Obj("srs"); srs["ease"] != 2.5 || srs["intervalDays"] != float64(0) {
		t.Fatalf("nested srs defaults: %+v", srs)
	}
	if !reflect.DeepEqual(e.Strs("platforms"), []string{"any"}) {
		t.Fatalf("platforms: %v", e["platforms"])
	}
	if v, ok := e["tags"].([]any); !ok || v == nil || len(v) != 0 {
		t.Fatalf("tags must be an empty list, not null: %#v", e["tags"])
	}
	b, _ := json.Marshal(e)
	if strings.Contains(string(b), "null") {
		t.Fatalf("a minimal command must not serialise nulls: %s", b)
	}
}

func TestRejectsBadValuesWithZodStyleIssues(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
		path string
	}{
		{"mastery out of range", with(map[string]any{"name": "ls", "mastery": 7.0}), "mastery"},
		{"missing required name", with(nil), "name"},
		{"wrong type", with(map[string]any{"name": "ls", "tags": "nope"}), "tags"},
		{"negative practice count", with(map[string]any{"name": "ls", "practiceCount": -1.0}), "practiceCount"},
		{"fractional practice count", with(map[string]any{"name": "ls", "practiceCount": 1.5}), "practiceCount"},
		{"bad enum", with(map[string]any{"name": "ls", "risk": "spicy"}), "risk"},
		{"nested item missing a key", with(map[string]any{"name": "ls", "examples": []any{map[string]any{"command": "ls"}}}), "examples"},
	}
	for _, c := range cases {
		_, err := Schemas["commands"].Parse(c.in, ModeFull)
		ve, ok := err.(*ValidationError)
		if !ok || len(ve.Issues) == 0 || ve.Issues[0].Path[0] != c.path {
			t.Errorf("%s: err = %v", c.name, err)
		}
	}
}

func TestCreateModeDropsIdAndTimestampsAndMakesTitleOptional(t *testing.T) {
	e := parse(t, "commands", ModeCreate, map[string]any{"name": "ls", "id": "forced", "createdAt": "x", "bogus": 1})
	if _, has := e["id"]; has {
		t.Fatal("id must not be accepted on create")
	}
	if _, has := e["createdAt"]; has {
		t.Fatal("createdAt must not be accepted on create")
	}
	if _, has := e["bogus"]; has {
		t.Fatal("unknown keys must be dropped")
	}
	if _, has := e["title"]; has {
		t.Fatal("title stays absent so the caller can derive it from name")
	}
	if e.Str("category") != "" || e["archived"] != false {
		t.Fatalf("defaults still apply on create: %+v", e)
	}
}

func TestPatchModeOnlyReturnsProvidedKeys(t *testing.T) {
	e := parse(t, "commands", ModePatch, map[string]any{"mastery": 2.0, "id": "nope", "title": "new"})
	if len(e) != 2 || e.Num("mastery") != 2 || e.Title() != "new" {
		t.Fatalf("patch: %+v", e)
	}
	if _, err := Schemas["commands"].Parse(map[string]any{"mastery": 9.0}, ModePatch); err == nil {
		t.Fatal("an invalid patch value must be rejected")
	}
}

func TestNullableFieldsAcceptNullAndDefaultToIt(t *testing.T) {
	e := parse(t, "snippets", ModeFull, with(map[string]any{}))
	if v, has := e["code"]; !has || v != nil {
		t.Fatalf("code should default to null: %#v", e["code"])
	}
	p := parse(t, "snippets", ModePatch, map[string]any{"code": nil})
	if v, has := p["code"]; !has || v != nil {
		t.Fatalf("patching code to null must be kept: %#v", p)
	}
	l := parse(t, "log", ModeFull, with(map[string]any{"date": "2026-03-24"}))
	if v, has := l["timeSpentMin"]; !has || v != nil {
		t.Fatal("timeSpentMin should default to null")
	}
}

func TestSlugifyAndNormalizeTitle(t *testing.T) {
	for in, want := range map[string]string{"Ünïcode Tést!": "unicode-test", "  ": "untitled", "ls -la": "ls-la", "日本": "untitled"} {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"  Hello\u00a0 World  ": "hello world", "⚙️ Basic": "⚙ basic", "ﬁle": "file", "A\ufeffB": "a b"} {
		if got := NormalizeTitle(in); got != want {
			t.Errorf("NormalizeTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWikiLinks(t *testing.T) {
	md := "See [[Title]], [[cmd:ls]] and [[Other|alias]]. Not `[[code]]`, not [[:upper:]]\n```\n[[fenced]]\n```\n[[bogus:thing]]"
	got := ExtractWikiLinks(md)
	want := []WikiLink{
		{Raw: "[[Title]]", Target: "Title"},
		{Raw: "[[cmd:ls]]", Kind: "cmd", Target: "ls"},
		{Raw: "[[Other|alias]]", Target: "Other", Alias: "alias"},
		{Raw: "[[bogus:thing]]", Target: "bogus:thing"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	idx := BuildTitleIndex([]TitleIndexEntry{
		{Type: "commands", ID: "c1", Title: "List files", Name: "ls"},
		{Type: "notes", ID: "n1", Title: "ls"},
	})
	if typ, id, ok := ResolveWikiLink(idx, WikiLink{Target: "LS"}); !ok || typ != "notes" || id != "n1" {
		t.Fatalf("un-kinded links try notes first: %v %v %v", typ, id, ok)
	}
	if typ, id, ok := ResolveWikiLink(idx, WikiLink{Kind: "cmd", Target: "ls"}); !ok || typ != "commands" || id != "c1" {
		t.Fatalf("kinded link: %v %v %v", typ, id, ok)
	}
	if _, _, ok := ResolveWikiLink(idx, WikiLink{Target: "missing"}); ok {
		t.Fatal("unknown title resolved")
	}
}

func TestQuestions(t *testing.T) {
	md := "- a?\n- [x] b?\n-\nnot a bullet\n* [ ] c?"
	qs := ParseQuestions(md)
	if len(qs) != 3 || qs[0].Done || !qs[1].Done || qs[2].Line != 4 {
		t.Fatalf("%+v", qs)
	}
	if CountOpen(md) != 2 {
		t.Fatal("open count")
	}
	if got := strings.Split(ToggleQuestion(md, 0), "\n")[0]; got != "- [x] a?" {
		t.Fatalf("toggle on: %q", got)
	}
	if got := strings.Split(ToggleQuestion(md, 1), "\n")[1]; got != "- [ ] b?" {
		t.Fatalf("toggle off: %q", got)
	}
	if ToggleQuestion(md, 3) != md || ToggleQuestion(md, 99) != md {
		t.Fatal("toggling a non-bullet must change nothing")
	}
}

func TestConfigDefaultsAndValidation(t *testing.T) {
	s := DefaultSettings()
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseSettings([]byte(`{"theme":"light","bogus":1}`)); err == nil {
		t.Fatal("unknown settings keys must be rejected")
	}
	if got, err := ParseSettings([]byte(`{"theme":"light"}`)); err != nil || got.Theme != "light" || got.BackupKeep != 20 {
		t.Fatalf("partial settings: %+v %v", got, err)
	}
	for _, bad := range []string{`{"backupKeep":0}`, `{"theme":"neon"}`, `{"studyNewPerDay":101}`} {
		if _, err := ParseSettings([]byte(bad)); err == nil {
			t.Errorf("%s should be rejected", bad)
		}
	}
	if SectionKey("🧠 Intuition (VERY IMPORTANT)") != "intuition-very-important" || SectionKey("❓ Questions I still have") != QuestionsKey {
		t.Fatal("section keys")
	}
	if len(DefaultTemplates()["concepts"].Sections) != 13 {
		t.Fatal("concept template should have 13 sections")
	}
}

func TestSkillsV1MigrationAddsDefaultAliases(t *testing.T) {
	out, err := MigrateSkillsV1([]byte(`[{"name":"linux","weight":2},{"name":"Custom"}]`))
	if err != nil {
		t.Fatal(err)
	}
	var got []SkillDomain
	_ = json.Unmarshal(out, &got)
	if got[0].Weight != 2 || len(got[0].Aliases) == 0 || got[1].Weight != 1 || len(got[1].Aliases) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestNewIDIsUniqueAndSortable(t *testing.T) {
	seen := map[string]bool{}
	prev := ""
	for i := 0; i < 2000; i++ {
		id := NewID()
		if len(id) != 26 || seen[id] || id < prev {
			t.Fatalf("bad id %q after %q", id, prev)
		}
		seen[id], prev = true, id
	}
}
