package study

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// Recommendation is one suggested next step; Why is always shown to the learner.
type Recommendation struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	Why      string `json:"why"`
	Route    string `json:"route"`
	Priority int    `json:"priority"`
}

// RecConcept, RecCommand and RecLab are the fields recommendations read.
type RecConcept struct {
	ID, Title     string
	Archived      bool
	QuestionsOpen int
}
type RecCommand struct {
	ID, Name        string
	Archived        bool
	Mastery         float64
	LastPracticedAt string
	CreatedAt       string
}
type RecLab struct {
	ID, Title  string
	Archived   bool
	Status     string
	ConceptIDs []string
	UpdatedAt  string
}

// RecommendInput is everything recommendations are built from.
type RecommendInput struct {
	Domains   []DomainScore
	Concepts  []RecConcept
	Commands  []RecCommand
	Labs      []RecLab
	Due       int
	Now       time.Time
	StaleDays int // default 14
}

var partMax = [4]float64{35, 25, 30, 10}
var partKeys = [4]string{"concepts", "commands", "labs", "recency"}
var partRoute = map[string]string{"concepts": "/concepts", "commands": "/commands", "labs": "/labs", "recency": "/study"}
var partHint = map[string]string{
	"concepts": "study a concept", "commands": "practise its commands", "labs": "complete a lab",
	"recency": "do something in it this week",
}

func ago(d int) string {
	switch d {
	case 0:
		return "today"
	case 1:
		return "yesterday"
	}
	return fmt.Sprintf("%d days ago", d)
}

func daysAgo(iso string, now time.Time) int {
	t, ok := ParseTime(iso)
	if !ok {
		return 0
	}
	return max(0, int(math.Floor(float64(now.Sub(t))/float64(24*time.Hour))))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// BuildRecommendations turns the learner's state into at most ten suggestions, highest priority first.
func BuildRecommendations(in RecommendInput) []Recommendation {
	stale := in.StaleDays
	if stale == 0 {
		stale = 14
	}
	var out []Recommendation
	if in.Due > 0 {
		out = append(out, Recommendation{
			ID: "review", Kind: "review", Priority: 100, Route: "/study",
			Title: fmt.Sprintf("Review %d due card%s", in.Due, plural(in.Due, "", "s")),
			Why:   "Spaced repetition brings cards back just before you would forget them; clearing them keeps your streak and threat level low.",
		})
	}

	var withData []DomainScore
	for _, d := range in.Domains {
		if d.Counts.Concepts+d.Counts.Commands+d.Counts.Labs > 0 && d.Score < 60 {
			withData = append(withData, d)
		}
	}
	sort.SliceStable(withData, func(i, j int) bool {
		return withData[i].score() < withData[j].score()
	})
	if len(withData) > 0 {
		w := withData[0]
		vals := [4]float64{w.Parts.Concepts, w.Parts.Commands, w.Parts.Labs, w.Parts.Recency}
		type gap struct {
			k string
			g float64
		}
		gaps := make([]gap, 4)
		for i, k := range partKeys {
			gaps[i] = gap{k, 1 - vals[i]/partMax[i]}
		}
		sort.SliceStable(gaps, func(i, j int) bool { return gaps[i].g > gaps[j].g })
		worst := gaps[0]
		out = append(out, Recommendation{
			ID: "domain:" + w.Name, Kind: "weak-domain", Priority: 80, Route: partRoute[worst.k],
			Title: "Strengthen " + w.Name,
			Why: fmt.Sprintf("%s is your weakest weighted domain among those you have started (%d/100). Biggest gap: %s. Try to %s.",
				w.Name, w.Score, worst.k, partHint[worst.k]),
		})
	}

	n := 0
	for _, l := range in.Labs {
		if l.Archived || l.Status != "In Progress" || n >= 3 {
			continue
		}
		n++
		out = append(out, Recommendation{
			ID: "lab:" + l.ID, Kind: "lab-in-progress", Priority: 70, Route: "/labs/" + l.ID,
			Title: "Finish “" + l.Title + "”",
			Why:   fmt.Sprintf("In progress, last touched %s.", ago(daysAgo(l.UpdatedAt, in.Now))),
		})
	}

	used := map[string]bool{}
	for _, l := range in.Labs {
		if !l.Archived {
			for _, id := range l.ConceptIDs {
				used[id] = true
			}
		}
	}
	n = 0
	for _, c := range in.Concepts {
		if c.Archived || used[c.ID] || n >= 3 {
			continue
		}
		n++
		out = append(out, Recommendation{
			ID: "concept:" + c.ID, Kind: "concept-no-lab", Priority: 60, Route: "/concepts/" + c.ID,
			Title: "Practise “" + c.Title + "” in a lab",
			Why:   "You have notes on this concept but no lab uses it yet. Applying it makes it stick.",
		})
	}

	type staleCmd struct {
		c     RecCommand
		days  int
		never bool
	}
	var stales []staleCmd
	for _, c := range in.Commands {
		if c.Archived || c.Mastery >= 3 {
			continue
		}
		ref := c.LastPracticedAt
		if ref == "" {
			ref = c.CreatedAt
		}
		s := staleCmd{c: c, days: daysAgo(ref, in.Now), never: c.LastPracticedAt == ""}
		if s.days >= stale || s.never {
			stales = append(stales, s)
		}
	}
	sort.SliceStable(stales, func(i, j int) bool {
		if stales[i].never != stales[j].never {
			return stales[i].never
		}
		return stales[i].days > stales[j].days
	})
	if len(stales) > 3 {
		stales = stales[:3]
	}
	for _, s := range stales {
		why := fmt.Sprintf("Not practised for %d days (mastery %d/3).", s.days, int(s.c.Mastery))
		if s.never {
			why = fmt.Sprintf("Never practised (mastery %d/3).", int(s.c.Mastery))
		}
		out = append(out, Recommendation{
			ID: "cmd:" + s.c.ID, Kind: "stale-command", Priority: 50, Route: "/commands/" + s.c.ID,
			Title: "Revisit `" + s.c.Name + "`", Why: why,
		})
	}

	open := 0
	for _, c := range in.Concepts {
		if !c.Archived {
			open += c.QuestionsOpen
		}
	}
	if open > 0 {
		out = append(out, Recommendation{
			ID: "questions", Kind: "open-question", Priority: 40, Route: "/questions",
			Title: fmt.Sprintf("Answer %d open question%s", open, plural(open, "", "s")),
			Why:   "Questions you wrote down while studying are the cheapest next step: each one points at a gap.",
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Priority > out[j].Priority })
	if len(out) > 10 {
		out = out[:10]
	}
	return out
}

// score is the weakest-first ordering key: score divided by weight (a weight of 0 counts as 0.1).
func (d DomainScore) score() float64 { return float64(d.Score) / math.Max(d.Weight, 0.1) }
