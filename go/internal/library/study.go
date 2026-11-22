package library

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/reeper316099/Cipherkeep/go/internal/apperr"
	"github.com/reeper316099/Cipherkeep/go/internal/domain"
	"github.com/reeper316099/Cipherkeep/go/internal/jsonstore"
	"github.com/reeper316099/Cipherkeep/go/internal/repo"
	"github.com/reeper316099/Cipherkeep/go/internal/study"
)

// StateFile is study/srs-state.json: the scheduling state of every card that has been reviewed.
type StateFile struct {
	Items map[string]study.SrsState `json:"items"`
}

func cloneState(s StateFile) StateFile {
	out := StateFile{Items: make(map[string]study.SrsState, len(s.Items))}
	for k, v := range s.Items {
		out.Items[k] = v
	}
	return out
}

// QueueItem is one card to show.
type QueueItem struct {
	Card  study.Card     `json:"card"`
	IsNew bool           `json:"isNew"`
	DueAt string         `json:"dueAt,omitempty"`
	Srs   study.SrsState `json:"srs"`
}

// Queue is what to study now.
type Queue struct {
	Items        []QueueItem `json:"items"`
	Due          int         `json:"due"`
	NewAvailable int         `json:"newAvailable"`
	NewToday     int         `json:"newToday"`
}

// QueueOptions narrow the queue.
type QueueOptions struct {
	Collection string // "commands", "concepts" or both
	Limit      int    // default 20
	OnlyID     string // practise one entity on demand
	Now        time.Time
	TzOffset   int
	// CountOnly skips ordering the cards: only Due, NewAvailable and NewToday are filled in (what the stats need).
	CountOnly bool
}

// OpenQuestion is an unanswered "Questions I still have" bullet.
type OpenQuestion struct {
	ConceptID string `json:"conceptId"`
	Concept   string `json:"concept"`
	Text      string `json:"text"`
	Line      int    `json:"line"`
	Done      bool   `json:"done"`
}

// Answered is the result of answering a card.
type Answered struct {
	Srs     study.SrsState `json:"srs"`
	Mastery int            `json:"mastery"`
}

// StudyStats are the figures the study page and dashboard show.
type StudyStats struct {
	Due           int  `json:"due"`
	NewAvailable  int  `json:"newAvailable"`
	NewToday      int  `json:"newToday"`
	ReviewedToday int  `json:"reviewedToday"`
	Reviews30d    int  `json:"reviews30d"`
	Retention30d  *int `json:"retention30d"`
	NewPerDay     int  `json:"newPerDay"`
}

// SessionRow is one line of the review log.
type SessionRow struct {
	Ts         string `json:"ts"`
	ID         string `json:"id"`
	Collection string `json:"collection"`
	Rating     string `json:"rating"`
	Type       string `json:"type,omitempty"`
	Ms         int    `json:"ms,omitempty"`
	IsNew      bool   `json:"isNew,omitempty"`
}

// StudyService schedules and records reviews.
type StudyService struct {
	lib  *Library
	mu   sync.Mutex
	file *jsonstore.JsonFile[StateFile]
}

// NewStudyService creates the service.
func NewStudyService(l *Library) *StudyService { return &StudyService{lib: l} }

func (s *StudyService) state() (*jsonstore.JsonFile[StateFile], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file == nil {
		f, err := jsonstore.Open(s.lib.Store, "study/srs-state.json", jsonstore.Options[StateFile]{
			Version:  1,
			Defaults: func() StateFile { return StateFile{Items: map[string]study.SrsState{}} },
			Clone:    cloneState,
			Validate: func(f *StateFile) error {
				if f.Items == nil {
					f.Items = map[string]study.SrsState{}
				}
				return nil
			},
		})
		if err != nil {
			return nil, err
		}
		s.file = f
	}
	return s.file, nil
}

func cmdLike(e domain.Entity) study.CmdLike {
	c := study.CmdLike{
		ID: e.ID(), Name: e.Str("name"), Title: e.Title(), Explanation: e.Str("explanation"),
		WhatItDoes: e.Str("whatItDoes"), Syntax: e.Str("syntax"), Mastery: int(e.Num("mastery")),
	}
	for _, o := range e.Objs("options") {
		c.Options = append(c.Options, study.Option{Flag: str(o["flag"]), Meaning: str(o["meaning"])})
	}
	for _, x := range e.Objs("examples") {
		c.Examples = append(c.Examples, study.Example{Command: str(x["command"]), Description: str(x["description"])})
	}
	return c
}

func conceptLike(e domain.Entity) study.ConceptLike {
	c := study.ConceptLike{ID: e.ID(), Title: e.Title()}
	for _, s := range e.Objs("sections") {
		c.Sections = append(c.Sections, study.SectionLike{Key: str(s["key"]), Markdown: str(s["markdown"])})
	}
	return c
}

func str(v any) string { s, _ := v.(string); return s }

func (s *StudyService) cardFor(collection string, e domain.Entity, st *study.SrsState) (study.Card, bool) {
	seed := uint64(0)
	if st != nil {
		seed = uint64(st.Reps + st.Lapses)
	}
	if collection == "concepts" {
		return study.BuildConceptCard(conceptLike(e), seed)
	}
	c := cmdLike(e)
	t, ok := study.PickCommandCardType(c, seed)
	if !ok {
		return study.Card{}, false
	}
	return study.BuildCommandCard(c, t, seed)
}

// Queue lists the cards due now (oldest first), then new cards up to the daily limit.
func (s *StudyService) Queue(o QueueOptions) (Queue, error) {
	now := o.Now
	if now.IsZero() {
		now = time.Now()
	}
	limit := o.Limit
	if limit <= 0 {
		limit = 20
	}
	file, err := s.state()
	if err != nil {
		return Queue{}, err
	}
	items := file.Data().Items
	cols := []string{"commands", "concepts"}
	if o.Collection != "" {
		cols = []string{o.Collection}
	}
	today := study.DayKey(now, o.TzOffset)
	newToday := 0
	for _, st := range items {
		if st.FirstAt != "" && study.DayKeyOf(st.FirstAt, o.TzOffset) == today {
			newToday++
		}
	}
	quota := max(0, s.lib.Settings.Data().StudyNewPerDay-newToday)

	var due, fresh []QueueItem
	for _, collection := range cols {
		for _, e := range s.lib.Repos[collection].All() {
			if e.Archived() || (o.OnlyID != "" && e.ID() != o.OnlyID) {
				continue
			}
			st, has := items[e.ID()]
			var stp *study.SrsState
			if has {
				stp = &st
			}
			card, ok := s.cardFor(collection, e, stp)
			if !ok {
				continue
			}
			switch {
			case o.OnlyID != "":
				srs := study.NewSrs()
				if has {
					srs = st
				}
				due = append(due, QueueItem{Card: card, IsNew: !has || st.LastAt == "", DueAt: st.DueAt, Srs: srs})
			case has && st.LastAt != "":
				if study.IsDue(&st, now) {
					due = append(due, QueueItem{Card: card, DueAt: st.DueAt, Srs: st})
				}
			default:
				fresh = append(fresh, QueueItem{Card: card, IsNew: true, Srs: study.NewSrs()})
			}
		}
	}
	if o.CountOnly {
		return Queue{Items: []QueueItem{}, Due: len(due), NewAvailable: len(fresh), NewToday: newToday}, nil
	}
	dueTime := func(q QueueItem) time.Time { t, _ := study.ParseTime(q.DueAt); return t }
	sort.SliceStable(due, func(i, j int) bool { return dueTime(due[i]).Before(dueTime(due[j])) })
	keys := make([][]byte, len(fresh))
	for i := range fresh {
		keys[i] = repo.TitleKey(fresh[i].Card.Title)
	}
	order := make([]int, len(fresh))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return bytes.Compare(keys[order[a]], keys[order[b]]) < 0 })
	sortedFresh := make([]QueueItem, len(fresh))
	for i, j := range order {
		sortedFresh[i] = fresh[j]
	}
	fresh = sortedFresh
	picked := due
	if o.OnlyID == "" {
		picked = append(append([]QueueItem{}, due...), fresh[:min(quota, len(fresh))]...)
		picked = picked[:min(limit, len(picked))]
	}
	if picked == nil {
		picked = []QueueItem{}
	}
	return Queue{Items: picked, Due: len(due), NewAvailable: len(fresh), NewToday: newToday}, nil
}

// AnswerInput is a review.
type AnswerInput struct {
	ID         string
	Rating     study.Rating
	Collection string
	Ms         int
	CardType   string
	Now        time.Time
}

// Answer schedules the card, updates the entity's mastery, and logs the review.
func (s *StudyService) Answer(in AnswerInput) (Answered, error) {
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	collection := in.Collection
	if collection == "" {
		collection = "concepts"
		if _, ok := s.lib.Repos["commands"].Get(in.ID); ok {
			collection = "commands"
		}
	}
	e, ok := s.lib.Repos[collection].Get(in.ID)
	if !ok {
		return Answered{}, apperr.NotFound(collection, in.ID)
	}
	file, err := s.state()
	if err != nil {
		return Answered{}, err
	}
	prev, had := file.Data().Items[in.ID]
	if !had {
		prev = study.NewSrs()
	}
	next := study.Schedule(prev, in.Rating, now)
	if next.FirstAt == "" {
		next.FirstAt = domain.ISO(now)
	}
	if _, err := file.Update(func(d *StateFile) error { d.Items[in.ID] = next; return nil }); err != nil {
		return Answered{}, err
	}
	mastery := study.MasteryFromSrs(next)
	if collection == "commands" {
		srs := map[string]any{"ease": next.Ease, "intervalDays": next.IntervalDays}
		if next.DueAt != "" {
			srs["dueAt"] = next.DueAt
		}
		if _, err := s.lib.Repos["commands"].Patch(in.ID, map[string]any{
			"srs": srs, "mastery": float64(mastery), "lastPracticedAt": domain.ISO(now), "practiceCount": e.Num("practiceCount") + 1,
		}); err != nil {
			return Answered{}, err
		}
	} else if _, err := s.lib.Repos["concepts"].Patch(in.ID, map[string]any{"confidence": float64(mastery)}); err != nil {
		return Answered{}, err
	}
	if err := s.log(SessionRow{Ts: domain.ISO(now), ID: in.ID, Collection: collection, Rating: string(in.Rating), Type: in.CardType,
		Ms: in.Ms, IsNew: prev.LastAt == ""}); err != nil {
		return Answered{}, err
	}
	_ = s.lib.Activity.RecordAt(now, "study", collection, in.ID)
	return Answered{Srs: next, Mastery: mastery}, nil
}

func (s *StudyService) log(row SessionRow) error {
	rel := "study/sessions/" + row.Ts[:7] + ".jsonl"
	b, err := json.Marshal(row)
	if err != nil {
		return err
	}
	line, err := s.lib.Store.Codec.EncodeLine(string(b))
	if err != nil {
		return err
	}
	defer s.lib.Store.Lock(rel)()
	return appendLine(s.lib.Store, rel, line)
}

// Sessions returns the review log between two days (by UTC month).
func (s *StudyService) Sessions(fromDay, toDay string) []SessionRow {
	var out []SessionRow
	y, m := atoi(fromDay[:4]), atoi(fromDay[5:7])
	ty, tm := atoi(toDay[:4]), atoi(toDay[5:7])
	for y < ty || (y == ty && m <= tm) {
		for _, l := range readLines(s.lib.Store, fmt.Sprintf("study/sessions/%d-%02d.jsonl", y, m)) {
			var r SessionRow
			if json.Unmarshal([]byte(l), &r) == nil {
				out = append(out, r)
			}
		}
		if m++; m > 12 {
			m, y = 1, y+1
		}
	}
	return out
}

// DueCounts is how many studiable entities are due now, and how many are overdue by more than graceDays.
func (s *StudyService) DueCounts(now time.Time, graceDays int) (due, overdue int, err error) {
	file, err := s.state()
	if err != nil {
		return 0, 0, err
	}
	items := file.Data().Items
	for _, collection := range []string{"commands", "concepts"} {
		for _, e := range s.lib.Repos[collection].All() {
			st, ok := items[e.ID()]
			if e.Archived() || !ok || st.LastAt == "" || !study.IsDue(&st, now) {
				continue
			}
			due++
			if t, ok := study.ParseTime(st.DueAt); ok && now.Sub(t) > time.Duration(graceDays)*24*time.Hour {
				overdue++
			}
		}
	}
	return due, overdue, nil
}

// Stats returns the study figures.
func (s *StudyService) Stats(now time.Time, tzOffset int) (StudyStats, error) {
	if now.IsZero() {
		now = time.Now()
	}
	today := study.DayKey(now, tzOffset)
	q, err := s.Queue(QueueOptions{Now: now, TzOffset: tzOffset, CountOnly: true})
	if err != nil {
		return StudyStats{}, err
	}
	thirty := 30 * 24 * time.Hour
	rows := s.Sessions(study.DayKey(now.Add(-thirty), tzOffset), today)
	recent, todayCount, passed := 0, 0, 0
	for _, r := range rows {
		t, ok := study.ParseTime(r.Ts)
		if ok && !t.Before(now.Add(-thirty)) {
			recent++
			if r.Rating == "good" || r.Rating == "easy" {
				passed++
			}
		}
		if study.DayKeyOf(r.Ts, tzOffset) == today {
			todayCount++
		}
	}
	perDay := s.lib.Settings.Data().StudyNewPerDay
	st := StudyStats{
		Due: q.Due, NewAvailable: min(q.NewAvailable, max(0, perDay-q.NewToday)), NewToday: q.NewToday,
		ReviewedToday: todayCount, Reviews30d: recent, NewPerDay: perDay,
	}
	if recent > 0 {
		r := int(float64(passed)/float64(recent)*100 + 0.5)
		st.Retention30d = &r
	}
	return st, nil
}

// Questions lists every "Questions I still have" bullet across concepts (the unanswered ones unless includeDone).
func (s *StudyService) Questions(includeDone bool) []OpenQuestion {
	out := []OpenQuestion{}
	for _, c := range s.lib.Repos["concepts"].All() {
		if c.Archived() {
			continue
		}
		for _, sec := range c.Objs("sections") {
			if strings.TrimSpace(str(sec["key"])) != domain.QuestionsKey {
				continue
			}
			for _, q := range domain.ParseQuestions(str(sec["markdown"])) {
				if includeDone || !q.Done {
					out = append(out, OpenQuestion{ConceptID: c.ID(), Concept: c.Title(), Text: q.Text, Line: q.Line, Done: q.Done})
				}
			}
			break
		}
	}
	return out
}
