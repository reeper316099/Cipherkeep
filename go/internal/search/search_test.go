package search

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/reeper316099/Cipherkeep/go/internal/domain"
)

func contains(xs []string, want ...string) bool {
	for _, w := range want {
		found := false
		for _, x := range xs {
			found = found || x == w
		}
		if !found {
			return false
		}
	}
	return true
}

func TestTokenizeKeepsLeadingDashesAndEmitsUsefulSubTokens(t *testing.T) {
	if got := Tokenize("-la"); !contains(got, "-la", "la") {
		t.Fatalf("%v", got)
	}
	if got := Tokenize("--color=auto"); !contains(got, "--color=auto", "--color", "color", "auto") {
		t.Fatalf("%v", got)
	}
	if got := Tokenize("/etc/passwd"); !contains(got, "etc", "passwd") {
		t.Fatalf("%v", got)
	}
	if got := Tokenize("[[:upper:]]*"); !contains(got, "upper") {
		t.Fatalf("%v", got)
	}
	if got := Tokenize("Hello, World! (again)"); !reflect.DeepEqual(got, []string{"hello", "world", "again"}) {
		t.Fatalf("%v", got)
	}
	if got := Tokenize("a a a"); len(got) != 1 {
		t.Fatalf("tokens are unique: %v", got)
	}
}

func TestParseQueryExtractsOperatorsQuotedValuesAndFreeText(t *testing.T) {
	text, f := ParseQuery(`cmd:ls tag:Linux cat:"File System" status:done find me`)
	if text != "ls find me" {
		t.Fatalf("text %q", text)
	}
	if !reflect.DeepEqual(f.Types, []string{"commands"}) || !reflect.DeepEqual(f.Tags, []string{"linux"}) ||
		!reflect.DeepEqual(f.Cats, []string{"file system"}) || !reflect.DeepEqual(f.Statuses, []string{"done"}) {
		t.Fatalf("%+v", f)
	}
	if text, f := ParseQuery("type:notes http://example.com"); text != "http://example.com" || !reflect.DeepEqual(f.Types, []string{"notes"}) {
		t.Fatalf("%q %+v", text, f)
	}
	if _, f := ParseQuery("type:bogus"); f.HasTypes {
		t.Fatal("an unknown type alias is ignored")
	}
}

func cmd(id, name, cat, what, syntax string, flags ...string) domain.Entity {
	opts := []any{}
	for _, f := range flags {
		opts = append(opts, map[string]any{"flag": f, "meaning": "flag " + f})
	}
	e, err := domain.Schemas["commands"].Parse(map[string]any{
		"id": id, "title": name, "slug": name, "name": name, "category": cat, "whatItDoes": what, "syntax": syntax,
		"options": opts, "createdAt": "2026-01-01T00:00:00.000Z", "updatedAt": "2026-01-01T00:00:00.000Z",
		"tags": []any{"linux"},
	}, domain.ModeFull)
	if err != nil {
		panic(err)
	}
	return e
}

func note(id, title, md string) domain.Entity {
	e, err := domain.Schemas["notes"].Parse(map[string]any{
		"id": id, "title": title, "slug": title, "markdown": md, "createdAt": "2026-01-01T00:00:00.000Z", "updatedAt": "2026-01-01T00:00:00.000Z",
	}, domain.ModeFull)
	if err != nil {
		panic(err)
	}
	return e
}

func build(t *testing.T) *Index {
	t.Helper()
	ix := NewIndex()
	ix.Rebuild("commands", []domain.Entity{
		cmd("c1", "ls", "File System", "Lists directory contents", "ls [-la] [dir]", "-l", "-a", "--color=auto"),
		cmd("c2", "chmod", "Permissions", "Changes file permissions", "chmod <mode> <file>", "-R"),
		cmd("c3", "tcpdump", "Networking", "Captures network packets", "tcpdump -i <iface>", "-i"),
	})
	ix.Rebuild("notes", []domain.Entity{
		note("n1", "Permissions cheat sheet", "# Permissions\n\nUse chmod to change permissions.\n\n```bash\nchmod 755 file\n```\n"),
	})
	return ix
}

func ids(hs []Hit) []string {
	out := make([]string, len(hs))
	for i, h := range hs {
		out[i] = h.ID
	}
	return out
}

func TestFindsDashFlagsAndLongOptions(t *testing.T) {
	ix := build(t)
	for _, q := range []string{"-la", "la", "--color=auto", "color"} {
		if got := ids(ix.Search(q, Options{})); len(got) == 0 || got[0] != "c1" {
			t.Errorf("%q → %v", q, got)
		}
	}
}

func TestPrefixFuzzyAndAndSemantics(t *testing.T) {
	ix := build(t)
	if got := ids(ix.Search("tcpd", Options{})); len(got) != 1 || got[0] != "c3" {
		t.Fatalf("prefix: %v", got)
	}
	if got := ids(ix.Search("permisions", Options{})); !contains(got, "c2", "n1") { // one letter missing
		t.Fatalf("typo: %v", got)
	}
	if got := ids(ix.Search("tcp zzzzz", Options{})); len(got) != 0 {
		t.Fatalf("all words must match: %v", got)
	}
	if got := ids(ix.Search("chmod permissions", Options{})); !contains(got, "c2", "n1") {
		t.Fatalf("and: %v", got)
	}
	if got := ids(ix.Search("", Options{})); len(got) != 0 {
		t.Fatal("an empty query finds nothing")
	}
	if got := ids(ix.Search("xyz", Options{})); len(got) != 0 {
		t.Fatalf("%v", got)
	}
}

func TestNameAndTitleOutrankBodyMatches(t *testing.T) {
	ix := build(t)
	hits := ix.Search("chmod", Options{})
	if len(hits) < 2 || hits[0].ID != "c2" || hits[0].Score <= hits[1].Score {
		t.Fatalf("the command named chmod must rank above a note that mentions it: %+v", hits)
	}
}

func TestOperatorsFilterAndFilterOnlyQueriesListEverythingMatching(t *testing.T) {
	ix := build(t)
	if got := ids(ix.Search("cmd:ls", Options{})); !reflect.DeepEqual(got, []string{"c1"}) {
		t.Fatalf("cmd: %v", got)
	}
	if got := ids(ix.Search("permissions type:notes", Options{})); !reflect.DeepEqual(got, []string{"n1"}) {
		t.Fatalf("type: %v", got)
	}
	if got := ids(ix.Search(`cat:"file system"`, Options{})); !reflect.DeepEqual(got, []string{"c1"}) {
		t.Fatalf("cat: %v", got)
	}
	if got := ids(ix.Search("tag:linux", Options{})); !reflect.DeepEqual(got, []string{"c1", "c2", "c3"}) {
		t.Fatalf("tag only: %v", got)
	}
	if got := ids(ix.Search("tag:windows", Options{})); len(got) != 0 {
		t.Fatalf("%v", got)
	}
	if got := ids(ix.Search("chmod", Options{Types: []string{"notes"}})); !reflect.DeepEqual(got, []string{"n1"}) {
		t.Fatalf("types option: %v", got)
	}
	if got := ids(ix.Search("tag:linux", Options{Limit: 2})); len(got) != 2 {
		t.Fatalf("limit: %v", got)
	}
}

func TestIndexFollowsUpsertsAndRemovals(t *testing.T) {
	ix := build(t)
	ix.Upsert("commands", cmd("c4", "nmap", "Networking", "Scans hosts", "nmap <target>"))
	if got := ids(ix.Search("nmap", Options{})); !reflect.DeepEqual(got, []string{"c4"}) {
		t.Fatalf("after add: %v", got)
	}
	ix.Upsert("commands", cmd("c4", "ncat", "Networking", "Connects to hosts", "ncat <host>"))
	if len(ix.Search("nmap", Options{})) != 0 || len(ix.Search("ncat", Options{})) != 1 {
		t.Fatal("an upsert must replace the old terms")
	}
	ix.Remove("commands", "c4")
	if len(ix.Search("ncat", Options{})) != 0 {
		t.Fatal("remove")
	}
	ix.Rebuild("commands", nil)
	if len(ix.Search("chmod type:commands", Options{})) != 0 || len(ix.Search("chmod", Options{})) != 1 {
		t.Fatal("rebuilding a collection must not touch the others")
	}
	if len(ix.dict) == 0 || len(ix.sorted) != len(ix.dict) {
		t.Fatalf("the sorted term list is out of step: %d vs %d", len(ix.sorted), len(ix.dict))
	}
}

func TestHighlightShowsTheMatch(t *testing.T) {
	ix := build(t)
	h := ix.Search("755", Options{})
	if len(h) != 1 || !strings.Contains(h[0].Highlight, "755") {
		t.Fatalf("%+v", h)
	}
	if got := ix.Search("ls", Options{})[0].Highlight; got == "" {
		t.Fatal("empty highlight")
	}
}

func TestLevenshtein(t *testing.T) {
	cases := []struct {
		a, b string
		max  int
		want int
	}{{"kitten", "sitting", 3, 3}, {"abc", "abc", 2, 0}, {"abc", "abd", 2, 1}, {"abcdef", "uvwxyz", 2, 3}, {"a", "abcd", 2, 3}}
	for _, c := range cases {
		if got := levenshtein(c.a, c.b, c.max); got != c.want {
			t.Errorf("%s/%s: %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestPrefixQueriesOver5kCommandsAreWellUnder50ms(t *testing.T) {
	ents := make([]domain.Entity, 5000)
	for i := range ents {
		ents[i] = cmd(fmt.Sprintf("id%d", i), fmt.Sprintf("cmd%d", i), "General", fmt.Sprintf("does thing number %d", i), "x", "-x")
	}
	ix := NewIndex()
	ix.Rebuild("commands", ents)
	ix.Search("cmd4", Options{}) // warm
	t0 := time.Now()
	hits := ix.Search("cmd49", Options{})
	if d := time.Since(t0); len(hits) == 0 || d > 50*time.Millisecond {
		t.Fatalf("%d hits in %s", len(hits), d)
	}
}
