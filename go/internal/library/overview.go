package library

import (
	"sort"
	"time"

	"github.com/reeper316099/Cipherkeep/go/internal/domain"
	"github.com/reeper316099/Cipherkeep/go/internal/jsonstore"
	"github.com/reeper316099/Cipherkeep/go/internal/study"
)

// History is study/skill-history.json: a daily snapshot of every skill's score, for the trend.
type History struct {
	Days map[string]map[string]float64 `json:"days"`
}

func cloneHistory(h History) History {
	out := History{Days: make(map[string]map[string]float64, len(h.Days))}
	for d, m := range h.Days {
		c := make(map[string]float64, len(m))
		for k, v := range m {
			c[k] = v
		}
		out.Days[d] = c
	}
	return out
}

// Activity that counts as practice: creating, editing, studying, lab work. Imports do not.
var practice = map[string]bool{"create": true, "update": true, "delete": true, "study": true, "lab": true}

const heatDays = 371 // 53 weeks

// OverviewOptions control the dashboard computation.
type OverviewOptions struct {
	Now      time.Time
	TzOffset int
}

// Banner is the header strip of the dashboard.
type Banner struct {
	Name   string       `json:"name"`
	Status string       `json:"status"`
	Mode   string       `json:"mode"`
	Threat study.Threat `json:"threat"`
}

// KPIs are the headline numbers.
type KPIs struct {
	CommandsMastered  int `json:"commandsMastered"`
	CommandsTotal     int `json:"commandsTotal"`
	ConceptsStudied   int `json:"conceptsStudied"`
	ConceptsConfident int `json:"conceptsConfident"`
	LabsCompleted     int `json:"labsCompleted"`
	LabsTotal         int `json:"labsTotal"`
	ActiveProjects    int `json:"activeProjects"`
}

// PlatformStatus is a platform's lab counts.
type PlatformStatus struct {
	Total     int `json:"total"`
	Completed int `json:"completed"`
}

// LabStats breaks labs down.
type LabStats struct {
	ByPlatform   map[string]PlatformStatus `json:"byPlatform"`
	ByStatus     map[string]int            `json:"byStatus"`
	ByDifficulty map[string]int            `json:"byDifficulty"`
}

// Heatmap is the activity per day for the last 53 weeks.
type Heatmap struct {
	From string         `json:"from"`
	To   string         `json:"to"`
	Days map[string]int `json:"days"`
}

// StudyOverview is the study figures plus due and overdue counts.
type StudyOverview struct {
	StudyStats
	Due     int `json:"due"`
	Overdue int `json:"overdue"`
}

// SkillRow is one skill with its trend against a week ago.
type SkillRow struct {
	study.DomainScore
	Trend *float64 `json:"trend"`
}

// TrendPoint is one day of the skill history.
type TrendPoint struct {
	Day    string             `json:"day"`
	Scores map[string]float64 `json:"scores"`
}

// RecentItem is one recent activity row.
type RecentItem struct {
	Ts         string `json:"ts"`
	Type       string `json:"type"`
	Collection string `json:"collection,omitempty"`
	ID         string `json:"id,omitempty"`
	Title      string `json:"title,omitempty"`
}

// Overview is everything the dashboard shows.
type Overview struct {
	GeneratedAt     string                 `json:"generatedAt"`
	Banner          Banner                 `json:"banner"`
	Counts          map[string]int         `json:"counts"`
	KPIs            KPIs                   `json:"kpis"`
	Mastery         []int                  `json:"mastery"`
	Labs            LabStats               `json:"labs"`
	TimePerWeek     []study.WeekMinutes    `json:"timePerWeek"`
	Streak          study.Streak           `json:"streak"`
	Heatmap         Heatmap                `json:"heatmap"`
	Study           StudyOverview          `json:"study"`
	SkillMap        []SkillRow             `json:"skillMap"`
	SkillTrend      []TrendPoint           `json:"skillTrend"`
	Recommendations []study.Recommendation `json:"recommendations"`
	Recent          []RecentItem           `json:"recent"`
}

func live(l *Library, coll string) []domain.Entity {
	var out []domain.Entity
	for _, e := range l.Repos[coll].All() {
		if !e.Archived() {
			out = append(out, e)
		}
	}
	return out
}

func common(e domain.Entity) study.Common {
	return study.Common{ID: e.ID(), Category: e.Str("category"), Tags: e.Strs("tags"), UpdatedAt: e.Str("updatedAt")}
}

// BuildOverview computes the dashboard.
func (l *Library) BuildOverview(o OverviewOptions) (Overview, error) {
	now := o.Now
	if now.IsZero() {
		now = time.Now()
	}
	off := o.TzOffset
	today := study.DayKey(now, off)
	commands, concepts, labs, projects, logs := live(l, "commands"), live(l, "concepts"), live(l, "labs"), live(l, "projects"), live(l, "log")

	// ---- activity: heatmap, streak, staleness ----
	from := study.AddDays(today, -(heatDays - 1))
	var events []ActivityEvent
	for _, e := range l.Activity.Range(study.AddDays(from, -1), study.AddDays(today, 1)) {
		if practice[e.Type] {
			events = append(events, e)
		}
	}
	heat := map[string]int{}
	for _, e := range events {
		if d := study.DayKeyOf(e.Ts, off); d != "" && d >= from && d <= today {
			heat[d]++
		}
	}
	active := make(map[string]bool, len(heat))
	lastDay := ""
	for d := range heat {
		active[d] = true
		if d > lastDay {
			lastDay = d
		}
	}
	streak := study.ComputeStreak(active, today)
	var daysSince *int
	if lastDay != "" {
		n := max(0, study.DaysBetween(lastDay, today))
		daysSince = &n
	}

	// ---- study ----
	due, overdue, err := l.Study.DueCounts(now, 3)
	if err != nil {
		return Overview{}, err
	}
	stats, err := l.Study.Stats(now, off)
	if err != nil {
		return Overview{}, err
	}

	// ---- skill map (+ trend from daily snapshots) ----
	domains := l.Skills.Data()
	if len(domains) == 0 {
		domains = domain.DefaultSkillDomains
	}
	var data study.SkillData
	for _, c := range concepts {
		data.Concepts = append(data.Concepts, study.SkillConcept{Common: common(c), Difficulty: c.Str("difficulty"), Confidence: c.Num("confidence")})
	}
	for _, c := range commands {
		data.Commands = append(data.Commands, study.SkillCommand{Common: common(c), Mastery: c.Num("mastery"), LastPracticedAt: c.Str("lastPracticedAt")})
	}
	for _, lab := range labs {
		data.Labs = append(data.Labs, study.SkillLab{Common: common(lab), Status: lab.Str("status"), Difficulty: lab.Str("difficulty"),
			Domain: lab.Str("domain"), SolvedAt: lab.Str("solvedAt"), ConceptIDs: lab.Strs("conceptIds")})
	}
	skills := study.ScoreDomains(domains, data, now)
	history, err := jsonstore.Open(l.Store, "study/skill-history.json", jsonstore.Options[History]{
		Version: 1, Defaults: func() History { return History{Days: map[string]map[string]float64{}} }, Clone: cloneHistory,
		Validate: func(h *History) error {
			if h.Days == nil {
				h.Days = map[string]map[string]float64{}
			}
			return nil
		},
	})
	if err != nil {
		return Overview{}, err
	}
	snapshot := make(map[string]float64, len(skills))
	for _, s := range skills {
		snapshot[s.Name] = float64(s.Score)
	}
	if !sameScores(history.Data().Days[today], snapshot) {
		if _, err := history.Update(func(h *History) error {
			h.Days[today] = snapshot
			for d := range h.Days {
				if study.DaysBetween(d, today) > 400 {
					delete(h.Days, d)
				}
			}
			return nil
		}); err != nil {
			return Overview{}, err
		}
	}
	hist := history.Data().Days
	var pastDays []string
	for d := range hist {
		if study.DaysBetween(d, today) >= 7 {
			pastDays = append(pastDays, d)
		}
	}
	sort.Strings(pastDays)
	var base map[string]float64
	if len(pastDays) > 0 {
		base = hist[pastDays[len(pastDays)-1]]
	}
	skillMap := make([]SkillRow, len(skills))
	for i, s := range skills {
		skillMap[i] = SkillRow{DomainScore: s}
		if base != nil {
			t := float64(s.Score) - base[s.Name]
			skillMap[i].Trend = &t
		}
	}
	allDays := make([]string, 0, len(hist))
	for d := range hist {
		allDays = append(allDays, d)
	}
	sort.Strings(allDays)
	if len(allDays) > 90 {
		allDays = allDays[len(allDays)-90:]
	}
	trend := make([]TrendPoint, len(allDays))
	for i, d := range allDays {
		trend[i] = TrendPoint{Day: d, Scores: hist[d]}
	}

	// ---- time per week: log entries + lab time (on solve/update date) ----
	var minutes []study.MinutesEntry
	for _, e := range logs {
		minutes = append(minutes, study.MinutesEntry{Day: e.Str("date"), Minutes: e.Num("timeSpentMin")})
	}
	for _, lab := range labs {
		if lab.Num("timeSpentMin") > 0 {
			day := lab.Str("solvedAt")
			if day == "" {
				day = study.DayKeyOf(lab.Str("updatedAt"), off)
			}
			if len(day) > 10 {
				day = day[:10]
			}
			minutes = append(minutes, study.MinutesEntry{Day: day, Minutes: lab.Num("timeSpentMin")})
		}
	}

	by := func(es []domain.Entity, key string) map[string]int {
		m := map[string]int{}
		for _, e := range es {
			k := e.Str(key)
			if k == "" {
				k = "—"
			}
			m[k]++
		}
		return m
	}
	platformStatus := map[string]PlatformStatus{}
	for _, lab := range labs {
		p := lab.Str("platform")
		if p == "" {
			p = "—"
		}
		ps := platformStatus[p]
		ps.Total++
		if lab.Str("status") == "Completed" {
			ps.Completed++
		}
		platformStatus[p] = ps
	}

	openByConcept := map[string]int{}
	for _, q := range l.Study.Questions(false) {
		openByConcept[q.ConceptID]++
	}
	rec := study.RecommendInput{Domains: skills, Due: due, Now: now}
	for _, c := range concepts {
		rec.Concepts = append(rec.Concepts, study.RecConcept{ID: c.ID(), Title: c.Title(), QuestionsOpen: openByConcept[c.ID()]})
	}
	for _, c := range commands {
		rec.Commands = append(rec.Commands, study.RecCommand{ID: c.ID(), Name: c.Str("name"), Mastery: c.Num("mastery"),
			LastPracticedAt: c.Str("lastPracticedAt"), CreatedAt: c.Str("createdAt")})
	}
	for _, lab := range labs {
		rec.Labs = append(rec.Labs, study.RecLab{ID: lab.ID(), Title: lab.Title(), Status: lab.Str("status"),
			ConceptIDs: lab.Strs("conceptIds"), UpdatedAt: lab.Str("updatedAt")})
	}
	recs := study.BuildRecommendations(rec)
	if recs == nil {
		recs = []study.Recommendation{}
	}

	titleOf := func(coll, id string) string {
		if r := l.Repos[coll]; r != nil && id != "" {
			if e, ok := r.Get(id); ok {
				return e.Title()
			}
		}
		return ""
	}
	sorted := append([]ActivityEvent(nil), events...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Ts > sorted[j].Ts })
	recent := []RecentItem{}
	for i := 0; i < len(sorted) && i < 12; i++ {
		e := sorted[i]
		recent = append(recent, RecentItem{Ts: e.Ts, Type: e.Type, Collection: e.Collection, ID: e.ID, Title: titleOf(e.Collection, e.ID)})
	}

	empty := true
	counts := map[string]int{}
	for _, c := range domain.Collections {
		all := l.Repos[c].All()
		if len(all) > 0 {
			empty = false
		}
		n := 0
		for _, e := range all {
			if !e.Archived() {
				n++
			}
		}
		counts[c] = n
	}
	mastery := make([]int, 4)
	k := KPIs{CommandsTotal: len(commands), ConceptsStudied: len(concepts), LabsTotal: len(labs)}
	for _, c := range commands {
		if m := int(c.Num("mastery")); m >= 0 && m <= 3 {
			mastery[m]++
		}
		if c.Num("mastery") == 3 {
			k.CommandsMastered++
		}
	}
	for _, c := range concepts {
		if c.Num("confidence") >= 2 {
			k.ConceptsConfident++
		}
	}
	for _, lab := range labs {
		if lab.Str("status") == "Completed" {
			k.LabsCompleted++
		}
	}
	for _, p := range projects {
		if p.Str("status") == "In progress" {
			k.ActiveProjects++
		}
	}

	return Overview{
		GeneratedAt: domain.ISO(now),
		Banner: Banner{Name: "HQ", Status: "ACTIVE", Mode: "LEARNING",
			Threat: study.ThreatLevelOf(study.ThreatInput{DaysSinceActivity: daysSince, Streak: streak.Current, Overdue: overdue, Empty: empty})},
		Counts: counts, KPIs: k, Mastery: mastery,
		Labs:        LabStats{ByPlatform: platformStatus, ByStatus: by(labs, "status"), ByDifficulty: by(labs, "difficulty")},
		TimePerWeek: study.WeeklyMinutes(minutes, today, 12),
		Streak:      streak, Heatmap: Heatmap{From: from, To: today, Days: heat},
		Study:    StudyOverview{StudyStats: stats, Due: due, Overdue: overdue},
		SkillMap: skillMap, SkillTrend: trend, Recommendations: recs, Recent: recent,
	}, nil
}

func sameScores(a, b map[string]float64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range b {
		if av, ok := a[k]; !ok || av != v {
			return false
		}
	}
	return true
}
