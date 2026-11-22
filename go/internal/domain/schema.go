package domain

import (
	"fmt"
	"math"
	"sort"
)

// Kind is the type of a schema field.
type Kind int

const (
	KString Kind = iota
	KBool
	KInt     // integer, optionally non-negative
	KNumber  // any number, optionally non-negative
	KEnum    // one of Enum
	KMastery // 0, 1, 2 or 3
	KList    // list of Item
	KObject  // object with Fields
	KRecord  // free-form object (unknown values)
)

// Field describes one property of an entity, like a Zod field.
type Field struct {
	Name     string
	Kind     Kind
	Required bool       // no default: a missing value is an error
	Default  func() any // value used when the field is missing (nil: the field is optional and stays absent)
	Nullable bool
	Enum     []string
	NonNeg   bool
	MinLen1  bool
	Item     *Field  // element type for lists
	Fields   []Field // properties for objects
}

// Schema is the set of fields of one collection's entities.
type Schema struct {
	Name   string
	Fields []Field
}

// Mode selects how input is validated.
type Mode int

const (
	// ModeFull validates a stored entity: every field, defaults applied.
	ModeFull Mode = iota
	// ModeCreate validates input for a new entity: id and timestamps are not accepted, title and slug are optional.
	ModeCreate
	// ModePatch validates a partial update: every field is optional and defaults are not applied.
	ModePatch
)

// Issue is one validation problem, shaped like a Zod issue.
type Issue struct {
	Code    string `json:"code"`
	Path    []any  `json:"path"`
	Message string `json:"message"`
}

// ValidationError carries the issues for an API 400 response.
type ValidationError struct{ Issues []Issue }

func (e *ValidationError) Error() string {
	if len(e.Issues) == 0 {
		return "validation failed"
	}
	i := e.Issues[0]
	return fmt.Sprintf("%v: %s", i.Path, i.Message)
}

// Parse validates input against the schema in the given mode and returns the cleaned entity: unknown keys are
// dropped, defaults are applied (except in patch mode), types are checked. A nil input is an empty object.
func (s *Schema) Parse(input map[string]any, mode Mode) (Entity, error) {
	var issues []Issue
	out := Entity{}
	for _, f := range s.Fields {
		if mode != ModeFull && (f.Name == "id" || f.Name == "createdAt" || f.Name == "updatedAt") {
			continue
		}
		f := f
		switch mode {
		case ModePatch:
			f.Required, f.Default = false, nil
		case ModeCreate:
			if f.Name == "slug" || f.Name == "title" {
				f.Required, f.Default = false, nil
			}
		}
		parseField(f, input, []any{f.Name}, out, &issues)
	}
	if len(issues) > 0 {
		return nil, &ValidationError{Issues: issues}
	}
	return out, nil
}

func parseField(f Field, in map[string]any, path []any, out map[string]any, issues *[]Issue) {
	v, present := in[f.Name]
	if !present || v == nil && !f.Nullable {
		if present && v == nil && f.Default == nil && !f.Required {
			return // an explicit null on an optional field is treated as absent
		}
		switch {
		case f.Default != nil:
			d := f.Default()
			if f.Kind == KObject {
				// A default object still passes through its own schema so nested defaults apply.
				if m, ok := d.(map[string]any); ok {
					sub := map[string]any{}
					for _, sf := range f.Fields {
						parseField(sf, m, append(append([]any{}, path...), sf.Name), sub, issues)
					}
					d = sub
				}
			}
			out[f.Name] = d
		case f.Required:
			*issues = append(*issues, Issue{Code: "invalid_type", Path: path, Message: "Required"})
		}
		return
	}
	if v == nil { // explicit null on a nullable field
		out[f.Name] = nil
		return
	}
	cleaned, ok := checkValue(f, v, path, issues)
	if ok {
		out[f.Name] = cleaned
	}
}

func bad(issues *[]Issue, path []any, expected string, got any) {
	*issues = append(*issues, Issue{Code: "invalid_type", Path: path, Message: fmt.Sprintf("Expected %s, received %s", expected, typeName(got))})
}

func typeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case bool:
		return "boolean"
	case float64, int, int64:
		return "number"
	case []any, []string:
		return "array"
	default:
		return "object"
	}
}

func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

func checkValue(f Field, v any, path []any, issues *[]Issue) (any, bool) {
	switch f.Kind {
	case KString:
		s, ok := v.(string)
		if !ok {
			bad(issues, path, "string", v)
			return nil, false
		}
		if f.MinLen1 && s == "" {
			*issues = append(*issues, Issue{Code: "too_small", Path: path, Message: "String must contain at least 1 character(s)"})
			return nil, false
		}
		return s, true
	case KBool:
		b, ok := v.(bool)
		if !ok {
			bad(issues, path, "boolean", v)
			return nil, false
		}
		return b, true
	case KInt, KNumber:
		n, ok := asFloat(v)
		if !ok || math.IsNaN(n) || math.IsInf(n, 0) {
			bad(issues, path, "number", v)
			return nil, false
		}
		if f.Kind == KInt && n != math.Trunc(n) {
			*issues = append(*issues, Issue{Code: "invalid_type", Path: path, Message: "Expected integer, received float"})
			return nil, false
		}
		if f.NonNeg && n < 0 {
			*issues = append(*issues, Issue{Code: "too_small", Path: path, Message: "Number must be greater than or equal to 0"})
			return nil, false
		}
		return n, true
	case KMastery:
		n, ok := asFloat(v)
		if !ok || n != math.Trunc(n) || n < 0 || n > 3 {
			*issues = append(*issues, Issue{Code: "invalid_union", Path: path, Message: "Invalid input"})
			return nil, false
		}
		return n, true
	case KEnum:
		s, ok := v.(string)
		if ok {
			for _, e := range f.Enum {
				if e == s {
					return s, true
				}
			}
		}
		*issues = append(*issues, Issue{Code: "invalid_enum_value", Path: path, Message: fmt.Sprintf("Invalid enum value. Expected one of %v", f.Enum)})
		return nil, false
	case KList:
		var items []any
		switch t := v.(type) {
		case []any:
			items = t
		case []string:
			items = make([]any, len(t))
			for i, s := range t {
				items[i] = s
			}
		default:
			bad(issues, path, "array", v)
			return nil, false
		}
		out := make([]any, 0, len(items))
		okAll := true
		for i, x := range items {
			p := append(append([]any{}, path...), i)
			c, ok := checkValue(*f.Item, x, p, issues)
			if !ok {
				okAll = false
				continue
			}
			out = append(out, c)
		}
		return out, okAll
	case KObject:
		m, ok := v.(map[string]any)
		if !ok {
			bad(issues, path, "object", v)
			return nil, false
		}
		sub := map[string]any{}
		for _, sf := range f.Fields {
			parseField(sf, m, append(append([]any{}, path...), sf.Name), sub, issues)
		}
		return sub, true
	case KRecord:
		m, ok := v.(map[string]any)
		if !ok {
			bad(issues, path, "object", v)
			return nil, false
		}
		return m, true
	}
	return v, true
}

// Merge returns base with the patch's keys written over it (a shallow merge, like object spread).
func Merge(base Entity, patch Entity) Entity {
	out := make(Entity, len(base)+len(patch))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range patch {
		out[k] = v
	}
	return out
}

// SortedKeys returns the keys of m in order (helper for deterministic output).
func SortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
