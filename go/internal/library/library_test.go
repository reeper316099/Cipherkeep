package library

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/reeper316099/Cipherkeep/go/internal/domain"
	"github.com/reeper316099/Cipherkeep/go/internal/study"
)

func ok(err error) {
	if err != nil {
		panic(err)
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func open(t *testing.T, dir string) *Library {
	t.Helper()
	lib, err := Open(Options{DataDir: dir, LaunchBackupDelay: -1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lib.Close() })
	return lib
}

func titlesOf(q Queue) []string {
	var out []string
	for _, i := range q.Items {
		out = append(out, i.Card.Title)
	}
	return out
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]int{}
	for _, x := range a {
		m[x]++
	}
	for _, x := range b {
		m[x]--
	}
	for _, n := range m {
		if n != 0 {
			return false
		}
	}
	return true
}

func TestStudyQueueAnswersStatsQuestionsAndOverview(t *testing.T) {
	dir := t.TempDir()
	lib := open(t, dir)
	cmds, concepts := lib.Repos["commands"], lib.Repos["concepts"]
	ls := must(cmds.Create(map[string]any{
		"name": "ls", "category": "File System", "explanation": "list entries",
		"options":  []any{map[string]any{"flag": "-a", "meaning": "include dotfiles"}},
		"examples": []any{map[string]any{"command": "ls -al ~", "description": "long list of home"}},
	}, ""))
	cp := must(cmds.Create(map[string]any{
		"name": "cp", "category": "File System", "explanation": "copy files",
		"examples": []any{map[string]any{"command": "cp a b", "description": "copy a to b"}},
	}, ""))
	tree := must(concepts.Create(map[string]any{
		"title": "Directory Tree", "category": "Linux",
		"sections": []any{
			map[string]any{"key": "what-is-it", "title": "What is it?", "markdown": "Nested folders"},
			map[string]any{"key": "questions-i-still-have", "title": "Questions", "markdown": "- Who can read /etc?\n- [x] Where are logs?\n-\n"},
		},
	}, ""))
	must(cmds.Create(map[string]any{"name": "bare", "category": "General"}, "")) // nothing to build a card from

	// offers new cards and skips entities that cannot form a card
	q := must(lib.Study.Queue(QueueOptions{}))
	if !sameSet(titlesOf(q), []string{"Directory Tree", "cp", "ls"}) || q.Due != 0 || q.NewAvailable != 3 {
		t.Fatalf("%+v", titlesOf(q))
	}
	for _, i := range q.Items {
		if !i.IsNew {
			t.Fatal("never-reviewed cards are new")
		}
	}
	only := must(lib.Study.Queue(QueueOptions{Collection: "concepts"}))
	if len(only.Items) != 1 || only.Items[0].Card.Type != study.CardWhat || only.Items[0].Card.Kind != "reveal" {
		t.Fatalf("%+v", only.Items)
	}

	// SM-2 + entity fields + persisted state + log
	r := must(lib.Study.Answer(AnswerInput{ID: ls.ID(), Rating: study.Good, CardType: "name"}))
	if r.Srs.IntervalDays != 1 || r.Srs.Reps != 1 || r.Srs.Ease != 2.5 || r.Mastery != 1 {
		t.Fatalf("%+v", r)
	}
	got, _ := cmds.Get(ls.ID())
	if got.Num("mastery") != 1 || got.Num("practiceCount") != 1 || got.Obj("srs")["intervalDays"] != 1.0 {
		t.Fatalf("%+v", got)
	}
	if lp, ok := study.ParseTime(got.Str("lastPracticedAt")); !ok || time.Since(lp) > 5*time.Second {
		t.Fatalf("lastPracticedAt %q", got.Str("lastPracticedAt"))
	}
	var state struct {
		Data struct {
			Items map[string]map[string]any `json:"items"`
		} `json:"data"`
	}
	raw := must(os.ReadFile(filepath.Join(dir, "study/srs-state.json")))
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	if it := state.Data.Items[ls.ID()]; it["reps"] != 1.0 || it["intervalDays"] != 1.0 || it["firstAt"] == nil {
		t.Fatalf("%+v", it)
	}
	logFile := filepath.Join(dir, "study/sessions", time.Now().UTC().Format("2006-01")+".jsonl")
	line := strings.Split(strings.TrimSpace(string(must(os.ReadFile(logFile)))), "\n")[0]
	if !strings.Contains(line, `"rating":"good"`) || !strings.Contains(line, `"isNew":true`) {
		t.Fatalf("log line: %s", line)
	}

	// a lapse comes back in minutes and shows as due later
	lapse := must(lib.Study.Answer(AnswerInput{ID: cp.ID(), Rating: study.Again}))
	if lapse.Srs.Reps != 0 || lapse.Srs.Lapses != 1 || lapse.Srs.IntervalDays != 0 || lapse.Mastery != 1 {
		t.Fatalf("%+v", lapse)
	}
	if must(lib.Study.Queue(QueueOptions{})).Due != 0 {
		t.Fatal("not due for 10 minutes")
	}
	file := must(lib.Study.state())
	must(file.Update(func(d *StateFile) error {
		cs, ls2 := d.Items[cp.ID()], d.Items[ls.ID()]
		cs.DueAt = domain.ISO(time.Now().Add(-time.Minute))
		ls2.DueAt = domain.ISO(time.Now().Add(-4 * 24 * time.Hour))
		d.Items[cp.ID()], d.Items[ls.ID()] = cs, ls2
		return nil
	}))
	q = must(lib.Study.Queue(QueueOptions{}))
	if q.Due != 2 || q.Items[0].Card.Title != "ls" || q.Items[0].IsNew || q.Items[1].IsNew {
		t.Fatalf("most overdue first: %v due=%d", titlesOf(q), q.Due)
	}
	if !q.Items[2].IsNew || q.Items[2].Card.Title != "Directory Tree" {
		t.Fatalf("new card after the due ones: %v", titlesOf(q))
	}

	// concept confidence, bad input
	c := must(lib.Study.Answer(AnswerInput{ID: tree.ID(), Rating: study.Easy, Collection: "concepts"}))
	if c.Srs.IntervalDays != 3 {
		t.Fatalf("%+v", c)
	}
	if g, _ := concepts.Get(tree.ID()); g.Num("confidence") != 1 {
		t.Fatalf("confidence %v", g.Num("confidence"))
	}
	if _, err := lib.Study.Answer(AnswerInput{ID: "nope", Rating: study.Good}); err == nil {
		t.Fatal("unknown id")
	}

	// practise one entity on demand even when it is not due
	on := must(lib.Study.Queue(QueueOptions{OnlyID: ls.ID()}))
	if len(on.Items) != 1 || on.Items[0].Card.EntityID != ls.ID() {
		t.Fatalf("%+v", on.Items)
	}

	// stats and the daily new-card limit
	s := must(lib.Study.Stats(time.Time{}, 0))
	if s.ReviewedToday != 3 || s.NewToday != 3 || s.NewPerDay != 10 {
		t.Fatalf("%+v", s)
	}
	if s.Retention30d == nil || *s.Retention30d != 67 { // good + easy of (good, again, easy)
		t.Fatalf("retention %v", s.Retention30d)
	}
	must(lib.Settings.Update(func(st *domain.Settings) error { st.StudyNewPerDay = 3; return nil }))
	fresh := must(cmds.Create(map[string]any{"name": "mkdir", "category": "File System", "explanation": "make dirs"}, ""))
	q = must(lib.Study.Queue(QueueOptions{}))
	for _, i := range q.Items {
		if i.Card.EntityID == fresh.ID() {
			t.Fatal("the quota is used up")
		}
	}
	if q.NewAvailable != 1 {
		t.Fatalf("newAvailable %d", q.NewAvailable)
	}
	must(lib.Settings.Update(func(st *domain.Settings) error { st.StudyNewPerDay = 5; return nil }))
	found := false
	for _, i := range must(lib.Study.Queue(QueueOptions{})).Items {
		found = found || i.Card.EntityID == fresh.ID()
	}
	if !found {
		t.Fatal("a bigger quota lets the new card in")
	}

	// questions
	open := lib.Study.Questions(false)
	if len(open) != 1 || open[0].Text != "Who can read /etc?" || open[0].Concept != "Directory Tree" || open[0].Done {
		t.Fatalf("%+v", open)
	}
	if all := lib.Study.Questions(true); len(all) != 2 || all[1].Text != "Where are logs?" {
		t.Fatalf("%+v", all)
	}

	// overview
	must(lib.Repos["labs"].Create(map[string]any{
		"title": "Bandit 0", "platform": "Bandit", "status": "Completed", "difficulty": "Easy", "domain": "linux",
		"conceptIds": []any{tree.ID()}, "timeSpentMin": 40.0,
	}, ""))
	must(lib.Repos["projects"].Create(map[string]any{"title": "Tool", "status": "In progress"}, ""))
	must(lib.Repos["log"].Create(map[string]any{"title": "Today", "date": time.Now().UTC().Format("2006-01-02"), "timeSpentMin": 30.0}, ""))
	for _, c := range []string{"commands", "concepts", "labs", "projects", "log"} {
		for _, e := range lib.Repos[c].All() {
			ok(lib.Activity.Record("create", c, e.ID()))
		}
	}
	o := must(lib.BuildOverview(OverviewOptions{}))
	if o.KPIs.CommandsTotal != 4 || o.KPIs.ConceptsStudied != 1 || o.KPIs.LabsCompleted != 1 || o.KPIs.ActiveProjects != 1 {
		t.Fatalf("%+v", o.KPIs)
	}
	if o.Mastery[0] != 2 || o.Mastery[1] != 2 {
		t.Fatalf("mastery %v", o.Mastery) // bare + mkdir unseen, ls + cp learning
	}
	if o.Streak.Current != 1 || o.Banner.Threat.Level != "LOW" || !strings.Contains(o.Banner.Threat.Explanation, "Score =") {
		t.Fatalf("%+v %+v", o.Streak, o.Banner)
	}
	if n := 0; true {
		for _, v := range o.Heatmap.Days {
			n += v
		}
		if n <= 5 {
			t.Fatalf("heatmap total %d", n)
		}
	}
	if o.Labs.ByPlatform["Bandit"] != (PlatformStatus{1, 1}) || len(o.TimePerWeek) != 12 || o.TimePerWeek[11].Minutes != 70 {
		t.Fatalf("%+v %+v", o.Labs, o.TimePerWeek)
	}
	var linux, crypto SkillRow
	for _, sk := range o.SkillMap {
		switch sk.Name {
		case "Linux":
			linux = sk
		case "Cryptography":
			crypto = sk
		}
	}
	if linux.Score <= 0 || linux.Counts.Concepts != 1 || linux.Counts.Labs != 1 || crypto.Score != 0 || len(o.SkillMap) != 13 {
		t.Fatalf("%+v", linux)
	}
	kinds := map[string]bool{}
	for _, r := range o.Recommendations {
		if r.Why == "" || r.Route == "" {
			t.Fatalf("recommendation without a reason: %+v", r)
		}
		kinds[r.Kind] = true
	}
	if !kinds["open-question"] || len(o.Recent) == 0 || o.Study.Due != 2 {
		t.Fatalf("%+v %d", kinds, o.Study.Due)
	}
	// the day's skill snapshot is kept for trends
	var hist struct {
		Data History `json:"data"`
	}
	if err := json.Unmarshal(must(os.ReadFile(filepath.Join(dir, "study/skill-history.json"))), &hist); err != nil {
		t.Fatal(err)
	}
	if today := time.Now().UTC().Format("2006-01-02"); hist.Data.Days[today]["Linux"] <= 0 {
		t.Fatalf("history: %+v", hist.Data.Days)
	}
}

func TestThreatLevelRisesWhenActivityIsStaleAndOldSkillFilesMigrate(t *testing.T) {
	dir := t.TempDir()
	stamp := "2026-01-01T00:00:00.000Z"
	write := func(rel, text string) {
		p := filepath.Join(dir, rel)
		ok(os.MkdirAll(filepath.Dir(p), 0o755))
		ok(os.WriteFile(p, []byte(text), 0o644))
	}
	write("data/commands/general.json", `{"schemaVersion":1,"data":{"items":[{"id":"01HZZZZZZZZZZZZZZZZZZZZZZZ","title":"x","slug":"x","name":"x","category":"General","createdAt":"`+stamp+`","updatedAt":"`+stamp+`"}]}}`)
	old := time.Now().UTC().Add(-6 * 24 * time.Hour)
	write("activity/"+old.Format("2006-01")+".jsonl", `{"ts":"`+domain.ISO(old)+`","type":"update"}`+"\n")
	write("config/skills.json", `{"schemaVersion":1,"data":[{"name":"Linux","weight":2},{"name":"Custom","weight":1}]}`)
	lib := open(t, dir)
	o := must(lib.BuildOverview(OverviewOptions{}))
	th := o.Banner.Threat
	if th.DaysSinceActivity == nil || *th.DaysSinceActivity < 5 || (th.Level != "ELEVATED" && th.Level != "HIGH") || o.Streak.Current != 0 {
		t.Fatalf("%+v streak %+v", th, o.Streak)
	}
	skills := lib.Skills.Data()
	if skills[0].Name != "Linux" || skills[0].Weight != 2 || !contains(skills[0].Aliases, "file system") || len(skills[1].Aliases) != 0 {
		t.Fatalf("%+v", skills)
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func TestAnEmptyProfileIsLowAndTheLibraryReopens(t *testing.T) {
	dir := t.TempDir()
	lib := open(t, dir)
	o := must(lib.BuildOverview(OverviewOptions{}))
	if o.Banner.Threat.Level != "LOW" || o.Banner.Threat.Score != 0 {
		t.Fatalf("%+v", o.Banner.Threat)
	}
	for _, f := range []string{"meta.json", "config/settings.json", "config/categories.json", "config/skills.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s was not created", f)
		}
	}
	must(lib.Repos["notes"].Create(map[string]any{"title": "n", "markdown": "kept"}, ""))
	lib.Close()
	if _, err := Open(Options{DataDir: dir, LaunchBackupDelay: -1}); err != nil {
		t.Fatalf("reopen: %v", err)
	}
}

func TestOnlyOneProcessMayHaveALibraryOpen(t *testing.T) {
	dir := t.TempDir()
	lib := open(t, dir)
	if _, err := Open(Options{DataDir: dir, LaunchBackupDelay: -1}); err == nil {
		t.Fatal("a second Open of the same folder must be refused")
	}
	_ = lib
}

func TestActivityLogIsTolerantOfTornLinesAndRanges(t *testing.T) {
	lib := open(t, t.TempDir())
	ok(lib.Activity.RecordAt(time.Date(2026, 1, 31, 23, 0, 0, 0, time.UTC), "create", "notes", "a"))
	ok(lib.Activity.RecordAt(time.Date(2026, 2, 1, 1, 0, 0, 0, time.UTC), "update", "notes", "a"))
	p := filepath.Join(lib.Dir, "activity/2026-02.jsonl")
	f := must(os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644))
	_, _ = f.WriteString(`{"ts":"2026-02-0`) // a torn last line
	f.Close()
	if got := lib.Activity.Range("2026-01-15", "2026-02-15"); len(got) != 2 || got[0].Type != "create" || got[1].Type != "update" {
		t.Fatalf("%+v", got)
	}
	if got := lib.Activity.Month("bogus"); got != nil {
		t.Fatal("a bad month gives nothing")
	}
}
