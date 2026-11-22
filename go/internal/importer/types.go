// Package importer plans and commits imports from Notion exports, CSV files, Markdown folders and JSON backups, and
// undoes them. It is the Go port of packages/server/src/import. Planning never writes; commit takes a pre-import
// backup first and records a manifest so the whole import can be undone.
package importer

// Strategy says what to do when an imported item matches one that already exists.
type Strategy string

const (
	StrategySkip            Strategy = "skip"
	StrategyUpdateEmptyOnly Strategy = "update-empty-only"
	StrategyOverwrite       Strategy = "overwrite"
	StrategyKeepBoth        Strategy = "keep-both"
)

// Valid reports whether s is one of the four strategies.
func (s Strategy) Valid() bool {
	switch s {
	case StrategySkip, StrategyUpdateEmptyOnly, StrategyOverwrite, StrategyKeepBoth:
		return true
	}
	return false
}

// RelTarget is the target of a relation: another item in this plan (Ref) or an entity already stored (ID).
type RelTarget struct {
	Ref string `json:"ref,omitempty"`
	ID  string `json:"id,omitempty"`
}

// Relation is a field whose value is the ids of other items.
type Relation struct {
	Field   string      `json:"field"`
	Targets []RelTarget `json:"targets"`
}

// PlanItem is one thing the import would create or merge.
type PlanItem struct {
	Collection string `json:"collection"`
	// Ref is the stable identity for idempotent re-import: the Notion page id, or csv:/md: plus the normalised title.
	Ref   string `json:"ref"`
	Title string `json:"title"`
	// Input holds the fields to create or merge: only keys that were actually derived, no schema defaults.
	Input     map[string]any `json:"input"`
	Relations []Relation     `json:"relations"`
	Source    string         `json:"source"`
	Warnings  []string       `json:"warnings"`
	// Alternatives are other classifications for loose pages (note or snippet).
	Alternatives  map[string]map[string]any `json:"alternatives,omitempty"`
	SkipByDefault string                    `json:"skipByDefault,omitempty"`
	// FixedID preserves an id (JSON backup restore) so cross-references stay valid.
	FixedID string `json:"fixedId,omitempty"`
}

// PlanWarning is something the user should know before committing.
type PlanWarning struct {
	Level   string `json:"level"` // info, warning
	Code    string `json:"code"`
	Message string `json:"message"`
	Source  string `json:"source,omitempty"`
}

// PlanDatabase is a Notion database CSV found in the export.
type PlanDatabase struct {
	CSV        string   `json:"csv"`
	Title      string   `json:"title"`
	Collection *string  `json:"collection"`
	Rows       int      `json:"rows"`
	Headers    []string `json:"headers"`
}

// TemplateSection is one heading of a page template.
type TemplateSection struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	Hint  string `json:"hint,omitempty"`
}

// Template is a page template: a name and the sections new pages start with.
type Template struct {
	Name     string            `json:"name"`
	Sections []TemplateSection `json:"sections"`
}

// PlanTemplate is a template found in the source.
type PlanTemplate struct {
	Collection string   `json:"collection"`
	Template   Template `json:"template"`
	Source     string   `json:"source"`
}

// PlanAttachment is a file to copy under <data>/attachments.
type PlanAttachment struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Bytes int    `json:"bytes"`
}

// Plan is everything an import would do.
type Plan struct {
	Kind        string           `json:"kind"` // notion, csv, markdown, json-backup
	SourceName  string           `json:"sourceName"`
	Items       []PlanItem       `json:"items"`
	Templates   []PlanTemplate   `json:"templates"`
	Attachments []PlanAttachment `json:"attachments"`
	Databases   []PlanDatabase   `json:"databases"`
	Warnings    []PlanWarning    `json:"warnings"`
}

func newPlan(kind, sourceName string) *Plan {
	return &Plan{Kind: kind, SourceName: sourceName, Items: []PlanItem{}, Templates: []PlanTemplate{},
		Attachments: []PlanAttachment{}, Databases: []PlanDatabase{}, Warnings: []PlanWarning{}}
}

// Options choose how to commit a plan.
type Options struct {
	Strategy Strategy `json:"strategy"`
	// Overrides reclassify loose pages per ref: note, snippet, skip or include.
	Overrides map[string]string `json:"overrides,omitempty"`
}

// SkippedItem is an item that was not imported, and why.
type SkippedItem struct {
	Ref    string `json:"ref"`
	Title  string `json:"title"`
	Reason string `json:"reason"`
}

// ItemWarning lists the warnings of one imported item.
type ItemWarning struct {
	Collection string   `json:"collection"`
	Title      string   `json:"title"`
	Warnings   []string `json:"warnings"`
}

// Report is what an import did.
type Report struct {
	ID           string         `json:"id"`
	Kind         string         `json:"kind"`
	SourceName   string         `json:"sourceName"`
	Strategy     Strategy       `json:"strategy"`
	CreatedAt    string         `json:"createdAt"`
	Created      map[string]int `json:"created"`
	Updated      map[string]int `json:"updated"`
	Unchanged    map[string]int `json:"unchanged"`
	Skipped      []SkippedItem  `json:"skipped"`
	Templates    []string       `json:"templates"`
	Attachments  int            `json:"attachments"`
	Warnings     []PlanWarning  `json:"warnings"`
	ItemWarnings []ItemWarning  `json:"itemWarnings"`
	Snapshot     *string        `json:"snapshot"`
}

// ManifestCreated records an entity the import created.
type ManifestCreated struct {
	Collection string `json:"collection"`
	ID         string `json:"id"`
}

// ManifestUpdated records an entity the import changed and what it looked like before.
type ManifestUpdated struct {
	Collection string         `json:"collection"`
	ID         string         `json:"id"`
	Before     map[string]any `json:"before"`
}

// ManifestTemplate records a template file the import wrote and its previous text (nil: there was none).
type ManifestTemplate struct {
	Collection string  `json:"collection"`
	Before     *string `json:"before"`
}

// Manifest is what undo needs to reverse an import.
type Manifest struct {
	ID          string             `json:"id"`
	CreatedAt   string             `json:"createdAt"`
	Created     []ManifestCreated  `json:"created"`
	Updated     []ManifestUpdated  `json:"updated"`
	Templates   []ManifestTemplate `json:"templates"`
	Attachments []string           `json:"attachments"`
	Undone      string             `json:"undone,omitempty"`
}
