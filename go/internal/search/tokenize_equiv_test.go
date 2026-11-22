package search

import (
	"math/rand"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// The reference tokenizer: the regexp version the hand-written one replaced (itself a port of the TypeScript code).
var (
	refSeparators = regexp.MustCompile("[\\s,;()\"'`<>|{}\\[\\]\\x{a0}\\x{1680}\\x{2000}-\\x{200a}\\x{2028}\\x{2029}\\x{202f}\\x{205f}\\x{3000}\\x{feff}]+")
	refLead       = regexp.MustCompile(`^[:*]+`)
	refTail       = regexp.MustCompile(`[.:!?*]+$`)
	refPath       = regexp.MustCompile(`[/\\.\-_~=]+`)
)

func refTokenize(text string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, raw := range refSeparators.Split(strings.ToLower(text), -1) {
		chunk := refTail.ReplaceAllString(refLead.ReplaceAllString(raw, ""), "")
		if chunk == "" {
			continue
		}
		add(chunk)
		dashless := strings.TrimLeft(chunk, "-")
		if dashless != chunk {
			add(dashless)
		}
		for _, part := range strings.Split(chunk, "=") {
			if part != "" && part != chunk {
				add(part)
				add(strings.TrimLeft(part, "-"))
			}
		}
		for _, seg := range refPath.Split(dashless, -1) {
			add(seg)
		}
	}
	return out
}

func TestHandWrittenTokenizerMatchesTheRegexpReference(t *testing.T) {
	alphabet := []string{"a", "B", "z", "9", "-", "--", "=", ".", ":", "*", "!", "?", "/", `\`, "_", "~", " ", "  ", "\t", "\n", "\u00a0", "\u3000", "\ufeff",
		",", ";", "(", ")", `"`, "'", "`", "<", ">", "|", "{", "}", "[", "]", "é", "Ünï", "日本", "😀", "-la", "--color=auto", "/etc/passwd", "[[:upper:]]*", "A.B"}
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 30000; i++ {
		var b strings.Builder
		for n := rng.Intn(14); n >= 0; n-- {
			b.WriteString(alphabet[rng.Intn(len(alphabet))])
		}
		in := b.String()
		got, want := Tokenize(in), refTokenize(in)
		if len(got) == 0 && len(want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("input %q\n got %q\nwant %q", in, got, want)
		}
	}
	// And on the fixed examples from the TypeScript tests.
	for _, in := range []string{"-la", "--color=auto", "/etc/passwd", "[[:upper:]]*", "Hello, World! (again)", ""} {
		if got, want := Tokenize(in), refTokenize(in); !reflect.DeepEqual(got, want) && !(len(got) == 0 && len(want) == 0) {
			t.Errorf("%q: %q vs %q", in, got, want)
		}
	}
}
