package study

import (
	"fmt"
	"math"
	"time"
)

// ThreatLevel is LOW, MODERATE, ELEVATED or HIGH.
type ThreatLevel string

// Threat is how stale practice is, with the arithmetic spelled out.
type Threat struct {
	Level             ThreatLevel `json:"level"`
	Score             int         `json:"score"`
	DaysSinceActivity *int        `json:"daysSinceActivity"`
	Streak            int         `json:"streak"`
	Overdue           int         `json:"overdue"`
	Explanation       string      `json:"explanation"`
}

// ThreatInput is what the threat level is computed from.
type ThreatInput struct {
	DaysSinceActivity *int // nil: no activity yet
	Streak            int
	Overdue           int
	// Empty is a brand-new profile with no content: nothing can be stale.
	Empty bool
}

// ThreatLevelOf computes the threat level.
//
//	score = 10 x days since last activity (3 if you have content but no activity yet; an empty profile is simply LOW)
//	        + 2 x cards overdue by > 3 days (max 20) - 3 x streak days (max 14)
//	LOW < 20, MODERATE 20-49, ELEVATED 50-89, HIGH >= 90
func ThreatLevelOf(in ThreatInput) Threat {
	if in.Empty {
		return Threat{Level: "LOW", Explanation: "LOW (score 0): nothing to practise yet. Add or import some content and this starts tracking your practice."}
	}
	days := 3
	if in.DaysSinceActivity != nil {
		days = *in.DaysSinceActivity
	}
	raw := days*10 + min(in.Overdue, 20)*2 - min(in.Streak, 14)*3
	score := max(0, raw)
	level := ThreatLevel("LOW")
	switch {
	case score >= 90:
		level = "HIGH"
	case score >= 50:
		level = "ELEVATED"
	case score >= 20:
		level = "MODERATE"
	}
	when := fmt.Sprintf("%d day(s) since last activity", days)
	switch {
	case in.DaysSinceActivity == nil:
		when = "no activity yet"
	case *in.DaysSinceActivity == 0:
		when = "active today"
	default:
		when = fmt.Sprintf("%d day(s) since last activity", *in.DaysSinceActivity)
	}
	return Threat{
		Level: level, Score: score, DaysSinceActivity: in.DaysSinceActivity, Streak: in.Streak, Overdue: in.Overdue,
		Explanation: fmt.Sprintf("%s (score %d): %s, %d card(s) overdue by more than 3 days, %d-day streak. Score = 10×days idle + 2×overdue (max 20) − 3×streak (max 14). LOW < 20, MODERATE < 50, ELEVATED < 90, HIGH above.",
			level, score, when, in.Overdue, in.Streak),
	}
}

// DayKey is the YYYY-MM-DD of a timestamp in the learner's timezone (offsetMin = Date.getTimezoneOffset()).
func DayKey(t time.Time, offsetMin int) string {
	return t.Add(-time.Duration(offsetMin) * time.Minute).UTC().Format("2006-01-02")
}

// DayKeyOf is DayKey for an ISO string; an unparseable timestamp gives "".
func DayKeyOf(ts string, offsetMin int) string {
	t, ok := ParseTime(ts)
	if !ok {
		return ""
	}
	return DayKey(t, offsetMin)
}

func parseDay(d string) time.Time { t, _ := time.Parse("2006-01-02", d); return t }

// AddDays adds n days to a YYYY-MM-DD string.
func AddDays(d string, n int) string { return parseDay(d).AddDate(0, 0, n).Format("2006-01-02") }

// DaysBetween is the whole number of days from a to b.
func DaysBetween(a, b string) int {
	return int(math.Round(parseDay(b).Sub(parseDay(a)).Hours() / 24))
}

// Streak is the current and longest run of active days.
type Streak struct {
	Current int `json:"current"`
	Longest int `json:"longest"`
}

// ComputeStreak counts consecutive active days ending today, or ending yesterday when today has no activity yet (so
// the streak is not wiped before you have had a chance to practise).
func ComputeStreak(active map[string]bool, today string) Streak {
	cur := 0
	d := AddDays(today, -1)
	if active[today] {
		d = today
	}
	for active[d] {
		cur++
		d = AddDays(d, -1)
	}
	days := make([]string, 0, len(active))
	for k := range active {
		days = append(days, k)
	}
	sortStrings(days)
	longest, run, prev := 0, 0, ""
	for _, day := range days {
		if prev != "" && DaysBetween(prev, day) == 1 {
			run++
		} else {
			run = 1
		}
		longest = max(longest, run)
		prev = day
	}
	return Streak{Current: cur, Longest: longest}
}

// WeekStart is the Monday-based week start of a day.
func WeekStart(d string) string {
	dow := (int(parseDay(d).Weekday()) + 6) % 7
	return AddDays(d, -dow)
}

// MinutesEntry is time spent on one day.
type MinutesEntry struct {
	Day     string
	Minutes float64
}

// WeekMinutes is the minutes in one week.
type WeekMinutes struct {
	Week    string  `json:"week"`
	Minutes float64 `json:"minutes"`
}

// WeeklyMinutes sums minutes into the last `weeks` Monday-based weeks ending at today (oldest first, zeros included).
func WeeklyMinutes(entries []MinutesEntry, today string, weeks int) []WeekMinutes {
	start := AddDays(WeekStart(today), -7*(weeks-1))
	out := make([]WeekMinutes, weeks)
	for i := range out {
		out[i].Week = AddDays(start, 7*i)
	}
	for _, e := range entries {
		w := WeekStart(e.Day)
		for i := range out {
			if out[i].Week == w {
				out[i].Minutes += e.Minutes
				break
			}
		}
	}
	return out
}
