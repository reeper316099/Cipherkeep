package importer

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/reeper316099/Cipherkeep/go/internal/csvx"
	"github.com/reeper316099/Cipherkeep/go/internal/domain"
	"github.com/reeper316099/Cipherkeep/go/internal/notion"
)

// Lookup finds entities already in the library by collection and normalised title.
type Lookup interface {
	Find(collection, normTitle string) (string, bool)
}

// NotionOptions configure planning a Notion export.
type NotionOptions struct {
	SourceName string
	Lookup     Lookup
	// Mapping overrides the collection of a database CSV by path; "skip" leaves the database out.
	Mapping map[string]string
}

type dbSignature struct {
	collection string
	required   []string
}

var dbSignatures = []dbSignature{
	{"commands", []string{"Syntax", "Explanation"}},
	{"labs", []string{"Platform", "Commands Used"}},
	{"concepts", []string{"Related Commands"}},
	{"log", []string{"Time Spent", "Key Takeaway"}},
	{"projects", []string{"Type", "Skills Used"}},
}

// DetectCollection recognises a Notion database by its column names ("" when unknown).
func DetectCollection(headers []string) string {
	set := map[string]bool{}
	for _, h := range headers {
		set[domain.JSTrim(h)] = true
	}
	for _, s := range dbSignatures {
		ok := true
		for _, r := range s.required {
			if !set[r] {
				ok = false
				break
			}
		}
		if ok {
			return s.collection
		}
	}
	return ""
}

func hash8(s string) string {
	h := sha1.Sum([]byte(s))
	return hex.EncodeToString(h[:])[:8]
}

var labStatus = map[string]string{"not started": "Not Started", "todo": "Not Started", "in progress": "In Progress", "started": "In Progress",
	"completed": "Completed", "done": "Completed", "complete": "Completed"}
var projectStatus = map[string]string{"not started": "Not started", "todo": "Not started", "in progress": "In progress", "done": "Done", "completed": "Done"}

type pendingRel struct {
	field          string // entity field, or links:<type>
	target         string
	refs           []notion.RelationRef
	titles         []string // free-text mentions
	warnUnresolved bool
}

type built struct {
	item    PlanItem
	pending []pendingRel
}

type row map[string]string

type database struct {
	csv, folder, title string
	collection         string
	rows               []row
	headers            []string
}

var (
	templateWord = regexp.MustCompile(`(?i)template`)
	templateName = regexp.MustCompile(`(?i)` + jsSpace + `*template` + jsSpace + `*`)
	templateBare = regexp.MustCompile(`(?i)\btemplate\b`)
	jsSpace      = `[\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]`
	hubLink      = regexp.MustCompile(`\]\(([^)]+\.csv)\)`)
	linkRe       = regexp.MustCompile(`(!?)\[([^\]]*)\]\(([^)\s]+)\)`)
	schemeRe     = regexp.MustCompile(`(?i)^[a-z][a-z0-9+.-]*:`)
	mdExt        = regexp.MustCompile(`(?i)\.md$`)
	csvExt       = regexp.MustCompile(`(?i)\.csv$`)
	mentionRe    = regexp.MustCompile(`^[\w.+-]{1,40}$`)
	fencedStrip  = regexp.MustCompile("(?s)```.*?```")
	spaces       = regexp.MustCompile(jsSpace + `+`)
	sectionH2    = regexp.MustCompile(`^##` + jsSpace + `+([^\n\r\x{2028}\x{2029}]*)$`)
	hintTrim     = regexp.MustCompile(`^` + jsSpace + `*(?:---+` + jsSpace + `*\n?)+|(?:\n?` + jsSpace + `*---+` + jsSpace + `*)+$`)
	cheatRe      = regexp.MustCompile(`(?i)cheat` + jsSpace + `*-?sheet`)
	writeupRe    = regexp.MustCompile(`(?i)write-?up`)
	guideRe      = regexp.MustCompile(`(?i)guide|setup|configuration|how to`)
)

func trimmedRows(t csvx.Table) []row {
	out := make([]row, 0, len(t.Rows))
	for _, r := range t.Rows {
		m := row{}
		for i, v := range r {
			if i < len(t.Headers) {
				m[domain.JSTrim(t.Headers[i])] = domain.JSTrim(v)
			}
		}
		out = append(out, m)
	}
	return out
}

func trimmedHeaders(t csvx.Table) []string {
	out := make([]string, len(t.Headers))
	for i, h := range t.Headers {
		out[i] = domain.JSTrim(h)
	}
	return out
}

func dirOf(p string) string { return path.Dir(p) }

func opt(props map[string]string, k string) *string {
	if v, ok := props[k]; ok {
		return &v
	}
	return nil
}

// PlanNotion plans the import of a Notion export.
func PlanNotion(vfs *VirtualFS, opts NotionOptions) *Plan {
	plan := newPlan("notion", opts.SourceName)
	warn := func(level, code, message, source string) {
		plan.Warnings = append(plan.Warnings, PlanWarning{Level: level, Code: code, Message: message, Source: source})
	}
	paths := vfs.Paths()
	var mds, csvs, otherFiles []string
	for _, p := range paths {
		l := strings.ToLower(p)
		switch {
		case strings.HasSuffix(l, ".md"):
			mds = append(mds, p)
		case strings.HasSuffix(l, ".csv"):
			csvs = append(csvs, p)
		default:
			otherFiles = append(otherFiles, p)
		}
	}
	used := map[string]bool{} // md paths consumed by a database, template or hub

	// ---- databases (dedupe `_all.csv`) ----
	byKey := map[string]string{}
	for _, p := range csvs {
		n := notion.SplitName(p)
		key := dirOf(p) + "/" + n.Title + "|" + n.ID
		if prev, ok := byKey[key]; !ok || (!n.All && notion.SplitName(prev).All) {
			byKey[key] = p
		}
	}
	chosen := make([]string, 0, len(byKey))
	for _, p := range byKey {
		chosen = append(chosen, p)
	}
	sortUTF16(chosen)
	var dbs []database
	for _, csv := range chosen {
		t := csvx.Parse(vfs.Text(csv), csvx.Options{SkipEmpty: true})
		headers := trimmedHeaders(t)
		title := notion.SplitName(csv).Title
		override, hasOverride := opts.Mapping[csv]
		collection := ""
		switch {
		case hasOverride && override == "skip":
		case hasOverride:
			collection = override
		default:
			collection = DetectCollection(headers)
		}
		folder := title
		if d := dirOf(csv); d != "." {
			folder = d + "/" + title
		}
		rows := trimmedRows(t)
		pd := PlanDatabase{CSV: csv, Title: title, Rows: len(rows), Headers: headers}
		if collection != "" {
			c := collection
			pd.Collection = &c
		}
		plan.Databases = append(plan.Databases, pd)
		if collection == "" {
			if !(hasOverride && override == "skip") {
				warn("warning", "UNKNOWN_DATABASE", fmt.Sprintf("Database \"%s\" was not recognised (headers: %s); its pages are imported as notes", title, strings.Join(headers, ", ")), csv)
			}
			continue
		}
		dbs = append(dbs, database{csv: csv, folder: folder, title: title, collection: collection, rows: rows, headers: headers})
	}

	var all []built
	pageIDToRef := map[string]struct{ ref, collection string }{}
	byTitle := map[string]map[string]string{}
	registerTitle := func(c, title, ref string) {
		m := byTitle[c]
		if m == nil {
			m = map[string]string{}
			byTitle[c] = m
		}
		k := domain.NormalizeTitle(title)
		if _, ok := m[k]; !ok {
			m[k] = ref
		}
	}

	// ---- rows + pages per database ----
	type pageMeta struct {
		path  string
		name  notion.Name
		taken bool
	}
	for _, db := range dbs {
		collection := db.collection
		titleCol := ""
		if len(db.headers) > 0 {
			titleCol = db.headers[0]
		}
		var pages []string
		for _, p := range mds {
			if strings.HasPrefix(p, db.folder+"/") && !strings.Contains(p[len(db.folder)+1:], "/") {
				pages = append(pages, p)
			}
		}
		if len(pages) == 0 { // flat layout: pages next to the CSV
			dir := dirOf(db.csv)
			titles := map[string]bool{}
			for _, r := range db.rows {
				titles[domain.NormalizeTitle(r[titleCol])] = true
			}
			for _, p := range mds {
				if dirOf(p) == dir && titles[domain.NormalizeTitle(notion.SplitName(p).Title)] {
					pages = append(pages, p)
				}
			}
		}
		metas := make([]*pageMeta, len(pages))
		// pagesByTitle lists, in order, the pages whose normalised title matches: a row takes the first one still free.
		pagesByTitle := map[string][]*pageMeta{}
		for i, p := range pages {
			metas[i] = &pageMeta{path: p, name: notion.SplitName(p)}
			k := domain.NormalizeTitle(metas[i].name.Title)
			pagesByTitle[k] = append(pagesByTitle[k], metas[i])
		}

		mkItem := func(title string, r row, page *pageMeta) {
			var parsed notion.Page
			if page != nil {
				used[page.path] = true
				parsed = notion.ParsePage(vfs.Text(page.path), true)
			} else {
				parsed = notion.Page{Title: title, Props: map[string]string{}}
			}
			props := map[string]string{}
			for k, v := range parsed.Props {
				props[k] = v
			}
			for k, v := range r {
				if v != "" {
					props[k] = v
				}
			}
			ref := "csv:" + collection + ":" + domain.NormalizeTitle(title)
			sourceName := db.csv
			if page != nil {
				sourceName = page.path
				if page.name.ID != "" {
					ref = page.name.ID
				}
			}
			warns := []string{}
			var pending []pendingRel
			var input domain.Entity
			switch collection {
			case "commands":
				res := notion.ParseCommandPage(notion.CommandSource{Name: title, Category: opt(props, "Category"), Props: props, Body: parsed.Body})
				input = res.Input
				warns = append(warns, res.Warnings...)
				if props["Used In"] != "" {
					pending = append(pending, pendingRel{field: "links:used-in", target: "labs", refs: notion.ParseRelationCell(props["Used In"])})
				}
			case "concepts":
				res := notion.ParseConceptPage(notion.ConceptSource{Name: title, Category: opt(props, "Category"), Difficulty: props["Difficulty"],
					Tags: notion.SplitList(props["Tags"]), Body: parsed.Body})
				input = res.Input
				warns = append(warns, res.Warnings...)
				pending = append(pending,
					pendingRel{field: "relatedCommandIds", target: "commands", refs: notion.ParseRelationCell(props["Related Commands"]), titles: res.CommandMentions, warnUnresolved: true},
					pendingRel{field: "relatedConceptIds", target: "concepts", titles: res.ConceptMentions})
			case "labs":
				status := labStatus[strings.ToLower(props["Status"])]
				if status == "" {
					status = "Not Started"
				}
				difficulty := props["Difficulty"]
				if difficulty == "" {
					difficulty = "Easy"
				}
				input = domain.Entity{"title": title, "platform": props["Platform"], "category": props["Platform"], "status": status,
					"difficulty": difficulty, "writeup": parsed.Body}
				pending = append(pending,
					pendingRel{field: "commandIds", target: "commands", refs: notion.ParseRelationCell(props["Commands Used"]), warnUnresolved: true},
					pendingRel{field: "conceptIds", target: "concepts", refs: notion.ParseRelationCell(props["Skills Used"]), warnUnresolved: true})
			case "projects":
				status := projectStatus[strings.ToLower(props["Status"])]
				if props["Status"] != "" && status == "" {
					warns = append(warns, "unknown status \""+props["Status"]+"\"")
				}
				if status == "" {
					status = "Not started"
				}
				skills := notion.SplitList(props["Skills Used"])
				typ := props["Type"]
				if typ == "" {
					typ = "Script"
				}
				input = domain.Entity{"title": title, "type": typ, "category": props["Type"], "status": status, "skillsUsed": toAny(skills), "notes": parsed.Body}
				if len(skills) > 0 {
					lower := make([]string, len(skills))
					for i, s := range skills {
						lower[i] = strings.ToLower(s)
					}
					input["tags"] = toAny(lower)
				}
				pending = append(pending, pendingRel{field: "conceptIds", target: "concepts", titles: skills})
			default:
				res := notion.ParseLogEntry(notion.LogSource{Name: title, Date: opt(props, "Date"), TimeSpent: opt(props, "Time Spent"),
					Topics: opt(props, "Topics Covered"), KeyTakeaway: opt(props, "Key Takeaway"), Body: parsed.Body})
				input = res.Input
				warns = append(warns, res.Warnings...)
				pending = append(pending, pendingRel{field: "commandIds", target: "commands", titles: res.Learned},
					pendingRel{field: "conceptIds", target: "concepts", titles: res.Learned})
			}
			// keep any property we did not map, so nothing is lost
			known := map[string]bool{"Category": true, "Example": true, "Explanation": true, "Syntax": true, "Used In": true, "Difficulty": true,
				"Tags": true, "Related Commands": true, "Platform": true, "Status": true, "Commands Used": true, "Skills Used": true, "Type": true,
				"Date": true, "Time Spent": true, "Topics Covered": true, "Key Takeaway": true, titleCol: true}
			unknown := map[string]any{}
			for k, v := range props {
				if !known[k] {
					unknown[k] = v
				}
			}
			if len(unknown) > 0 {
				ex, _ := input["extras"].(map[string]any)
				merged := map[string]any{}
				for k, v := range ex {
					merged[k] = v
				}
				merged["notionProperties"] = unknown
				input["extras"] = merged
				plural := "ies"
				if len(unknown) == 1 {
					plural = "y"
				}
				warns = append(warns, fmt.Sprintf("kept %d unmapped propert%s in extras", len(unknown), plural))
			}
			all = append(all, built{item: PlanItem{Collection: collection, Ref: ref, Title: title, Input: input, Relations: []Relation{}, Source: sourceName, Warnings: warns}, pending: pending})
			if page != nil && page.name.ID != "" {
				pageIDToRef[page.name.ID] = struct{ ref, collection string }{ref, collection}
			}
			registerTitle(collection, title, ref)
		}

		for _, r := range db.rows {
			title := r[titleCol]
			if title == "" {
				continue
			}
			var page *pageMeta
			for _, m := range pagesByTitle[domain.NormalizeTitle(title)] {
				if !m.taken {
					page = m
					break
				}
			}
			if page != nil {
				page.taken = true
			} else {
				warn("info", "ROW_WITHOUT_PAGE", fmt.Sprintf("Row \"%s\" has no page body; imported from CSV only", title), db.csv)
			}
			mkItem(title, r, page)
		}
		for _, page := range metas {
			if page.taken {
				continue
			}
			if templateWord.MatchString(page.name.Title) {
				used[page.path] = true
				plan.Templates = append(plan.Templates, PlanTemplate{Collection: collection, Source: page.path,
					Template: Template{Name: templateTitle(page.name.Title), Sections: parseSections(vfs.Text(page.path))}})
			} else {
				warn("warning", "ORPHAN_PAGE", fmt.Sprintf("Page \"%s\" is in %s but has no CSV row; imported from page properties", page.name.Title, db.title), page.path)
				mkItem(page.name.Title, nil, page)
			}
		}
	}

	// ---- loose pages ----
	csvNames := map[string]bool{}
	for _, c := range csvs {
		csvNames[path.Base(c)] = true
	}
	var atts []PlanAttachment
	attIndex := map[string]bool{}
	attUsed := map[string]bool{}
	var loose []built
	for _, p := range mds {
		if used[p] {
			continue
		}
		name := notion.SplitName(p)
		raw := vfs.Text(p)
		isHub := false
		for _, m := range hubLink.FindAllStringSubmatch(raw, -1) {
			if csvNames[path.Base(notion.DecodeNotionPath(m[1]))] {
				isHub = true
				break
			}
		}
		var dirSegs []string
		if d := dirOf(p); d != "." {
			dirSegs = strings.Split(d, "/")
		}
		parsed := notion.ParsePage(raw, false)
		title := parsed.Title
		if title == "" {
			title = name.Title
		}
		last := ""
		if len(dirSegs) > 0 {
			last = dirSegs[len(dirSegs)-1]
		}
		tplCollection := ""
		if templateBare.MatchString(title) && len(parsed.Props) == 0 {
			tplCollection = collectionFromFolder(notion.SplitName(last).Title)
		}
		if tplCollection != "" {
			plan.Templates = append(plan.Templates, PlanTemplate{Collection: tplCollection, Source: p,
				Template: Template{Name: templateTitle(title), Sections: parseSections(raw)}})
			continue
		}
		body := rewriteLinks(parsed.Body, dirOf(p), vfs, &atts, attIndex, attUsed)
		slugs := make([]string, len(dirSegs))
		for i, s := range dirSegs {
			slugs[i] = domain.Slugify(notion.SplitName(s).Title)
		}
		folder := strings.Join(slugs, "/") // same form NoteRepo stores
		blocks := notion.FencedBlocks(body)
		codeChars := 0
		for _, b := range blocks {
			codeChars += u16len(b.Code)
		}
		ratio := 0.0
		if bl := u16len(body); bl > 0 {
			ratio = float64(codeChars) / float64(bl)
		}
		noteInput := map[string]any{"title": title, "folder": folder, "markdown": body, "kind": guessKind(title), "category": ""}
		longest := ""
		best := -1
		codes := make([]string, len(blocks))
		for i, b := range blocks {
			codes[i] = b.Code
			if n := u16len(b.Code); n > best {
				best, longest = n, b.Lang
			}
		}
		if longest == "" {
			longest = "text"
		}
		desc := domain.JSTrim(spaces.ReplaceAllString(fencedStrip.ReplaceAllString(body, ""), " "))
		snippetInput := map[string]any{"title": title, "language": longest, "code": strings.Join(codes, "\n\n"), "description": u16prefix(desc, 300)}
		asSnippet := len(blocks) > 0 && ratio > 0.6
		item := PlanItem{Ref: name.ID, Title: title, Relations: []Relation{}, Source: p, Warnings: []string{},
			Alternatives: map[string]map[string]any{"notes": noteInput, "snippets": snippetInput}}
		if item.Ref == "" {
			item.Ref = "md:" + domain.NormalizeTitle(folder+"/"+title)
		}
		if asSnippet {
			item.Collection, item.Input = "snippets", snippetInput
		} else {
			item.Collection, item.Input = "notes", noteInput
		}
		if isHub {
			item.SkipByDefault = "dashboard hub page (progress and navigation are computed natively); override to import as a note"
		}
		if len(parsed.Props) > 0 {
			props := map[string]any{}
			for k, v := range parsed.Props {
				props[k] = v
			}
			item.Input["extras"] = map[string]any{"notionProperties": props}
		}
		loose = append(loose, built{item: item})
		if name.ID != "" {
			pageIDToRef[name.ID] = struct{ ref, collection string }{item.Ref, item.Collection}
		}
	}
	all = append(all, loose...)

	// ---- second pass: relations ----
	resolve := func(target string, ref notion.RelationRef) (RelTarget, bool) {
		if ref.ID != "" {
			if hit, ok := pageIDToRef[ref.ID]; ok && hit.collection == target {
				return RelTarget{Ref: hit.ref}, true
			}
		}
		k := domain.NormalizeTitle(ref.Title)
		if inPlan, ok := byTitle[target][k]; ok {
			return RelTarget{Ref: inPlan}, true
		}
		if opts.Lookup != nil {
			if id, ok := opts.Lookup.Find(target, k); ok {
				return RelTarget{ID: id}, true
			}
		}
		return RelTarget{}, false
	}
	for i := range all {
		item := &all[i].item
		for _, p := range all[i].pending {
			targets := []RelTarget{}
			seen := map[string]bool{}
			add := func(t RelTarget, ok bool) bool {
				if !ok {
					return false
				}
				key := "i:" + t.ID
				if t.Ref != "" {
					key = "r:" + t.Ref
				}
				if seen[key] || (t.Ref != "" && t.Ref == item.Ref) {
					return true
				}
				seen[key] = true
				targets = append(targets, t)
				return true
			}
			for _, r := range p.refs {
				if !add(resolve(p.target, r)) {
					item.Warnings = append(item.Warnings, fmt.Sprintf("unresolved %s relation: \"%s\"", p.target, r.Title))
				}
			}
			for _, t := range p.titles {
				if !add(resolve(p.target, notion.RelationRef{Title: t})) && p.warnUnresolved && mentionRe.MatchString(t) {
					item.Warnings = append(item.Warnings, fmt.Sprintf("mentioned %s \"%s\" not found", strings.TrimSuffix(p.target, "s"), t))
				}
			}
			if len(targets) > 0 {
				item.Relations = append(item.Relations, Relation{Field: p.field, Targets: targets})
			}
		}
		plan.Items = append(plan.Items, *item)
	}
	plan.Attachments = append(plan.Attachments, atts...)
	unref := 0
	for _, f := range otherFiles {
		if !attUsed[f] {
			unref++
		}
	}
	if unref > 0 {
		warn("info", "UNREFERENCED_FILES", fmt.Sprintf("%d file(s) in the export are not referenced by any page and were not imported", unref), "")
	}
	return plan
}

// templateTitle is a template page's name without the word "template".
func templateTitle(title string) string {
	loc := templateName.FindStringIndex(title)
	name := title
	if loc != nil {
		name = title[:loc[0]] + title[loc[1]:]
	}
	if name = domain.JSTrim(name); name != "" {
		return name
	}
	return title
}

func toAny(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

func parseSections(text string) []TemplateSection {
	page := notion.ParsePage(text, false)
	out := []TemplateSection{}
	var cur *struct {
		title string
		lines []string
	}
	flush := func() {
		if cur == nil {
			return
		}
		hint := domain.JSTrim(hintTrim.ReplaceAllString(strings.Join(cur.lines, "\n"), ""))
		out = append(out, TemplateSection{Key: notion.HeadingKey(cur.title), Title: cur.title, Hint: hint})
	}
	for _, l := range strings.Split(page.Body, "\n") {
		if h := sectionH2.FindStringSubmatch(l); h != nil {
			flush()
			cur = &struct {
				title string
				lines []string
			}{title: domain.JSTrim(h[1])}
		} else if cur != nil {
			cur.lines = append(cur.lines, l)
		}
	}
	flush()
	return out
}

func collectionFromFolder(name string) string {
	l := strings.ToLower(name)
	switch {
	case strings.Contains(l, "command"):
		return "commands"
	case strings.Contains(l, "concept"):
		return "concepts"
	case regexp.MustCompile(`(?i)\blabs?\b`).MatchString(name):
		return "labs"
	case strings.Contains(l, "project"):
		return "projects"
	case strings.Contains(l, "log"):
		return "log"
	}
	return ""
}

func guessKind(title string) string {
	switch {
	case cheatRe.MatchString(title):
		return "cheatsheet"
	case writeupRe.MatchString(title):
		return "writeup-notes"
	case guideRe.MatchString(title):
		return "guide"
	}
	return "general"
}

// rewriteLinks turns page links into [[wiki links]] and local files into ck:// attachment URLs, recording the files to
// copy. Fenced code is left alone.
func rewriteLinks(body, pageDir string, vfs *VirtualFS, atts *[]PlanAttachment, seen, used map[string]bool) string {
	return notion.MapOutsideFences(body, func(line string) string {
		var b strings.Builder
		last := 0
		for _, m := range linkRe.FindAllStringSubmatchIndex(line, -1) {
			b.WriteString(line[last:m[0]])
			last = m[1]
			whole, bang, text, href := line[m[0]:m[1]], line[m[2]:m[3]], line[m[4]:m[5]], line[m[6]:m[7]]
			if schemeRe.MatchString(href) || strings.HasPrefix(href, "#") {
				b.WriteString(whole)
				continue
			}
			dir := pageDir
			if dir == "." {
				dir = ""
			}
			resolved := path.Join(dir, notion.DecodeNotionPath(href))
			switch {
			case mdExt.MatchString(resolved):
				title := notion.SplitName(resolved).Title
				b.WriteString("[[" + title)
				if text != "" && text != title {
					b.WriteString("|" + text)
				}
				b.WriteString("]]")
			case csvExt.MatchString(resolved):
				b.WriteString(text)
			default:
				data, ok := vfs.Files[resolved]
				if !ok {
					b.WriteString(whole)
					continue
				}
				used[resolved] = true
				to := hash8(resolved) + "/" + path.Base(resolved)
				if !seen[resolved] {
					seen[resolved] = true
					*atts = append(*atts, PlanAttachment{From: resolved, To: to, Bytes: len(data)})
				}
				parts := strings.Split(to, "/")
				for i, s := range parts {
					parts[i] = encodeURIComponent(s)
				}
				b.WriteString(bang + "[" + text + "](ck://attachments/" + strings.Join(parts, "/") + ")")
			}
		}
		b.WriteString(line[last:])
		return b.String()
	})
}
