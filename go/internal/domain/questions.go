package domain

import (
	"regexp"
	"strings"
)

// Question is one bulleted question; "- [x]" marks it answered.
type Question struct {
	Text string `json:"text"`
	Done bool   `json:"done"`
	Line int    `json:"line"`
}

// QuestionsKey is the section key of "Questions I still have".
const QuestionsKey = "questions-i-still-have"

var bullet = regexp.MustCompile(`^(\s*[-*+]\s+)(?:\[([ xX])\]\s+)?(.*)$`)

// ParseQuestions lists the bulleted questions in md. Empty bullets are ignored.
func ParseQuestions(md string) []Question {
	var out []Question
	for line, l := range strings.Split(md, "\n") {
		m := bullet.FindStringSubmatch(l)
		if m == nil || strings.TrimSpace(m[3]) == "" {
			continue
		}
		out = append(out, Question{Text: strings.TrimSpace(m[3]), Done: m[2] != "" && m[2] != " ", Line: line})
	}
	return out
}

// CountOpen counts the unanswered questions.
func CountOpen(md string) int {
	n := 0
	for _, q := range ParseQuestions(md) {
		if !q.Done {
			n++
		}
	}
	return n
}

// ToggleQuestion flips the checkbox on one line.
func ToggleQuestion(md string, line int) string {
	lines := strings.Split(md, "\n")
	if line < 0 || line >= len(lines) {
		return md
	}
	m := bullet.FindStringSubmatch(lines[line])
	if m == nil {
		return md
	}
	done := m[2] != "" && m[2] != " "
	mark := "x"
	if done {
		mark = " "
	}
	lines[line] = m[1] + "[" + mark + "] " + m[3]
	return strings.Join(lines, "\n")
}
