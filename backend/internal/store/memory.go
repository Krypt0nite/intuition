package store

import (
	"context"
	"sort"
	"sync"
	"time"

	"intuition/internal/selection"
)

// MemoryStore keeps everything in process memory. Data is lost on restart.
type MemoryStore struct {
	mu       sync.RWMutex
	players  map[string]Player
	answers  map[string][]AnswerRecord // player -> chronological
	stats    map[string]map[string]selection.History
	sessions map[string][]SessionRecord
	daily    map[string][]string
	dailyRes map[string]map[string]DailyResult // day -> player -> result
}

func NewMemory() *MemoryStore {
	return &MemoryStore{
		players: map[string]Player{}, answers: map[string][]AnswerRecord{},
		stats: map[string]map[string]selection.History{}, sessions: map[string][]SessionRecord{},
		daily: map[string][]string{}, dailyRes: map[string]map[string]DailyResult{},
	}
}

func (m *MemoryStore) Kind() string { return "memory" }
func (m *MemoryStore) Close() error { return nil }

func (m *MemoryStore) UpsertPlayer(_ context.Context, p Player) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	if old, ok := m.players[p.ID]; ok {
		old.Name, old.LastSeenAt = p.Name, now
		m.players[p.ID] = old
		return nil
	}
	p.CreatedAt, p.LastSeenAt = now, now
	m.players[p.ID] = p
	return nil
}

func (m *MemoryStore) GetPlayer(_ context.Context, id string) (*Player, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.players[id]
	if !ok {
		return nil, ErrNotFound
	}
	return &p, nil
}

func (m *MemoryStore) RecordAnswers(_ context.Context, recs []AnswerRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range recs {
		if r.AnsweredAt.IsZero() {
			r.AnsweredAt = time.Now()
		}
		m.answers[r.PlayerID] = append(m.answers[r.PlayerID], r)
		ps := m.stats[r.PlayerID]
		if ps == nil {
			ps = map[string]selection.History{}
			m.stats[r.PlayerID] = ps
		}
		h := ps[r.QuestionID]
		h.TimesSeen++
		if r.Correct {
			h.TimesCorrect++
		} else {
			h.TimesWrong++
		}
		h.LastSeenAt, h.LastCorrect = r.AnsweredAt, r.Correct
		ps[r.QuestionID] = h
	}
	return nil
}

func (m *MemoryStore) PlayerHistory(_ context.Context, playerID string) (map[string]selection.History, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := map[string]selection.History{}
	for k, v := range m.stats[playerID] {
		out[k] = v
	}
	return out, nil
}

func (m *MemoryStore) PlayerAnswers(_ context.Context, playerID string, limit int) ([]AnswerRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	all := m.answers[playerID]
	out := make([]AnswerRecord, 0, min(limit, len(all)))
	for i := len(all) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, all[i])
	}
	return out, nil
}

func (m *MemoryStore) SaveSession(_ context.Context, s SessionRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[s.PlayerID] = append(m.sessions[s.PlayerID], s)
	return nil
}

func (m *MemoryStore) RecentSessions(_ context.Context, playerID string, limit int) ([]SessionRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	all := m.sessions[playerID]
	var out []SessionRecord
	for i := len(all) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, all[i])
	}
	return out, nil
}

func (m *MemoryStore) DailySet(_ context.Context, day string) ([]string, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids, ok := m.daily[day]
	return ids, ok, nil
}

func (m *MemoryStore) SaveDailySet(_ context.Context, day string, ids []string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.daily[day]; ok {
		return existing, nil
	}
	m.daily[day] = ids
	return ids, nil
}

func (m *MemoryStore) DailyQuestionIDsSince(_ context.Context, day string) (map[string]bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := map[string]bool{}
	for d, ids := range m.daily {
		if d >= day {
			for _, id := range ids {
				out[id] = true
			}
		}
	}
	return out, nil
}

func (m *MemoryStore) StartDaily(_ context.Context, r DailyResult) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	day := m.dailyRes[r.Day]
	if day == nil {
		day = map[string]DailyResult{}
		m.dailyRes[r.Day] = day
	}
	if _, ok := day[r.PlayerID]; ok {
		return false, nil
	}
	day[r.PlayerID] = r
	return true, nil
}

func (m *MemoryStore) UpdateDaily(_ context.Context, r DailyResult) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	day := m.dailyRes[r.Day]
	if day == nil {
		return ErrNotFound
	}
	old, ok := day[r.PlayerID]
	if !ok {
		return ErrNotFound
	}
	r.StartedAt = old.StartedAt
	day[r.PlayerID] = r
	return nil
}

func (m *MemoryStore) GetDaily(_ context.Context, dayKey, playerID string) (*DailyResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.dailyRes[dayKey][playerID]
	if !ok {
		return nil, ErrNotFound
	}
	return &r, nil
}

func (m *MemoryStore) sortedDaily(dayKey string) []DailyResult {
	var list []DailyResult
	for _, r := range m.dailyRes[dayKey] {
		if r.Finished {
			r.PlayerName = m.players[r.PlayerID].Name
			list = append(list, r)
		}
	}
	sort.Slice(list, func(i, j int) bool { return rankLess(list[i], list[j]) })
	return list
}

func rankLess(a, b DailyResult) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	if a.TotalMS != b.TotalMS {
		return a.TotalMS < b.TotalMS
	}
	return a.FinishedAt.Before(b.FinishedAt)
}

func (m *MemoryStore) DailyLeaderboard(_ context.Context, dayKey string, limit int) ([]DailyResult, int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	list := m.sortedDaily(dayKey)
	total := len(list)
	if len(list) > limit {
		list = list[:limit]
	}
	return list, total, nil
}

func (m *MemoryStore) DailyRank(_ context.Context, dayKey, playerID string) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for i, r := range m.sortedDaily(dayKey) {
		if r.PlayerID == playerID {
			return i + 1, nil
		}
	}
	return 0, ErrNotFound
}
