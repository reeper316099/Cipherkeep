package notion

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/reeper316099/Cipherkeep/go/internal/domain"
)

// S returns a pointer to s, for the optional string fields of the page sources.
func S(s string) *string { return &s }

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// strs converts a string slice to the []any form entities use (never nil, so it writes [] and not null).
func strs(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

// ParsedCommand is a command page mapped to Command fields.
type ParsedCommand struct {
	Input    domain.Entity
	Warnings []string
}

// CommandSource is a Notion command page: its name, CSV row and page properties (merged, CSV wins) and body.
type CommandSource struct {
	Name     string
	Category *string
	Props    map[string]string
	Body     string
}

// Example is a command example with its description.
type Example struct{ Command, Description string }

var headerWords = regexp.MustCompile(`(?i)^(options?|flags?|command|commands|meaning|result|results|description|example|examples|key|action)$`)

// dataRows drops a first row that looks like a header: Notion exports header-less tables with data in the header slot.
func dataRows(t Table) [][]string {
	if len(t.Rows) == 0 {
		return nil
	}
	first := t.Rows[0]
	looks := len(first) > 0
	for _, c := range first {
		if !headerWords.MatchString(CellText(c)) {
			looks = false
			break
		}
	}
	if looks {
		return t.Rows[1:]
	}
	return t.Rows
}

var quoteTail = regexp.MustCompile(`"` + jsSpace + `*$`)

// SplitExampleLine splits `cmd = "description"` on the first `= "`; plain lines become command-only. ok is false for
// blank lines, comments and lines with no command.
func SplitExampleLine(line string) (ex Example, ok bool) {
	raw := domain.JSTrim(line)
	if raw == "" || strings.HasPrefix(raw, "#") {
		return Example{}, false
	}
	i := strings.Index(raw, `= "`)
	if i < 0 {
		return Example{Command: raw}, true
	}
	command := domain.JSTrim(raw[:i])
	desc := domain.JSTrim(quoteTail.ReplaceAllString(raw[i+3:], ""))
	if command == "" {
		return Example{}, false
	}
	return Example{Command: command, Description: desc}, true
}

func cell(r []string, i int) string {
	if i < len(r) {
		return r[i]
	}
	return ""
}

// ParseExamples reads the examples out of an "Examples" section: fenced blocks (one per line) and tables.
func ParseExamples(sec *Section) []Example {
	var out []Example
	if sec == nil {
		return out
	}
	for _, b := range FencedBlocks(sec.Markdown) {
		for _, line := range strings.Split(b.Code, "\n") {
			if ex, ok := SplitExampleLine(line); ok {
				out = append(out, ex)
			}
		}
	}
	for _, t := range ParseTables(sec.Markdown) {
		for _, r := range dataRows(t) {
			if c := CellText(cell(r, 0)); c != "" {
				rest := ""
				if len(r) > 1 {
					rest = strings.Join(r[1:], " ")
				}
				out = append(out, Example{Command: c, Description: CellText(rest)})
			}
		}
	}
	return out
}

// PlaceholderRe matches `<x>`, `{x}` and `[x]` placeholder tokens.
var PlaceholderRe = regexp.MustCompile(`<([A-Za-z][\w -]*)>|\{([A-Za-z_][\w-]*)\}|\[([A-Za-z][\w -]*)\]`)

func placeholderName(g []string) string {
	for _, x := range g[1:] {
		if x != "" {
			return domain.JSTrim(x)
		}
	}
	return ""
}

// DetectPlaceholders lists the distinct `<target>`, `{ip}`, `[file]` tokens in the texts, in first-seen order.
func DetectPlaceholders(texts ...string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, t := range texts {
		for _, m := range PlaceholderRe.FindAllStringSubmatch(t, -1) {
			n := placeholderName(m)
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	return out
}

// FillPlaceholders replaces placeholder tokens that have a non-empty value and leaves the rest untouched.
func FillPlaceholders(text string, values map[string]string) string {
	return replaceAllFunc(PlaceholderRe, text, func(g []string) string {
		if v := values[placeholderName(g)]; v != "" {
			return v
		}
		return g[0]
	})
}

var destructive = setOf("rm", "dd", "mkfs", "shred", "fdisk", "wipefs", "userdel", "format", "del")
var caution = setOf("mv", "chmod", "chown", "kill", "killall", "pkill", "sudo", "su", "iptables", "ufw", "crontab",
	"passwd", "usermod", "truncate", "reboot", "shutdown", "systemctl")

func setOf(v ...string) map[string]bool {
	m := make(map[string]bool, len(v))
	for _, s := range v {
		m[s] = true
	}
	return m
}

var destructiveExample = regexp.MustCompile(`\brm` + jsSpace + `+-\w*[rf]|\bdd` + jsSpace + `+[^|]*of=|\bmkfs\b|:\(\)` + jsSpace + `*\{`)

// ClassifyRisk rates a command safe, caution or destructive from its name and examples.
func ClassifyRisk(name string, examples []string) string {
	n := domain.JSTrim(name)
	if i := strings.IndexFunc(n, domain.IsJSSpace); i >= 0 {
		n = n[:i]
	}
	n = strings.ToLower(n)
	if destructive[n] {
		return "destructive"
	}
	for _, e := range examples {
		if destructiveExample.MatchString(e) {
			return "destructive"
		}
	}
	if caution[n] {
		return "caution"
	}
	return "safe"
}

var (
	argLine    = regexp.MustCompile(`^[-*+]` + jsSpace + `+(` + jsDot + `*?)` + jsSpace + `*(?:\x{2192}|->|\x{2014}|\x{2013}|:)` + jsSpace + `*(` + jsDot + `*)$`)
	hintLine   = regexp.MustCompile(`^\(` + jsDot + `*\)$`)
	bulletLine = regexp.MustCompile(`^[-*+]` + jsSpace + `+[^\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]`)
	bulletHead = regexp.MustCompile(`^[-*+]` + jsSpace + `+`)
)

func parseArguments(md string, dropped *[]string) []any {
	out := []any{}
	for _, line := range strings.Split(md, "\n") {
		t := domain.JSTrim(line)
		if t == "" {
			continue
		}
		if m := argLine.FindStringSubmatch(t); m != nil {
			out = append(out, map[string]any{"name": strings.ReplaceAll(UnescapeAll(m[1]), "`", ""), "meaning": UnescapeAll(m[2])})
		} else if hintLine.MatchString(t) {
			*dropped = append(*dropped, t) // template hint like "(Inputs the command takes.)"
		} else if bulletLine.MatchString(t) {
			out = append(out, map[string]any{"name": UnescapeAll(bulletHead.ReplaceAllString(t, "")), "meaning": ""})
		}
	}
	return out
}

var fenceStrip = regexp.MustCompile("^```\\w*\\n?|\\n?```$")

func findSection(sections []Section, keys ...string) int {
	for i, s := range sections {
		for _, k := range keys {
			if s.Key == k {
				return i
			}
		}
	}
	return -1
}

// ParseCommandPage maps a Notion command page (plus its CSV and property values) to the structured Command fields.
func ParseCommandPage(src CommandSource) ParsedCommand {
	warnings := []string{}
	preamble, sections := SplitSections(src.Body)
	used := map[int]bool{}
	take := func(keys ...string) *Section {
		i := findSection(sections, keys...)
		if i < 0 {
			return nil
		}
		used[i] = true
		return &sections[i]
	}
	what := take("what-it-does")
	syntaxSec := take("basic-syntax")
	argSec := take("arguments")
	optSec := take("common-options-flags", "common-options", "options", "flags", "commands")
	exSec := take("examples")
	dropped := []string{}

	options := []any{}
	if optSec != nil {
		for _, t := range ParseTables(optSec.Markdown) {
			for _, r := range dataRows(t) {
				if flag := CellText(cell(r, 0)); flag != "" {
					rest := ""
					if len(r) > 1 {
						rest = strings.Join(r[1:], " ")
					}
					options = append(options, map[string]any{"flag": flag, "meaning": CellText(rest)})
				}
			}
		}
	}
	examples := ParseExamples(exSec)

	syntaxFromProps := domain.JSTrim(UnescapeAll(src.Props["Syntax"]))
	syntaxFromBody := ""
	if syntaxSec != nil {
		syntaxFromBody = UnescapeAll(domain.JSTrim(fenceStrip.ReplaceAllString(syntaxSec.Markdown, "")))
	}
	syntax := syntaxFromProps
	if syntax == "" {
		syntax = syntaxFromBody
	}

	if primary := domain.JSTrim(UnescapeAll(src.Props["Example"])); primary != "" {
		have := false
		for _, e := range examples {
			if e.Command == primary {
				have = true
				break
			}
		}
		if !have {
			examples = append([]Example{{Command: primary, Description: domain.JSTrim(UnescapeAll(src.Props["Explanation"]))}}, examples...)
		}
	}

	var extra []Section
	for i, s := range sections {
		if !used[i] {
			extra = append(extra, s)
		}
	}
	parts := []string{preamble}
	for _, s := range extra {
		parts = append(parts, "## "+s.Title+"\n\n"+s.Markdown)
	}
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	notes := strings.Join(kept, "\n\n")
	if len(extra) > 0 {
		warnings = append(warnings, fmt.Sprintf("kept %d unrecognised section(s) in notes", len(extra)))
	}
	if (what == nil || what.Markdown == "") && src.Props["Explanation"] == "" {
		warnings = append(warnings, "no description found")
	}

	extras := map[string]any{}
	if optSec != nil && optSec.Key == "commands" {
		extras["optionsHeading"] = StripLeadingSymbols(optSec.Title)
	}
	if syntaxFromProps != "" && syntaxFromBody != "" && syntaxFromProps != syntaxFromBody {
		extras["bodySyntax"] = syntaxFromBody
	}
	args := []any{}
	if argSec != nil {
		args = parseArguments(argSec.Markdown, &dropped)
	}
	if len(dropped) > 0 {
		extras["droppedTemplateHints"] = strs(dropped)
	}

	exOut := make([]any, len(examples))
	cmds := make([]string, len(examples))
	for i, e := range examples {
		exOut[i] = map[string]any{"command": e.Command, "description": e.Description}
		cmds[i] = e.Command
	}
	category := src.Props["Category"]
	if src.Category != nil {
		category = *src.Category
	}
	input := domain.Entity{
		"name":         src.Name,
		"title":        src.Name,
		"category":     category,
		"syntax":       syntax,
		"explanation":  UnescapeAll(src.Props["Explanation"]),
		"whatItDoes":   "",
		"arguments":    args,
		"options":      options,
		"examples":     exOut,
		"placeholders": strs(DetectPlaceholders(append([]string{syntax}, cmds...)...)),
		"risk":         ClassifyRisk(src.Name, cmds),
		"notes":        notes,
	}
	if what != nil {
		input["whatItDoes"] = what.Markdown
	}
	if len(extras) > 0 {
		input["extras"] = extras
	}
	return ParsedCommand{Input: input, Warnings: warnings}
}
