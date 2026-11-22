package domain

import (
	"regexp"
	"strings"
)

// WikiLink is a [[Title]], [[cmd:ls]] or [[Title|alias]] reference.
type WikiLink struct {
	Raw    string
	Target string
	Kind   string // "cmd" for [[cmd:ls]]; empty for [[Title]]
	Alias  string
}

var (
	codeSpans   = regexp.MustCompile("(?s)```.*?```|~~~.*?~~~|`[^`\n]*`")
	wikiRe      = regexp.MustCompile(`\[\[([^\][\n]+?)\]\]`)
	kindRe      = regexp.MustCompile(`^(\w+):(.+)$`)
	linkKinds   = map[string]bool{"cmd": true, "concept": true, "lab": true, "note": true, "project": true, "log": true, "snippet": true}
	kindToColl  = map[string]string{"cmd": "commands", "concept": "concepts", "lab": "labs", "note": "notes", "project": "projects", "log": "log", "snippet": "snippets"}
	unkindedOrd = []string{"notes", "concepts", "commands", "labs", "projects", "snippets", "log"}
)

// ExtractWikiLinks finds wiki links in Markdown, ignoring code and glob classes like [[:upper:]].
func ExtractWikiLinks(md string) []WikiLink {
	stripped := codeSpans.ReplaceAllString(md, "")
	var out []WikiLink
	for _, m := range wikiRe.FindAllStringSubmatch(stripped, -1) {
		inner := m[1]
		if strings.HasPrefix(inner, ":") || strings.HasPrefix(inner, "!") || strings.HasSuffix(inner, ":]") {
			continue
		}
		left, alias, _ := strings.Cut(inner, "|")
		if km := kindRe.FindStringSubmatch(left); km != nil && linkKinds[km[1]] {
			out = append(out, WikiLink{Raw: m[0], Kind: km[1], Target: strings.TrimSpace(km[2]), Alias: alias})
		} else {
			out = append(out, WikiLink{Raw: m[0], Target: strings.TrimSpace(left), Alias: alias})
		}
	}
	return out
}

// TitleIndexEntry is one entity as the title index sees it.
type TitleIndexEntry struct {
	Type, ID, Title string
	Name            string // commands are also addressable by name
}

// TitleIndex maps collection -> normalised title -> id.
type TitleIndex map[string]map[string]string

// BuildTitleIndex indexes titles (and command names); the first entity with a given title wins.
func BuildTitleIndex(entries []TitleIndexEntry) TitleIndex {
	idx := TitleIndex{}
	for _, e := range entries {
		m := idx[e.Type]
		if m == nil {
			m = map[string]string{}
			idx[e.Type] = m
		}
		for _, t := range []string{e.Title, e.Name} {
			if t == "" {
				continue
			}
			if k := NormalizeTitle(t); k != "" {
				if _, taken := m[k]; !taken {
					m[k] = e.ID
				}
			}
		}
	}
	return idx
}

// ResolveWikiLink finds the entity a link points at: [[cmd:ls]] is exact, [[Title]] tries each collection in turn.
func ResolveWikiLink(idx TitleIndex, l WikiLink) (typ, id string, ok bool) {
	key := NormalizeTitle(l.Target)
	order := unkindedOrd
	if l.Kind != "" {
		order = []string{kindToColl[l.Kind]}
	}
	for _, t := range order {
		if id, found := idx[t][key]; found {
			return t, id, true
		}
	}
	return "", "", false
}
