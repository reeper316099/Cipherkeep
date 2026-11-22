package domain

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

var (
	nonSlug   = regexp.MustCompile(`[^a-z0-9]+`)
	combining = regexp.MustCompile("[̀-ͯ]")
)

// Slugify turns a title into a URL- and file-safe slug (accents removed, lower case, dashes), or "untitled".
func Slugify(input string) string {
	s := norm.NFKD.String(input)
	s = combining.ReplaceAllString(s, "")
	s = strings.ToLower(s)
	s = nonSlug.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "untitled"
	}
	return s
}

// IsJSSpace matches what JavaScript's \s matches.
func IsJSSpace(r rune) bool {
	return isJSSpace(r)
}

func isJSSpace(r rune) bool {
	if r == 0x85 { // Go treats U+0085 as space, JavaScript does not
		return false
	}
	return unicode.IsSpace(r) || r == 0xFEFF
}

// NormalizeTitle is a case-, Unicode- and emoji-variation-insensitive key for matching titles.
func NormalizeTitle(s string) string {
	s = norm.NFKC.String(s)
	s = strings.NewReplacer("︎", "", "️", "").Replace(s)
	var b strings.Builder
	space := false
	for _, r := range s {
		if isJSSpace(r) {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	return strings.ToLower(b.String())
}

// JSTrim trims the characters JavaScript's String.prototype.trim removes.
func JSTrim(s string) string { return strings.TrimFunc(s, isJSSpace) }
