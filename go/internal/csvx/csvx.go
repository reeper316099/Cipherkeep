// Package csvx reads and writes CSV the way PapaParse does, which is what the TypeScript importer and exporter use:
// the delimiter is guessed, quoted fields may hold newlines, duplicate headers get a numeric suffix, and a row shorter
// than the header simply has fewer cells. encoding/csv is stricter and rejects files Notion and spreadsheets produce.
package csvx

import (
	"strconv"
	"strings"
)

// Table is a parsed CSV with a header row.
type Table struct {
	Headers []string
	// Rows holds each data row's cells in header order. A short row has fewer cells than there are headers; extra cells
	// beyond the headers are dropped.
	Rows [][]string
}

// Options are the parse options (PapaParse's header: true mode is always used).
type Options struct {
	// SkipEmpty drops rows whose cells are all blank (skipEmptyLines: 'greedy').
	SkipEmpty bool
	// MaxRows stops after this many data rows (0: all). Used to read just the headers.
	MaxRows int
}

const bom = "\xef\xbb\xbf" // UTF-8 byte-order mark

var candidates = []rune{',', '\t', '|', ';', 0x1e, 0x1f}

// Parse reads text as a CSV with a header row.
func Parse(text string, o Options) Table {
	text = strings.TrimPrefix(text, bom)
	delim := guessDelimiter(text)
	limit := o.MaxRows
	rows := readRows(text, delim, 0)
	if o.SkipEmpty {
		kept := rows[:0:0]
		for _, r := range rows {
			if strings.TrimSpace(strings.Join(r, "")) != "" {
				kept = append(kept, r)
			}
		}
		rows = kept
	}
	if len(rows) == 0 {
		return Table{}
	}
	t := Table{Headers: dedupe(rows[0])}
	for _, r := range rows[1:] {
		if len(r) > len(t.Headers) {
			r = r[:len(t.Headers)]
		}
		t.Rows = append(t.Rows, r)
		if limit > 0 && len(t.Rows) >= limit {
			break
		}
	}
	return t
}

// dedupe renames repeated headers "a", "a" to "a", "a_1" as PapaParse 5.4 and later do.
func dedupe(h []string) []string {
	seen := map[string]int{}
	out := make([]string, len(h))
	for i, name := range h {
		if n, dup := seen[name]; dup {
			seen[name] = n + 1
			out[i] = name + "_" + strconv.Itoa(n)
		} else {
			seen[name] = 1
			out[i] = name
		}
	}
	return out
}

// readRows splits text into rows of cells. max > 0 stops after that many rows.
func readRows(text string, delim rune, max int) [][]string {
	var rows [][]string
	var row []string
	var cur strings.Builder
	rs := []rune(text)
	i := 0
	atRowStart := true
	endRow := func() {
		row = append(row, cur.String())
		cur.Reset()
		rows = append(rows, row)
		row = nil
		atRowStart = true
	}
	for i < len(rs) {
		if max > 0 && len(rows) >= max {
			return rows
		}
		c := rs[i]
		switch {
		case c == '"' && cur.Len() == 0:
			// quoted field: runs to the closing quote; "" is a literal quote
			i++
			for i < len(rs) {
				if rs[i] == '"' {
					if i+1 < len(rs) && rs[i+1] == '"' {
						cur.WriteRune('"')
						i += 2
						continue
					}
					i++
					break
				}
				cur.WriteRune(rs[i])
				i++
			}
			// anything between the closing quote and the delimiter is kept as written
			for i < len(rs) && rs[i] != delim && rs[i] != '\n' && rs[i] != '\r' {
				cur.WriteRune(rs[i])
				i++
			}
			atRowStart = false
		case c == delim:
			row = append(row, cur.String())
			cur.Reset()
			i++
			atRowStart = false
		case c == '\r' || c == '\n':
			if c == '\r' && i+1 < len(rs) && rs[i+1] == '\n' {
				i++
			}
			i++
			endRow()
		default:
			cur.WriteRune(c)
			i++
			atRowStart = false
		}
	}
	if !atRowStart || cur.Len() > 0 || len(row) > 0 {
		endRow()
	}
	return rows
}

// guessDelimiter picks the delimiter whose first ten rows have the most consistent number of fields, as PapaParse does.
func guessDelimiter(text string) rune {
	best, bestDelta, bestAvg := ',', 1<<30, 0.0
	for _, d := range candidates {
		rows := readRows(text, d, 10)
		var delta, total, count int
		prev := -1
		for _, r := range rows {
			if len(r) == 1 && r[0] == "" {
				continue // empty line
			}
			n := len(r)
			total += n
			count++
			if prev >= 0 {
				if n > prev {
					delta += n - prev
				} else {
					delta += prev - n
				}
			}
			prev = n
		}
		if count == 0 {
			continue
		}
		avg := float64(total) / float64(count)
		if (bestDelta == 1<<30 || delta <= bestDelta) && avg > 1.99 {
			if bestDelta != 1<<30 && delta == bestDelta && avg <= bestAvg {
				continue
			}
			best, bestDelta, bestAvg = d, delta, avg
		}
	}
	return best
}

func needsQuotes(s string) bool {
	if s == "" {
		return false
	}
	return strings.ContainsAny(s, "\r\n\",") || strings.Contains(s, bom) || s[0] == ' ' || s[len(s)-1] == ' '
}

func field(s string) string {
	if needsQuotes(s) {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return s
}

// Unparse writes a header and rows with "\n" line breaks and no trailing newline (PapaParse.unparse with a "\n"
// newline). Fields are quoted when they hold a comma, quote, line break or edge space.
func Unparse(fields []string, rows [][]string) string {
	var b strings.Builder
	write := func(r []string) {
		for i, s := range r {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(field(s))
		}
	}
	write(fields)
	for _, r := range rows {
		b.WriteByte('\n')
		write(r)
	}
	return b.String()
}
