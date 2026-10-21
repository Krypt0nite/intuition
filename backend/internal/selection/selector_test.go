package selection

import (
	"fmt"
	"testing"
	"time"

	"intuition/internal/content"
)

func realLib(t *testing.T) *content.Library {
	lib, rep, err := content.LoadDir("../../../content")
	if err != nil || rep.HasErrors() {
		t.Fatalf("load: %v %v", err, rep.Errors())
	}
	return lib
}

func ids(picks []Pick) []string {
	out := make([]string, len(picks))
	for i, p := range picks {
		out[i] = p.Question.ID
	}
	return out
}

func TestSessionHasNoRepeatsOrSharedVariantGroups(t *testing.T) {
	sel := New(realLib(t), DefaultConfig())
	for seed := int64(0); seed < 200; seed++ {
		picks := sel.Select(NewProfile(), Request{Count: 25, AllowChains: true, Seed: seed})
		if len(picks) != 25 {
			t.Fatalf("seed %d: got %d picks", seed, len(picks))
		}
		seenID, seenGroup := map[string]bool{}, map[string]bool{}
		for _, p := range picks {
			if seenID[p.Question.ID] {
				t.Fatalf("seed %d: repeated %s", seed, p.Question.ID)
			}
			if seenGroup[p.Question.VariantGroup] {
				t.Fatalf("seed %d: variant group %s repeated", seed, p.Question.VariantGroup)
			}
			seenID[p.Question.ID], seenGroup[p.Question.VariantGroup] = true, true
		}
	}
}

func TestCategoryDiversity(t *testing.T) {
	sel := New(realLib(t), DefaultConfig())
	for seed := int64(0); seed < 200; seed++ {
		picks := sel.Select(NewProfile(), Request{Count: 10, Seed: seed})
		cats := map[string]bool{}
		for i, p := range picks {
			cats[p.Question.Category] = true
			if i > 0 && p.Question.Chain == nil && picks[i-1].Question.Category == p.Question.Category {
				t.Fatalf("seed %d: back-to-back %s at %d: %v", seed, p.Question.Category, i, ids(picks))
			}
		}
		if len(cats) < 7 {
			t.Fatalf("seed %d: only %d categories in 10 questions", seed, len(cats))
		}
	}
}

func TestCategoryFilter(t *testing.T) {
	sel := New(realLib(t), DefaultConfig())
	picks := sel.Select(NewProfile(), Request{Count: 10, Categories: []string{"physics"}, Seed: 1})
	if len(picks) != 10 {
		t.Fatalf("got %d", len(picks))
	}
	for _, p := range picks {
		if p.Question.Category != "physics" {
			t.Fatalf("non-physics question %s", p.Question.ID)
		}
	}
}

func TestPrefersUnseenAndRespectsHardCooldown(t *testing.T) {
	lib := realLib(t)
	sel := New(lib, DefaultConfig())
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	prof := NewProfile()
	// Everything except 12 questions was seen an hour ago.
	pool := lib.Servable(false)
	unseen := map[string]bool{}
	for i, q := range pool {
		if q.Chain != nil {
			continue
		}
		if len(unseen) < 12 && i%5 == 0 {
			unseen[q.ID] = true
			continue
		}
		prof.Record(q, true, now.Add(-time.Hour))
	}
	picks := sel.Select(prof, Request{Count: 10, Seed: 3, Now: now})
	for _, p := range picks {
		if !unseen[p.Question.ID] {
			t.Fatalf("picked %s which is inside the hard cooldown while unseen questions remained", p.Question.ID)
		}
	}
}

func TestFallbackWhenEverythingSeenRecently(t *testing.T) {
	lib := realLib(t)
	sel := New(lib, DefaultConfig())
	now := time.Now()
	prof := NewProfile()
	for i, q := range lib.Servable(false) {
		prof.Record(q, true, now.Add(-time.Duration(i)*time.Minute))
	}
	picks := sel.Select(prof, Request{Count: 10, Seed: 9, Now: now})
	if len(picks) != 10 {
		t.Fatalf("expected a full session from fallback, got %d", len(picks))
	}
}

// Simulates a dedicated player: 3 standard games a day for 30 days.
func TestMonthOfPlayNeverRepeatsInsideCooldown(t *testing.T) {
	lib := realLib(t)
	sel := New(lib, DefaultConfig())
	prof := NewProfile()
	last := map[string]time.Time{}
	start := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	repeats, total := 0, 0
	for day := 0; day < 30; day++ {
		for game := 0; game < 3; game++ {
			now := start.Add(time.Duration(day)*24*time.Hour + time.Duration(game)*4*time.Hour)
			picks := sel.Select(prof, Request{Count: 10, AllowChains: true, Seed: int64(day*10 + game), Now: now})
			for _, p := range picks {
				total++
				if at, ok := last[p.Question.ID]; ok {
					repeats++
					if now.Sub(at) < DefaultConfig().HardCooldown {
						t.Fatalf("day %d: %s repeated after only %v", day, p.Question.ID, now.Sub(at))
					}
				}
				last[p.Question.ID] = now
				prof.Record(p.Question, (day+game)%3 != 0, now)
			}
		}
	}
	t.Logf("%d questions served over 30 days, %d repeats (library %d)", total, repeats, sel.PoolSize())
}

func TestDifficultyTracksExperience(t *testing.T) {
	lib := realLib(t)
	sel := New(lib, DefaultConfig())
	avg := func(p *Profile) float64 {
		sum, n := 0, 0
		for s := int64(0); s < 50; s++ {
			for _, pk := range sel.Select(p, Request{Count: 10, Seed: s}) {
				sum += pk.Question.Difficulty.Level()
				n++
			}
		}
		return float64(sum) / float64(n)
	}
	vet := NewProfile()
	vet.TotalAnswered = 600
	vet.Seen["x"] = History{TimesCorrect: 450, TimesWrong: 150}
	novice, veteran := avg(NewProfile()), avg(vet)
	if veteran-novice < 0.4 {
		t.Fatalf("expected veterans to get harder questions: novice %.2f veteran %.2f", novice, veteran)
	}
}

func TestChainsStayConsecutive(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ChainChance = 1
	sel := New(realLib(t), cfg)
	found := 0
	for seed := int64(0); seed < 50; seed++ {
		picks := sel.Select(NewProfile(), Request{Count: 10, AllowChains: true, Seed: seed})
		for i, p := range picks {
			if c := p.Question.Chain; c != nil && c.Step == 1 {
				found++
				for k := 1; k < c.Total; k++ {
					n := picks[i+k].Question.Chain
					if n == nil || n.ID != c.ID || n.Step != k+1 {
						t.Fatalf("seed %d: chain broken at %d: %v", seed, i+k, ids(picks))
					}
				}
			}
		}
	}
	if found == 0 {
		t.Fatal("chain never inserted")
	}
}

func TestDailyIsDeterministicAndVaried(t *testing.T) {
	sel := New(realLib(t), DefaultConfig())
	a := sel.Daily("2026-10-06", "salt", nil)
	b := sel.Daily("2026-10-06", "salt", nil)
	c := sel.Daily("2026-10-07", "salt", nil)
	if fmt.Sprint(qids(a)) != fmt.Sprint(qids(b)) {
		t.Fatal("same day produced different sets")
	}
	if fmt.Sprint(qids(a)) == fmt.Sprint(qids(c)) {
		t.Fatal("different days produced the same set")
	}
	cats := map[string]bool{}
	for _, q := range a {
		cats[q.Category] = true
	}
	if len(a) != 5 || len(cats) != 5 {
		t.Fatalf("want 5 questions from 5 categories: %v", qids(a))
	}
	recent := map[string]bool{}
	for _, q := range a {
		recent[q.ID] = true
	}
	for _, q := range sel.Daily("2026-10-06", "salt", recent) {
		if recent[q.ID] {
			t.Fatalf("recent daily question %s reused", q.ID)
		}
	}
}

func qids(qs []*content.Question) []string {
	out := []string{}
	for _, q := range qs {
		out = append(out, q.ID)
	}
	return out
}
