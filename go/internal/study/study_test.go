package study

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/reeper316099/Cipherkeep/go/internal/domain"
)

var t0 = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

func sched(s SrsState, r Rating) SrsState { return Schedule(s, r, t0) }

func TestSM2WalksOneSixThenIntervalTimesEase(t *testing.T) {
	s := sched(NewSrs(), Good)
	if s.IntervalDays != 1 || s.Reps != 1 || s.Ease != 2.5 {
		t.Fatalf("%+v", s)
	}
	if due, _ := ParseTime(s.DueAt); due.Sub(t0) != 24*time.Hour {
		t.Fatalf("due %s", s.DueAt)
	}
	s = sched(s, Good)
	if s.IntervalDays != 6 {
		t.Fatalf("second review: %v", s.IntervalDays)
	}
	s = sched(s, Good)
	if s.IntervalDays != 15 { // 6 x 2.5
		t.Fatalf("third review: %v", s.IntervalDays)
	}
	s = sched(s, Good)
	if s.IntervalDays != 38 {
		t.Fatalf("fourth review: %v", s.IntervalDays)
	}
}

func TestLapsesResetRepetitionsCostEaseAndReturnWithinMinutes(t *testing.T) {
	grown := sched(sched(sched(NewSrs(), Good), Good), Good)
	s := sched(grown, Again)
	if s.Reps != 0 || s.Lapses != 1 || s.IntervalDays != 0 || s.Ease != 2.3 {
		t.Fatalf("%+v", s)
	}
	if due, _ := ParseTime(s.DueAt); due.Sub(t0) != 10*time.Minute {
		t.Fatalf("due %s", s.DueAt)
	}
	if !IsDue(&s, t0.Add(11*time.Minute)) || IsDue(&s, t0) {
		t.Fatal("due-ness")
	}
}

func TestHardGrowsSlowlyEasyFasterAndEaseStaysInRange(t *testing.T) {
	base := sched(sched(NewSrs(), Good), Good) // interval 6
	if got := sched(base, Hard).IntervalDays; got != 7 {
		t.Fatalf("hard: %v", got)
	}
	if sched(base, Easy).IntervalDays <= sched(base, Good).IntervalDays {
		t.Fatal("easy must grow faster than good")
	}
	s := NewSrs()
	for i := 0; i < 30; i++ {
		s = sched(s, Again)
	}
	if s.Ease != 1.3 {
		t.Fatalf("floor: %v", s.Ease)
	}
	s = NewSrs()
	for i := 0; i < 30; i++ {
		s = sched(s, Easy)
	}
	if s.Ease != 3 {
		t.Fatalf("ceiling: %v", s.Ease)
	}
	if s.IntervalDays > MaxIntervalDays {
		t.Fatalf("interval %v exceeds the cap", s.IntervalDays)
	}
}

func TestFirstEasyJumpsToThreeDaysSecondReviewsDifferByRating(t *testing.T) {
	if sched(NewSrs(), Easy).IntervalDays != 3 {
		t.Fatal("first easy")
	}
	one := sched(NewSrs(), Good)
	got := []float64{sched(one, Hard).IntervalDays, sched(one, Good).IntervalDays, sched(one, Easy).IntervalDays}
	if !reflect.DeepEqual(got, []float64{4, 6, 8}) {
		t.Fatalf("%v", got)
	}
}

func TestMasteryFromIntervals(t *testing.T) {
	cases := []struct {
		s    SrsState
		want int
	}{{NewSrs(), 0}, {SrsState{LastAt: "x", IntervalDays: 1}, 1}, {SrsState{LastAt: "x", IntervalDays: 6}, 2}, {SrsState{LastAt: "x", IntervalDays: 21}, 3}}
	for _, c := range cases {
		if got := MasteryFromSrs(c.s); got != c.want {
			t.Errorf("%+v: %d, want %d", c.s, got, c.want)
		}
	}
}

func TestTypedAnswerChecking(t *testing.T) {
	yes := [][2]string{{"ls   -a  -l ~", "ls -la ~"}, {"$ ls -al ~", "ls -la ~"}, {"`ls -al ~`", "ls -la ~"}, {"ls ~ -la", "ls -la ~"},
		{"ls --color=auto", "ls --color=auto"}, {"grep -i secret notes.txt", "grep -i <pattern> [file]"}}
	no := [][2]string{{"ll -la ~", "ls -la ~"}, {"ls -l ~", "ls -la ~"}, {"ls -lah ~", "ls -la ~"}, {"cp b a", "cp a b"}, {"ls", "ls ~"},
		{"", "ls"}, {"ls --color", "ls --color=auto"}, {"grep -i secret", "grep -i <pattern> [file]"}}
	for _, c := range yes {
		if !CommandsMatch(c[0], c[1]) {
			t.Errorf("%q should match %q", c[0], c[1])
		}
	}
	for _, c := range no {
		if CommandsMatch(c[0], c[1]) {
			t.Errorf("%q should not match %q", c[0], c[1])
		}
	}
	if got := TokenizeCommand(`grep "two words" 'x y'`); !reflect.DeepEqual(got, []string{"grep", "two words", "x y"}) {
		t.Fatalf("%q", got)
	}
	if ok, m := CheckTask("rm -r  d", []string{"rm -rf d", "rm -r d"}); !ok || m != "rm -r d" {
		t.Fatalf("%v %q", ok, m)
	}
	if ok, _ := CheckTask("rm d", []string{"rm -r d"}); ok {
		t.Fatal("rm d vs rm -r d")
	}
	if !CheckFill(" -a ", []string{"-a"}) || CheckFill("-A", []string{"-a"}) {
		t.Fatal("fill checking")
	}
}

func cmd(mod func(*CmdLike)) CmdLike {
	c := CmdLike{ID: "c1", Name: "ls", Title: "ls", Explanation: "list entries", WhatItDoes: "Prints entries.", Syntax: "ls [flags]",
		Options:  []Option{{"-a", "include dotfiles"}, {"cd ..", "not a flag"}},
		Examples: []Example{{"ls -al ~", "long list of home"}, {"ls", ""}}}
	if mod != nil {
		mod(&c)
	}
	return c
}

func TestBuildsEachCommandCardTypeFromItsData(t *testing.T) {
	name, _ := BuildCommandCard(cmd(nil), CardName, 0)
	if name.Kind != "reveal" || name.Prompt != "What does `ls` do?" || name.Answer != "list entries" {
		t.Fatalf("%+v", name)
	}
	task, _ := BuildCommandCard(cmd(nil), CardTask, 0)
	if task.Kind != "type" || task.Prompt != "Type the command: long list of home" || !contains(task.Expected, "ls -al ~") {
		t.Fatalf("%+v", task)
	}
	flag, _ := BuildCommandCard(cmd(nil), CardFlag, 0)
	if flag.Prompt != "In `ls`, what does `-a` do?" || flag.Answer != "include dotfiles" {
		t.Fatalf("%+v", flag)
	}
	fill, _ := BuildCommandCard(cmd(nil), CardFill, 0)
	if fill.Kind != "type" || fill.Prompt != "Fill in the blank: ls ____ ~" || !reflect.DeepEqual(fill.Expected, []string{"-al"}) {
		t.Fatalf("%+v", fill)
	}
}

func TestSkipsCardTypesTheDataCannotSupport(t *testing.T) {
	bare := cmd(func(c *CmdLike) {
		c.Explanation, c.WhatItDoes, c.Options, c.Examples = "", "", nil, []Example{{"ls", ""}}
	})
	if len(AvailableCommandCards(bare)) != 0 {
		t.Fatal("bare command should support no cards")
	}
	if _, ok := PickCommandCardType(bare, 0); ok {
		t.Fatal("picked a card for a bare command")
	}
	if _, ok := BuildCommandCard(bare, CardFlag, 0); ok {
		t.Fatal("built a flag card without flags")
	}
	if got := AvailableCommandCards(cmd(nil)); !reflect.DeepEqual(got, []CardType{CardName, CardTask, CardFlag, CardFill}) {
		t.Fatalf("%v", got)
	}
}

func TestPicksEasierCardsForNewCommandsAndHarderOnesAsMasteryGrows(t *testing.T) {
	m := func(n int) CmdLike { return cmd(func(c *CmdLike) { c.Mastery = n }) }
	if ty, _ := PickCommandCardType(m(0), 5); ty != CardName && ty != CardTask {
		t.Fatalf("mastery 0: %s", ty)
	}
	if ty, _ := PickCommandCardType(m(3), 7); ty != CardFill && ty != CardTask && ty != CardFlag {
		t.Fatalf("mastery 3: %s", ty)
	}
}

func TestFillCardsAlwaysBlankExactlyOneTokenWhateverTheIDHashesTo(t *testing.T) {
	many := func(id string) CmdLike {
		return cmd(func(c *CmdLike) { c.ID, c.Examples = id, []Example{{"ls -a -l -h ~", "long list"}} })
	}
	flag := regexp.MustCompile(`^-[alh]$`)
	for i := 0; i < 300; i++ {
		fill, ok := BuildCommandCard(many(fmt.Sprintf("01HZ%dABCDEFG", i)), CardFill, uint64(i%5))
		if !ok || strings.Count(fill.Prompt, "____") != 1 || len(fill.Expected) != 1 || !flag.MatchString(fill.Expected[0]) {
			t.Fatalf("id %d: %+v", i, fill)
		}
	}
}

func TestBuildsConceptCardsFromIntuitionOrWhatIsItIgnoringBlankTemplateBullets(t *testing.T) {
	c := func(secs ...SectionLike) ConceptLike { return ConceptLike{ID: "k", Title: "Wildcards", Sections: secs} }
	got, _ := BuildConceptCard(c(SectionLike{"intuition-very-important", `> "Like a shortcut"`}, SectionLike{"what-is-it", "-"}), 0)
	if got.Type != CardTeach || got.Answer != `"Like a shortcut"` {
		t.Fatalf("%+v", got)
	}
	got, _ = BuildConceptCard(c(SectionLike{"what-is-it", "Patterns for file names"}), 0)
	if got.Type != CardWhat || got.Answer != "Patterns for file names" {
		t.Fatalf("%+v", got)
	}
	if _, ok := BuildConceptCard(c(SectionLike{"what-is-it", "- "}), 0); ok {
		t.Fatal("a blank bullet must not make a card")
	}
}

func days(d ...string) map[string]bool {
	m := map[string]bool{}
	for _, x := range d {
		m[x] = true
	}
	return m
}

func TestStreaksDayKeysAndWeeklyMinutes(t *testing.T) {
	if s := ComputeStreak(days("2026-06-01", "2026-05-31", "2026-05-30"), "2026-06-01"); s != (Streak{3, 3}) {
		t.Fatalf("%+v", s)
	}
	if ComputeStreak(days("2026-05-31", "2026-05-30"), "2026-06-01").Current != 2 {
		t.Fatal("yesterday counts")
	}
	if ComputeStreak(days("2026-05-29"), "2026-06-01").Current != 0 {
		t.Fatal("broken streak")
	}
	if ComputeStreak(days("2026-05-01", "2026-05-02", "2026-05-03", "2026-05-10"), "2026-06-01").Longest != 3 {
		t.Fatal("longest")
	}
	if ComputeStreak(map[string]bool{}, "2026-06-01") != (Streak{}) {
		t.Fatal("empty")
	}
	ts, _ := ParseTime("2026-06-01T02:30:00Z")
	if DayKey(ts, 0) != "2026-06-01" || DayKey(ts, 300) != "2026-05-31" || AddDays("2026-02-28", 1) != "2026-03-01" {
		t.Fatal("day keys")
	}
	if WeekStart("2026-06-03") != "2026-06-01" || WeekStart("2026-06-07") != "2026-06-01" {
		t.Fatal("week start is Monday")
	}
	w := WeeklyMinutes([]MinutesEntry{{"2026-06-02", 30}, {"2026-06-03", 45}, {"2026-05-20", 10}, {"2025-01-01", 999}}, "2026-06-05", 4)
	weeks, mins := []string{}, []float64{}
	for _, x := range w {
		weeks, mins = append(weeks, x.Week), append(mins, x.Minutes)
	}
	if !reflect.DeepEqual(weeks, []string{"2026-05-11", "2026-05-18", "2026-05-25", "2026-06-01"}) || !reflect.DeepEqual(mins, []float64{0, 10, 0, 75}) {
		t.Fatalf("%v %v", weeks, mins)
	}
}

func ip(n int) *int { return &n }

func TestThreatLevelEscalatesRewardsStreaksAndExplainsItself(t *testing.T) {
	lvl := func(in ThreatInput) ThreatLevel { return ThreatLevelOf(in).Level }
	if lvl(ThreatInput{DaysSinceActivity: ip(0), Streak: 5}) != "LOW" ||
		lvl(ThreatInput{DaysSinceActivity: ip(1), Overdue: 4}) != "LOW" ||
		lvl(ThreatInput{DaysSinceActivity: ip(2)}) != "MODERATE" ||
		lvl(ThreatInput{DaysSinceActivity: ip(5)}) != "ELEVATED" ||
		lvl(ThreatInput{DaysSinceActivity: ip(9)}) != "HIGH" ||
		lvl(ThreatInput{}) != "MODERATE" {
		t.Fatal("levels")
	}
	if e := ThreatLevelOf(ThreatInput{Empty: true}); e.Level != "LOW" || e.Score != 0 {
		t.Fatalf("%+v", e)
	}
	if ThreatLevelOf(ThreatInput{DaysSinceActivity: ip(2), Streak: 14}).Score != 0 {
		t.Fatal("streak cap")
	}
	th := ThreatLevelOf(ThreatInput{DaysSinceActivity: ip(3), Overdue: 30})
	if th.Score != 70 || !strings.Contains(th.Explanation, "ELEVATED") || !strings.Contains(th.Explanation, "Score = 10×days idle") {
		t.Fatalf("%+v", th)
	}
}

var now0 = time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)

func com(id, cat string) Common {
	return Common{ID: id, Category: cat, UpdatedAt: "2026-06-09T00:00:00Z"}
}

func byName(ds []DomainScore) map[string]DomainScore {
	m := map[string]DomainScore{}
	for _, d := range ds {
		m[d.Name] = d
	}
	return m
}

func TestDomainsForMapsLabelsViaAliases(t *testing.T) {
	d := domain.DefaultSkillDomains
	for in, want := range map[string][]string{"File System": {"Linux"}, "Text Processing": {"Bash"}, "crypto": {"Cryptography"}} {
		if got := DomainsFor([]string{in}, d); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %v", in, got)
		}
	}
	if got := DomainsFor([]string{"nonsense", ""}, d); len(got) != 0 {
		t.Fatalf("%v", got)
	}
}

func TestSkillScoresRewardConceptsMasteryAndCompletedLabs(t *testing.T) {
	data := SkillData{
		Concepts: []SkillConcept{{com("k1", "Linux"), "Hard", 3}, {com("k2", "Linux"), "Easy", 0}},
		Commands: []SkillCommand{{com("c1", "File System"), 3, "2026-06-09T00:00:00Z"}, {com("c2", "Networking"), 1, ""}},
		Labs: []SkillLab{
			{Common: com("l1", "Bandit"), Status: "Completed", Difficulty: "Medium", Domain: "linux", SolvedAt: "2026-06-09T00:00:00Z"},
			{Common: com("l2", "x"), Status: "In Progress", Difficulty: "Hard", ConceptIDs: []string{"k1"}},
		},
	}
	s := byName(ScoreDomains(domain.DefaultSkillDomains, data, now0))
	if s["Cryptography"].Score != 0 {
		t.Fatal("empty domain must score 0")
	}
	l := s["Linux"]
	if l.Counts != (Counts{2, 1, 2}) {
		t.Fatalf("counts %+v", l.Counts) // l2 joins via its concept
	}
	if l.Parts.Labs != 7.5 || l.Parts.Commands != 2.5 {
		t.Fatalf("parts %+v", l.Parts)
	}
	if want := 35 * ((2*1 + 1*0.3) / 6); l.Parts.Concepts < want-0.5 || l.Parts.Concepts > want+0.5 {
		t.Fatalf("concepts part %v, want about %v", l.Parts.Concepts, want)
	}
	if l.Score <= s["Networking"].Score || l.Score > 100 {
		t.Fatalf("scores %d vs %d", l.Score, s["Networking"].Score)
	}
}

func TestRecencyDecaysOverTime(t *testing.T) {
	data := SkillData{Concepts: []SkillConcept{{com("k", "Linux"), "Easy", 3}}}
	fresh := byName(ScoreDomains(domain.DefaultSkillDomains, data, now0))["Linux"]
	old := byName(ScoreDomains(domain.DefaultSkillDomains, data, now0.Add(120*24*time.Hour)))["Linux"]
	if fresh.Parts.Recency <= 9 || old.Parts.Recency >= 0.3 {
		t.Fatalf("%v %v", fresh.Parts.Recency, old.Parts.Recency)
	}
}

func TestRecommendationsAreOrderedAndAlwaysExplain(t *testing.T) {
	now := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	domains := ScoreDomains(domain.DefaultSkillDomains, SkillData{
		Concepts: []SkillConcept{{Common{ID: "k1", Category: "Networking", UpdatedAt: "2026-06-19T00:00:00Z"}, "Easy", 1}},
	}, now)
	in := RecommendInput{
		Domains: domains, Due: 3, Now: now,
		Concepts: []RecConcept{{"k1", "TCP", false, 2}, {"k2", "DNS", false, 0}},
		Commands: []RecCommand{
			{"c1", "nmap", false, 1, "2026-05-01T00:00:00Z", "2026-04-01T00:00:00Z"},
			{"c2", "ls", false, 0, "", "2026-06-19T00:00:00Z"},
			{"c3", "cd", false, 3, "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z"},
		},
		Labs: []RecLab{{"l1", "Bandit 3", false, "In Progress", []string{"k1"}, "2026-06-18T00:00:00Z"}},
	}
	r := BuildRecommendations(in)
	var kinds []string
	for _, x := range r {
		kinds = append(kinds, x.Kind)
		if len(x.Why) <= 10 {
			t.Errorf("%s has no explanation", x.ID)
		}
	}
	want := []string{"review", "weak-domain", "lab-in-progress", "concept-no-lab", "stale-command", "stale-command", "open-question"}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("kinds %v", kinds)
	}
	if r[0].Title != "Review 3 due cards" || !strings.Contains(r[3].Title, "DNS") || r[1].Title != "Strengthen Networking" {
		t.Fatalf("%+v", r)
	}
	if r[4].Title != "Revisit `ls`" || r[5].Title != "Revisit `nmap`" {
		t.Fatalf("stale order: %q, %q", r[4].Title, r[5].Title)
	}
	if got := BuildRecommendations(RecommendInput{Now: now}); len(got) != 0 {
		t.Fatalf("expected quiet, got %+v", got)
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

func TestCommandScannersHandleQuotesPlaceholdersAndMarkers(t *testing.T) {
	toks := map[string][]string{
		`grep "two words" 'x y'`: {"grep", "two words", "x y"},
		`echo "unclosed word`:    {"echo", `"unclosed`, "word"},
		`echo "" x`:              {"echo", "", "x"},
		"  $   ls   -la  ":       {"ls", "-la"},
		"`ls -la`":               {"ls", "-la"},
		`a"b c"`:                 {`a"b`, `c"`},
		`"a"b`:                   {"a", "b"},
		"# comment":              {"comment"},
		"$ls":                    {"$ls"},
		"":                       nil,
	}
	for in, want := range toks {
		if got := TokenizeCommand(in); !reflect.DeepEqual(got, want) && !(len(got) == 0 && len(want) == 0) {
			t.Errorf("TokenizeCommand(%q) = %q, want %q", in, got, want)
		}
	}
	for tok, want := range map[string]bool{"<file>": true, "{x}": true, "[dir]": true, "[Dir name-2]": true, "<>": false, "[1a]": false,
		"[a]b": false, "<a>b>": false, "file": false, "[a b]": true} {
		if got := isPlaceholder(tok); got != want {
			t.Errorf("isPlaceholder(%q) = %v", tok, got)
		}
	}
	for tok, want := range map[string]bool{"-a": true, "--color": true, "-": false, "--": false, "-1": false, "--1": false, "x": false} {
		if got := isRealFlag(tok); got != want {
			t.Errorf("isRealFlag(%q) = %v", tok, got)
		}
	}
	for tok, want := range map[string]bool{"-la": true, "-l1": true, "-": false, "--a": false, "-a-b": false} {
		if got := isShortFlags(tok); got != want {
			t.Errorf("isShortFlags(%q) = %v", tok, got)
		}
	}
}
