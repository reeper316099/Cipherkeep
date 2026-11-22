package notion

import (
	"regexp"
	"strings"

	"github.com/reeper316099/Cipherkeep/go/internal/domain"
)

// RelationRef is one entry of a Notion relation cell.
type RelationRef struct {
	Title string
	Path  string // empty when the cell had only a bare name
	ID    string // 32 hex characters, lower case, empty when unknown
}

const (
	jsSpace = `[\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]`
	jsDot   = `[^\n\r\x{2028}\x{2029}]`
)

// relationRe matches one `Title (target)` entry at the start of the remaining text. The target always ends in .md or is
// an http(s) URL, which anchors the parse when titles contain commas or parentheses.
var relationRe = regexp.MustCompile(`^(` + jsDot + `*?)` + jsSpace + `*\(((?:https?://[^()\s]+)|(?:[^()]*?\.md))\)` + jsSpace + `*(?:,` + jsSpace + `*|$)`)

var relationID = regexp.MustCompile(`(?i)([0-9a-f]{32})(?:\.md)?$`)

// ParseRelationCell parses `Title (url-encoded/path.md), Other (...)`.
func ParseRelationCell(cell string) []RelationRef {
	text := domain.JSTrim(cell)
	if text == "" {
		return nil
	}
	var out []RelationRef
	last := 0
	for last < len(text) {
		m := relationRe.FindStringSubmatchIndex(text[last:])
		if m == nil {
			break
		}
		title := domain.JSTrim(text[last+m[2] : last+m[3]])
		path := DecodeNotionPath(text[last+m[4] : last+m[5]])
		id := ""
		if im := relationID.FindStringSubmatch(path); im != nil {
			id = strings.ToLower(im[1])
		}
		if title == "" {
			title = SplitName(path).Title
		}
		out = append(out, RelationRef{Title: title, Path: path, ID: id})
		last += m[1]
		if m[1] == 0 {
			break
		}
	}
	if last < len(text) {
		for _, t := range strings.Split(text[last:], ",") {
			if t = domain.JSTrim(t); t != "" {
				out = append(out, RelationRef{Title: t})
			}
		}
	}
	return out
}

// SplitList splits a plain `a, b, c` multi-select or tag cell on commas and semicolons.
func SplitList(cell string) []string {
	var out []string
	for _, s := range strings.FieldsFunc(cell, func(r rune) bool { return r == ',' || r == ';' }) {
		if s = domain.JSTrim(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
