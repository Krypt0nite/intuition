// Package selection decides which questions a player (or a room) sees.
//
// It is deliberately not random(questionBank). Each slot in a session is
// planned (category rotation + difficulty arc), then filled by scoring every
// eligible question on novelty, cooldown, concept spacing, variant-group
// exclusion, difficulty fit and the player's weak spots. See docs/ARCHITECTURE.md.
package selection

import (
	"hash/fnv"
	"math"
	"math/rand"
	"sort"
	"time"

	"intuition/internal/content"
)

// History is what we know about one player × question.
type History struct {
	TimesSeen    int       `json:"timesSeen"`
	TimesCorrect int       `json:"timesCorrect"`
	TimesWrong   int       `json:"timesWrong"`
	LastSeenAt   time.Time `json:"lastSeenAt"`
	LastCorrect  bool      `json:"lastCorrect"`
}

// Profile is the selection-relevant view of a player (or merged room).
type Profile struct {
	Seen           map[string]History
	TotalAnswered  int
	RecentConcepts map[string]int // concepts touched by the last ~25 answers
}

func NewProfile() *Profile {
	return &Profile{Seen: map[string]History{}, RecentConcepts: map[string]int{}}
}

// Record updates the profile in memory after an answer (used by demo/sim).
func (p *Profile) Record(q *content.Question, correct bool, at time.Time) {
	h := p.Seen[q.ID]
	h.TimesSeen++
	if correct {
		h.TimesCorrect++
	} else {
		h.TimesWrong++
	}
	h.LastSeenAt, h.LastCorrect = at, correct
	p.Seen[q.ID] = h
	p.TotalAnswered++
	for _, c := range q.Concepts {
		p.RecentConcepts[c]++
	}
}

func (p *Profile) accuracy() float64 {
	c, w := 0, 0
	for _, h := range p.Seen {
		c += h.TimesCorrect
		w += h.TimesWrong
	}
	return (float64(c) + 1) / (float64(c+w) + 2)
}

// Merge combines several players into one profile so a room avoids questions
// that ANY player saw recently, and targets the group's median experience.
func Merge(ps []*Profile) *Profile {
	out := NewProfile()
	var totals []int
	for _, p := range ps {
		if p == nil {
			continue
		}
		for id, h := range p.Seen {
			cur, ok := out.Seen[id]
			if !ok {
				out.Seen[id] = h
				continue
			}
			if h.LastSeenAt.After(cur.LastSeenAt) {
				cur.LastSeenAt, cur.LastCorrect = h.LastSeenAt, h.LastCorrect
			}
			cur.TimesSeen = max(cur.TimesSeen, h.TimesSeen)
			cur.TimesCorrect += h.TimesCorrect
			cur.TimesWrong += h.TimesWrong
			out.Seen[id] = cur
		}
		for c, n := range p.RecentConcepts {
			out.RecentConcepts[c] += n
		}
		totals = append(totals, p.TotalAnswered)
	}
	if len(totals) > 0 {
		sort.Ints(totals)
		out.TotalAnswered = totals[len(totals)/2]
	}
	return out
}

type Config struct {
	HardCooldown   time.Duration // never repeat inside this window if any alternative exists
	SoftCooldown   time.Duration // repeats are penalised until this much time has passed
	ReinforceShare float64       // fraction of slots that may target weak concepts
	ChainChance    float64       // probability of inserting a chain round in a long session
	IncludeDrafts  bool
}

func DefaultConfig() Config {
	return Config{HardCooldown: 72 * time.Hour, SoftCooldown: 30 * 24 * time.Hour, ReinforceShare: 0.2, ChainChance: 0.35}
}

type Request struct {
	Count       int
	Categories  []string // empty = all
	AllowChains bool
	Seed        int64
	Now         time.Time
	Exclude     map[string]bool
}

// Pick is one selected question plus why it was chosen (useful for debugging).
type Pick struct {
	Question *content.Question
	Reason   string // new | review | reinforce | explore | chain | fallback
}

type Selector struct {
	lib    *content.Library
	pool   []*content.Question // servable chain heads and normal questions, sorted by id
	chains [][]*content.Question
	cfg    Config
}

func New(lib *content.Library, cfg Config) *Selector {
	s := &Selector{lib: lib, cfg: cfg}
	for _, q := range lib.Servable(cfg.IncludeDrafts) {
		if q.Chain == nil {
			s.pool = append(s.pool, q)
		}
	}
	sort.Slice(s.pool, func(i, j int) bool { return s.pool[i].ID < s.pool[j].ID })
	ids := make([]string, 0, len(lib.Chains))
	for id := range lib.Chains {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		steps := lib.Chains[id]
		if steps[0].Servable(cfg.IncludeDrafts) {
			s.chains = append(s.chains, steps)
		}
	}
	return s
}

// PoolSize is the number of selectable single questions.
func (s *Selector) PoolSize() int { return len(s.pool) }

type slot struct {
	category  string
	level     int
	reinforce bool
	explore   bool
	chain     *content.Question
}

type state struct {
	picked   map[string]bool
	groups   map[string]bool
	concepts map[string]int
	subcats  map[string]int
}

type perf struct{ seen, correct int }

// difficulty mixes by experience tier: novice → veteran.
var tierMixes = [4][4]float64{
	{0.45, 0.40, 0.13, 0.02},
	{0.25, 0.40, 0.25, 0.10},
	{0.15, 0.35, 0.32, 0.18},
	{0.10, 0.30, 0.35, 0.25},
}

// Tier maps a profile to an experience tier 0..3 (Day 1 easy … Day 60 rare).
func Tier(p *Profile) int {
	n := p.TotalAnswered
	t := 0
	switch {
	case n >= 400:
		t = 3
	case n >= 150:
		t = 2
	case n >= 30:
		t = 1
	}
	if n >= 20 {
		acc := p.accuracy()
		if acc > 0.8 && t < 3 {
			t++
		} else if acc < 0.45 && t > 0 {
			t--
		}
	}
	return t
}

// Select builds a session of up to req.Count questions.
func (s *Selector) Select(p *Profile, req Request) []Pick {
	if p == nil {
		p = NewProfile()
	}
	if req.Now.IsZero() {
		req.Now = time.Now()
	}
	rng := rand.New(rand.NewSource(req.Seed))
	allowed := map[string]bool{}
	for _, c := range req.Categories {
		allowed[c] = true
	}
	inCats := func(q *content.Question) bool { return len(allowed) == 0 || allowed[q.Category] }
	excluded := func(q *content.Question) bool { return req.Exclude != nil && req.Exclude[q.ID] }

	eligible := make([]*content.Question, 0, len(s.pool))
	for _, q := range s.pool {
		if inCats(q) && !excluded(q) {
			eligible = append(eligible, q)
		}
	}
	n := req.Count
	if n <= 0 || len(eligible) == 0 {
		return nil
	}

	// Concept performance and subcategory exposure from history.
	cperf := map[string]perf{}
	seenSub := map[string]bool{}
	for id, h := range p.Seen {
		q := s.lib.Get(id)
		if q == nil {
			continue
		}
		seenSub[q.Category+"/"+q.Subcategory] = true
		for _, c := range q.Concepts {
			pf := cperf[c]
			pf.seen += h.TimesCorrect + h.TimesWrong
			pf.correct += h.TimesCorrect
			cperf[c] = pf
		}
	}
	hasWeak := false
	for _, pf := range cperf {
		if pf.seen >= 2 && float64(pf.correct)/float64(pf.seen) < 0.5 {
			hasWeak = true
			break
		}
	}

	st := &state{picked: map[string]bool{}, groups: map[string]bool{}, concepts: map[string]int{}, subcats: map[string]int{}}
	slots := make([]slot, n)

	// 1. Optional chain round, placed as a consecutive block (never first).
	if req.AllowChains && n >= 8 && rng.Float64() < s.cfg.ChainChance {
		if ch := s.pickChain(p, req, inCats, excluded, rng); ch != nil && len(ch) < n {
			start := 1 + rng.Intn(n-len(ch))
			for i, q := range ch {
				slots[start+i] = slot{category: q.Category, level: q.Difficulty.Level(), chain: q}
			}
		}
	}

	// 2. Difficulty arc.
	levels := planDifficulty(tierMixes[Tier(p)], n, rng)
	li := 0
	for i := range slots {
		if slots[i].chain == nil {
			slots[i].level = levels[li]
			li++
		}
	}

	// 3. Category rotation (deficit scheduling, no back-to-back repeats).
	cats := availableCategories(eligible)
	counts := map[string]float64{}
	for i := range slots {
		if slots[i].chain != nil {
			counts[slots[i].category]++
			continue
		}
		best, bestScore := "", math.Inf(-1)
		for _, c := range cats {
			sc := float64(i+1)/float64(len(cats)) - counts[c] + rng.Float64()*0.3
			if i > 0 && slots[i-1].category == c {
				sc -= 10
			}
			if i > 1 && slots[i-2].category == c && len(cats) >= 4 {
				sc -= 2
			}
			if i+1 < n && slots[i+1].chain != nil && slots[i+1].category == c {
				sc -= 10
			}
			if sc > bestScore {
				best, bestScore = c, sc
			}
		}
		slots[i].category = best
		counts[best]++
	}

	// 4. Mark reinforce / explore slots (never the first slot).
	free := []int{}
	for i := 1; i < n; i++ {
		if slots[i].chain == nil {
			free = append(free, i)
		}
	}
	rng.Shuffle(len(free), func(a, b int) { free[a], free[b] = free[b], free[a] })
	if hasWeak {
		k := int(math.Round(float64(n) * s.cfg.ReinforceShare))
		for j := 0; j < k && len(free) > 0; j++ {
			slots[free[0]].reinforce = true
			free = free[1:]
		}
	}
	if n >= 8 && len(free) > 0 {
		slots[free[0]].explore = true
	}

	// 5. Fill slots. Reserve the chain's variant groups first so no regular
	// question that tests the same intuition lands earlier in the session.
	for _, sl := range slots {
		if sl.chain != nil {
			st.groups[sl.chain.VariantGroup] = true
		}
	}
	var out []Pick
	for i, sl := range slots {
		if sl.chain != nil {
			s.take(st, sl.chain)
			out = append(out, Pick{Question: sl.chain, Reason: "chain"})
			continue
		}
		prevCat := ""
		if i > 0 {
			prevCat = slots[i-1].category
		}
		q, reason := s.fill(p, st, sl, prevCat, eligible, req.Now, cperf, seenSub, rng)
		if q == nil {
			break
		}
		s.take(st, q)
		out = append(out, Pick{Question: q, Reason: reason})
	}
	return out
}

func (s *Selector) take(st *state, q *content.Question) {
	st.picked[q.ID] = true
	st.groups[q.VariantGroup] = true
	for _, c := range q.Concepts {
		st.concepts[c]++
	}
	st.subcats[q.Category+"/"+q.Subcategory]++
}

func (s *Selector) inHardCooldown(p *Profile, q *content.Question, now time.Time) bool {
	h, ok := p.Seen[q.ID]
	return ok && now.Sub(h.LastSeenAt) < s.cfg.HardCooldown
}

// fill picks the best question for a slot, relaxing constraints step by step
// so a session is never left short while any question remains.
func (s *Selector) fill(p *Profile, st *state, sl slot, prevCat string, eligible []*content.Question,
	now time.Time, cperf map[string]perf, seenSub map[string]bool, rng *rand.Rand) (*content.Question, string) {

	type filter func(q *content.Question) bool
	notPicked := func(q *content.Question) bool { return !st.picked[q.ID] }
	freshGroup := func(q *content.Question) bool { return !st.groups[q.VariantGroup] }
	cool := func(q *content.Question) bool { return !s.inHardCooldown(p, q, now) }
	ladder := []struct {
		f      filter
		reason string
	}{
		{func(q *content.Question) bool { return q.Category == sl.category && cool(q) && freshGroup(q) }, ""},
		{func(q *content.Question) bool { return q.Category != prevCat && cool(q) && freshGroup(q) }, ""},
		{func(q *content.Question) bool { return cool(q) && freshGroup(q) }, ""},
		{freshGroup, "fallback"},
		{func(*content.Question) bool { return true }, "fallback"},
	}
	for _, step := range ladder {
		var cands []*content.Question
		for _, q := range eligible {
			if notPicked(q) && step.f(q) {
				cands = append(cands, q)
			}
		}
		if len(cands) == 0 {
			continue
		}
		type scored struct {
			q *content.Question
			s float64
		}
		list := make([]scored, len(cands))
		for i, q := range cands {
			list[i] = scored{q, s.score(q, p, st, sl, now, cperf, seenSub, rng)}
		}
		sort.SliceStable(list, func(a, b int) bool { return list[a].s > list[b].s })
		// Weighted choice among the top three keeps sessions varied.
		weights := []float64{0.6, 0.25, 0.15}
		k := min(3, len(list))
		r, acc := rng.Float64()*sum(weights[:k]), 0.0
		choice := list[0].q
		for i := 0; i < k; i++ {
			acc += weights[i]
			if r <= acc {
				choice = list[i].q
				break
			}
		}
		reason := step.reason
		if reason == "" {
			_, seen := p.Seen[choice.ID]
			switch {
			case sl.reinforce && seen:
				reason = "reinforce"
			case sl.explore && !seenSub[choice.Category+"/"+choice.Subcategory]:
				reason = "explore"
			case seen:
				reason = "review"
			default:
				reason = "new"
			}
		}
		return choice, reason
	}
	return nil, ""
}

func (s *Selector) score(q *content.Question, p *Profile, st *state, sl slot, now time.Time,
	cperf map[string]perf, seenSub map[string]bool, rng *rand.Rand) float64 {
	sc := 0.0
	if h, seen := p.Seen[q.ID]; !seen {
		sc += 3
	} else {
		since := now.Sub(h.LastSeenAt)
		f := math.Min(1, float64(since)/float64(s.cfg.SoftCooldown))
		sc += 1.5*f - 0.5
		sc -= 0.3 * float64(h.TimesSeen)
		if !h.LastCorrect && since > s.cfg.HardCooldown {
			sc += 0.5 // a chance at redemption
			if sl.reinforce {
				sc += 1
			}
		}
	}
	sc -= 0.8 * math.Abs(float64(q.Difficulty.Level()-sl.level))
	for _, c := range q.Concepts {
		sc -= 1.2 * float64(st.concepts[c])
		sc -= 0.4 * math.Min(2, float64(p.RecentConcepts[c]))
	}
	if st.subcats[q.Category+"/"+q.Subcategory] > 0 {
		sc -= 0.6
	}
	if sl.reinforce {
		weakest := 1.0
		for _, c := range q.Concepts {
			if pf, ok := cperf[c]; ok && pf.seen >= 2 {
				weakest = math.Min(weakest, float64(pf.correct)/float64(pf.seen))
			}
		}
		if weakest < 0.5 {
			sc += 1.5 * (1 - weakest)
		}
	}
	if sl.explore && !seenSub[q.Category+"/"+q.Subcategory] {
		sc += 1.2
	}
	return sc + rng.Float64()*0.6
}

func (s *Selector) pickChain(p *Profile, req Request, inCats, excluded func(*content.Question) bool, rng *rand.Rand) []*content.Question {
	var best []*content.Question
	bestScore := math.Inf(-1)
	for _, ch := range s.chains {
		ok, score := true, rng.Float64()
		for _, q := range ch {
			if !inCats(q) || excluded(q) || s.inHardCooldown(p, q, req.Now) {
				ok = false
				break
			}
			if _, seen := p.Seen[q.ID]; !seen {
				score += 1
			}
		}
		if ok && score > bestScore {
			best, bestScore = ch, score
		}
	}
	return best
}

func availableCategories(qs []*content.Question) []string {
	set := map[string]bool{}
	for _, q := range qs {
		set[q.Category] = true
	}
	out := make([]string, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// planDifficulty turns a mix into n levels shaped as an arc: start gentle,
// finish with the hardest question, shuffle the middle.
func planDifficulty(mix [4]float64, n int, rng *rand.Rand) []int {
	counts := make([]int, 4)
	type frac struct {
		i int
		f float64
	}
	var fr []frac
	total := 0
	for i, m := range mix {
		x := m * float64(n)
		counts[i] = int(math.Floor(x))
		total += counts[i]
		fr = append(fr, frac{i, x - math.Floor(x)})
	}
	sort.SliceStable(fr, func(a, b int) bool { return fr[a].f > fr[b].f })
	for j := 0; total < n; j++ {
		counts[fr[j%4].i]++
		total++
	}
	levels := make([]int, 0, n)
	for i, c := range counts {
		for k := 0; k < c; k++ {
			levels = append(levels, i+1)
		}
	}
	if n > 2 {
		mid := levels[1 : n-1]
		rng.Shuffle(len(mid), func(a, b int) { mid[a], mid[b] = mid[b], mid[a] })
	}
	return levels
}

func sum(xs []float64) float64 {
	t := 0.0
	for _, x := range xs {
		t += x
	}
	return t
}

// Daily returns the deterministic daily set for a date: five questions, five
// different categories, an easy→expert arc, excluding recent daily questions.
func (s *Selector) Daily(day, salt string, recent map[string]bool) []*content.Question {
	h := fnv.New64a()
	h.Write([]byte(salt + ":" + day))
	rng := rand.New(rand.NewSource(int64(h.Sum64() >> 1)))
	levels := []int{1, 2, 2, 3, 4}
	cats := availableCategories(s.pool)
	rng.Shuffle(len(cats), func(a, b int) { cats[a], cats[b] = cats[b], cats[a] })
	used, groups, concepts := map[string]bool{}, map[string]bool{}, map[string]bool{}
	var out []*content.Question
	for i, lvl := range levels {
		var cands []*content.Question
		for relax := 0; relax < 4 && len(cands) == 0; relax++ {
			for _, q := range s.pool {
				if used[q.ID] || groups[q.VariantGroup] {
					continue
				}
				if relax < 3 && recent[q.ID] {
					continue
				}
				if relax < 2 && len(cats) > 0 && q.Category != cats[i%len(cats)] {
					continue
				}
				if relax < 1 && q.Difficulty.Level() != lvl {
					continue
				}
				clash := false
				for _, c := range q.Concepts {
					clash = clash || concepts[c]
				}
				if clash && relax < 3 {
					continue
				}
				cands = append(cands, q)
			}
		}
		if len(cands) == 0 {
			break
		}
		q := cands[rng.Intn(len(cands))]
		used[q.ID], groups[q.VariantGroup] = true, true
		for _, c := range q.Concepts {
			concepts[c] = true
		}
		out = append(out, q)
	}
	return out
}
