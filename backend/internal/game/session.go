package game

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	mrand "math/rand"
	"sync"
	"time"

	"intuition/internal/content"
)

var (
	ErrSessionFinished = errors.New("session_finished")
	ErrStaleQuestion   = errors.New("stale_question")
	ErrInvalidChoice   = errors.New("invalid_choice")
	ErrNotFound        = errors.New("session_not_found")
	ErrForbidden       = errors.New("forbidden")
)

// MaxCountedResponse caps response times used for stats and tie-breaks, so an
// idle tab doesn't skew averages.
const MaxCountedResponse = 60 * time.Second

// Record is one answered question inside a session.
type Record struct {
	QuestionID string    `json:"questionId"`
	Category   string    `json:"category"`
	Difficulty string    `json:"difficulty"`
	Choice     int       `json:"choice"` // original option index, -1 = no answer
	Display    int       `json:"display"`
	Correct    bool      `json:"correct"`
	Confidence int       `json:"confidence"`
	ResponseMS int64     `json:"responseMs"`
	At         time.Time `json:"at"`
}

// Session is a solo or daily game. All answers are checked here, server-side.
type Session struct {
	mu         sync.Mutex
	ID         string
	PlayerID   string
	Kind       string // quick | standard | marathon | daily
	DailyDate  string
	Confidence bool
	Questions  []*content.Question
	Orders     [][]int
	Index      int
	IssuedAt   time.Time
	Records    []Record
	Streak     int
	BestStreak int
	StartedAt  time.Time
	FinishedAt time.Time
	LastActive time.Time
}

// Outcome is returned after a submitted answer.
type Outcome struct {
	Record       Record `json:"-"`
	Correct      bool   `json:"correct"`
	Choice       int    `json:"choice"`
	CorrectIndex int    `json:"correctIndex"`
	Score        int    `json:"score"`
	Answered     int    `json:"answered"`
	Total        int    `json:"total"`
	Streak       int    `json:"streak"`
	ResponseMS   int64  `json:"responseMs"`
	Duplicate    bool   `json:"duplicate,omitempty"`
	Done         bool   `json:"done"`
}

func NewID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func NewSession(playerID, kind string, qs []*content.Question, confidence bool, seed int64, now time.Time) *Session {
	rng := mrand.New(mrand.NewSource(seed))
	s := &Session{ID: NewID(), PlayerID: playerID, Kind: kind, Confidence: confidence, Questions: qs,
		StartedAt: now, IssuedAt: now, LastActive: now}
	for _, q := range qs {
		s.Orders = append(s.Orders, ShuffleOrder(q, rng))
	}
	return s
}

func (s *Session) Lock()   { s.mu.Lock() }
func (s *Session) Unlock() { s.mu.Unlock() }

// Done reports whether every question has been answered.
func (s *Session) Done() bool { return s.Index >= len(s.Questions) }

// Score counts correct answers.
func (s *Session) Score() int {
	n := 0
	for _, r := range s.Records {
		if r.Correct {
			n++
		}
	}
	return n
}

// Current returns the public view of the current question (nil when done).
func (s *Session) Current(tax *content.Taxonomy) *QuestionView {
	if s.Done() {
		return nil
	}
	v := BuildQuestionView(s.Questions[s.Index], s.Orders[s.Index], tax, s.Index, len(s.Questions))
	return &v
}

// Submit validates and scores an answer. Re-submitting the previous question
// (a network retry) is idempotent and returns the original outcome.
func (s *Session) Submit(questionID string, display, confidence int, now time.Time) (*Outcome, error) {
	s.LastActive = now
	if n := len(s.Records); n > 0 && s.Records[n-1].QuestionID == questionID {
		out := s.outcome(n-1, s.Records[n-1])
		out.Duplicate = true
		return out, nil
	}
	if s.Done() {
		return nil, ErrSessionFinished
	}
	q := s.Questions[s.Index]
	if q.ID != questionID {
		return nil, ErrStaleQuestion
	}
	order := s.Orders[s.Index]
	if display < 0 || display >= len(order) {
		return nil, ErrInvalidChoice
	}
	if confidence < 0 || confidence > 3 || !s.Confidence {
		confidence = 0
	}
	rt := now.Sub(s.IssuedAt)
	if rt > MaxCountedResponse {
		rt = MaxCountedResponse
	}
	orig := order[display]
	rec := Record{QuestionID: q.ID, Category: q.Category, Difficulty: string(q.Difficulty), Choice: orig, Display: display,
		Correct: orig == q.CorrectIndex, Confidence: confidence, ResponseMS: rt.Milliseconds(), At: now}
	s.Records = append(s.Records, rec)
	if rec.Correct {
		s.Streak++
		s.BestStreak = max(s.BestStreak, s.Streak)
	} else {
		s.Streak = 0
	}
	idx := s.Index
	s.Index++
	s.IssuedAt = now
	if s.Done() {
		s.FinishedAt = now
	}
	return s.outcome(idx, rec), nil
}

func (s *Session) outcome(idx int, rec Record) *Outcome {
	return &Outcome{
		Record: rec, Correct: rec.Correct, Choice: rec.Display,
		CorrectIndex: DisplayIndexOf(s.Orders[idx], s.Questions[idx].CorrectIndex),
		Score:        s.Score(), Answered: len(s.Records), Total: len(s.Questions), Streak: s.Streak,
		ResponseMS: rec.ResponseMS, Done: s.Done(),
	}
}

// RevealFor returns the reveal payload for question idx.
func (s *Session) RevealFor(idx int) RevealView { return BuildReveal(s.Questions[idx], s.Orders[idx]) }

// Manager holds live solo sessions in memory with a TTL.
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*Session
	ttl      time.Duration
}

func NewManager(ttl time.Duration) *Manager {
	return &Manager{sessions: map[string]*Session{}, ttl: ttl}
}

func (m *Manager) Put(s *Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[s.ID] = s
}

// Get returns a session owned by playerID.
func (m *Manager) Get(id, playerID string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return nil, ErrNotFound
	}
	if s.PlayerID != playerID {
		return nil, ErrForbidden
	}
	return s, nil
}

// FindDaily returns an unfinished daily session for the player and day.
func (m *Manager) FindDaily(playerID, day string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sessions {
		if s.PlayerID == playerID && s.Kind == "daily" && s.DailyDate == day && !s.Done() {
			return s
		}
	}
	return nil
}

// Sweep removes sessions idle for longer than the TTL.
func (m *Manager) Sweep(now time.Time) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for id, s := range m.sessions {
		if now.Sub(s.LastActive) > m.ttl {
			delete(m.sessions, id)
			n++
		}
	}
	return n
}

func (m *Manager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions)
}
