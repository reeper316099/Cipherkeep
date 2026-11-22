// Package notion parses what a Notion export looks like on disk: page and database names, Markdown pages with property
// blocks, tables, relation cells, dates and durations, and maps command, concept and log pages to structured fields.
// It is the Go port of packages/shared/src/notion and has no I/O.
package notion

import (
	"regexp"
	"strings"

	"golang.org/x/text/unicode/norm"

	"github.com/reeper316099/Cipherkeep/go/internal/domain"
)

var idSuffix = regexp.MustCompile(`(?i)\s([0-9a-f]{32})$`)

// Name is a Notion file name split into its parts.
type Name struct {
	Title string
	ID    string // lower case, empty when the name has no id
	Ext   string // "", ".md" or ".csv"
	// All is true for the "_all.csv" duplicate Notion writes next to a database CSV.
	All bool
}

// SplitName splits `ls 32df3e3018aa80d89e90dddec81b898b.md` into title, id and extension. Directories are ignored: only
// the last path segment is looked at.
func SplitName(filename string) Name {
	base := filename
	if i := strings.LastIndex(filename, "/"); i >= 0 {
		base = filename[i+1:]
	}
	n := Name{}
	stem := base
	lower := strings.ToLower(stem)
	if strings.HasSuffix(lower, ".md") {
		n.Ext, stem = ".md", stem[:len(stem)-3]
	} else if strings.HasSuffix(lower, ".csv") {
		n.Ext, stem = ".csv", stem[:len(stem)-4]
	}
	if n.Ext == ".csv" && strings.HasSuffix(stem, "_all") {
		n.All, stem = true, stem[:len(stem)-4]
	}
	if m := idSuffix.FindStringSubmatchIndex(stem); m != nil {
		n.ID = strings.ToLower(stem[m[2]:m[3]])
		stem = stem[:m[0]]
	}
	n.Title = domain.JSTrim(stem)
	return n
}

var leadingSymbols = regexp.MustCompile(`^[^\p{L}\p{N}]+`)

// StripLeadingSymbols removes leading emoji and symbols ("🧠 What is it?" becomes "What is it?").
func StripLeadingSymbols(s string) string {
	return domain.JSTrim(leadingSymbols.ReplaceAllString(s, ""))
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// HeadingKey is an emoji-free slug of a heading ("🔧 Common Options (Flags)" becomes "common-options-flags").
func HeadingKey(heading string) string {
	k := strings.ToLower(StripLeadingSymbols(heading))
	k = nonAlnum.ReplaceAllString(k, "-")
	return strings.Trim(k, "-")
}

// DecodeNotionPath percent-decodes a Notion link path and normalises it to NFC with forward slashes.
func DecodeNotionPath(p string) string {
	out := p
	if d, ok := percentDecode(p); ok {
		out = d
	}
	return strings.ReplaceAll(norm.NFC.String(out), `\`, "/")
}

// percentDecode is decodeURIComponent: it fails (and the text is kept raw) on a malformed escape.
func percentDecode(s string) (string, bool) {
	if !strings.Contains(s, "%") {
		return s, true
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			b.WriteByte(s[i])
			continue
		}
		if i+2 >= len(s) {
			return "", false
		}
		h, ok1 := unhex(s[i+1])
		l, ok2 := unhex(s[i+2])
		if !ok1 || !ok2 {
			return "", false
		}
		b.WriteByte(h<<4 | l)
		i += 2
	}
	out := b.String()
	if !validUTF8(out) {
		return "", false
	}
	return out, true
}

func unhex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// StripBOM removes a leading UTF-8 byte-order mark (Notion CSVs start with one).
func StripBOM(s string) string { return strings.TrimPrefix(s, "\ufeff") }
