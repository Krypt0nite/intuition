// Package game contains the server-authoritative rules shared by solo,
// daily and multiplayer play: option shuffling, public views that never
// contain the answer, scoring and end-of-game summaries.
package game

import (
	"math/rand"

	"intuition/internal/content"
)

var labels = []string{"A", "B", "C", "D"}

// OptionView is an answer button as the client sees it.
type OptionView struct {
	Index int    `json:"index"`
	Label string `json:"label"`
	Text  string `json:"text"`
}

// QuestionView is everything the client may know BEFORE answering.
// It deliberately has no correct answer, explanation or reveal fields.
type QuestionView struct {
	ID            string       `json:"id"`
	Category      string       `json:"category"`
	CategoryName  string       `json:"categoryName"`
	CategoryEmoji string       `json:"categoryEmoji"`
	CategoryColor string       `json:"categoryColor"`
	Subcategory   string       `json:"subcategory"`
	Difficulty    string       `json:"difficulty"`
	QuestionType  string       `json:"questionType"`
	AnswerType    string       `json:"answerType"`
	Emoji         string       `json:"emoji,omitempty"`
	Scenario      string       `json:"scenario"`
	Question      string       `json:"question"`
	Options       []OptionView `json:"options"`
	Index         int          `json:"index"`
	Total         int          `json:"total"`
	Chain         *ChainView   `json:"chain,omitempty"`
}

type ChainView struct {
	Step  int `json:"step"`
	Total int `json:"total"`
}

// RevealView is sent only after the answer is locked or time runs out.
type RevealView struct {
	QuestionID    string            `json:"questionId"`
	CorrectIndex  int               `json:"correctIndex"` // display index
	Reveal        string            `json:"reveal"`
	Explanation   string            `json:"explanation"`
	IntuitionTrap string            `json:"intuitionTrap"`
	Principle     string            `json:"principle"`
	FunFact       string            `json:"funFact,omitempty"`
	Caveat        string            `json:"caveat,omitempty"`
	Assumptions   []string          `json:"assumptions,omitempty"`
	GoDeeper      *content.GoDeeper `json:"goDeeper,omitempty"`
	Source        string            `json:"source,omitempty"`
	SourceURL     string            `json:"sourceUrl,omitempty"`
}

// ShuffleOrder returns order[displayIndex] = originalIndex.
func ShuffleOrder(q *content.Question, rng *rand.Rand) []int {
	order := make([]int, len(q.Options))
	for i := range order {
		order[i] = i
	}
	if q.ShouldShuffle() {
		rng.Shuffle(len(order), func(a, b int) { order[a], order[b] = order[b], order[a] })
	}
	return order
}

// DisplayIndexOf maps an original option index to its display position.
func DisplayIndexOf(order []int, original int) int {
	for d, o := range order {
		if o == original {
			return d
		}
	}
	return -1
}

func BuildQuestionView(q *content.Question, order []int, tax *content.Taxonomy, index, total int) QuestionView {
	v := QuestionView{
		ID: q.ID, Category: q.Category, Subcategory: q.Subcategory, Difficulty: string(q.Difficulty),
		QuestionType: q.QuestionType, AnswerType: q.AnswerType, Emoji: q.Emoji,
		Scenario: q.Scenario, Question: q.Question, Index: index, Total: total,
	}
	if c := tax.Category(q.Category); c != nil {
		v.CategoryName, v.CategoryEmoji, v.CategoryColor = c.Name, c.Emoji, c.Color
	}
	for d, o := range order {
		v.Options = append(v.Options, OptionView{Index: d, Label: labels[d], Text: q.Options[o]})
	}
	if q.Chain != nil {
		v.Chain = &ChainView{Step: q.Chain.Step, Total: q.Chain.Total}
	}
	return v
}

func BuildReveal(q *content.Question, order []int) RevealView {
	return RevealView{
		QuestionID: q.ID, CorrectIndex: DisplayIndexOf(order, q.CorrectIndex),
		Reveal: q.Reveal, Explanation: q.Explanation, IntuitionTrap: q.IntuitionTrap,
		Principle: q.Principle, FunFact: q.FunFact, Caveat: q.Caveat, Assumptions: q.Assumptions,
		GoDeeper: q.GoDeeper, Source: q.Source, SourceURL: q.SourceURL,
	}
}
