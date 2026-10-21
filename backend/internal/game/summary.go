package game

import (
	"math"
	"sort"

	"intuition/internal/content"
)

type Tally struct {
	Correct int `json:"correct"`
	Total   int `json:"total"`
}

func (t Tally) Accuracy() float64 {
	if t.Total == 0 {
		return 0
	}
	return float64(t.Correct) / float64(t.Total)
}

// MissedItem describes a question the player got wrong (or didn't answer).
type MissedItem struct {
	QuestionID    string `json:"questionId"`
	Category      string `json:"category"`
	Emoji         string `json:"emoji,omitempty"`
	Question      string `json:"question"`
	Reveal        string `json:"reveal"`
	Principle     string `json:"principle"`
	YourAnswer    string `json:"yourAnswer"`
	CorrectAnswer string `json:"correctAnswer"`
	Confidence    int    `json:"confidence"`
}

// Summary is shown on the results screen and stored with the session.
type Summary struct {
	Score             int              `json:"score"`
	Total             int              `json:"total"`
	Accuracy          float64          `json:"accuracy"`
	BestStreak        int              `json:"bestStreak"`
	AvgResponseMS     int64            `json:"avgResponseMs"`
	FastestCorrectMS  int64            `json:"fastestCorrectMs"`
	FastestQuestionID string           `json:"fastestQuestionId,omitempty"`
	ByCategory        map[string]Tally `json:"byCategory"`
	Strongest         string           `json:"strongest,omitempty"`
	Weakest           string           `json:"weakest,omitempty"`
	ConfidentAnswers  int              `json:"confidentAnswers"`
	Surprises         int              `json:"surprises"` // wrong while "fairly sure" or "certain"
	SurpriseRate      float64          `json:"surpriseRate"`
	BiggestSurprise   *MissedItem      `json:"biggestSurprise,omitempty"`
	Missed            []MissedItem     `json:"missed"`
	Principles        []string         `json:"principles"`
	HardestCorrectID  string           `json:"hardestCorrectId,omitempty"`
	Reliability       int              `json:"reliability"` // % rounded, for the share card
}

// Summarize computes a summary from answer records. Unanswered questions in
// totalQuestions (e.g. multiplayer timeouts) should be present as records
// with Choice -1.
func Summarize(records []Record, lib *content.Library) Summary {
	s := Summary{ByCategory: map[string]Tally{}, Missed: []MissedItem{}, Principles: []string{}}
	streak := 0
	var rtSum int64
	rtN := 0
	seenPrinciple := map[string]bool{}
	hardest := 0
	for _, r := range records {
		q := lib.Get(r.QuestionID)
		if q == nil {
			continue
		}
		s.Total++
		t := s.ByCategory[q.Category]
		t.Total++
		if r.Choice >= 0 {
			rtSum += r.ResponseMS
			rtN++
		}
		if r.Correct {
			s.Score++
			t.Correct++
			streak++
			s.BestStreak = max(s.BestStreak, streak)
			if s.FastestCorrectMS == 0 || r.ResponseMS < s.FastestCorrectMS {
				s.FastestCorrectMS, s.FastestQuestionID = r.ResponseMS, q.ID
			}
			if q.Difficulty.Level() > hardest {
				hardest, s.HardestCorrectID = q.Difficulty.Level(), q.ID
			}
		} else {
			streak = 0
			item := MissedItem{QuestionID: q.ID, Category: q.Category, Emoji: q.Emoji, Question: q.Question,
				Reveal: q.Reveal, Principle: q.Principle, CorrectAnswer: q.Options[q.CorrectIndex], Confidence: r.Confidence}
			if r.Choice >= 0 && r.Choice < len(q.Options) {
				item.YourAnswer = q.Options[r.Choice]
			}
			s.Missed = append(s.Missed, item)
			if r.Confidence >= 2 {
				s.Surprises++
				if s.BiggestSurprise == nil || r.Confidence > s.BiggestSurprise.Confidence {
					cp := item
					s.BiggestSurprise = &cp
				}
			}
		}
		if r.Confidence >= 2 {
			s.ConfidentAnswers++
		}
		s.ByCategory[q.Category] = t
		if !seenPrinciple[q.Principle] {
			seenPrinciple[q.Principle] = true
			s.Principles = append(s.Principles, q.Principle)
		}
	}
	if s.Total > 0 {
		s.Accuracy = float64(s.Score) / float64(s.Total)
		s.Reliability = int(math.Round(s.Accuracy * 100))
	}
	if rtN > 0 {
		s.AvgResponseMS = rtSum / int64(rtN)
	}
	if s.ConfidentAnswers > 0 {
		s.SurpriseRate = float64(s.Surprises) / float64(s.ConfidentAnswers)
	}
	s.Strongest, s.Weakest = strongestWeakest(s.ByCategory)
	return s
}

func strongestWeakest(by map[string]Tally) (string, string) {
	cats := make([]string, 0, len(by))
	for c := range by {
		cats = append(cats, c)
	}
	if len(cats) == 0 {
		return "", ""
	}
	sort.Slice(cats, func(i, j int) bool {
		a, b := by[cats[i]], by[cats[j]]
		if a.Accuracy() != b.Accuracy() {
			return a.Accuracy() > b.Accuracy()
		}
		if a.Total != b.Total {
			return a.Total > b.Total
		}
		return cats[i] < cats[j]
	})
	strong, weak := cats[0], cats[len(cats)-1]
	if by[strong].Correct == 0 {
		strong = ""
	}
	if by[weak].Correct == by[weak].Total || weak == strong {
		weak = ""
	}
	return strong, weak
}
