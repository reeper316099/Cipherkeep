// Package study is the study engine: spaced repetition, card building and answer checking, the skill map, the
// threat level, streaks and recommendations. It is pure (no files, no clocks except the time passed in) and is the
// Go port of packages/shared/src/study.
package study

import (
	"math"
	"time"
)

// Rating is how well a card was remembered.
type Rating string

const (
	Again Rating = "again"
	Hard  Rating = "hard"
	Good  Rating = "good"
	Easy  Rating = "easy"
)

// Ratings lists every rating.
var Ratings = []Rating{Again, Hard, Good, Easy}

// SrsState is the scheduling state of one card.
type SrsState struct {
	Ease         float64 `json:"ease"`
	IntervalDays float64 `json:"intervalDays"`
	DueAt        string  `json:"dueAt,omitempty"`
	Reps         int     `json:"reps"`
	Lapses       int     `json:"lapses"`
	LastAt       string  `json:"lastAt,omitempty"`
	// FirstAt is when the card was first reviewed (drives the daily new-card limit).
	FirstAt string `json:"firstAt,omitempty"`
}

// NewSrs is the state of a card never reviewed.
func NewSrs() SrsState { return SrsState{Ease: 2.5} }

// MaxIntervalDays is a safety cap (10 years) so repeated "easy" can never overflow dates.
const MaxIntervalDays = 3650

const (
	day        = 24 * time.Hour
	againDelay = 10 * time.Minute
)

var quality = map[Rating]int{Again: 1, Hard: 3, Good: 4, Easy: 5}

// round4 is JavaScript's +(x).toFixed(4).
func round4(x float64) float64 { return math.Round(x*1e4) / 1e4 }

// jsRound is JavaScript's Math.round (halves round up).
func jsRound(x float64) float64 { return math.Floor(x + 0.5) }

func iso(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

// Schedule is SM-2 with Anki-style tweaks.
//   - ease changes by the SM-2 formula for hard/good/easy; "again" costs 0.2; clamped to [1.3, 3.0]
//   - "again" resets repetitions and brings the card back in 10 minutes
//   - first successful review: 1 day (easy: 3); second: 6 days (hard: 4, easy: 8); then interval x ease
//     (hard uses x1.2, easy adds a x1.3 bonus). The interval never shrinks on a pass.
func Schedule(prev SrsState, rating Rating, now time.Time) SrsState {
	q := float64(quality[rating])
	if rating == Again {
		return SrsState{
			Ease: math.Max(1.3, round4(prev.Ease-0.2)), IntervalDays: 0, Reps: 0, Lapses: prev.Lapses + 1,
			LastAt: iso(now), DueAt: iso(now.Add(againDelay)), FirstAt: prev.FirstAt,
		}
	}
	ease := math.Min(3, math.Max(1.3, round4(prev.Ease+(0.1-(5-q)*(0.08+(5-q)*0.02)))))
	var interval float64
	switch {
	case prev.Reps == 0:
		interval = 1
		if rating == Easy {
			interval = 3
		}
	case prev.Reps == 1:
		interval = 6
		if rating == Hard {
			interval = 4
		} else if rating == Easy {
			interval = 8
		}
	default:
		factor := ease
		if rating == Hard {
			factor = 1.2
		} else if rating == Easy {
			factor = ease * 1.3
		}
		interval = math.Max(prev.IntervalDays+1, jsRound(prev.IntervalDays*factor))
	}
	interval = math.Min(interval, MaxIntervalDays)
	return SrsState{
		Ease: ease, IntervalDays: interval, Reps: prev.Reps + 1, Lapses: prev.Lapses,
		LastAt: iso(now), DueAt: iso(now.Add(time.Duration(interval) * day)), FirstAt: prev.FirstAt,
	}
}

// MasteryFromSrs maps intervals to mastery: 0 unseen, 1 learning, 2 comfortable (interval >= 6 d), 3 mastered (>= 21 d).
func MasteryFromSrs(s SrsState) int {
	switch {
	case s.LastAt == "":
		return 0
	case s.IntervalDays >= 21:
		return 3
	case s.IntervalDays >= 6:
		return 2
	}
	return 1
}

// ParseTime parses the ISO timestamps (and plain dates) the app stores; ok is false when it cannot.
func ParseTime(s string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// IsDue reports whether a card with this state is due at now.
func IsDue(s *SrsState, now time.Time) bool {
	if s == nil || s.DueAt == "" {
		return false
	}
	t, ok := ParseTime(s.DueAt)
	return ok && !t.After(now)
}
