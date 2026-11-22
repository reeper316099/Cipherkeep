package notion

import (
	"regexp"
	"strings"

	"github.com/reeper316099/Cipherkeep/go/internal/domain"
)

var difficultyMap = map[string]string{
	"beginner": "Easy", "easy": "Easy", "intermediate": "Medium", "medium": "Medium", "advanced": "Hard", "hard": "Hard",
}

// MapDifficulty maps Notion's difficulty words to Easy, Medium or Hard; unknown text is kept and empty means Easy.
func MapDifficulty(raw string) string {
	if v, ok := difficultyMap[strings.ToLower(domain.JSTrim(raw))]; ok {
		return v
	}
	if t := domain.JSTrim(raw); t != "" {
		return t
	}
	return "Easy"
}

var (
	bulletRe = regexp.MustCompile(`^` + jsSpace + `*(?:[-*+]|\d+\.)` + jsSpace + `*(` + jsDot + `*)$`)
	stubRe   = regexp.MustCompile(`(?i)^(component|step) \d$`)
)

// BulletItems returns real bullets only: empty `-` items and unfilled template stubs are skipped.
func BulletItems(md string) []string {
	out := []string{}
	for _, l := range strings.Split(md, "\n") {
		m := bulletRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		if t := domain.JSTrim(m[1]); t != "" && !stubRe.MatchString(t) {
			out = append(out, t)
		}
	}
	return out
}

// ConceptSource is a Notion concept page.
type ConceptSource struct {
	Name       string
	Category   *string
	Difficulty string
	Tags       []string
	Body       string
}

// ParsedConcept is a concept page mapped to Concept fields.
type ParsedConcept struct {
	Input domain.Entity
	// CommandMentions are the titles in "Tools / Commands Related", ConceptMentions those in "Related concepts", for
	// relation resolution.
	CommandMentions []string
	ConceptMentions []string
	Warnings        []string
}

// ParseConceptPage maps a Notion concept page to Concept fields.
func ParseConceptPage(src ConceptSource) ParsedConcept {
	preamble, sections := SplitSections(src.Body)
	out := []any{}
	if preamble != "" {
		out = append(out, map[string]any{"key": "intro", "title": "Intro", "markdown": preamble})
	}
	byKey := map[string]Section{}
	for _, s := range sections {
		out = append(out, map[string]any{"key": s.Key, "title": s.Title, "markdown": s.Markdown})
		byKey[s.Key] = s
	}
	questions, hasQ := byKey[HeadingKey("Questions I still have")]
	tools, hasTools := byKey["tools-commands-related"]
	related, hasRel := byKey["related-concepts"]
	diffRaw := src.Difficulty
	if diffRaw == "" {
		diffRaw = domain.JSTrim(byKey["difficulty-level"].Markdown)
	}
	p := ParsedConcept{Warnings: []string{}, CommandMentions: []string{}, ConceptMentions: []string{}}
	if hasTools {
		p.CommandMentions = BulletItems(tools.Markdown)
	}
	if hasRel {
		p.ConceptMentions = BulletItems(related.Markdown)
	}
	open := 0
	if hasQ {
		open = len(BulletItems(questions.Markdown))
	}
	category := ""
	if src.Category != nil {
		category = *src.Category
	}
	p.Input = domain.Entity{
		"title":         src.Name,
		"category":      category,
		"tags":          strs(src.Tags),
		"difficulty":    MapDifficulty(diffRaw),
		"sections":      out,
		"questionsOpen": open,
	}
	return p
}

// TitleKey is the key titles are matched on.
func TitleKey(s string) string { return domain.NormalizeTitle(s) }
