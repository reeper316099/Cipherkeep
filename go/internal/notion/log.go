package notion

import (
	"strings"

	"github.com/reeper316099/Cipherkeep/go/internal/domain"
)

// LogSource is a Notion log-entry page. A nil field means the property was absent.
type LogSource struct {
	Name        string
	Date        *string
	TimeSpent   *string
	Topics      *string
	KeyTakeaway *string
	Body        string
}

// ParsedLog is a log entry mapped to LogEntry fields.
type ParsedLog struct {
	Input domain.Entity
	// Learned are the bullet titles under "What I learned", for command and concept resolution.
	Learned  []string
	Warnings []string
}

// ParseLogEntry maps a Notion log page to LogEntry fields.
func ParseLogEntry(src LogSource) ParsedLog {
	warnings := []string{}
	date, ok := ParseNotionDate(deref(src.Date))
	if !ok {
		date, ok = ParseNotionDate(src.Name)
	}
	if !ok {
		shown := src.Name
		if src.Date != nil {
			shown = *src.Date
		}
		warnings = append(warnings, "could not parse date \""+shown+"\"")
		date = "1970-01-01"
	}
	spent := deref(src.TimeSpent)
	var minutes any
	if m, ok := ParseTimeSpent(spent); ok {
		minutes = m
	} else if spent != "" {
		warnings = append(warnings, "could not parse time spent \""+spent+"\" (raw kept)")
	}
	_, sections := SplitSections(src.Body)
	learned := []string{}
	if i := findSection(sections, "what-i-learned"); i >= 0 {
		learned = BulletItems(sections[i].Markdown)
	}
	topics := []string{}
	for _, s := range strings.Split(deref(src.Topics), ",") {
		if s = domain.JSTrim(s); s != "" {
			topics = append(topics, s)
		}
	}
	var spentRaw, topicsRaw any
	if spent != "" {
		spentRaw = spent
	}
	if t := deref(src.Topics); t != "" {
		topicsRaw = t
	}
	return ParsedLog{
		Warnings: warnings,
		Learned:  learned,
		Input: domain.Entity{
			"title":        src.Name,
			"date":         date,
			"timeSpentMin": minutes,
			"timeSpentRaw": spentRaw,
			"topics":       strs(topics),
			"topicsRaw":    topicsRaw,
			"keyTakeaway":  deref(src.KeyTakeaway),
			"body":         src.Body,
		},
	}
}
