// Package search is the full-text index: a small BM25 engine with the same tokenizer, field boosts, prefix and fuzzy
// matching and operators (cmd:, tag:, cat:, status:, type:) as the MiniSearch-based TypeScript version.
package search

import (
	"strings"
	"unicode/utf8"

	"github.com/reeper316099/Cipherkeep/go/internal/domain"
)

// isSeparator is what the TypeScript tokenizer splits on: JavaScript whitespace and ,;()"'`<>|{}[]
func isSeparator(r rune) bool {
	switch r {
	case ',', ';', '(', ')', '"', '\'', '`', '<', '>', '|', '{', '}', '[', ']':
		return true
	}
	return domain.IsJSSpace(r)
}

func isPathSep(r rune) bool {
	switch r {
	case '/', '\\', '.', '-', '_', '~', '=':
		return true
	}
	return false
}

// tokenSet drops repeats while keeping first-seen order. Small sets are checked by scanning; big ones by map.
type tokenSet struct {
	out  []string
	seen map[string]struct{}
}

func (t *tokenSet) add(s string) {
	if s == "" {
		return
	}
	if t.seen != nil {
		if _, dup := t.seen[s]; dup {
			return
		}
		t.seen[s] = struct{}{}
		t.out = append(t.out, s)
		return
	}
	for _, o := range t.out {
		if o == s {
			return
		}
	}
	t.out = append(t.out, s)
	if len(t.out) >= 24 {
		t.seen = make(map[string]struct{}, 128)
		for _, o := range t.out {
			t.seen[o] = struct{}{}
		}
	}
}

// Tokenize keeps leading dashes (-la, --color=auto) and also emits useful sub-tokens (the dash-less form, the parts
// around "=", path and word segments) so both "-la" and "la", and "--color=auto" and "color", find them.
func Tokenize(text string) []string {
	var set tokenSet
	lower := strings.ToLower(text)
	start := -1
	flush := func(end int) {
		if start < 0 {
			return
		}
		chunk := lower[start:end]
		start = -1
		chunk = strings.TrimLeft(chunk, ":*")
		chunk = strings.TrimRight(chunk, ".:!?*")
		if chunk == "" {
			return
		}
		set.add(chunk)
		dashless := strings.TrimLeft(chunk, "-")
		if dashless != chunk {
			set.add(dashless)
		}
		if strings.IndexByte(chunk, '=') >= 0 {
			for _, part := range strings.Split(chunk, "=") {
				if part != "" && part != chunk {
					set.add(part)
					set.add(strings.TrimLeft(part, "-"))
				}
			}
		}
		if strings.IndexFunc(dashless, isPathSep) >= 0 {
			segStart := 0
			for i, r := range dashless {
				if isPathSep(r) {
					set.add(dashless[segStart:i])
					segStart = i + utf8.RuneLen(r)
				}
			}
			set.add(dashless[segStart:])
		} else {
			set.add(dashless)
		}
	}
	for i, r := range lower {
		if isSeparator(r) {
			flush(i)
		} else if start < 0 {
			start = i
		}
	}
	flush(len(lower))
	return set.out
}
