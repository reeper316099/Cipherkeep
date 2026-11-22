package importer

import (
	"encoding/json"
	"fmt"
	"math"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/reeper316099/Cipherkeep/go/internal/apperr"
	"github.com/reeper316099/Cipherkeep/go/internal/csvx"
	"github.com/reeper316099/Cipherkeep/go/internal/domain"
	"github.com/reeper316099/Cipherkeep/go/internal/notion"
	"github.com/reeper316099/Cipherkeep/go/internal/repo"
)

// FieldKind is how a CSV cell maps onto a schema field.
type FieldKind string

const (
	KindString  FieldKind = "string"
	KindNumber  FieldKind = "number"
	KindBoolean FieldKind = "boolean"
	KindStrings FieldKind = "strings"
	KindJSON    FieldKind = "json"
)

// KindOf maps a schema field to the way CSV cells for it are read and written.
func KindOf(f domain.Field) FieldKind {
	switch f.Kind {
	case domain.KBool:
		return KindBoolean
	case domain.KInt, domain.KNumber, domain.KMastery:
		return KindNumber
	case domain.KList:
		if f.Item != nil && f.Item.Kind == domain.KString {
			return KindStrings
		}
		return KindJSON
	case domain.KObject, domain.KRecord:
		return KindJSON
	}
	return KindString
}

var guarded = regexp.MustCompile(`^'[=+\-@]`)

// UnguardCell removes the `'` the exporter puts before cells that could be read as spreadsheet formulas.
func UnguardCell(s string) string {
	if guarded.MatchString(s) {
		return s[1:]
	}
	return s
}

var (
	jsNumberRe = regexp.MustCompile(`^[+-]?(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?$`)
	truthy     = regexp.MustCompile(`(?i)^(?:true|yes|1|x)$`)
	collapse   = regexp.MustCompile(`[\s_-]+`)
)

// jsNumber is JavaScript's Number(string) for a trimmed, non-empty string (NaN when it is not a number).
func jsNumber(s string) float64 {
	switch {
	case jsNumberRe.MatchString(s):
		f, _ := strconv.ParseFloat(s, 64)
		return f
	case s == "Infinity" || s == "+Infinity":
		return math.Inf(1)
	case s == "-Infinity":
		return math.Inf(-1)
	case len(s) > 2 && s[0] == '0':
		base := map[byte]int{'x': 16, 'X': 16, 'o': 8, 'O': 8, 'b': 2, 'B': 2}[s[1]]
		if base != 0 {
			if n, err := strconv.ParseUint(s[2:], base, 64); err == nil {
				return float64(n)
			}
		}
	}
	return math.NaN()
}

// PlanCSV plans an import of CSV files into one collection (or hands a Notion database CSV to the Notion planner).
func PlanCSV(vfs *VirtualFS, opts CSVOptions) (*Plan, error) {
	var csvs []string
	for _, p := range vfs.Paths() {
		if strings.HasSuffix(strings.ToLower(p), ".csv") {
			csvs = append(csvs, p)
		}
	}
	if len(csvs) == 0 {
		return nil, apperr.New(400, "BAD_SOURCE", "No CSV found")
	}
	first := csvx.Parse(vfs.Text(csvs[0]), csvx.Options{MaxRows: 1})
	headers := trimmedHeaders(first)
	if opts.Collection == "" && DetectCollection(headers) != "" {
		return PlanNotion(vfs, NotionOptions{SourceName: opts.SourceName, Lookup: opts.Lookup, Mapping: opts.Mapping}), nil
	}
	schema, ok := domain.Schemas[opts.Collection]
	if opts.Collection == "" || !ok {
		e := apperr.New(400, "NEEDS_COLLECTION", "Choose which collection this CSV belongs to")
		e.Details = map[string]any{"headers": headers}
		return nil, e
	}
	keyByLower := map[string]domain.Field{}
	for _, f := range schema.Fields {
		keyByLower[strings.ToLower(f.Name)] = f
	}
	plan := newPlan("csv", opts.SourceName)
	for _, csv := range csvs {
		t := csvx.Parse(vfs.Text(csv), csvx.Options{SkipEmpty: true})
		for idx, r := range t.Rows {
			input := map[string]any{}
			extra := map[string]any{}
			warns := []string{}
			for i, raw0 := range r {
				if i >= len(t.Headers) {
					break
				}
				h := domain.JSTrim(t.Headers[i])
				raw := UnguardCell(domain.JSTrim(raw0))
				if raw == "" {
					continue
				}
				f, found := keyByLower[collapse.ReplaceAllString(strings.ToLower(h), "")]
				if !found {
					f, found = keyByLower[strings.ToLower(h)]
				}
				if !found {
					extra[h] = raw
					continue
				}
				if l := strings.ToLower(f.Name); l == "id" || l == "createdat" || l == "updatedat" {
					continue
				}
				v, err := readCell(KindOf(f), raw)
				if err != nil {
					warns = append(warns, "could not read column \""+h+"\"; kept raw")
					extra[h] = raw
					continue
				}
				input[f.Name] = v
			}
			title := domain.JSTrim(jsStr(firstNonNil(input["title"], input["name"])))
			if title == "" {
				plan.Warnings = append(plan.Warnings, PlanWarning{Level: "warning", Code: "ROW_WITHOUT_TITLE",
					Message: fmt.Sprintf("Row %d in %s has no title; skipped", idx+2, csv), Source: csv})
				continue
			}
			if opts.Collection == "commands" && isFalsy(input["name"]) {
				input["name"] = title
			}
			if _, ok := input["title"]; !ok {
				input["title"] = title
			}
			if len(extra) > 0 {
				input["extras"] = map[string]any{"csvColumns": extra}
			}
			ref := "csv:" + opts.Collection + ":" + domain.NormalizeTitle(title)
			if s, ok := input["importRef"].(string); ok && s != "" {
				ref = s
			}
			plan.Items = append(plan.Items, PlanItem{Collection: opts.Collection, Ref: ref, Title: title, Input: input,
				Relations: []Relation{}, Source: csv, Warnings: warns})
		}
	}
	return plan, nil
}

// CSVOptions configure PlanCSV.
type CSVOptions struct {
	SourceName string
	Collection string
	Lookup     Lookup
	Mapping    map[string]string
}

func readCell(kind FieldKind, raw string) (any, error) {
	switch kind {
	case KindNumber:
		if n := jsNumber(raw); !math.IsNaN(n) && !math.IsInf(n, 0) {
			return n, nil
		}
		// JavaScript keeps NaN, which the schema then rejects and which JSON writes as null. Go cannot encode NaN, so the
		// value is left unset (null in the plan); the row still imports, without that field.
		return nil, nil
	case KindBoolean:
		return truthy.MatchString(raw), nil
	case KindStrings:
		if strings.HasPrefix(raw, "[") {
			var v []any
			if err := json.Unmarshal([]byte(raw), &v); err != nil {
				return nil, err
			}
			return v, nil
		}
		return toAny(notion.SplitList(strings.ReplaceAll(raw, "|", ";"))), nil
	case KindJSON:
		var v any
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			return nil, err
		}
		return v, nil
	}
	return raw, nil
}

func firstNonNil(vs ...any) any {
	for _, v := range vs {
		if v != nil {
			return v
		}
	}
	return ""
}

func isFalsy(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case float64:
		return x == 0 || math.IsNaN(x)
	case bool:
		return !x
	}
	return false
}

// jsStr is JavaScript's String(v) for the value types frontmatter and JSON hold.
func jsStr(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1e21 {
			return strconv.FormatFloat(x, 'f', -1, 64)
		}
		return strconv.FormatFloat(x, 'g', -1, 64)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			if e != nil {
				parts[i] = jsStr(e)
			}
		}
		return strings.Join(parts, ",")
	case map[string]any:
		return "[object Object]"
	}
	return fmt.Sprint(v)
}

// str is data[k] ?? "" as a string.
func str(m map[string]any, k string) string {
	if v, ok := m[k]; ok && v != nil {
		return jsStr(v)
	}
	return ""
}

var h1Re = regexp.MustCompile(`(?m)(?:^|[\r\x{2028}\x{2029}])#` + jsSpace + `+(` + `[^\n\r\x{2028}\x{2029}]` + `+)(?:\r|\x{2028}|\x{2029}|$)`)

// PlanMarkdownFolder plans an import of a folder of Markdown files. Files exported by Cipherkeep carry ck_collection
// frontmatter and become commands or concepts again; anything else becomes a note.
func PlanMarkdownFolder(vfs *VirtualFS, sourceName string) *Plan {
	plan := newPlan("markdown", sourceName)
	for _, p := range vfs.Paths() {
		if !strings.HasSuffix(strings.ToLower(p), ".md") {
			continue
		}
		data, body := repo.ParseFrontmatter(vfs.Text(p))
		stem := strings.TrimSuffix(path.Base(p), ".md")
		h1 := ""
		if m := h1Re.FindStringSubmatch(body); m != nil {
			h1 = domain.JSTrim(m[1])
		}
		title := stem
		switch {
		case data["title"] != nil:
			title = jsStr(data["title"])
		case h1 != "":
			title = h1
		}
		dir := path.Dir(p)
		var tags []string
		switch t := data["tags"].(type) {
		case []any:
			for _, x := range t {
				tags = append(tags, jsStr(x))
			}
		case string:
			tags = notion.SplitList(t)
		}
		kind := str(data, "ck_collection")
		var item PlanItem
		ref := "md:" + domain.NormalizeTitle(p)
		switch kind {
		case "commands":
			props := map[string]string{}
			for _, k := range []string{"syntax", "explanation", "example", "category"} {
				props[strings.ToUpper(k[:1])+k[1:]] = str(data, k)
			}
			r := notion.ParseCommandPage(notion.CommandSource{Name: title, Category: notion.S(str(data, "category")), Props: props, Body: body})
			input := r.Input
			input["tags"] = toAny(tags)
			if risk, ok := data["risk"].(string); ok {
				input["risk"] = risk
			}
			item = PlanItem{Collection: "commands", Title: title, Input: input, Warnings: r.Warnings}
		case "concepts":
			r := notion.ParseConceptPage(notion.ConceptSource{Name: title, Category: notion.S(str(data, "category")), Difficulty: str(data, "difficulty"), Tags: tags, Body: body})
			item = PlanItem{Collection: "concepts", Title: title, Input: r.Input, Warnings: r.Warnings}
		default:
			folder := dir
			if dir == "." {
				folder = ""
			}
			input := map[string]any{"title": title, "folder": folder, "markdown": domain.JSTrim(body), "tags": toAny(tags),
				"category": str(data, "category"), "kind": "general"}
			rest := map[string]any{}
			for k, v := range data {
				switch k {
				case "title", "tags", "category", "ck_collection", "ck_id", "id":
				default:
					rest[k] = v
				}
			}
			if len(rest) > 0 {
				input["extras"] = map[string]any{"frontmatter": rest}
			}
			item = PlanItem{Collection: "notes", Title: title, Input: input, Warnings: []string{}}
		}
		item.Ref, item.Source, item.Relations = ref, p, []Relation{}
		plan.Items = append(plan.Items, item)
	}
	if len(plan.Items) == 0 {
		plan.Warnings = append(plan.Warnings, PlanWarning{Level: "warning", Code: "EMPTY", Message: "No Markdown files found"})
	}
	return plan
}

// PlanJSONBackup plans a restore from a Cipherkeep JSON export (format 1). Ids are kept so references stay valid.
func PlanJSONBackup(vfs *VirtualFS, sourceName string) (*Plan, error) {
	file := ""
	for _, p := range vfs.Paths() {
		if strings.HasSuffix(strings.ToLower(p), ".json") {
			file = p
			break
		}
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(vfs.Text(file)), &doc); err != nil {
		var any1 any
		if json.Unmarshal([]byte(vfs.Text(file)), &any1) != nil {
			return nil, apperr.New(400, "BAD_SOURCE", "Not valid JSON")
		}
		return nil, apperr.New(400, "BAD_SOURCE", "Not a Cipherkeep JSON backup")
	}
	colls, hasColls := doc["collections"].(map[string]any)
	if f, ok := doc["format"].(float64); !ok || f != 1 || (!hasColls && doc["collections"] == nil) {
		return nil, apperr.New(400, "BAD_SOURCE", "Not a Cipherkeep JSON backup")
	}
	plan := newPlan("json-backup", sourceName)
	for _, c := range domain.Collections {
		list, _ := colls[c].([]any)
		for _, raw := range list {
			e, _ := raw.(map[string]any)
			ent, err := domain.Schemas[c].Parse(e, domain.ModeFull)
			if err != nil {
				label := jsStr(firstNonNil(e["title"], e["id"]))
				plan.Warnings = append(plan.Warnings, PlanWarning{Level: "warning", Code: "INVALID_ENTITY", Message: fmt.Sprintf("Skipped invalid %s entry \"%s\"", c, label)})
				continue
			}
			ref := "json:" + jsStr(ent["id"])
			if v, ok := ent["importRef"]; ok && v != nil {
				ref = jsStr(v)
			}
			plan.Items = append(plan.Items, PlanItem{Collection: c, Ref: ref, Title: jsStr(ent["title"]), Input: ent,
				Relations: []Relation{}, Source: file, Warnings: []string{}, FixedID: jsStr(ent["id"])})
		}
	}
	return plan, nil
}

// JSString is JavaScript's String(v) for the value types entities hold.
func JSString(v any) string { return jsStr(v) }
