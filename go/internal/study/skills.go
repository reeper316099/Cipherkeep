package study

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/reeper316099/Cipherkeep/go/internal/domain"
)

// Common fields the skill map reads from every entity.
type Common struct {
	ID        string
	Category  string
	Tags      []string
	Archived  bool
	UpdatedAt string
}

// SkillConcept is a concept as the skill map sees it.
type SkillConcept struct {
	Common
	Difficulty string
	Confidence float64
}

// SkillCommand is a command as the skill map sees it.
type SkillCommand struct {
	Common
	Mastery         float64
	LastPracticedAt string
}

// SkillLab is a lab as the skill map sees it.
type SkillLab struct {
	Common
	Status     string
	Difficulty string
	Domain     string
	SolvedAt   string
	ConceptIDs []string
}

// SkillData is everything the skill map is computed from.
type SkillData struct {
	Concepts []SkillConcept
	Commands []SkillCommand
	Labs     []SkillLab
}

// Parts are the components of a domain score.
type Parts struct {
	Concepts float64 `json:"concepts"`
	Commands float64 `json:"commands"`
	Labs     float64 `json:"labs"`
	Recency  float64 `json:"recency"`
}

// Counts are how many items fed a domain.
type Counts struct {
	Concepts int `json:"concepts"`
	Commands int `json:"commands"`
	Labs     int `json:"labs"`
}

// DomainScore is one row of the skill map.
type DomainScore struct {
	Name         string  `json:"name"`
	Weight       float64 `json:"weight"`
	Score        int     `json:"score"`
	Parts        Parts   `json:"parts"`
	Counts       Counts  `json:"counts"`
	LastActivity string  `json:"lastActivity,omitempty"`
}

var (
	diffConcept = map[string]float64{"Easy": 1, "Medium": 1.5, "Hard": 2}
	diffLab     = map[string]float64{"Easy": 1, "Medium": 2, "Hard": 3}
)

func norm(s string) string { return strings.ToLower(domain.JSTrim(s)) }

// aliasIndex maps a normalised label to the indexes of the domains it counts towards.
type aliasIndex map[string][]int

func buildAliasIndex(domains []domain.SkillDomain) aliasIndex {
	idx := aliasIndex{}
	for i, d := range domains {
		for _, a := range append([]string{d.Name}, d.Aliases...) {
			k := norm(a)
			if l := idx[k]; len(l) == 0 || l[len(l)-1] != i {
				idx[k] = append(l, i)
			}
		}
	}
	return idx
}

// labelCache memoises label -> domain indexes (categories and tags repeat across thousands of entities).
type labelCache struct {
	idx   aliasIndex
	cache map[string][]int
}

func (c *labelCache) of(label string) []int {
	if label == "" {
		return nil
	}
	if v, ok := c.cache[label]; ok {
		return v
	}
	v := c.idx[norm(label)]
	c.cache[label] = v
	return v
}

// domainSet is the domains (by index, in ascending order, no repeats) a group of labels maps to.
func (c *labelCache) domainsOf(labels ...[]string) []int {
	var out []int
	add := func(is []int) {
		for _, i := range is {
			dup := false
			for _, o := range out {
				dup = dup || o == i
			}
			if !dup {
				out = append(out, i)
			}
		}
	}
	for _, group := range labels {
		for _, l := range group {
			add(c.of(l))
		}
	}
	sort.Ints(out)
	return out
}

// DomainsFor maps a set of labels (category, tags, lab domain) to the domains whose aliases they match.
func DomainsFor(labels []string, domains []domain.SkillDomain) []string {
	c := &labelCache{idx: buildAliasIndex(domains), cache: map[string][]int{}}
	var out []string
	for _, i := range c.domainsOf(labels) {
		out = append(out, domains[i].Name)
	}
	return out
}

func round1(v float64) float64 { return math.Floor(v*10+0.5) / 10 }

// ScoreDomains computes each domain's score 0-100:
//
//	concepts  <= 35  (sum of difficulty x (0.3 + 0.7 x confidence/3), saturating at 6)
//	commands  <= 25  (sum of mastery/3, saturating at 10)
//	labs      <= 30  (sum of completed-lab difficulty weight 1/2/3, saturating at 8)
//	recency   <= 10  (10 x e^(-days since last activity in the domain / 30))
//
// It makes one pass over the entities, adding each to the domains it belongs to, rather than one pass per domain.
func ScoreDomains(domains []domain.SkillDomain, data SkillData, now time.Time) []DomainScore {
	lc := &labelCache{idx: buildAliasIndex(domains), cache: map[string][]int{}}
	type acc struct {
		concepts, commands, labs float64
		counts                   Counts
		last                     time.Time
	}
	accs := make([]acc, len(domains))
	touch := func(a *acc, iso string) {
		if t, ok := ParseTime(iso); ok && t.After(a.last) {
			a.last = t
		}
	}
	conceptDomains := make(map[string][]int, len(data.Concepts))
	for _, c := range data.Concepts {
		ds := lc.domainsOf([]string{c.Category}, c.Tags)
		conceptDomains[c.ID] = ds
		if c.Archived {
			continue
		}
		w, ok := diffConcept[c.Difficulty]
		if !ok {
			w = 1
		}
		for _, i := range ds {
			a := &accs[i]
			a.counts.Concepts++
			a.concepts += w * (0.3 + 0.7*c.Confidence/3)
			touch(a, c.UpdatedAt)
		}
	}
	for _, c := range data.Commands {
		if c.Archived {
			continue
		}
		for _, i := range lc.domainsOf([]string{c.Category}, c.Tags) {
			a := &accs[i]
			a.counts.Commands++
			a.commands += c.Mastery / 3
			touch(a, c.LastPracticedAt)
		}
	}
	for _, l := range data.Labs {
		if l.Archived {
			continue
		}
		groups := [][]string{{l.Domain}, l.Tags}
		ds := lc.domainsOf(groups...)
		for _, id := range l.ConceptIDs {
			ds = append(ds, conceptDomains[id]...)
		}
		seen := map[int]bool{}
		for _, i := range ds {
			if seen[i] {
				continue
			}
			seen[i] = true
			a := &accs[i]
			a.counts.Labs++
			if l.Status == "Completed" {
				w, ok := diffLab[l.Difficulty]
				if !ok {
					w = 1
				}
				a.labs += w
				if l.SolvedAt != "" {
					touch(a, l.SolvedAt)
				} else {
					touch(a, l.UpdatedAt)
				}
			} else {
				touch(a, l.UpdatedAt)
			}
		}
	}
	out := make([]DomainScore, 0, len(domains))
	for i, d := range domains {
		a := accs[i]
		days := math.Inf(1)
		if !a.last.IsZero() {
			days = math.Max(0, float64(now.Sub(a.last))/float64(24*time.Hour))
		}
		parts := Parts{
			Concepts: 35 * math.Min(1, a.concepts/6),
			Commands: 25 * math.Min(1, a.commands/10),
			Labs:     30 * math.Min(1, a.labs/8),
		}
		if a.counts.Concepts+a.counts.Commands+a.counts.Labs != 0 {
			parts.Recency = 10 * math.Exp(-days/30)
		}
		score := int(jsRound(parts.Concepts + parts.Commands + parts.Labs + parts.Recency))
		ds := DomainScore{
			Name: d.Name, Weight: d.Weight, Score: min(100, score), Counts: a.counts,
			Parts: Parts{round1(parts.Concepts), round1(parts.Commands), round1(parts.Labs), round1(parts.Recency)},
		}
		if !a.last.IsZero() {
			ds.LastActivity = iso(a.last)
		}
		out = append(out, ds)
	}
	return out
}
