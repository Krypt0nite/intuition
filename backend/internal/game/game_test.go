package game

import (
	"encoding/json"
	"math/rand"
	"strings"
	"testing"
	"time"

	"intuition/internal/content"
)

func lib(t *testing.T) *content.Library {
	l, rep, err := content.LoadDir("../../../content")
	if err != nil || rep.HasErrors() {
		t.Fatal(err)
	}
	return l
}

func TestQuestionViewNeverLeaksAnswer(t *testing.T) {
	l := lib(t)
	rng := rand.New(rand.NewSource(1))
	for _, q := range l.Questions {
		v := BuildQuestionView(q, ShuffleOrder(q, rng), l.Taxonomy, 0, 1)
		b, _ := json.Marshal(v)
		s := string(b)
		for _, banned := range []string{"correct", "explanation", "intuitionTrap", "principle", "funFact", "reveal"} {
			if strings.Contains(strings.ToLower(s), strings.ToLower(`"`+banned)) {
				t.Fatalf("%s: view leaks field %q: %s", q.ID, banned, s)
			}
		}
		if strings.Contains(s, q.Explanation[:30]) {
			t.Fatalf("%s: explanation text leaked", q.ID)
		}
	}
}

func TestShuffleMapsCorrectAnswerAndVariesPosition(t *testing.T) {
	l := lib(t)
	q := l.Get("phy-moon-hammer-feather")
	pos := map[int]int{}
	for seed := int64(0); seed < 300; seed++ {
		order := ShuffleOrder(q, rand.New(rand.NewSource(seed)))
		d := DisplayIndexOf(order, q.CorrectIndex)
		if q.Options[order[d]] != q.Options[q.CorrectIndex] {
			t.Fatal("display index does not map to the correct option")
		}
		pos[d]++
	}
	for i := 0; i < 3; i++ {
		if pos[i] < 60 {
			t.Fatalf("correct answer rarely at position %d: %v", i, pos)
		}
	}
	fixed := l.Get("phy-proton-mass-origin") // shuffle:false
	order := ShuffleOrder(fixed, rand.New(rand.NewSource(5)))
	for i, o := range order {
		if i != o {
			t.Fatal("ordered-scale question was shuffled")
		}
	}
}

func TestSessionScoringValidationAndIdempotency(t *testing.T) {
	l := lib(t)
	qs := []*content.Question{l.Get("phy-elevator-scale"), l.Get("chem-rusted-nail-mass"), l.Get("math-gamblers-fallacy")}
	now := time.Now()
	s := NewSession("p1", "quick", qs, true, 42, now)
	correctDisplay := func(i int) int { return DisplayIndexOf(s.Orders[i], qs[i].CorrectIndex) }

	if _, err := s.Submit(qs[1].ID, 0, 0, now); err != ErrStaleQuestion {
		t.Fatalf("expected stale question, got %v", err)
	}
	if _, err := s.Submit(qs[0].ID, 9, 0, now); err != ErrInvalidChoice {
		t.Fatalf("expected invalid choice, got %v", err)
	}
	o, err := s.Submit(qs[0].ID, correctDisplay(0), 3, now.Add(2*time.Second))
	if err != nil || !o.Correct || o.Score != 1 || o.Streak != 1 || o.ResponseMS != 2000 {
		t.Fatalf("bad outcome %+v %v", o, err)
	}
	dup, err := s.Submit(qs[0].ID, (correctDisplay(0)+1)%3, 3, now)
	if err != nil || !dup.Duplicate || !dup.Correct || dup.Score != 1 {
		t.Fatalf("duplicate submit should be idempotent: %+v %v", dup, err)
	}
	wrong := (correctDisplay(1) + 1) % len(qs[1].Options)
	o, _ = s.Submit(qs[1].ID, wrong, 3, now.Add(3*time.Second))
	if o.Correct || o.Streak != 0 {
		t.Fatalf("expected wrong answer: %+v", o)
	}
	o, _ = s.Submit(qs[2].ID, correctDisplay(2), 1, now.Add(4*time.Second))
	if !o.Done || o.Score != 2 {
		t.Fatalf("expected done with 2: %+v", o)
	}
	if _, err := s.Submit("anything", 0, 0, now); err != ErrSessionFinished {
		t.Fatalf("expected finished, got %v", err)
	}
	sum := Summarize(s.Records, l)
	if sum.Score != 2 || sum.Total != 3 || sum.Surprises != 1 || sum.BiggestSurprise == nil || len(sum.Missed) != 1 {
		t.Fatalf("bad summary %+v", sum)
	}
	if sum.Weakest != "chemistry" || sum.Reliability != 67 || len(sum.Principles) != 3 {
		t.Fatalf("bad summary details %+v", sum)
	}
}
