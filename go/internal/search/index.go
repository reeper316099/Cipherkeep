package search

import (
	"math"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/reeper316099/Cipherkeep/go/internal/domain"
	"github.com/reeper316099/Cipherkeep/go/internal/repo"
)

// BM25 parameters and match weights, the MiniSearch defaults.
const (
	bm25K        = 1.2
	bm25B        = 0.7
	bm25D        = 0.5
	prefixWeight = 0.375
	fuzzyWeight  = 0.45
	fuzzyAmount  = 0.2
	maxFuzzy     = 6
)

type termEntry struct {
	fields [numFields][]int32 // sorted doc ids per field
}

type docRec struct {
	Doc
	short int32
	lens  [numFields]int32
	terms [numFields][]string
}

// Index is the in-memory search index over every collection.
type Index struct {
	mu       sync.RWMutex
	dict     map[string]*termEntry
	sorted   []string
	sortedOK bool
	byLen    map[int]map[string]struct{} // terms grouped by length, so typo matching only looks at plausible lengths
	docs     []*docRec
	byKey    map[string]int32
	count    int
	totalLen [numFields]int64
}

// NewIndex returns an empty index.
func NewIndex() *Index {
	return &Index{dict: map[string]*termEntry{}, byKey: map[string]int32{}, sortedOK: true, byLen: map[int]map[string]struct{}{}}
}

// tokenized is a document with its fields already split into terms.
type tokenized struct {
	doc  Doc
	toks [numFields][]string
}

func tokenizeDoc(d Doc) tokenized {
	t := tokenized{doc: d}
	for f := 0; f < numFields; f++ {
		t.toks[f] = Tokenize(d.field(f))
	}
	return t
}

func (ix *Index) addLocked(d Doc) { ix.addTokenized(tokenizeDoc(d)) }

func (ix *Index) addTokenized(td tokenized) {
	d := td.doc
	short := int32(len(ix.docs))
	rec := &docRec{Doc: d, short: short}
	for f := 0; f < numFields; f++ {
		toks := td.toks[f]
		rec.lens[f], rec.terms[f] = int32(len(toks)), toks
		ix.totalLen[f] += int64(len(toks))
		for _, t := range toks {
			e := ix.dict[t]
			if e == nil {
				e = &termEntry{}
				ix.dict[t] = e
				ix.lenSet(t)[t] = struct{}{}
				if ix.sortedOK {
					i := sort.SearchStrings(ix.sorted, t)
					ix.sorted = append(ix.sorted, "")
					copy(ix.sorted[i+1:], ix.sorted[i:])
					ix.sorted[i] = t
				}
			}
			e.fields[f] = append(e.fields[f], short)
		}
	}
	ix.docs = append(ix.docs, rec)
	ix.byKey[d.Key] = short
	ix.count++
}

func (ix *Index) lenSet(t string) map[string]struct{} {
	n := lengthOf(t)
	m := ix.byLen[n]
	if m == nil {
		m = map[string]struct{}{}
		ix.byLen[n] = m
	}
	return m
}

func (ix *Index) removeLocked(key string) {
	short, ok := ix.byKey[key]
	if !ok {
		return
	}
	rec := ix.docs[short]
	for f := 0; f < numFields; f++ {
		ix.totalLen[f] -= int64(rec.lens[f])
		for _, t := range rec.terms[f] {
			e := ix.dict[t]
			if e == nil {
				continue
			}
			p := e.fields[f]
			if i := sort.Search(len(p), func(i int) bool { return p[i] >= short }); i < len(p) && p[i] == short {
				e.fields[f] = append(p[:i], p[i+1:]...)
			}
			empty := true
			for _, fp := range e.fields {
				empty = empty && len(fp) == 0
			}
			if empty {
				delete(ix.dict, t)
				delete(ix.lenSet(t), t)
				if ix.sortedOK {
					if i := sort.SearchStrings(ix.sorted, t); i < len(ix.sorted) && ix.sorted[i] == t {
						ix.sorted = append(ix.sorted[:i], ix.sorted[i+1:]...)
					}
				}
			}
		}
	}
	ix.docs[short] = nil
	delete(ix.byKey, key)
	ix.count--
}

func (ix *Index) ensureSorted() {
	if ix.sortedOK {
		return
	}
	ix.sorted = ix.sorted[:0]
	for t := range ix.dict {
		ix.sorted = append(ix.sorted, t)
	}
	sort.Strings(ix.sorted)
	ix.sortedOK = true
}

// Upsert adds or replaces one entity.
func (ix *Index) Upsert(typ string, e domain.Entity) {
	d := ToDoc(typ, e)
	ix.mu.Lock()
	ix.removeLocked(d.Key)
	ix.addLocked(d)
	ix.mu.Unlock()
}

// Remove drops one entity.
func (ix *Index) Remove(typ, id string) {
	ix.mu.Lock()
	ix.removeLocked(typ + ":" + id)
	ix.mu.Unlock()
}

// Rebuild replaces everything indexed for a collection.
func (ix *Index) Rebuild(typ string, all []domain.Entity) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	var stale []string
	for _, rec := range ix.docs {
		if rec != nil && rec.Type == typ {
			stale = append(stale, rec.Key)
		}
	}
	bulk := len(all)+len(stale) > 200 // a big change is cheaper to re-sort once than to insert term by term
	if bulk {
		ix.sortedOK = false
	}
	for _, k := range stale {
		ix.removeLocked(k)
	}
	// Splitting text into terms is the expensive part and each document is independent, so it runs in parallel;
	// the results are merged into the index in order.
	docs := make([]tokenized, len(all))
	workers := min(runtime.NumCPU(), 8)
	if len(all) < 64 {
		workers = 1
	}
	var wg sync.WaitGroup
	next := make(chan int, 256)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				docs[i] = tokenizeDoc(ToDoc(typ, all[i]))
			}
		}()
	}
	for i := range all {
		next <- i
	}
	close(next)
	wg.Wait()
	for _, d := range docs {
		ix.addTokenized(d)
	}
}

// Attach indexes a repository now and keeps the index current as it changes.
func (ix *Index) Attach(r repo.Repo) {
	ix.Rebuild(r.Name(), r.All())
	name := r.Name()
	r.OnUpsert(func(e domain.Entity) { ix.Upsert(name, e) })
	r.OnRemove(func(id string) { ix.Remove(name, id) })
	r.OnRebuild(func() { ix.Rebuild(name, r.All()) })
}

// Hit is one search result.
type Hit struct {
	Type      string  `json:"type"`
	ID        string  `json:"id"`
	Title     string  `json:"title"`
	Category  string  `json:"category"`
	Score     float64 `json:"score"`
	Highlight string  `json:"highlight"`
}

// Options narrow a search.
type Options struct {
	Limit int // default 50
	Types []string
}

// Filters are the operators parsed out of a query.
type Filters struct {
	Types    []string
	HasTypes bool
	Tags     []string
	Cats     []string
	Statuses []string
}

var (
	operators = map[string]bool{"cmd": true, "tag": true, "cat": true, "status": true, "type": true}
	typeAlias = map[string]string{
		"cmd": "commands", "command": "commands", "commands": "commands", "concept": "concepts", "concepts": "concepts",
		"lab": "labs", "labs": "labs", "project": "projects", "projects": "projects", "log": "log",
		"note": "notes", "notes": "notes", "snippet": "snippets", "snippets": "snippets",
	}
	queryRe = regexp.MustCompile(`(\w+):(?:"([^"]*)"|(\S*))|(\S+)`)
)

// ParseQuery splits `cmd:ls tag:linux cat:"File System" status:done free text` into filters and text.
func ParseQuery(q string) (text string, f Filters) {
	var terms []string
	for _, m := range queryRe.FindAllStringSubmatch(q, -1) {
		key := strings.ToLower(m[1])
		if key == "" || !operators[key] {
			terms = append(terms, m[0])
			continue
		}
		val := m[2]
		if val == "" {
			val = m[3]
		}
		val = domain.JSTrim(val)
		switch key {
		case "cmd":
			f.Types, f.HasTypes = append(f.Types, "commands"), true
			if val != "" {
				terms = append(terms, val)
			}
		case "type":
			if t, ok := typeAlias[strings.ToLower(val)]; ok {
				f.Types, f.HasTypes = append(f.Types, t), true
			}
		case "tag":
			f.Tags = append(f.Tags, strings.ToLower(val))
		case "cat":
			f.Cats = append(f.Cats, strings.ToLower(val))
		default:
			f.Statuses = append(f.Statuses, strings.ToLower(val))
		}
	}
	return strings.Join(terms, " "), f
}

func (f Filters) any() bool {
	return f.HasTypes || len(f.Tags) > 0 || len(f.Cats) > 0 || len(f.Statuses) > 0
}

func lengthOf(s string) int { return utf8.RuneCountInString(s) }

// accumulate adds the BM25 score of every doc in a term's postings, scaled by the match weight.
func (ix *Index) accumulate(res map[int32]float64, e *termEntry, weight float64) {
	n := float64(ix.count)
	for f := 0; f < numFields; f++ {
		p := e.fields[f]
		if len(p) == 0 {
			continue
		}
		df := float64(len(p))
		idf := math.Log(1 + (n-df+0.5)/(df+0.5))
		avg := float64(ix.totalLen[f]) / n
		fieldWeight := weight * boosts[f]
		for _, short := range p {
			rec := ix.docs[short]
			if rec == nil {
				continue
			}
			norm := 1.0 - bm25B + bm25B*float64(rec.lens[f])/avg // a posting implies a non-empty field, so avg > 0
			raw := idf * (bm25D + 1*(bm25K+1)/(1+bm25K*norm))
			res[short] += fieldWeight * raw
		}
	}
}

// termResults is every doc matching one query term exactly, by prefix, or fuzzily, with its summed score.
func (ix *Index) termResults(term string) map[int32]float64 {
	res := map[int32]float64{}
	if e := ix.dict[term]; e != nil {
		ix.accumulate(res, e, 1)
	}
	tl := lengthOf(term)
	handled := map[string]bool{term: true}
	// prefix matches
	i := sort.SearchStrings(ix.sorted, term)
	for ; i < len(ix.sorted) && strings.HasPrefix(ix.sorted[i], term); i++ {
		t := ix.sorted[i]
		if t == term {
			continue
		}
		handled[t] = true
		dist := float64(lengthOf(t) - tl)
		ix.accumulate(res, ix.dict[t], prefixWeight*float64(lengthOf(t))/(float64(lengthOf(t))+0.3*dist))
	}
	// fuzzy matches: only for terms longer than 4 characters
	if tl > 4 {
		maxDist := int(math.Min(maxFuzzy, math.Floor(float64(tl)*fuzzyAmount+0.5)))
		if maxDist > 0 {
			for lt := tl - maxDist; lt <= tl+maxDist; lt++ {
				for t := range ix.byLen[lt] {
					if handled[t] {
						continue
					}
					if d := levenshtein(term, t, maxDist); d > 0 && d <= maxDist {
						ix.accumulate(res, ix.dict[t], fuzzyWeight*float64(lt)/(float64(lt)+float64(d)))
					}
				}
			}
		}
	}
	return res
}

// levenshtein is the edit distance between a and b, or maxDist+1 as soon as it is known to exceed maxDist.
func levenshtein(a, b string, maxDist int) int {
	ra, rb := []rune(a), []rune(b)
	if abs(len(ra)-len(rb)) > maxDist {
		return maxDist + 1
	}
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		rowMin := cur[0]
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			rowMin = min(rowMin, cur[j])
		}
		if rowMin > maxDist {
			return maxDist + 1
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func has(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// Search runs a query. Every term must match (AND); each term also matches by prefix and, for longer terms, with a
// small typo. Results are ordered by score.
func (ix *Index) Search(q string, opts Options) []Hit {
	text, filters := ParseQuery(q)
	types := append(append([]string{}, filters.Types...), opts.Types...)
	keep := func(r *docRec) bool {
		if len(types) > 0 && !has(types, r.Type) {
			return false
		}
		tags := strings.Fields(strings.ToLower(r.Tags))
		for _, t := range filters.Tags {
			if !has(tags, t) {
				return false
			}
		}
		for _, c := range filters.Cats {
			if strings.ToLower(r.Category) != c {
				return false
			}
		}
		for _, s := range filters.Statuses {
			if strings.ToLower(r.Status) != s {
				return false
			}
		}
		return true
	}
	// Searches run in parallel; only re-sorting the term list (after a big rebuild) needs the write lock.
	ix.mu.RLock()
	if !ix.sortedOK {
		ix.mu.RUnlock()
		ix.mu.Lock()
		ix.ensureSorted()
		ix.mu.Unlock()
		ix.mu.RLock()
	}
	defer ix.mu.RUnlock()
	type scored struct {
		short int32
		score float64
	}
	var results []scored
	switch {
	case domain.JSTrim(text) != "":
		terms := Tokenize(text)
		var sets []map[int32]float64
		for _, t := range terms {
			sets = append(sets, ix.termResults(t))
		}
		if len(sets) > 0 {
			for short, s := range sets[0] {
				total, all := s, true
				for _, other := range sets[1:] {
					o, ok := other[short]
					if !ok {
						all = false
						break
					}
					total += o
				}
				if all {
					results = append(results, scored{short, total * float64(len(terms))})
				}
			}
		}
		sort.Slice(results, func(i, j int) bool {
			if results[i].score != results[j].score {
				return results[i].score > results[j].score
			}
			return results[i].short < results[j].short
		})
	case filters.any():
		for _, rec := range ix.docs {
			if rec != nil {
				results = append(results, scored{rec.short, 1}) // MiniSearch scores a wildcard match as 1
			}
		}
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}
	hl := Tokenize(text)
	var out []Hit
	for _, r := range results {
		rec := ix.docs[r.short]
		if rec == nil || !keep(rec) {
			continue
		}
		out = append(out, Hit{Type: rec.Type, ID: rec.ID, Title: rec.Title, Category: rec.Category, Score: r.score,
			Highlight: snippet(rec.Body+"\n"+rec.Code, hl, 120)})
		if len(out) == limit {
			break
		}
	}
	return out
}

// snippet cuts a window of text around the earliest match. Positions count UTF-16 code units, like the JavaScript
// version, so an emoji takes two and the window lands in the same place.
func snippet(text string, terms []string, width int) string {
	lower := utf16.Encode([]rune(strings.ToLower(text)))
	units := utf16.Encode([]rune(text))
	at := -1
	for _, t := range terms {
		needle := utf16.Encode([]rune(strings.ToLower(strings.TrimLeft(t, "-"))))
		if i := indexUnits(lower, needle); i >= 0 && (at < 0 || i < at) {
			at = i
		}
	}
	cut := func(from, to int) string {
		to = min(to, len(units))
		if from >= to {
			return ""
		}
		return domain.JSTrim(string(utf16.Decode(units[from:to])))
	}
	if at < 0 {
		return cut(0, width)
	}
	start := max(0, at-30)
	prefix := ""
	if start > 0 {
		prefix = "…"
	}
	return prefix + cut(start, start+width)
}

func indexUnits(h, n []uint16) int {
	if len(n) == 0 {
		return 0
	}
	for i := 0; i+len(n) <= len(h); i++ {
		match := true
		for j := range n {
			if h[i+j] != n[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
