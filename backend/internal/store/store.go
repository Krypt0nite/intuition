// Package store persists players, answer history, sessions and daily results.
// MemoryStore is used for development/tests; PostgresStore for production.
package store

import (
	"context"
	"errors"
	"time"

	"intuition/internal/content"
	"intuition/internal/selection"
)

var ErrNotFound = errors.New("not found")

type Player struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
}

// AnswerRecord is one answered (or timed-out) question.
type AnswerRecord struct {
	PlayerID   string    `json:"playerId"`
	QuestionID string    `json:"questionId"`
	Category   string    `json:"category"`
	Difficulty string    `json:"difficulty"`
	Context    string    `json:"context"` // solo | daily | room
	SessionID  string    `json:"sessionId"`
	Choice     int       `json:"choice"` // original option index, -1 = no answer
	Correct    bool      `json:"correct"`
	Confidence int       `json:"confidence"` // 0 none, 1 guessing, 2 fairly sure, 3 certain
	ResponseMS int64     `json:"responseMs"`
	AnsweredAt time.Time `json:"answeredAt"`
}

type SessionRecord struct {
	ID         string         `json:"id"`
	PlayerID   string         `json:"playerId"`
	Kind       string         `json:"kind"`
	Score      int            `json:"score"`
	Total      int            `json:"total"`
	Summary    map[string]any `json:"summary,omitempty"`
	StartedAt  time.Time      `json:"startedAt"`
	FinishedAt time.Time      `json:"finishedAt"`
}

type DailyResult struct {
	Day        string    `json:"day"`
	PlayerID   string    `json:"playerId"`
	PlayerName string    `json:"playerName,omitempty"`
	Score      int       `json:"score"`
	Total      int       `json:"total"`
	TotalMS    int64     `json:"totalMs"`
	Finished   bool      `json:"finished"`
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt,omitempty"`
}

// Store is the persistence contract shared by memory and Postgres backends.
type Store interface {
	UpsertPlayer(ctx context.Context, p Player) error
	GetPlayer(ctx context.Context, id string) (*Player, error)

	RecordAnswers(ctx context.Context, recs []AnswerRecord) error
	PlayerHistory(ctx context.Context, playerID string) (map[string]selection.History, error)
	PlayerAnswers(ctx context.Context, playerID string, limit int) ([]AnswerRecord, error) // newest first

	SaveSession(ctx context.Context, s SessionRecord) error
	RecentSessions(ctx context.Context, playerID string, limit int) ([]SessionRecord, error)

	DailySet(ctx context.Context, day string) ([]string, bool, error)
	SaveDailySet(ctx context.Context, day string, ids []string) ([]string, error) // returns the stored set (first writer wins)
	DailyQuestionIDsSince(ctx context.Context, day string) (map[string]bool, error)
	StartDaily(ctx context.Context, r DailyResult) (bool, error) // false if a row already existed
	UpdateDaily(ctx context.Context, r DailyResult) error
	GetDaily(ctx context.Context, day, playerID string) (*DailyResult, error)
	DailyLeaderboard(ctx context.Context, day string, limit int) ([]DailyResult, int, error) // entries, total finished
	DailyRank(ctx context.Context, day, playerID string) (int, error)

	Kind() string
	Close() error
}

// LoadProfile builds the selection profile for a player from stored history.
func LoadProfile(ctx context.Context, st Store, lib *content.Library, playerID string) (*selection.Profile, error) {
	p := selection.NewProfile()
	if playerID == "" {
		return p, nil
	}
	hist, err := st.PlayerHistory(ctx, playerID)
	if err != nil {
		return p, err
	}
	p.Seen = hist
	for _, h := range hist {
		p.TotalAnswered += h.TimesSeen
	}
	recent, err := st.PlayerAnswers(ctx, playerID, 25)
	if err != nil {
		return p, err
	}
	for _, a := range recent {
		if q := lib.Get(a.QuestionID); q != nil {
			for _, c := range q.Concepts {
				p.RecentConcepts[c]++
			}
		}
	}
	return p, nil
}
