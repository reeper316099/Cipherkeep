package notion

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/reeper316099/Cipherkeep/go/internal/domain"
)

var (
	timeNoise  = regexp.MustCompile(`[~\x{2248}]|\b(?:about|approx\.?|approximately|around|roughly)\b`)
	timeAnd    = regexp.MustCompile(`\band\b`)
	timeClock  = regexp.MustCompile(`^(\d{1,2}):(\d{2})$`)
	timeHalf   = regexp.MustCompile(`half an? hour`)
	timeAnHour = regexp.MustCompile(`\ban? hour\b`)
	timeUnit   = regexp.MustCompile(`(\d+(?:\.\d+)?)` + jsSpace + `*(hours?|hrs?|h|minutes?|mins?|m)\b`)
)

// ParseTimeSpent turns "1 hr", "45 mins", "2h 30m", "~1.5 hours", "1:30" or "half an hour" into minutes.
func ParseTimeSpent(raw string) (int, bool) {
	if raw == "" {
		return 0, false
	}
	s := strings.ToLower(raw)
	s = timeNoise.ReplaceAllString(s, " ")
	s = timeAnd.ReplaceAllString(s, " ")
	s = domain.JSTrim(s)
	if s == "" {
		return 0, false
	}
	if m := timeClock.FindStringSubmatch(s); m != nil {
		h, _ := strconv.Atoi(m[1])
		mi, _ := strconv.Atoi(m[2])
		return h*60 + mi, true
	}
	total, matched := 0.0, false
	if timeHalf.MatchString(s) {
		total, matched = total+30, true
	} else if timeAnHour.MatchString(s) {
		total, matched = total+60, true
	}
	for _, m := range timeUnit.FindAllStringSubmatch(s, -1) {
		n, _ := strconv.ParseFloat(m[1], 64)
		if strings.HasPrefix(m[2], "h") {
			n *= 60
		}
		total += n
		matched = true
	}
	if !matched {
		return 0, false
	}
	return int(jsRound(total)), true
}

var months = []string{"january", "february", "march", "april", "may", "june", "july", "august", "september", "october", "november", "december"}

var (
	dateISO  = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})`)
	dateMDY  = regexp.MustCompile(`^([A-Za-z]+)\.?\s+(\d{1,2})(?:st|nd|rd|th)?,?\s+(\d{4})`)
	dateDMY  = regexp.MustCompile(`^(\d{1,2})\s+([A-Za-z]+)\.?,?\s+(\d{4})`)
	dateSlsh = regexp.MustCompile(`^(\d{1,2})/(\d{1,2})/(\d{4})`)
)

func isoDate(y, m, d int) (string, bool) {
	// JavaScript's Date.UTC maps years 0..99 to 1900..1999, so the round-trip check below rejects them there too.
	if y < 100 || m < 1 || m > 12 || d < 1 {
		return "", false
	}
	t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	if t.Year() != y || int(t.Month()) != m || t.Day() != d {
		return "", false
	}
	return fmt.Sprintf("%04d-%02d-%02d", y, m, d), true
}

func monthIndex(word string) int {
	w := strings.ToLower(word)
	if len(w) > 3 {
		w = w[:3]
	}
	for i, m := range months {
		if strings.HasPrefix(m, w) {
			return i
		}
	}
	return -1
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

// ParseNotionDate reads "March 24, 2026", "2026-03-24", "24 Mar 2026" or "03/24/2026" and returns YYYY-MM-DD (the start
// of a range). A leading "@" and anything after an arrow are ignored.
func ParseNotionDate(raw string) (string, bool) {
	if raw == "" {
		return "", false
	}
	s := strings.TrimPrefix(raw, "@")
	if i := strings.Index(s, "→"); i >= 0 {
		s = s[:i]
	}
	s = domain.JSTrim(s)
	if m := dateISO.FindStringSubmatch(s); m != nil {
		return isoDate(atoi(m[1]), atoi(m[2]), atoi(m[3]))
	}
	if m := dateMDY.FindStringSubmatch(s); m != nil {
		if mi := monthIndex(m[1]); mi >= 0 {
			return isoDate(atoi(m[3]), mi+1, atoi(m[2]))
		}
		return "", false
	}
	if m := dateDMY.FindStringSubmatch(s); m != nil {
		if mi := monthIndex(m[2]); mi >= 0 {
			return isoDate(atoi(m[3]), mi+1, atoi(m[1]))
		}
		return "", false
	}
	if m := dateSlsh.FindStringSubmatch(s); m != nil {
		return isoDate(atoi(m[3]), atoi(m[1]), atoi(m[2]))
	}
	return "", false
}
