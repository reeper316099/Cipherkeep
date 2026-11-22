package starter

import (
	"bytes"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/reeper316099/Cipherkeep/go/internal/domain"
	"github.com/reeper316099/Cipherkeep/go/internal/importer"
	"github.com/reeper316099/Cipherkeep/go/internal/library"
)

func byColl(p *importer.Plan, c string) []importer.PlanItem {
	var out []importer.PlanItem
	for _, i := range p.Items {
		if i.Collection == c {
			out = append(out, i)
		}
	}
	return out
}

// The pack is embedded from go/internal/starter/data; this keeps it identical to the files the Node app bundles.
func TestEmbeddedPackEqualsStarterPackFolder(t *testing.T) {
	for _, n := range []string{"commands", "concepts", "labs", "notes"} {
		want, err := os.ReadFile("../../../starter-pack/" + n + ".json")
		if err != nil {
			t.Fatal(err)
		}
		got, _ := files.ReadFile("data/" + n + ".json")
		if !bytes.Equal(got, want) {
			t.Errorf("data/%s.json differs from starter-pack/%s.json: copy it again", n, n)
		}
	}
}

func TestContent(t *testing.T) {
	if !reflect.DeepEqual(Counts(), map[string]int{"commands": 45, "concepts": 10, "notes": 4, "labs": 3}) {
		t.Fatalf("counts %v", Counts())
	}
	p := Plan()
	if len(p.Items) != 62 {
		t.Fatalf("%d items", len(p.Items))
	}
	refs := map[string]bool{}
	for _, it := range p.Items {
		if _, err := domain.Schemas[it.Collection].Parse(it.Input, domain.ModeCreate); err != nil {
			t.Errorf("%s:%s invalid: %v", it.Collection, it.Title, err)
		}
		if !strings.HasPrefix(it.Ref, "starter:") || refs[it.Ref] {
			t.Errorf("bad or duplicate ref %q", it.Ref)
		}
		refs[it.Ref] = true
		tagged := false
		for _, tg := range it.Input["tags"].([]any) {
			tagged = tagged || tg == "starter"
		}
		if !tagged {
			t.Errorf("%s is not tagged", it.Title)
		}
	}
}

func TestCommands(t *testing.T) {
	var destructive []string
	names := map[string]bool{}
	for _, it := range byColl(Plan(), "commands") {
		c := it.Input
		name := c["name"].(string)
		if names[name] {
			t.Errorf("duplicate %s", name)
		}
		names[name] = true
		exs := c["examples"].([]any)
		if strings.TrimSpace(c["syntax"].(string)) == "" || len(strings.TrimSpace(c["explanation"].(string))) <= 5 ||
			len(strings.TrimSpace(c["whatItDoes"].(string))) <= 20 || len(exs) < 2 || len(c["options"].([]any)) < 2 {
			t.Errorf("%s is incomplete", name)
		}
		re := regexp.MustCompile(`(^|[\s|;(])` + regexp.QuoteMeta(name) + `(\s|$)`)
		for _, e := range exs {
			m := e.(map[string]any)
			if len(strings.TrimSpace(m["description"].(string))) <= 4 {
				t.Errorf("%s: short example description", name)
			}
			if !re.MatchString(m["command"].(string)) {
				t.Errorf("%s: example %q does not use the command", name, m["command"])
			}
		}
		if c["risk"] == "destructive" {
			destructive = append(destructive, name)
		}
	}
	if !reflect.DeepEqual(destructive, []string{"rm"}) {
		t.Fatalf("destructive: %v", destructive)
	}
}

func TestRelationsPointIntoThePack(t *testing.T) {
	p := Plan()
	refs := map[string]bool{}
	for _, i := range p.Items {
		refs[i.Ref] = true
	}
	n := 0
	for _, i := range p.Items {
		for _, r := range i.Relations {
			for _, tg := range r.Targets {
				n++
				if !refs[tg.Ref] {
					t.Errorf("%s: dangling %s", i.Title, tg.Ref)
				}
			}
		}
	}
	if n == 0 {
		t.Fatal("no relations")
	}
}

func TestLoadIsIdempotentAndNeverOverwritesEdits(t *testing.T) {
	lib, err := library.Open(library.Options{DataDir: t.TempDir(), LaunchBackupDelay: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	load := func(s importer.Strategy) *importer.Report {
		r, err := importer.Commit(lib, Plan(), importer.Options{Strategy: s}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	first := load(importer.StrategyUpdateEmptyOnly)
	total := 0
	for _, n := range first.Created {
		total += n
	}
	if total != 62 {
		t.Fatalf("created %d", total)
	}
	var ls domain.Entity
	for _, e := range lib.Repos["commands"].All() {
		if e["name"] == "ls" {
			ls = e
		}
	}
	if _, err := lib.Repos["commands"].Patch(ls.ID(), map[string]any{"explanation": "mine"}); err != nil {
		t.Fatal(err)
	}
	second := load(importer.StrategyUpdateEmptyOnly)
	if len(second.Created) != 0 || len(second.Updated) != 0 {
		t.Fatalf("second load changed things: %+v %+v", second.Created, second.Updated)
	}
	got, _ := lib.Repos["commands"].Get(ls.ID())
	if got["explanation"] != "mine" {
		t.Fatal("edit was overwritten")
	}
	if _, err := importer.Undo(lib, first.ID); err != nil {
		t.Fatal(err)
	}
	if len(lib.Repos["commands"].All()) != 0 {
		t.Fatal("undo left starter commands behind")
	}
}
