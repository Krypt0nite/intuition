package server

import (
	"context"
	"errors"
	"hash/fnv"
	"math/rand"
	"net/http"
	"sort"
	"time"

	"intuition/internal/content"
	"intuition/internal/game"
	"intuition/internal/selection"
	"intuition/internal/store"
)

var modeCounts = map[string]int{"quick": 5, "standard": 10, "marathon": 25}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "questions": s.sel.PoolSize(), "contentVersion": s.lib.Version,
		"store": s.store.Kind(), "rooms": s.rooms.Count(), "sessions": s.sessions.Count(),
	})
}

func (s *Server) handleCategories(w http.ResponseWriter, r *http.Request) {
	counts := map[string]int{}
	for _, q := range s.lib.Servable(false) {
		counts[q.Category]++
	}
	type cat struct {
		*content.CategoryInfo
		Count int `json:"count"`
	}
	out := []cat{}
	for _, c := range s.lib.Taxonomy.Categories {
		out = append(out, cat{c, counts[c.ID]})
	}
	writeJSON(w, http.StatusOK, map[string]any{"categories": out})
}

// ---- Players ----

func (s *Server) handleCreatePlayer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	name := CleanName(body.Name)
	if name == "" {
		name = "Curious player"
	}
	id := newPlayerID()
	if err := s.store.UpsertPlayer(r.Context(), store.Player{ID: id, Name: name}); err != nil {
		s.log.Error("create player", "err", err)
		writeErr(w, http.StatusInternalServerError, "store_error", "Could not create player")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"playerId": id, "token": s.sign(id), "name": name})
}

func (s *Server) handleRenamePlayer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &body); err != nil || CleanName(body.Name) == "" {
		writeErr(w, http.StatusBadRequest, "bad_name", "Names need 1-24 characters")
		return
	}
	name := CleanName(body.Name)
	if err := s.store.UpsertPlayer(r.Context(), store.Player{ID: playerID(r), Name: name}); err != nil {
		writeErr(w, http.StatusInternalServerError, "store_error", "Could not save name")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": name})
}

// ensurePlayer recreates a player row if the token is valid but the store is
// fresh (e.g. the in-memory store after a restart).
func (s *Server) ensurePlayer(ctx context.Context, id string) {
	if _, err := s.store.GetPlayer(ctx, id); errors.Is(err, store.ErrNotFound) {
		_ = s.store.UpsertPlayer(ctx, store.Player{ID: id, Name: "Curious player"})
	}
}

// ---- Solo sessions ----

func (s *Server) handleStartSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode       string   `json:"mode"`
		Categories []string `json:"categories"`
		Confidence bool     `json:"confidence"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	n, ok := modeCounts[body.Mode]
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad_mode", "mode must be quick, standard or marathon")
		return
	}
	pid := playerID(r)
	s.ensurePlayer(r.Context(), pid)
	prof, err := store.LoadProfile(r.Context(), s.store, s.lib, pid)
	if err != nil {
		s.log.Warn("load profile", "err", err)
	}
	picks := s.sel.Select(prof, selection.Request{Count: n, Categories: body.Categories, AllowChains: true, Seed: rand.Int63(), Now: time.Now()})
	if len(picks) == 0 {
		writeErr(w, http.StatusUnprocessableEntity, "no_questions", "No questions match those categories")
		return
	}
	qs := make([]*content.Question, len(picks))
	for i, p := range picks {
		qs[i] = p.Question
	}
	sess := game.NewSession(pid, body.Mode, qs, body.Confidence, rand.Int63(), time.Now())
	s.sessions.Put(sess)
	writeJSON(w, http.StatusCreated, s.sessionState(sess))
}

func (s *Server) sessionState(sess *game.Session) map[string]any {
	out := map[string]any{
		"sessionId": sess.ID, "kind": sess.Kind, "total": len(sess.Questions), "confidence": sess.Confidence,
		"answered": len(sess.Records), "score": sess.Score(), "streak": sess.Streak, "question": sess.Current(s.lib.Taxonomy),
	}
	if sess.DailyDate != "" {
		out["dailyDate"] = sess.DailyDate
	}
	if sess.Done() {
		out["summary"] = game.Summarize(sess.Records, s.lib)
	}
	return out
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	sess, err := s.sessions.Get(r.PathValue("id"), playerID(r))
	if err != nil {
		writeErr(w, http.StatusNotFound, "session_not_found", "This game has expired; start a new one")
		return
	}
	sess.Lock()
	defer sess.Unlock()
	writeJSON(w, http.StatusOK, s.sessionState(sess))
}

func (s *Server) handleAnswer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		QuestionID string `json:"questionId"`
		Choice     int    `json:"choice"`
		Confidence int    `json:"confidence"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	pid := playerID(r)
	sess, err := s.sessions.Get(r.PathValue("id"), pid)
	if err != nil {
		writeErr(w, http.StatusNotFound, "session_not_found", "This game has expired; start a new one")
		return
	}
	sess.Lock()
	defer sess.Unlock()
	now := time.Now()
	out, err := sess.Submit(body.QuestionID, body.Choice, body.Confidence, now)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error(), "That answer doesn't match the current question")
		return
	}
	idx := out.Answered - 1
	if !out.Duplicate {
		q := sess.Questions[idx]
		ctxName := "solo"
		if sess.Kind == "daily" {
			ctxName = "daily"
		}
		rec := out.Record
		if err := s.store.RecordAnswers(r.Context(), []store.AnswerRecord{{PlayerID: pid, QuestionID: q.ID, Category: q.Category,
			Difficulty: string(q.Difficulty), Context: ctxName, SessionID: sess.ID, Choice: rec.Choice, Correct: rec.Correct,
			Confidence: rec.Confidence, ResponseMS: rec.ResponseMS, AnsweredAt: now}}); err != nil {
			s.log.Warn("record answer", "err", err)
		}
		if sess.Kind == "daily" {
			s.updateDaily(r.Context(), sess, out.Done, now)
		}
	}
	resp := map[string]any{"result": out, "reveal": sess.RevealFor(idx), "next": sess.Current(s.lib.Taxonomy)}
	if out.Done {
		sum := game.Summarize(sess.Records, s.lib)
		resp["summary"] = sum
		if !out.Duplicate {
			if err := s.store.SaveSession(r.Context(), store.SessionRecord{ID: sess.ID, PlayerID: pid, Kind: sess.Kind,
				Score: sum.Score, Total: sum.Total, StartedAt: sess.StartedAt, FinishedAt: now,
				Summary: map[string]any{"accuracy": sum.Accuracy, "bestStreak": sum.BestStreak, "byCategory": sum.ByCategory, "surprises": sum.Surprises}}); err != nil {
				s.log.Warn("save session", "err", err)
			}
		}
		if sess.Kind == "daily" {
			if rank, err := s.store.DailyRank(r.Context(), sess.DailyDate, pid); err == nil {
				resp["dailyRank"] = rank
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// ---- Daily challenge ----

func (s *Server) today(now time.Time) (string, time.Time) {
	local := now.In(s.cfg.DailyLocation)
	day := local.Format("2006-01-02")
	y, m, d := local.Date()
	next := time.Date(y, m, d+1, 0, 0, 0, 0, s.cfg.DailyLocation)
	return day, next
}

func (s *Server) dailySet(ctx context.Context, day string) ([]*content.Question, error) {
	s.dailyMu.Lock()
	defer s.dailyMu.Unlock()
	resolve := func(ids []string) []*content.Question {
		var qs []*content.Question
		for _, id := range ids {
			if q := s.lib.Get(id); q != nil && q.Servable(false) {
				qs = append(qs, q)
			}
		}
		return qs
	}
	if ids, ok := s.lib.Schedule[day]; ok {
		if qs := resolve(ids); len(qs) >= 3 {
			return qs, nil
		}
	}
	if ids, ok, err := s.store.DailySet(ctx, day); err != nil {
		return nil, err
	} else if ok {
		if qs := resolve(ids); len(qs) >= 3 {
			return qs, nil
		}
	}
	since, _ := time.Parse("2006-01-02", day)
	recent, err := s.store.DailyQuestionIDsSince(ctx, since.AddDate(0, 0, -60).Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	for _, ids := range s.lib.Schedule {
		for _, id := range ids {
			recent[id] = true
		}
	}
	qs := s.sel.Daily(day, s.cfg.DailySalt, recent)
	ids := make([]string, len(qs))
	for i, q := range qs {
		ids[i] = q.ID
	}
	stored, err := s.store.SaveDailySet(ctx, day, ids)
	if err != nil {
		return nil, err
	}
	return resolve(stored), nil
}

func (s *Server) handleDaily(w http.ResponseWriter, r *http.Request) {
	pid := playerID(r)
	day, next := s.today(time.Now())
	board, total, err := s.store.DailyLeaderboard(r.Context(), day, 20)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "store_error", "Could not load the daily leaderboard")
		return
	}
	out := map[string]any{"date": day, "nextResetAt": next.UnixMilli(), "leaderboard": nonNil(board), "totalPlayers": total,
		"questionCount": 5, "played": false, "inProgress": false}
	if res, err := s.store.GetDaily(r.Context(), day, pid); err == nil {
		out["result"] = res
		out["played"] = res.Finished
		out["inProgress"] = !res.Finished && s.sessions.FindDaily(pid, day) != nil
		if res.Finished {
			if rank, err := s.store.DailyRank(r.Context(), day, pid); err == nil {
				out["rank"] = rank
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleDailyStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Confidence bool `json:"confidence"`
	}
	_ = readJSON(r, &body)
	pid := playerID(r)
	s.ensurePlayer(r.Context(), pid)
	now := time.Now()
	day, _ := s.today(now)
	if sess := s.sessions.FindDaily(pid, day); sess != nil {
		sess.Lock()
		defer sess.Unlock()
		writeJSON(w, http.StatusOK, s.sessionState(sess))
		return
	}
	qs, err := s.dailySet(r.Context(), day)
	if err != nil || len(qs) == 0 {
		writeErr(w, http.StatusInternalServerError, "daily_unavailable", "Today's challenge isn't available")
		return
	}
	inserted, err := s.store.StartDaily(r.Context(), store.DailyResult{Day: day, PlayerID: pid, Total: len(qs), StartedAt: now})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "store_error", "Could not start the daily challenge")
		return
	}
	if !inserted {
		// Already played (or abandoned after a restart): one attempt per day.
		res, _ := s.store.GetDaily(r.Context(), day, pid)
		if res != nil && !res.Finished {
			res.Finished, res.FinishedAt = true, now
			_ = s.store.UpdateDaily(r.Context(), *res)
		}
		writeJSON(w, http.StatusConflict, map[string]any{"error": "already_played", "message": "You've already played today's challenge", "result": res})
		return
	}
	h := fnv.New64a()
	h.Write([]byte(day))
	sess := game.NewSession(pid, "daily", qs, body.Confidence, int64(h.Sum64()>>1), now)
	sess.DailyDate = day
	s.sessions.Put(sess)
	writeJSON(w, http.StatusCreated, s.sessionState(sess))
}

func (s *Server) updateDaily(ctx context.Context, sess *game.Session, done bool, now time.Time) {
	var total int64
	for _, rec := range sess.Records {
		total += rec.ResponseMS
	}
	res := store.DailyResult{Day: sess.DailyDate, PlayerID: sess.PlayerID, Score: sess.Score(), Total: len(sess.Questions),
		TotalMS: total, Finished: done, StartedAt: sess.StartedAt}
	if done {
		res.FinishedAt = now
	}
	if err := s.store.UpdateDaily(ctx, res); err != nil {
		s.log.Warn("update daily", "err", err)
	}
}

func (s *Server) handleDailyLeaderboard(w http.ResponseWriter, r *http.Request) {
	day := r.URL.Query().Get("date")
	if _, err := time.Parse("2006-01-02", day); err != nil {
		day, _ = s.today(time.Now())
	}
	board, total, err := s.store.DailyLeaderboard(r.Context(), day, 50)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "store_error", "Could not load the leaderboard")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"date": day, "leaderboard": nonNil(board), "totalPlayers": total})
}

func nonNil(b []store.DailyResult) []store.DailyResult {
	if b == nil {
		return []store.DailyResult{}
	}
	return b
}

// ---- Lifetime stats ----

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	pid := playerID(r)
	answers, err := s.store.PlayerAnswers(r.Context(), pid, 5000)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "store_error", "Could not load stats")
		return
	}
	sessions, _ := s.store.RecentSessions(r.Context(), pid, 10)
	type tally = game.Tally
	byCat, byDiff := map[string]tally{}, map[string]tally{}
	calib := map[int]tally{1: {}, 2: {}, 3: {}}
	concept := map[string]tally{}
	correct, best, run, confident, surprises := 0, 0, 0, 0, 0
	var rt int64
	// answers are newest first; walk oldest first for streaks.
	for i := len(answers) - 1; i >= 0; i-- {
		a := answers[i]
		add := func(m map[string]tally, k string) {
			t := m[k]
			t.Total++
			if a.Correct {
				t.Correct++
			}
			m[k] = t
		}
		add(byCat, a.Category)
		add(byDiff, a.Difficulty)
		if q := s.lib.Get(a.QuestionID); q != nil {
			for _, c := range q.Concepts {
				add(concept, c)
			}
		}
		if a.Confidence > 0 {
			t := calib[a.Confidence]
			t.Total++
			if a.Correct {
				t.Correct++
			}
			calib[a.Confidence] = t
		}
		if a.Confidence >= 2 {
			confident++
			if !a.Correct {
				surprises++
			}
		}
		rt += a.ResponseMS
		if a.Correct {
			correct++
			run++
			best = max(best, run)
		} else {
			run = 0
		}
	}
	mastered := []string{}
	for c, t := range concept {
		if t.Total >= 2 && t.Accuracy() >= 0.8 {
			mastered = append(mastered, c)
		}
	}
	sort.Strings(mastered)
	out := map[string]any{
		"answered": len(answers), "correct": correct, "bestStreak": best, "currentStreak": run,
		"byCategory": byCat, "byDifficulty": byDiff, "calibration": calib,
		"conceptsMastered": len(mastered), "conceptsSeen": len(concept),
		"surprises": surprises, "confidentAnswers": confident, "recentSessions": sessions,
		"tier": selection.Tier(func() *selection.Profile { p := selection.NewProfile(); p.TotalAnswered = len(answers); return p }()),
	}
	if len(answers) > 0 {
		out["accuracy"] = float64(correct) / float64(len(answers))
		out["avgResponseMs"] = rt / int64(len(answers))
	}
	if confident > 0 {
		out["surpriseRate"] = float64(surprises) / float64(confident)
	}
	writeJSON(w, http.StatusOK, out)
}
