package domain

// Field constructors, so the schemas below read like the Zod ones they mirror.
func fStr(n string) Field     { return Field{Name: n, Kind: KString, Required: true} }
func fStrMin1(n string) Field { return Field{Name: n, Kind: KString, Required: true, MinLen1: true} }
func fStrD(n, d string) Field { return Field{Name: n, Kind: KString, Default: func() any { return d }} }
func fStrOpt(n string) Field  { return Field{Name: n, Kind: KString} }
func fStrNull(n string) Field {
	return Field{Name: n, Kind: KString, Nullable: true, Default: func() any { return nil }}
}
func fBoolD(n string, d bool) Field {
	return Field{Name: n, Kind: KBool, Default: func() any { return d }}
}
func fNumOpt(n string, nonNeg bool) Field { return Field{Name: n, Kind: KNumber, NonNeg: nonNeg} }
func fNumNullD(n string) Field {
	return Field{Name: n, Kind: KNumber, NonNeg: true, Nullable: true, Default: func() any { return nil }}
}
func fIntOpt(n string) Field { return Field{Name: n, Kind: KInt, NonNeg: true} }
func fIntD(n string, d float64) Field {
	return Field{Name: n, Kind: KInt, NonNeg: true, Default: func() any { return d }}
}
func fMasteryD(n string) Field {
	return Field{Name: n, Kind: KMastery, Default: func() any { return float64(0) }}
}
func fEnumD(n, d string, vals ...string) Field {
	return Field{Name: n, Kind: KEnum, Enum: vals, Default: func() any { return d }}
}
func fStrList(n string) Field {
	item := Field{Kind: KString}
	return Field{Name: n, Kind: KList, Item: &item, Default: func() any { return []any{} }}
}
func fObjList(n string, props ...Field) Field {
	item := Field{Kind: KObject, Fields: props}
	return Field{Name: n, Kind: KList, Item: &item, Default: func() any { return []any{} }}
}

// commonFields are shared by every entity.
func commonFields() []Field {
	return []Field{
		fStrMin1("id"),
		fStr("title"),
		fStr("slug"),
		fStrD("category", ""),
		fStrList("tags"),
		fStrMin1("createdAt"),
		fStrMin1("updatedAt"),
		fBoolD("favorite", false),
		fBoolD("archived", false),
		fStrOpt("importRef"),
		fObjList("links", fStr("type"), fStr("targetId")),
		{Name: "extras", Kind: KRecord, Default: func() any { return map[string]any{} }},
	}
}

func schema(name string, extra ...Field) *Schema {
	return &Schema{Name: name, Fields: append(commonFields(), extra...)}
}

// Schemas maps each collection to its schema.
var Schemas = map[string]*Schema{
	"commands": schema("commands",
		fStr("name"),
		Field{Name: "platforms", Kind: KList, Item: &Field{Kind: KEnum, Enum: []string{"linux", "macos", "windows", "any"}},
			Default: func() any { return []any{"any"} }},
		fStrD("whatItDoes", ""),
		fStrD("syntax", ""),
		fStrD("explanation", ""),
		fObjList("arguments", fStr("name"), fStr("meaning")),
		fObjList("options", fStr("flag"), fStr("meaning")),
		fObjList("examples", fStr("command"), fStr("description")),
		fStrList("placeholders"),
		fEnumD("risk", "safe", "safe", "caution", "destructive"),
		fStrD("notes", ""),
		fMasteryD("mastery"),
		fStrMin1Opt("lastPracticedAt"),
		fIntD("practiceCount", 0),
		Field{Name: "srs", Kind: KObject, Default: func() any { return map[string]any{} }, Fields: []Field{
			{Name: "ease", Kind: KNumber, Default: func() any { return 2.5 }},
			{Name: "intervalDays", Kind: KNumber, Default: func() any { return float64(0) }},
			fStrMin1Opt("dueAt"),
		}},
	),
	"concepts": schema("concepts",
		fStrD("difficulty", "Easy"),
		fObjList("sections", fStr("key"), fStr("title"), fStr("markdown")),
		fStrList("relatedCommandIds"),
		fStrList("relatedConceptIds"),
		fMasteryD("confidence"),
		fIntD("questionsOpen", 0),
	),
	"labs": schema("labs",
		fStr("platform"),
		fEnumD("status", "Not Started", "Not Started", "In Progress", "Completed"),
		fStrD("difficulty", "Easy"),
		fStrOpt("domain"),
		fNumOpt("points", false),
		fStrMin1Opt("solvedAt"),
		fNumOpt("timeSpentMin", true),
		fIntOpt("hintsUsed"),
		fStrList("commandIds"),
		fStrList("conceptIds"),
		fStrD("writeup", ""),
		fBoolD("writeupPrivate", true),
		fStrList("scriptIds"),
	),
	"projects": schema("projects",
		fStrD("type", "Script"),
		fEnumD("status", "Not started", "Not started", "In progress", "Done"),
		fStrList("skillsUsed"),
		fStrList("conceptIds"),
		fStrOpt("repoUrl"),
		fStrD("notes", ""),
	),
	"log": schema("log",
		fStr("date"),
		fNumNullD("timeSpentMin"),
		fStrNull("timeSpentRaw"),
		fStrList("topics"),
		fStrNull("topicsRaw"),
		fStrD("keyTakeaway", ""),
		fStrD("body", ""),
		fStrList("conceptIds"),
		fStrList("commandIds"),
		fStrList("labIds"),
	),
	"notes": schema("notes",
		fStrD("folder", ""),
		fStrD("markdown", ""),
		fEnumD("kind", "general", "cheatsheet", "guide", "writeup-notes", "general"),
	),
	"snippets": schema("snippets",
		fStrD("language", "text"),
		fStrNull("code"),
		fStrNull("filePath"),
		fStrD("description", ""),
	),
}

func fStrMin1Opt(n string) Field { return Field{Name: n, Kind: KString, MinLen1: true} }
