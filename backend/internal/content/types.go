// Package content defines the question schema, loads question packs from
// disk, and validates them. JSON files under content/ are the source of truth;
// the game engine never hardcodes questions.
package content

import "slices"

// Difficulty is about how hard the *reasoning* is, not how obscure the fact is.
type Difficulty string

const (
	Easy   Difficulty = "easy"   // common everyday intuition
	Medium Difficulty = "medium" // one scientific concept
	Hard   Difficulty = "hard"   // combining concepts
	Expert Difficulty = "expert" // genuinely counterintuitive reasoning
)

// Level maps difficulty to 1..4 (0 for unknown).
func (d Difficulty) Level() int {
	switch d {
	case Easy:
		return 1
	case Medium:
		return 2
	case Hard:
		return 3
	case Expert:
		return 4
	}
	return 0
}

// DifficultyForLevel is the inverse of Level.
func DifficultyForLevel(l int) Difficulty {
	switch l {
	case 1:
		return Easy
	case 2:
		return Medium
	case 3:
		return Hard
	case 4:
		return Expert
	}
	return ""
}

// Review workflow: only reviewed/verified questions are served by default.
const (
	StatusDraft    = "draft"
	StatusReviewed = "reviewed"
	StatusVerified = "verified"
)

// GoDeeper is the optional expandable section after the main explanation.
type GoDeeper struct {
	Equation string `json:"equation,omitempty"`
	Text     string `json:"text"`
	Example  string `json:"example,omitempty"`
}

// Chain links questions that must be played consecutively ("chain intuition").
type Chain struct {
	ID    string `json:"id"`
	Step  int    `json:"step"`
	Total int    `json:"total"`
}

// Question is one item in the bank. Field names match the JSON authoring format.
type Question struct {
	ID            string     `json:"id"`
	Category      string     `json:"category"`
	Subcategory   string     `json:"subcategory"`
	Difficulty    Difficulty `json:"difficulty"`
	QuestionType  string     `json:"questionType"`
	AnswerType    string     `json:"answerType"`
	VisualType    string     `json:"visualType,omitempty"`
	Emoji         string     `json:"emoji,omitempty"`
	Scenario      string     `json:"scenario"`
	Question      string     `json:"question"`
	Options       []string   `json:"options"`
	CorrectIndex  int        `json:"correctIndex"`
	Shuffle       *bool      `json:"shuffle,omitempty"`
	Reveal        string     `json:"reveal"`
	Explanation   string     `json:"explanation"`
	IntuitionTrap string     `json:"intuitionTrap"`
	Principle     string     `json:"principle"`
	FunFact       string     `json:"funFact,omitempty"`
	Caveat        string     `json:"caveat,omitempty"`
	Assumptions   []string   `json:"assumptions,omitempty"`
	GoDeeper      *GoDeeper  `json:"goDeeper,omitempty"`
	Tags          []string   `json:"tags"`
	Concepts      []string   `json:"concepts"`
	Prerequisites []string   `json:"prerequisites,omitempty"`
	VariantGroup  string     `json:"variantGroup"`
	EstimatedTime int        `json:"estimatedTime,omitempty"`
	Chain         *Chain     `json:"chain,omitempty"`
	Source        string     `json:"source,omitempty"`
	SourceURL     string     `json:"sourceUrl,omitempty"`
	ReviewStatus  string     `json:"reviewStatus,omitempty"`
	ReviewNotes   string     `json:"reviewNotes,omitempty"`

	// Derived at load time.
	File string `json:"-"`
	Pack string `json:"-"`
	Hash string `json:"-"`
}

// ShouldShuffle reports whether option order may be randomised. Ordered
// scales ("much larger / slightly larger / same") set shuffle:false.
func (q *Question) ShouldShuffle() bool { return q.Shuffle == nil || *q.Shuffle }

// Servable reports whether the question may be shown to players.
func (q *Question) Servable(includeDrafts bool) bool {
	if q.ReviewStatus == StatusReviewed || q.ReviewStatus == StatusVerified {
		return true
	}
	return includeDrafts && q.ReviewStatus == StatusDraft
}

// IsChainHead is true for normal questions and for step 1 of a chain.
func (q *Question) IsChainHead() bool { return q.Chain == nil || q.Chain.Step == 1 }

// CategoryInfo describes one top-level domain.
type CategoryInfo struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Emoji         string   `json:"emoji"`
	Prefix        string   `json:"prefix"`
	Color         string   `json:"color"`
	Subcategories []string `json:"subcategories"`
}

// Taxonomy is loaded from content/taxonomy.json and drives category validation.
type Taxonomy struct {
	Categories    []*CategoryInfo `json:"categories"`
	QuestionTypes []string        `json:"questionTypes"`
	AnswerTypes   []string        `json:"answerTypes"`

	byID map[string]*CategoryInfo
}

func (t *Taxonomy) index() {
	t.byID = make(map[string]*CategoryInfo, len(t.Categories))
	for _, c := range t.Categories {
		t.byID[c.ID] = c
	}
}

// Category returns the category or nil.
func (t *Taxonomy) Category(id string) *CategoryInfo {
	if t.byID == nil {
		t.index()
	}
	return t.byID[id]
}

// HasSubcategory reports whether sub is declared for category cat.
func (t *Taxonomy) HasSubcategory(cat, sub string) bool {
	c := t.Category(cat)
	return c != nil && slices.Contains(c.Subcategories, sub)
}

// CategoryIDs returns category ids in taxonomy order.
func (t *Taxonomy) CategoryIDs() []string {
	ids := make([]string, 0, len(t.Categories))
	for _, c := range t.Categories {
		ids = append(ids, c.ID)
	}
	return ids
}

// Library is the loaded, validated question bank.
type Library struct {
	Questions []*Question
	ByID      map[string]*Question
	Taxonomy  *Taxonomy
	Chains    map[string][]*Question // chain id -> steps in order
	Schedule  map[string][]string    // curated daily sets: YYYY-MM-DD -> ids
	Version   string
	Dir       string
}

// Get returns a question by id or nil.
func (l *Library) Get(id string) *Question { return l.ByID[id] }

// Servable returns all questions that may be shown to players.
func (l *Library) Servable(includeDrafts bool) []*Question {
	out := make([]*Question, 0, len(l.Questions))
	for _, q := range l.Questions {
		if q.Servable(includeDrafts) {
			out = append(out, q)
		}
	}
	return out
}
