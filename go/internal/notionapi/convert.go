package notionapi

import (
	"context"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/reeper316099/Cipherkeep/go/internal/domain"
	"github.com/reeper316099/Cipherkeep/go/internal/importer"
)

func obj(v any) Obj {
	m, _ := v.(map[string]any)
	return m
}

func arr(v any) []any {
	l, _ := v.([]any)
	return l
}

func s(v any) string {
	str, _ := v.(string)
	return str
}

// plain joins the plain text of a rich-text list.
func plain(rt any) string {
	var b strings.Builder
	for _, t := range arr(rt) {
		b.WriteString(s(obj(t)["plain_text"]))
	}
	return b.String()
}

func hex32(id string) string { return strings.ToLower(strings.ReplaceAll(id, "-", "")) }

var unsafeName = regexp.MustCompile("[/\\\\:*?\"<>|\x00-\x1f]")

func safeName(name string) string {
	n := domain.JSTrim(unsafeName.ReplaceAllString(name, "-"))
	r := []rune(n)
	// slice(0, 120) counts UTF-16 units; the names that matter are short, so runes are close enough for the cut
	if len(r) > 120 {
		n = string(r[:120])
	}
	if n == "" {
		return "Untitled"
	}
	return n
}

// RichTextToMarkdown renders Notion rich text: code, bold, italic, strikethrough and links, keeping edge spaces outside
// the markers.
func RichTextToMarkdown(rt any) string {
	var b strings.Builder
	for _, item := range arr(rt) {
		t := obj(item)
		raw := s(t["plain_text"])
		if domain.JSTrim(raw) == "" {
			b.WriteString(raw)
			continue
		}
		core := strings.TrimFunc(raw, domain.IsJSSpace)
		lead := raw[:strings.Index(raw, core)]
		trail := raw[len(lead)+len(core):]
		a := obj(t["annotations"])
		if a["code"] == true {
			core = "`" + strings.ReplaceAll(core, "`", "'") + "`"
		}
		if a["bold"] == true {
			core = "**" + core + "**"
		}
		if a["italic"] == true {
			core = "*" + core + "*"
		}
		if a["strikethrough"] == true {
			core = "~~" + core + "~~"
		}
		if href := s(t["href"]); href != "" {
			core = "[" + core + "](" + href + ")"
		}
		b.WriteString(lead + core + trail)
	}
	return b.String()
}

// ConvertStats counts what could not be converted faithfully.
type ConvertStats struct {
	Images      int `json:"images"`
	Unsupported int `json:"unsupported"`
}

// Children returns the child blocks of a block.
type Children func(ctx context.Context, blockID string) ([]Obj, error)

var backtickRun = regexp.MustCompile("`+")

func indent(lines []string, by string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		if l != "" {
			out[i] = by + l
		} else {
			out[i] = l
		}
	}
	return out
}

func renderBlocks(ctx context.Context, blocks []Obj, getChildren Children, stats *ConvertStats, depth int) ([]string, error) {
	var out []string
	n := 0 // numbered-list counter
	kids := func(b Obj, d int) ([]string, error) {
		if b["has_children"] != true || d >= 8 {
			return nil, nil
		}
		c, err := getChildren(ctx, s(b["id"]))
		if err != nil {
			return nil, err
		}
		return renderBlocks(ctx, c, getChildren, stats, d)
	}
	for _, b := range blocks {
		typ := s(b["type"])
		body := obj(b[typ])
		text := RichTextToMarkdown(body["rich_text"])
		if typ != "numbered_list_item" {
			n = 0
		}
		switch typ {
		case "paragraph":
			k, err := kids(b, depth+1)
			if err != nil {
				return nil, err
			}
			out = append(out, text, "")
			out = append(out, k...)
		case "heading_1", "heading_2", "heading_3", "heading_4":
			level, _ := strconv.Atoi(typ[len(typ)-1:])
			if level > 6 {
				level = 6
			}
			k, err := kids(b, depth+1)
			if err != nil {
				return nil, err
			}
			out = append(out, strings.Repeat("#", level)+" "+text, "")
			out = append(out, k...) // toggleable headings
		case "bulleted_list_item", "toggle", "to_do":
			box := ""
			if typ == "to_do" {
				box = "[ ] "
				if body["checked"] == true {
					box = "[x] "
				}
			}
			label := text
			if typ == "toggle" {
				label = "**" + text + "**"
			}
			k, err := kids(b, depth+1)
			if err != nil {
				return nil, err
			}
			out = append(out, "- "+box+label)
			out = append(out, indent(k, "  ")...)
		case "numbered_list_item":
			n++
			k, err := kids(b, depth+1)
			if err != nil {
				return nil, err
			}
			out = append(out, strconv.Itoa(n)+". "+text)
			out = append(out, indent(k, "   ")...)
		case "quote":
			k, err := kids(b, depth+1)
			if err != nil {
				return nil, err
			}
			out = append(out, indent(append([]string{text}, k...), "> ")...)
			out = append(out, "")
		case "callout":
			icon := s(obj(body["icon"])["emoji"])
			if icon != "" {
				icon += " "
			}
			out = append(out, "> "+icon+text, "")
		case "code":
			code := plain(body["rich_text"])
			longest := 3
			for _, m := range backtickRun.FindAllString(code, -1) {
				longest = max(longest, len(m)+1)
			}
			fence := strings.Repeat("`", longest)
			lang := s(body["language"])
			if lang == "plain text" {
				lang = ""
			}
			out = append(out, fence+lang, code, fence, "")
		case "equation":
			out = append(out, "$$"+s(body["expression"])+"$$", "")
		case "divider":
			out = append(out, "---", "")
		case "child_page":
			title := s(obj(b["child_page"])["title"])
			if _, ok := obj(b["child_page"])["title"]; !ok {
				title = "Untitled"
			}
			out = append(out, "[["+title+"]]", "")
		case "link_to_page":
			// the target page is imported on its own
		case "bookmark", "embed", "link_preview":
			if u := s(obj(b[typ])["url"]); u != "" {
				out = append(out, "["+u+"]("+u+")", "")
			}
		case "image", "video", "pdf", "file", "audio":
			f := obj(b[typ])
			cap := plain(f["caption"])
			if u := s(obj(f["external"])["url"]); s(f["type"]) == "external" && u != "" {
				if typ == "image" {
					out = append(out, "!["+cap+"]("+u+")", "")
				} else {
					label := cap
					if label == "" {
						label = u
					}
					out = append(out, "["+label+"]("+u+")", "")
				}
			} else {
				// Notion-hosted files are short-lived signed links on other hosts; they are not downloaded.
				stats.Images++
				note := ""
				if cap != "" {
					note = ": " + cap
				}
				out = append(out, "*("+typ+" not downloaded"+note+")*", "")
			}
		case "table":
			rows, err := getChildren(ctx, s(b["id"]))
			if err != nil {
				return nil, err
			}
			var cells [][]string
			for _, r := range rows {
				var row []string
				for _, c := range arr(obj(r["table_row"])["cells"]) {
					cell := RichTextToMarkdown(c)
					cell = strings.ReplaceAll(strings.ReplaceAll(cell, "|", `\|`), "\n", " ")
					row = append(row, cell)
				}
				cells = append(cells, row)
			}
			if len(cells) > 0 {
				width := 0
				for _, r := range cells {
					width = max(width, len(r))
				}
				pad := func(r []string) string {
					full := append(append([]string{}, r...), make([]string, width-len(r))...)
					return "| " + strings.Join(full, " | ") + " |"
				}
				seps := make([]string, width)
				for i := range seps {
					seps[i] = "---"
				}
				out = append(out, pad(cells[0]), "| "+strings.Join(seps, " | ")+" |")
				for _, r := range cells[1:] {
					out = append(out, pad(r))
				}
				out = append(out, "")
			}
		case "column_list", "column", "synced_block":
			k, err := kids(b, depth+1)
			if err != nil {
				return nil, err
			}
			out = append(out, k...)
		case "template", "breadcrumb", "table_of_contents", "table_row":
		default:
			stats.Unsupported++
			if text != "" {
				out = append(out, text, "")
			}
		}
	}
	return out, nil
}

var blankRuns = regexp.MustCompile(`\n{3,}`)

// BlocksToMarkdown converts a page's blocks to Markdown.
func BlocksToMarkdown(ctx context.Context, blocks []Obj, getChildren Children, stats *ConvertStats) (string, error) {
	if stats == nil {
		stats = &ConvertStats{}
	}
	lines, err := renderBlocks(ctx, blocks, getChildren, stats, 0)
	if err != nil {
		return "", err
	}
	return domain.JSTrim(blankRuns.ReplaceAllString(strings.Join(lines, "\n"), "\n\n")), nil
}

var months = []string{"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"}
var isoDate = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})`)

func niceDate(iso string) string {
	m := isoDate.FindStringSubmatch(iso)
	if m == nil {
		return iso
	}
	mo, _ := strconv.Atoi(m[2])
	d, _ := strconv.Atoi(m[3])
	if mo < 1 || mo > 12 {
		return "undefined " + strconv.Itoa(d) + ", " + m[1]
	}
	return months[mo-1] + " " + strconv.Itoa(d) + ", " + m[1]
}

// PropertyToCell writes a Notion property value the way a Notion CSV export would. titleOf finds the title of a page by
// id, for relations; rollups, buttons and other computed values are not imported.
func PropertyToCell(p Obj, titleOf func(pageID string) (string, bool)) string {
	typ := s(p["type"])
	v := p[typ]
	switch typ {
	case "title", "rich_text":
		return plain(v)
	case "number":
		if v == nil {
			return ""
		}
		return importer.JSString(v)
	case "select", "status":
		return s(obj(v)["name"])
	case "multi_select":
		var names []string
		for _, o := range arr(v) {
			names = append(names, s(obj(o)["name"]))
		}
		return strings.Join(names, ", ")
	case "date":
		d := obj(v)
		start := s(d["start"])
		if start == "" {
			return ""
		}
		if end := s(d["end"]); end != "" {
			return niceDate(start) + " → " + niceDate(end)
		}
		return niceDate(start)
	case "checkbox":
		if v == true {
			return "Yes"
		}
		return "No"
	case "url", "email", "phone_number":
		return s(v)
	case "people", "files":
		var names []string
		for _, o := range arr(v) {
			if n := s(obj(o)["name"]); n != "" {
				names = append(names, n)
			}
		}
		return strings.Join(names, ", ")
	case "relation":
		var parts []string
		for _, r := range arr(v) {
			id := s(obj(r)["id"])
			if t, ok := titleOf(id); ok && t != "" {
				parts = append(parts, t+" (https://www.notion.so/"+hex32(id)+")")
			}
		}
		return strings.Join(parts, ", ")
	case "formula":
		f := obj(v)
		if t := s(f["type"]); t != "" && f[t] != nil {
			return importer.JSString(f[t])
		}
		return ""
	case "created_time", "last_edited_time":
		if v == nil {
			return ""
		}
		return importer.JSString(v)
	case "unique_id":
		u := obj(v)
		if n, ok := u["number"].(float64); ok && !math.IsNaN(n) {
			prefix := s(u["prefix"])
			if prefix != "" {
				prefix += "-"
			}
			return prefix + importer.JSString(n)
		}
		return ""
	}
	return ""
}

func csvEscape(v string) string {
	if strings.ContainsAny(v, "\",\n\r") {
		return `"` + strings.ReplaceAll(v, `"`, `""`) + `"`
	}
	return v
}
