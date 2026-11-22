package importer

import (
	"net/url"
	"sort"
	"strings"
	"unicode/utf16"
)

// utf16Less orders strings the way JavaScript's default sort does (by UTF-16 code unit), which differs from byte order
// for characters outside the Basic Multilingual Plane such as emoji.
func utf16Less(a, b string) bool {
	ua, ub := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
	for i := 0; i < len(ua) && i < len(ub); i++ {
		if ua[i] != ub[i] {
			return ua[i] < ub[i]
		}
	}
	return len(ua) < len(ub)
}

func sortUTF16(s []string) { sort.SliceStable(s, func(i, j int) bool { return utf16Less(s[i], s[j]) }) }

// u16len is JavaScript's String.length.
func u16len(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// u16prefix is s.slice(0, n) for n UTF-16 units; a cut through a surrogate pair drops the half character.
func u16prefix(s string, n int) string {
	units := 0
	for i, r := range s {
		w := 1
		if r >= 0x10000 {
			w = 2
		}
		if units+w > n {
			return s[:i]
		}
		units += w
	}
	return s
}

// encodeURIComponent is JavaScript's: letters, digits and - _ . ! ~ * ' ( ) stay as they are.
func encodeURIComponent(s string) string {
	e := url.QueryEscape(s)
	e = strings.ReplaceAll(e, "+", "%20")
	for from, to := range map[string]string{"%21": "!", "%27": "'", "%28": "(", "%29": ")", "%2A": "*", "%7E": "~"} {
		e = strings.ReplaceAll(e, from, to)
	}
	return e
}
