package notion

import (
	"regexp"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/dlclark/regexp2"

	"github.com/reeper316099/Cipherkeep/go/internal/domain"
)

// Section is one `## ` section of a page.
type Section struct {
	Title    string // original heading text, emoji included
	Key      string // emoji-stripped slug
	Markdown string
}

// fenceOf reports the opening fence marker when the line starts (after whitespace) with ``` or ~~~.
func fenceOf(line string) (string, bool) {
	t := strings.TrimLeftFunc(line, domain.IsJSSpace)
	if strings.HasPrefix(t, "```") {
		return "```", true
	}
	if strings.HasPrefix(t, "~~~") {
		return "~~~", true
	}
	return "", false
}

// fenceState tracks whether lines are inside a fenced code block, the way every scanner in the TS code does it.
type fenceState struct{ open string }

// step consumes one line. It reports whether the line is a fence line; `inside` is true for lines in a block.
func (f *fenceState) step(line string) (isFence bool) {
	m, ok := fenceOf(line)
	if !ok {
		return false
	}
	if f.open == "" {
		f.open = m
	} else if m == f.open {
		f.open = ""
	}
	return true
}

// MapOutsideFences applies fn to every line outside fenced code blocks.
func MapOutsideFences(md string, fn func(string) string) string {
	var f fenceState
	lines := strings.Split(md, "\n")
	for i, line := range lines {
		if f.step(line) {
			continue
		}
		if f.open == "" {
			lines[i] = fn(line)
		}
	}
	return strings.Join(lines, "\n")
}

var unescapeAllRe = regexp.MustCompile("\\\\([\\\\`*_{}\\[\\]()<>#+\\-.!|~])")

// UnescapeAll removes every backslash escape (for short structured fields such as `ls \[flags\]`).
func UnescapeAll(s string) string { return unescapeAllRe.ReplaceAllString(s, "$1") }

var (
	codeSpanRe      = regexp.MustCompile("`[^`]*`")
	bracketEscapeRe = regexp.MustCompile(`\\([\[\]])`)
)

// unescapeBrackets removes the escaped brackets Notion over-produces, leaving inline code spans alone.
func unescapeBrackets(line string) string {
	var b strings.Builder
	last := 0
	for _, m := range codeSpanRe.FindAllStringIndex(line, -1) {
		b.WriteString(bracketEscapeRe.ReplaceAllString(line[last:m[0]], "$1"))
		b.WriteString(line[m[0]:m[1]])
		last = m[1]
	}
	b.WriteString(bracketEscapeRe.ReplaceAllString(line[last:], "$1"))
	return b.String()
}

var calloutKinds = []struct{ icon, kind string }{
	{"\U0001F4A1", "TIP"},
	{"⚠️", "WARNING"},
	{"⚠", "WARNING"},
	{"❗", "IMPORTANT"},
	{"\U0001F6A8", "CAUTION"},
	{"☠️", "CAUTION"},
	{"\U0001F525", "IMPORTANT"},
	{"\U0001F4CC", "NOTE"},
	{"ℹ️", "NOTE"},
}

var (
	asideRe   = regexp.MustCompile(`(?s)<aside>` + jsSpace + `*(.*?)` + jsSpace + `*</aside>`)
	detailsRe = regexp.MustCompile(`(?s)<details>` + jsSpace + `*<summary>(.*?)</summary>(.*?)</details>`)
)

func trimEnd(s string) string { return strings.TrimRightFunc(s, domain.IsJSSpace) }

// replaceAllFunc is String.replace with a function: fn gets the capture groups and its result is used literally.
func replaceAllFunc(re *regexp.Regexp, s string, fn func(groups []string) string) string {
	var b strings.Builder
	last := 0
	for _, m := range re.FindAllStringSubmatchIndex(s, -1) {
		b.WriteString(s[last:m[0]])
		groups := make([]string, len(m)/2)
		for i := range groups {
			if m[2*i] >= 0 {
				groups[i] = s[m[2*i]:m[2*i+1]]
			}
		}
		b.WriteString(fn(groups))
		last = m[1]
	}
	b.WriteString(s[last:])
	return b.String()
}

func quoteLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = trimEnd("> " + l)
	}
	return out
}

// ConvertHTMLBlocks turns `<aside>` callouts into `> [!KIND]` and `<details>` toggles into a blockquote with a bold summary.
func ConvertHTMLBlocks(md string) string {
	out := replaceAllFunc(asideRe, md, func(g []string) string {
		lines := strings.Split(domain.JSTrim(g[1]), "\n")
		first := lines[0]
		kind := "NOTE"
		for _, c := range calloutKinds {
			if strings.HasPrefix(first, c.icon) {
				lines[0] = domain.JSTrim(first[len(c.icon):])
				kind = c.kind
				break
			}
		}
		return strings.Join(append([]string{"> [!" + kind + "]"}, quoteLines(lines)...), "\n")
	})
	return replaceAllFunc(detailsRe, out, func(g []string) string {
		head := "> **▸ " + domain.JSTrim(g[1]) + "**"
		return strings.Join(append([]string{head}, quoteLines(strings.Split(domain.JSTrim(g[2]), "\n"))...), "\n")
	})
}

func endsWithPipe(s string) bool { return strings.HasSuffix(trimEnd(s), "|") }
func startsWithPipe(s string) bool {
	return strings.HasPrefix(strings.TrimLeftFunc(s, domain.IsJSSpace), "|")
}

// RepairTables joins table rows that Notion broke across lines (a newline inside a cell).
func RepairTables(md string) string {
	var out []string
	inRow := false
	var f fenceState
	for _, line := range strings.Split(md, "\n") {
		isFence := f.step(line)
		if f.open != "" || isFence {
			out = append(out, line)
			inRow = false
			continue
		}
		if inRow {
			out[len(out)-1] += " " + domain.JSTrim(line)
			inRow = !endsWithPipe(line)
			continue
		}
		out = append(out, line)
		inRow = startsWithPipe(line) && !endsWithPipe(line)
	}
	return strings.Join(out, "\n")
}

var (
	trailingBlanks = regexp.MustCompile(`[ \t]+($|[\n\x{2028}\x{2029}])`)
	blankRuns      = regexp.MustCompile(`\n{3,}`)
	crlf           = regexp.MustCompile(`\r\n?`)
)

// NormalizeBody cleans up a Notion Markdown body: callouts, broken tables, over-escaped brackets, blank runs.
func NormalizeBody(md string) string {
	text := crlf.ReplaceAllString(StripBOM(md), "\n")
	converted := RepairTables(ConvertHTMLBlocks(text))
	s := MapOutsideFences(converted, unescapeBrackets)
	s = trailingBlanks.ReplaceAllString(s, "$1")
	s = blankRuns.ReplaceAllString(s, "\n\n")
	return domain.JSTrim(s)
}

// Page is a parsed Notion Markdown page.
type Page struct {
	Title string
	Props map[string]string
	Keys  []string // property names in file order
	Body  string
}

var h1Re = regexp.MustCompile(`^#` + jsSpace + `+(` + jsDot + `*)$`)

// parseProp reads a `Key: value` line the way /^([^\s:#>|`-][^:\n]{0,60}):(?: (.*))?$/ does (the 60 counts UTF-16 units).
func parseProp(line string) (key, val string, ok bool) {
	if line == "" {
		return
	}
	r0, n0 := firstRune(line)
	if domain.IsJSSpace(r0) || strings.ContainsRune(":#>|`-", r0) {
		return
	}
	rest := line[n0:]
	ci := strings.IndexByte(rest, ':')
	if ci < 0 || len(utf16.Encode([]rune(rest[:ci]))) > 60 {
		return
	}
	key = line[:n0+ci]
	after := rest[ci+1:]
	switch {
	case after == "":
	case strings.HasPrefix(after, " "):
		val = after[1:]
		if strings.ContainsAny(val, "\r  ") {
			return "", "", false
		}
	default:
		return "", "", false
	}
	return domain.JSTrim(key), domain.JSTrim(val), true
}

func firstRune(s string) (rune, int) {
	r, n := utf8.DecodeRuneInString(s)
	return r, n
}

// ParsePage parses `# Title`, an optional `Key: value` property block (only when expectProps, for database row pages)
// and the remaining body.
func ParsePage(text string, expectProps bool) Page {
	lines := strings.Split(crlf.ReplaceAllString(StripBOM(text), "\n"), "\n")
	i := 0
	skipBlank := func() {
		for i < len(lines) && domain.JSTrim(lines[i]) == "" {
			i++
		}
	}
	skipBlank()
	title := ""
	if i < len(lines) {
		if m := h1Re.FindStringSubmatch(lines[i]); m != nil {
			title = domain.JSTrim(m[1])
			i++
		}
	}
	skipBlank()
	p := Page{Props: map[string]string{}}
	if expectProps {
		j := i
		type kv struct{ k, v string }
		var found []kv
		for j < len(lines) && domain.JSTrim(lines[j]) != "" {
			k, v, ok := parseProp(lines[j])
			if !ok {
				break
			}
			found = append(found, kv{k, v})
			j++
		}
		clean := j >= len(lines) || domain.JSTrim(lines[j]) == ""
		if len(found) > 0 && clean {
			for _, e := range found {
				if _, dup := p.Props[e.k]; !dup {
					p.Keys = append(p.Keys, e.k)
				}
				p.Props[e.k] = e.v
			}
			i = j
		}
	}
	p.Title = title
	p.Body = NormalizeBody(strings.Join(lines[i:], "\n"))
	return p
}

var (
	h2Re         = regexp.MustCompile(`^##` + jsSpace + `+(` + jsDot + `*)$`)
	leadingRules = regexp.MustCompile(`^` + jsSpace + `*(?:---+` + jsSpace + `*\n)+`)
	trailRules   = regexp.MustCompile(`(?:\n` + jsSpace + `*---+` + jsSpace + `*)+$`)
)

func cleanSection(md string) string {
	md = leadingRules.ReplaceAllString(md, "")
	md = trailRules.ReplaceAllString(md, "")
	return domain.JSTrim(md)
}

// SplitSections splits a body on `## ` headings outside code fences. Text before the first heading is the preamble.
func SplitSections(body string) (preamble string, sections []Section) {
	var pre []string
	var cur *struct {
		title string
		lines []string
	}
	finish := func() {
		sections = append(sections, Section{Title: cur.title, Key: HeadingKey(cur.title), Markdown: cleanSection(strings.Join(cur.lines, "\n"))})
	}
	var f fenceState
	for _, line := range strings.Split(body, "\n") {
		isFence := f.step(line)
		var h []string
		if f.open == "" && !isFence {
			h = h2Re.FindStringSubmatch(line)
		}
		switch {
		case h != nil:
			if cur != nil {
				finish()
			}
			cur = &struct {
				title string
				lines []string
			}{title: domain.JSTrim(h[1])}
		case cur != nil:
			cur.lines = append(cur.lines, line)
		default:
			pre = append(pre, line)
		}
	}
	if cur != nil {
		finish()
	}
	return cleanSection(strings.Join(pre, "\n")), sections
}

// Table is a GFM table; the separator row is removed and Rows[0] is Notion's "header" row.
type Table struct{ Rows [][]string }

func splitRow(line string) []string {
	t := domain.JSTrim(line)
	t = strings.TrimPrefix(t, "|")
	t = strings.TrimSuffix(t, "|")
	var cells []string
	var cur strings.Builder
	inCode := false
	for i := 0; i < len(t); i++ {
		c := t[i]
		switch {
		case c == '\\' && i+1 < len(t) && t[i+1] == '|':
			cur.WriteByte('|')
			i++
		case c == '`':
			inCode = !inCode
			cur.WriteByte(c)
		case c == '|' && !inCode:
			cells = append(cells, domain.JSTrim(cur.String()))
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	return append(cells, domain.JSTrim(cur.String()))
}

var separatorCell = regexp.MustCompile(`^:?-{2,}:?$`)

// ParseTables finds GFM tables outside code fences.
func ParseTables(md string) []Table {
	var tables []Table
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			var rows [][]string
			for _, l := range cur {
				r := splitRow(l)
				sep := true
				for _, c := range r {
					if !separatorCell.MatchString(c) {
						sep = false
						break
					}
				}
				if !sep {
					rows = append(rows, r)
				}
			}
			if len(rows) > 0 {
				tables = append(tables, Table{Rows: rows})
			}
		}
		cur = nil
	}
	var f fenceState
	for _, line := range strings.Split(md, "\n") {
		if f.stepFlush(line, flush) {
			continue
		}
		if f.open == "" && startsWithPipe(line) {
			cur = append(cur, line)
		} else {
			flush()
		}
	}
	flush()
	return tables
}

// stepFlush is step, calling flush first on a fence line (tables end at a fence).
func (f *fenceState) stepFlush(line string, flush func()) bool {
	if _, ok := fenceOf(line); !ok {
		return false
	}
	flush()
	f.step(line)
	return true
}

var (
	brRe     = regexp.MustCompile(`(?i)<br` + jsSpace + `*/?>`)
	inlineCd = regexp.MustCompile("`([^`]*)`")
	boldRe   = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	spaceRun = regexp.MustCompile(jsSpace + `+`)
	// The two below need a lookahead, which RE2 does not have.
	starEm   = regexp2.MustCompile(`(^|[^*A-Za-z0-9_])\*([^*]+)\*(?!\*)`, regexp2.None)
	underEm  = regexp2.MustCompile(`(^|[^A-Za-z0-9_])_([^_]+)_(?![A-Za-z0-9_])`, regexp2.None)
	fencedRe = regexp2.MustCompile("(?:^|\\n)(```|~~~)([^\\n]*)\\n([\\s\\S]*?)\\n?\\1(?=\\n|\\z)", regexp2.None)
)

func re2Replace(re *regexp2.Regexp, s, repl string) string {
	out, err := re.Replace(s, repl, -1, -1)
	if err != nil {
		return s
	}
	return out
}

// CellText is the plain text of a table cell: no backticks, emphasis markers or <br>.
func CellText(s string) string {
	s = brRe.ReplaceAllString(s, " ")
	s = inlineCd.ReplaceAllString(s, "$1")
	s = boldRe.ReplaceAllString(s, "$1")
	s = re2Replace(starEm, s, "$1$2")
	s = re2Replace(underEm, s, "$1$2")
	s = UnescapeAll(s)
	return domain.JSTrim(spaceRun.ReplaceAllString(s, " "))
}

// Block is a fenced code block.
type Block struct{ Lang, Code string }

// FencedBlocks returns the body of every fenced code block, with its language.
func FencedBlocks(md string) []Block {
	var out []Block
	m, err := fencedRe.FindStringMatch(md)
	for err == nil && m != nil {
		g := m.Groups()
		out = append(out, Block{Lang: domain.JSTrim(g[2].String()), Code: g[3].String()})
		m, err = fencedRe.FindNextMatch(m)
	}
	return out
}
