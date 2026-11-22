package notion

import (
	"reflect"
	"strings"
	"testing"
)

func eq(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v\nwant %#v", got, want)
	}
}

func TestSplitName(t *testing.T) {
	eq(t, SplitName("ls 32df3e3018aa80d89e90dddec81b898b.md"), Name{Title: "ls", ID: "32df3e3018aa80d89e90dddec81b898b", Ext: ".md"})
	n := SplitName("\U0001F4DA Cyber Concepts 32cf3e3018aa8051999ed048cba85e91_all.csv")
	eq(t, n, Name{Title: "\U0001F4DA Cyber Concepts", ID: "32cf3e3018aa8051999ed048cba85e91", Ext: ".csv", All: true})
	eq(t, SplitName("No id here.md"), Name{Title: "No id here", Ext: ".md"})
	eq(t, SplitName("dir/sub/ls 32DF3E3018AA80D89E90DDDEC81B898B.md").ID, "32df3e3018aa80d89e90dddec81b898b")
}

func TestHeadingKey(t *testing.T) {
	eq(t, HeadingKey("\U0001F9E0 Intuition (VERY IMPORTANT)"), "intuition-very-important")
	eq(t, HeadingKey("\U0001F527 Common Options (Flags)"), "common-options-flags")
}

func TestDecodeNotionPath(t *testing.T) {
	eq(t, DecodeNotionPath("Dash/%F0%9F%92%BB%20Commands/pwd.md"), "Dash/\U0001F4BB Commands/pwd.md")
	eq(t, DecodeNotionPath(`a\b%zz`), "a/b%zz") // malformed escape keeps the raw text
	eq(t, DecodeNotionPath("100%"), "100%")
}

func TestTimeSpent(t *testing.T) {
	for raw, want := range map[string]int{
		"~1.5 hours": 90, "1 hr": 60, "45 mins": 45, "2h 30m": 150, "1:30": 90, "half an hour": 30,
		"an hour and 15 minutes": 75, "about 2 hrs": 120, "≈1 hour": 60,
	} {
		got, ok := ParseTimeSpent(raw)
		if !ok || got != want {
			t.Errorf("%q: got %d,%v want %d", raw, got, ok, want)
		}
	}
	for _, raw := range []string{"a while", "", "  "} {
		if _, ok := ParseTimeSpent(raw); ok {
			t.Errorf("%q should not parse", raw)
		}
	}
}

func TestDates(t *testing.T) {
	for raw, want := range map[string]string{
		"March 24, 2026": "2026-03-24", "2026-03-24": "2026-03-24", "24 Mar 2026": "2026-03-24", "03/24/2026": "2026-03-24",
		"@March 24, 2026 → March 25, 2026": "2026-03-24", "Sept. 3rd, 2026": "2026-09-03",
	} {
		got, ok := ParseNotionDate(raw)
		if !ok || got != want {
			t.Errorf("%q: got %q,%v want %q", raw, got, ok, want)
		}
	}
	for _, raw := range []string{"February 31, 2026", "0050-01-01", "Foo 3, 2026", "", "soon"} {
		if got, ok := ParseNotionDate(raw); ok {
			t.Errorf("%q should be rejected, got %q", raw, got)
		}
	}
}

func TestRelations(t *testing.T) {
	r := ParseRelationCell("pwd (Dash/%F0%9F%92%BB%20Commands/pwd%2032df3e3018aa804783b1f8a9de1e33c1.md), a, b (x/b%2032df3e3018aa803c957dd5c1468647de.md)")
	if len(r) != 2 {
		t.Fatalf("got %d refs: %#v", len(r), r)
	}
	eq(t, r[0].Title, "pwd")
	eq(t, r[0].ID, "32df3e3018aa804783b1f8a9de1e33c1")
	eq(t, r[1].Title, "a, b")
	eq(t, r[1].ID, "32df3e3018aa803c957dd5c1468647de")
	var titles []string
	for _, x := range ParseRelationCell("ls, cd") {
		titles = append(titles, x.Title)
	}
	eq(t, titles, []string{"ls", "cd"})
	if got := ParseRelationCell(""); len(got) != 0 {
		t.Fatal("empty cell should have no refs")
	}
	u := ParseRelationCell("Docs (https://example.com/x)")
	eq(t, u, []RelationRef{{Title: "Docs", Path: "https://example.com/x"}})
	// a bare link falls back to the file name for the title
	eq(t, ParseRelationCell("(dir/Name%2032df3e3018aa804783b1f8a9de1e33c1.md)")[0].Title, "Name")
	eq(t, SplitList("a, b; c ,,"), []string{"a", "b", "c"})
}

func TestParsePage(t *testing.T) {
	text := "# ls\n\nCategory: File System\nSyntax: ls \\[flags\\]\n\n## \U0001F4CC What it does\n\nlists\n"
	p := ParsePage(text, true)
	eq(t, p.Title, "ls")
	eq(t, p.Props, map[string]string{"Category": "File System", "Syntax": "ls \\[flags\\]"})
	eq(t, p.Keys, []string{"Category", "Syntax"})
	if !strings.HasPrefix(p.Body, "## \U0001F4CC What it does") {
		t.Fatalf("body: %q", p.Body)
	}
	loose := ParsePage("# Page\n\nNote: not a property\n\ntext", false)
	eq(t, loose.Props, map[string]string{})
	if !strings.Contains(loose.Body, "Note: not a property") {
		t.Fatal("loose body lost its first line")
	}
	// a property block that does not end cleanly is not a property block
	bad := ParsePage("# T\n\nA: 1\nnot a prop\n\nbody", true)
	eq(t, len(bad.Props), 0)
	// a very long key is not a property
	eq(t, len(ParsePage("# T\n\n"+strings.Repeat("k", 70)+": v\n\nbody", true).Props), 0)
}

func TestNormalizeBody(t *testing.T) {
	eq(t, NormalizeBody("use \\[flags\\] here\n\n```\necho \\[x\\]\n```"), "use [flags] here\n\n```\necho \\[x\\]\n```")
	eq(t, NormalizeBody("a `\\[x\\]` and \\[y\\]"), "a `\\[x\\]` and [y]")
	eq(t, UnescapeAll("ls \\[flags\\] \\*"), "ls [flags] *")
	eq(t, NormalizeBody("\xef\xbb\xbfa  \r\n\r\n\r\n\r\nb\t\n"), "a\n\nb")
}

func TestHTMLBlocks(t *testing.T) {
	md := NormalizeBody("<aside>\n\U0001F4A1 Remember this\n</aside>\n\n<details><summary>More</summary>\nhidden text\n</details>")
	for _, want := range []string{"> [!TIP]", "> Remember this", "> **▸ More**", "> hidden text"} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q in %q", want, md)
		}
	}
	eq(t, ConvertHTMLBlocks("<aside>\n⚠️ careful\n\nmore\n</aside>"), "> [!WARNING]\n> careful\n>\n> more")
	eq(t, ConvertHTMLBlocks("<aside>plain</aside>"), "> [!NOTE]\n> plain")
}

func TestRepairTables(t *testing.T) {
	eq(t, RepairTables("| a | b\nc |\n| d | e |"), "| a | b c |\n| d | e |")
	eq(t, RepairTables("```\n| not\ntable\n```"), "```\n| not\ntable\n```")
}

func TestSectionsAndTables(t *testing.T) {
	pre, sections := SplitSections("intro\n\n## A\n\none\n\n---\n\n## \U0001F9E0 B\n\n```\n## not heading\n```")
	eq(t, pre, "intro")
	var keys []string
	for _, s := range sections {
		keys = append(keys, s.Key)
	}
	eq(t, keys, []string{"a", "b"})
	eq(t, sections[0].Markdown, "one")
	eq(t, ParseTables("| a | b |\n| --- | --- |\n| `x\\|y` | z |")[0].Rows, [][]string{{"a", "b"}, {"`x|y`", "z"}})
	eq(t, len(ParseTables("```\n| a |\n```")), 0)
}

func TestCellTextAndFences(t *testing.T) {
	eq(t, CellText("`ls` **bold** *it* _u_ a<br/>b \\[x\\]"), "ls bold it u a b [x]")
	eq(t, CellText("2*3*4 snake_case_name"), "2*3*4 snake_case_name")
	b := FencedBlocks("x\n```bash\nls -l\n```\ntext\n~~~\nraw\n~~~")
	eq(t, b, []Block{{"bash", "ls -l"}, {"", "raw"}})
	eq(t, len(FencedBlocks("```\nunclosed")), 0)
}

func TestCommandHelpers(t *testing.T) {
	ex, ok := SplitExampleLine(`ls -al ~ = "lists all"`)
	eq(t, ex, Example{"ls -al ~", "lists all"})
	eq(t, ok, true)
	ex, _ = SplitExampleLine(`echo a = "b" = "c"`)
	eq(t, ex, Example{"echo a", `b" = "c`})
	ex, _ = SplitExampleLine("plain command")
	eq(t, ex, Example{"plain command", ""})
	if _, ok := SplitExampleLine(""); ok {
		t.Fatal("blank")
	}
	if _, ok := SplitExampleLine("# comment"); ok {
		t.Fatal("comment")
	}
	eq(t, DetectPlaceholders("ls [flags] <target> {ip} [[:upper:]]* [!abc]"), []string{"flags", "target", "ip"})
	eq(t, FillPlaceholders("ls [flags] <target> {ip}", map[string]string{"target": "x", "ip": ""}), "ls [flags] x {ip}")
	eq(t, ClassifyRisk("rm", nil), "destructive")
	eq(t, ClassifyRisk("mv", nil), "caution")
	eq(t, ClassifyRisk("ls", nil), "safe")
	eq(t, ClassifyRisk("find", []string{"find . -exec rm -rf {} +"}), "destructive")
	eq(t, ClassifyRisk("  RM  -rf", nil), "destructive")
	eq(t, ClassifyRisk("x", []string{"dd if=a of=/dev/sda"}), "destructive")
}

func TestCommandPage(t *testing.T) {
	body := strings.Join([]string{
		"## \U0001F4CC What it does", "", "Shows things", "", "---", "",
		"## \U0001F9E9 Arguments", "", "(Inputs the command takes.)", "", "- target → thing to show", "", "---", "",
		"## \U0001F527 Common Options (Flags)", "", "| Option | Meaning |", "| --- | --- |", "| -a | all |", "| -l | long |", "", "---", "",
		"## \U0001F4A1 Examples", "", "```bash", `show -a x = "all of x"`, "```", "",
		"| `show -l y` | Long y |", "| --- | --- |", "| `show -a z` | All z |", "",
		"## \U0001F9EA Extra", "", "kept",
	}, "\n")
	cat := "General"
	r := ParseCommandPage(CommandSource{Name: "show", Category: &cat, Body: body,
		Props: map[string]string{"Syntax": "show \\[flags\\] \\[target\\]", "Example": "show -a", "Explanation": "shows"}})
	in := r.Input
	eq(t, in["syntax"], "show [flags] [target]")
	eq(t, in["arguments"], []any{map[string]any{"name": "target", "meaning": "thing to show"}})
	eq(t, in["options"], []any{map[string]any{"flag": "-a", "meaning": "all"}, map[string]any{"flag": "-l", "meaning": "long"}})
	var cmds []string
	for _, e := range in["examples"].([]any) {
		cmds = append(cmds, e.(map[string]any)["command"].(string))
	}
	eq(t, cmds, []string{"show -a", "show -a x", "show -l y", "show -a z"})
	eq(t, in["placeholders"], []any{"flags", "target"})
	if !strings.Contains(in["notes"].(string), "## \U0001F9EA Extra") {
		t.Fatal("extra section not kept in notes")
	}
	eq(t, in["extras"].(map[string]any)["droppedTemplateHints"], []any{"(Inputs the command takes.)"})
	if !strings.Contains(strings.Join(r.Warnings, ","), "unrecognised") {
		t.Fatal("expected an unrecognised-section warning")
	}
}

func TestConceptAndLog(t *testing.T) {
	body := "## \U0001F9E0 What is it?\n\ntext\n\n---\n\n## \U0001F6E0️ Tools / Commands Related\n\n- cp\n- find\n\n---\n\n## ❓ Questions I still have\n\n- one?\n- \n- two?\n\n## \U0001F381 Custom\n\nmine"
	r := ParseConceptPage(ConceptSource{Name: "X", Difficulty: "Beginner", Body: body})
	eq(t, len(r.Input["sections"].([]any)), 4)
	eq(t, r.Input["questionsOpen"], 2)
	eq(t, r.Input["difficulty"], "Easy")
	eq(t, r.CommandMentions, []string{"cp", "find"})
	eq(t, MapDifficulty("Advanced"), "Hard")
	eq(t, MapDifficulty("Wizard"), "Wizard")
	eq(t, MapDifficulty(""), "Easy")
	eq(t, BulletItems("- Step 1\n- real\n1. numbered\n-\n"), []string{"real", "numbered"})

	l := ParseLogEntry(LogSource{Name: "March 24, 2026", Date: S("March 24, 2026"), TimeSpent: S("~1.5 hours"), Topics: S("A, B"),
		Body: "## \U0001F4DA What I learned\n\n- less\n- df"})
	eq(t, l.Input["date"], "2026-03-24")
	eq(t, l.Input["timeSpentMin"], 90)
	eq(t, l.Input["timeSpentRaw"], "~1.5 hours")
	eq(t, l.Input["topics"], []any{"A", "B"})
	eq(t, l.Learned, []string{"less", "df"})

	bad := ParseLogEntry(LogSource{Name: "Notes", TimeSpent: S("a while")})
	eq(t, bad.Input["date"], "1970-01-01")
	eq(t, bad.Input["timeSpentMin"], nil)
	eq(t, bad.Warnings, []string{`could not parse date "Notes"`, `could not parse time spent "a while" (raw kept)`})
}
