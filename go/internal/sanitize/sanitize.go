// Package sanitize redacts a lab writeup before it is shared: flags, addresses, credentials and private keys, plus the
// user's own patterns. It is the Go port of packages/shared/src/sanitize.ts.
//
// One deliberate difference: custom patterns are compiled with Go's RE2 engine, which cannot backtrack catastrophically,
// so a hostile pattern cannot hang the app. JavaScript-only syntax (lookahead, backreferences) is reported as an
// invalid pattern instead of being applied.
package sanitize

import (
	"regexp"
	"strings"
)

// Result is the redacted text, how many replacements were made, and the custom patterns that were not applied.
type Result struct {
	Text            string   `json:"text"`
	Redactions      int      `json:"redactions"`
	InvalidPatterns []string `json:"invalidPatterns"`
}

const (
	space    = `\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`
	spaceSet = `[` + space + `]`
)

type rule struct {
	re      *regexp.Regexp
	replace func(groups []string) string
}

func lit(s string) func([]string) string { return func([]string) string { return s } }

var defaultRules = []rule{
	{regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`), lit("[PRIVATE KEY REDACTED]")},
	{regexp.MustCompile(`\b[A-Za-z][A-Za-z0-9_]{1,24}\{[^}` + space + `]{4,}\}`), lit("[FLAG REDACTED]")},
	{regexp.MustCompile(`(?i)\b(password|passwd|pwd|secret|token|api[_-]?key)(` + spaceSet + `*[:=]` + spaceSet + `*)([^` + space + `]+)`),
		func(g []string) string { return g[1] + g[2] + "[REDACTED]" }},
	{regexp.MustCompile(`\bBearer` + spaceSet + `+[A-Za-z0-9._~+/-]{8,}=*`), lit("Bearer [REDACTED]")},
	{regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), lit("[AWS KEY REDACTED]")},
	{regexp.MustCompile(`\b[\w.+-]+@[\w-]+(?:\.[\w-]+)+\b`), lit("[EMAIL]")},
	{regexp.MustCompile(`\b(?:(?:25[0-5]|2[0-4]\d|1?\d?\d)\.){3}(?:25[0-5]|2[0-4]\d|1?\d?\d)\b`), lit("[IP]")},
}

var nestedQuantifier = regexp.MustCompile(`(\+|\*|\{\d+,?\d*\})` + spaceSet + `*\)` + spaceSet + `*(\+|\*|\{)`)

// IsSafePattern rejects patterns with an obviously catastrophic shape (nested quantifiers) or excessive length.
func IsSafePattern(p string) bool {
	return p != "" && utf16Count(p) <= 200 && !nestedQuantifier.MatchString(p)
}

func utf16Count(p string) int {
	n := 0
	for _, r := range p {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// Writeup applies the built-in rules and then the user's extra patterns (case-insensitive).
func Writeup(input string, extra []string) Result {
	text := input
	redactions := 0
	apply := func(r rule) {
		var b strings.Builder
		last := 0
		for _, m := range r.re.FindAllStringSubmatchIndex(text, -1) {
			b.WriteString(text[last:m[0]])
			groups := make([]string, len(m)/2)
			for i := range groups {
				if m[2*i] >= 0 {
					groups[i] = text[m[2*i]:m[2*i+1]]
				}
			}
			redactions++
			b.WriteString(r.replace(groups))
			last = m[1]
		}
		b.WriteString(text[last:])
		text = b.String()
	}
	for _, r := range defaultRules {
		apply(r)
	}
	invalid := []string{}
	for _, p := range extra {
		if !IsSafePattern(p) {
			invalid = append(invalid, p)
			continue
		}
		re, err := regexp.Compile("(?i)" + p)
		if err != nil {
			invalid = append(invalid, p)
			continue
		}
		apply(rule{re, lit("[REDACTED]")})
	}
	return Result{Text: text, Redactions: redactions, InvalidPatterns: invalid}
}
