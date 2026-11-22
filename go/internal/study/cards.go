package study

import (
	"regexp"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/reeper316099/Cipherkeep/go/internal/domain"
)

// CardType is the kind of question a card asks.
type CardType string

const (
	CardName  CardType = "name"
	CardTask  CardType = "task"
	CardFlag  CardType = "flag"
	CardFill  CardType = "fill"
	CardTeach CardType = "teach"
	CardWhat  CardType = "what"
)

// Card is one question. Kind "reveal" flips and self-rates; "type" checks what the learner typed.
type Card struct {
	EntityID   string   `json:"entityId"`
	Collection string   `json:"collection"`
	Type       CardType `json:"type"`
	Title      string   `json:"title"`
	Prompt     string   `json:"prompt"`
	Kind       string   `json:"kind"`
	Answer     string   `json:"answer"`
	Expected   []string `json:"expected,omitempty"`
	Hint       string   `json:"hint,omitempty"`
}

var blankBullets = regexp.MustCompile(`(?m)^\s*(?:[-*+]|\d+\.)\s*$`)

// NormalizeCommandText trims whitespace, surrounding backticks and a leading "$ " or "# ".
func NormalizeCommandText(s string) string {
	s = domain.JSTrim(s)
	s = strings.TrimLeft(s, "`")
	s = strings.TrimRight(s, "`")
	if len(s) > 1 && (s[0] == '$' || s[0] == '#') {
		rest := s[1:]
		trimmed := strings.TrimLeftFunc(rest, domain.IsJSSpace)
		if len(trimmed) < len(rest) { // the marker must be followed by whitespace
			s = trimmed
		}
	}
	return domain.JSTrim(s)
}

// TokenizeCommand splits on whitespace; quoted spans stay one token (quotes stripped). A quote with no closing quote is
// just part of an ordinary word.
func TokenizeCommand(s string) []string {
	text := NormalizeCommandText(s)
	var out []string
	i := 0
	for i < len(text) {
		r, size := utf8.DecodeRuneInString(text[i:])
		if domain.IsJSSpace(r) {
			i += size
			continue
		}
		if r == '"' || r == '\'' {
			if j := strings.IndexByte(text[i+1:], byte(r)); j >= 0 {
				out = append(out, text[i+1:i+1+j])
				i += j + 2
				continue
			}
		}
		j := i
		for j < len(text) {
			r2, sz := utf8.DecodeRuneInString(text[j:])
			if domain.IsJSSpace(r2) {
				break
			}
			j += sz
		}
		out = append(out, text[i:j])
		i = j
	}
	return out
}

// isPlaceholder reports whether a token is <name>, {name} or [name] (letters, digits, "_", "-" and spaces after the
// first letter), which matches any value.
func isPlaceholder(t string) bool {
	n := len(t)
	if n < 3 {
		return false
	}
	switch t[0] {
	case '<':
		return t[n-1] == '>' && !strings.Contains(t[1:n-1], ">")
	case '{':
		return t[n-1] == '}' && !strings.Contains(t[1:n-1], "}")
	case '[':
		if t[n-1] != ']' || !isASCIILetter(t[1]) {
			return false
		}
		for _, c := range []byte(t[2 : n-1]) {
			if !(isASCIILetter(c) || (c >= '0' && c <= '9') || c == '_' || c == ' ' || c == '-') {
				return false
			}
		}
		return true
	}
	return false
}

func isASCIILetter(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

// isShortFlags matches -abc (letters and digits only).
func isShortFlags(t string) bool {
	if len(t) < 2 || t[0] != '-' {
		return false
	}
	for _, c := range []byte(t[1:]) {
		if !(isASCIILetter(c) || (c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}

// isRealFlag matches -x or --x...: one or two dashes and then a letter.
func isRealFlag(t string) bool {
	if strings.HasPrefix(t, "--") {
		return len(t) > 2 && isASCIILetter(t[2])
	}
	return len(t) > 1 && t[0] == '-' && isASCIILetter(t[1])
}

type parsedCmd struct {
	cmd        string
	shorts     map[rune]bool
	longs      map[string]bool
	positional []string
}

func parseCmd(s string) parsedCmd {
	toks := TokenizeCommand(s)
	p := parsedCmd{shorts: map[rune]bool{}, longs: map[string]bool{}}
	if len(toks) == 0 {
		return p
	}
	p.cmd = toks[0]
	dd := false
	for _, t := range toks[1:] {
		switch {
		case dd:
			p.positional = append(p.positional, t)
		case t == "--":
			dd = true
		case strings.HasPrefix(t, "--"):
			p.longs[t] = true
		case isShortFlags(t):
			for _, ch := range t[1:] { // -la -> l, a
				p.shorts[ch] = true
			}
		default:
			p.positional = append(p.positional, t)
		}
	}
	return p
}

func sameRunes(a, b map[rune]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func sameStrs(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

// CommandsMatch reports whether input expresses the same command as reference. It tolerates whitespace, flag order
// and clustering (-la = -l -a), a leading "$", and reference placeholders (<file>, [dir]) matching any value.
// Positional arguments must keep their order.
func CommandsMatch(input, reference string) bool {
	a, r := parseCmd(input), parseCmd(reference)
	if a.cmd == "" || a.cmd != r.cmd {
		return false
	}
	if !sameRunes(a.shorts, r.shorts) || !sameStrs(a.longs, r.longs) || len(a.positional) != len(r.positional) {
		return false
	}
	for i, p := range r.positional {
		if !isPlaceholder(p) && p != a.positional[i] {
			return false
		}
	}
	return true
}

// CheckTask returns whether input matches any expected command, and which one.
func CheckTask(input string, expected []string) (correct bool, matched string) {
	for _, e := range expected {
		if CommandsMatch(input, e) {
			return true, e
		}
	}
	return false, ""
}

// CheckFill is an exact comparison after trimming.
func CheckFill(input string, expected []string) bool {
	in := domain.JSTrim(input)
	for _, e := range expected {
		if e == in {
			return true
		}
	}
	return false
}

// CmdLike is the part of a command that card building reads.
type CmdLike struct {
	ID, Name, Title, Explanation, WhatItDoes, Syntax string
	Mastery                                          int
	Options                                          []Option
	Examples                                         []Example
}

// Option is a command flag and what it does.
type Option struct{ Flag, Meaning string }

// Example is a command line and what it does.
type Example struct{ Command, Description string }

// SectionLike is a concept section as cards see it.
type SectionLike struct{ Key, Markdown string }

// ConceptLike is the part of a concept that card building reads.
type ConceptLike struct {
	ID, Title string
	Sections  []SectionLike
}

// hash is 32-bit FNV-1a over UTF-16 code units, like the TypeScript version.
func hash(s string) uint32 {
	h := uint32(2166136261)
	for _, u := range utf16.Encode([]rune(s)) {
		h = (h ^ uint32(u)) * 16777619
	}
	return h
}

func pickStr(xs []string, seed uint64) (string, bool) {
	if len(xs) == 0 {
		return "", false
	}
	return xs[seed%uint64(len(xs))], true
}

func pickIdx(xs []int, seed uint64) int { return xs[seed%uint64(len(xs))] }

func blank(md string) bool { return domain.JSTrim(blankBullets.ReplaceAllString(md, "")) == "" }

func trimmed(s string) string { return domain.JSTrim(s) }

// fillCandidates are the token indexes that can be blanked: flags and arguments, never the command itself.
func fillCandidates(command string) []int {
	toks := TokenizeCommand(command)
	var out []int
	for i := 1; i < len(toks); i++ {
		if !isPlaceholder(toks[i]) {
			out = append(out, i)
		}
	}
	return out
}

// AvailableCommandCards lists the card types a command's data can support.
func AvailableCommandCards(c CmdLike) []CardType {
	var out []CardType
	if trimmed(c.Explanation) != "" || trimmed(c.WhatItDoes) != "" {
		out = append(out, CardName)
	}
	described := trimmed(c.Explanation) != ""
	for _, e := range c.Examples {
		described = described || trimmed(e.Description) != ""
	}
	if len(c.Examples) > 0 && described {
		out = append(out, CardTask)
	}
	for _, o := range c.Options {
		if isRealFlag(o.Flag) && trimmed(o.Meaning) != "" {
			out = append(out, CardFlag)
			break
		}
	}
	for _, e := range c.Examples {
		if len(fillCandidates(e.Command)) > 0 {
			out = append(out, CardFill)
			break
		}
	}
	return out
}

var orderByMastery = [][]CardType{
	{CardName, CardTask},
	{CardName, CardTask, CardFlag},
	{CardTask, CardFlag, CardFill, CardName},
	{CardFill, CardTask, CardFlag},
}

func has(xs []CardType, t CardType) bool {
	for _, x := range xs {
		if x == t {
			return true
		}
	}
	return false
}

// PickCommandCardType chooses a card type suited to how well the command is known, rotating with seed.
func PickCommandCardType(c CmdLike, seed uint64) (CardType, bool) {
	avail := AvailableCommandCards(c)
	if len(avail) == 0 {
		return "", false
	}
	order := orderByMastery[0]
	if c.Mastery >= 0 && c.Mastery < len(orderByMastery) {
		order = orderByMastery[c.Mastery]
	}
	var pref []CardType
	for _, t := range order {
		if has(avail, t) {
			pref = append(pref, t)
		}
	}
	if len(pref) == 0 {
		pref = avail
	}
	return pref[seed%uint64(len(pref))], true
}

// BuildCommandCard builds a card of the given type, or reports false when the command's data cannot support it.
func BuildCommandCard(c CmdLike, t CardType, seed uint64) (Card, bool) {
	base := Card{EntityID: c.ID, Collection: "commands", Title: c.Name, Type: t}
	s := uint64(hash(c.ID)) + seed
	switch t {
	case CardName:
		answer := trimmed(c.Explanation)
		if answer == "" {
			answer = trimmed(c.WhatItDoes)
		}
		if answer == "" {
			return Card{}, false
		}
		base.Kind, base.Prompt, base.Answer = "reveal", "What does `"+c.Name+"` do?", answer
		return base, true
	case CardTask:
		var described []Example
		for _, e := range c.Examples {
			if trimmed(e.Description) != "" {
				described = append(described, e)
			}
		}
		var ex Example
		switch {
		case len(described) > 0:
			ex = described[s%uint64(len(described))]
		case trimmed(c.Explanation) != "" && len(c.Examples) > 0:
			ex = c.Examples[0]
		default:
			return Card{}, false
		}
		what := trimmed(ex.Description)
		if what == "" {
			what = trimmed(c.Explanation)
		}
		expected := []string{ex.Command}
		for _, e := range c.Examples {
			if trimmed(e.Description) == trimmed(ex.Description) || e == ex {
				expected = append(expected, e.Command)
			}
		}
		base.Kind, base.Prompt, base.Answer = "type", "Type the command: "+what, ex.Command
		base.Expected = dedupe(expected)
		base.Hint = "starts with “" + c.Name + "”"
		return base, true
	case CardFlag:
		var flags []Option
		for _, o := range c.Options {
			if isRealFlag(o.Flag) && trimmed(o.Meaning) != "" {
				flags = append(flags, o)
			}
		}
		if len(flags) == 0 {
			return Card{}, false
		}
		o := flags[s%uint64(len(flags))]
		base.Kind, base.Prompt, base.Answer = "reveal", "In `"+c.Name+"`, what does `"+o.Flag+"` do?", o.Meaning
		return base, true
	case CardFill:
		var cands []Example
		for _, e := range c.Examples {
			if len(fillCandidates(e.Command)) > 0 {
				cands = append(cands, e)
			}
		}
		if len(cands) == 0 {
			return Card{}, false
		}
		ex := cands[s%uint64(len(cands))]
		toks := TokenizeCommand(ex.Command)
		idxs := fillCandidates(ex.Command)
		var flagIdx []int
		for _, i := range idxs {
			if strings.HasPrefix(toks[i], "-") { // prefer flags: they are what you forget
				flagIdx = append(flagIdx, i)
			}
		}
		pool := idxs
		if len(flagIdx) > 0 {
			pool = flagIdx
		}
		i := pickIdx(pool, uint64(uint32(s))>>3) // like JavaScript's `s >>> 3`: wrap to 32 bits, then shift unsigned
		shown := make([]string, len(toks))
		for k, tk := range toks {
			shown[k] = tk
			if k == i {
				shown[k] = "____"
			}
		}
		base.Kind, base.Prompt, base.Answer = "type", "Fill in the blank: "+strings.Join(shown, " "), ex.Command
		base.Expected = []string{toks[i]}
		base.Hint = ex.Description
		return base, true
	}
	return Card{}, false
}

func dedupe(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

var quoteMark = regexp.MustCompile(`(?m)^>\s?`)

// BuildConceptCard builds a "teach" card from the intuition section or a "what" card from "What is it?", ignoring
// blank template bullets.
func BuildConceptCard(c ConceptLike, seed uint64) (Card, bool) {
	sec := func(key string) string {
		for _, s := range c.Sections {
			if s.Key == key {
				return s.Markdown
			}
		}
		return ""
	}
	intuition, what := sec("intuition-very-important"), sec("what-is-it")
	base := Card{EntityID: c.ID, Collection: "concepts", Title: c.Title, Kind: "reveal"}
	useTeach := !blank(intuition) && (blank(what) || (uint64(hash(c.ID))+seed)%2 == 0)
	if useTeach {
		base.Type = CardTeach
		base.Prompt = "Explain “" + c.Title + "” in your own words. What is it basically like…?"
		base.Answer = domain.JSTrim(quoteMark.ReplaceAllString(intuition, ""))
		return base, true
	}
	if !blank(what) {
		base.Type, base.Prompt, base.Answer = CardWhat, "What is “"+c.Title+"”?", what
		return base, true
	}
	return Card{}, false
}
