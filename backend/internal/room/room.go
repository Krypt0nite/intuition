// Package room implements real-time multiplayer rooms. Each room is an actor:
// a single goroutine owns all state and processes closures from its inbox, so
// there are no locks around game state and no data races.
package room

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"math/rand"
	"slices"
	"sort"
	"time"

	"intuition/internal/content"
	"intuition/internal/game"
	"intuition/internal/selection"
	"intuition/internal/store"
)

type Phase string

const (
	PhaseLobby    Phase = "lobby"
	PhaseQuestion Phase = "question"
	PhaseReveal   Phase = "reveal"
	PhaseResults  Phase = "results"
	PhaseClosed   Phase = "closed"
)

const MaxPlayers = 16

var (
	ErrRoomClosed      = errors.New("room_closed")
	ErrRoomFull        = errors.New("room_full")
	ErrGameInProgress  = errors.New("game_in_progress")
	ErrNotHost         = errors.New("not_host")
	ErrBadPhase        = errors.New("wrong_phase")
	ErrNotInRoom       = errors.New("not_in_room")
	ErrStaleQuestion   = errors.New("stale_question")
	ErrAlreadyAnswered = errors.New("already_answered")
	ErrInvalidChoice   = errors.New("invalid_choice")
	ErrTooLate         = errors.New("too_late")
	ErrNoQuestions     = errors.New("no_questions")
	ErrNoPlayers       = errors.New("no_players")
	ErrTimeout         = errors.New("timeout")
)

// Conn is a player's connection. Send must never block.
type Conn interface {
	Send(msg []byte) bool
}

type Settings struct {
	Questions     int      `json:"questions"`
	TimerSeconds  int      `json:"timerSeconds"`
	RevealSeconds int      `json:"revealSeconds"`
	Categories    []string `json:"categories"`
	Confidence    bool     `json:"confidence"`
	SpeedBonus    bool     `json:"speedBonus"`
	AllowLateJoin bool     `json:"allowLateJoin"`
}

func DefaultSettings() Settings {
	return Settings{Questions: 10, TimerSeconds: 25, RevealSeconds: 20, Categories: []string{}, SpeedBonus: true, AllowLateJoin: true}
}

// Normalize clamps settings to supported values.
func (s Settings) Normalize(tax *content.Taxonomy) Settings {
	switch {
	case s.Questions <= 5:
		s.Questions = 5
	case s.Questions <= 10:
		s.Questions = 10
	default:
		s.Questions = 25
	}
	s.TimerSeconds = min(max(s.TimerSeconds, 10), 60)
	if s.RevealSeconds == 0 {
		s.RevealSeconds = 20
	}
	s.RevealSeconds = min(max(s.RevealSeconds, 8), 60)
	cats := []string{}
	for _, c := range s.Categories {
		if tax.Category(c) != nil && !slices.Contains(cats, c) {
			cats = append(cats, c)
		}
	}
	s.Categories = cats
	return s
}

// Timing lets tests run rooms with millisecond timers.
type Timing struct {
	Question  func(Settings) time.Duration
	Reveal    func(Settings) time.Duration
	HostGrace time.Duration
	LobbyDrop time.Duration // remove players disconnected this long while in the lobby
}

func DefaultTiming() Timing {
	return Timing{
		Question:  func(s Settings) time.Duration { return time.Duration(s.TimerSeconds) * time.Second },
		Reveal:    func(s Settings) time.Duration { return time.Duration(s.RevealSeconds) * time.Second },
		HostGrace: 20 * time.Second,
		LobbyDrop: 90 * time.Second,
	}
}

type Deps struct {
	Lib      *content.Library
	Selector *selection.Selector
	Store    store.Store
	Timing   Timing
	Log      *slog.Logger
	onClose  func(code string)
}

type player struct {
	ID, Name       string
	conn           Conn
	Connected      bool
	DisconnectedAt time.Time
	JoinedAt       time.Time
	Late           bool
	Score, Speed   int
	Streak, Best   int
	Ready          bool
	records        []game.Record
	lobbyGen       int
}

type answer struct {
	Display, Original int
	Correct           bool
	Confidence        int
	At                time.Time
	ResponseMS        int64
}

type questionStat struct {
	QuestionID   string
	Participants int
	Correct      int
}

// Room is safe for concurrent use through its exported methods.
type Room struct {
	Code  string
	deps  Deps
	inbox chan func()
	done  chan struct{}

	// Actor-owned state.
	phase        Phase
	hostID       string
	settings     Settings
	players      map[string]*player
	order        []string
	questions    []*content.Question
	orders       [][]int
	qIndex       int
	qStarted     time.Time
	deadline     time.Time
	revealEnds   time.Time
	answers      map[string]*answer
	participants map[string]bool
	stats        []questionStat
	timerGen     int
	hostGen      int
	gameID       string
	startedAt    time.Time
	lastActive   time.Time
	lastQuestion []byte
	lastReveal   []byte
	lastResults  []byte
}

func newRoom(code string, settings Settings, deps Deps) *Room {
	r := &Room{
		Code: code, deps: deps, inbox: make(chan func(), 64), done: make(chan struct{}),
		phase: PhaseLobby, settings: settings, players: map[string]*player{}, lastActive: time.Now(),
	}
	go r.run()
	return r
}

func (r *Room) run() {
	for f := range r.inbox {
		f()
		if r.phase == PhaseClosed {
			close(r.done)
			if r.deps.onClose != nil {
				r.deps.onClose(r.Code)
			}
			return
		}
	}
}

// do schedules f on the room goroutine. It returns false if the room is closed.
func (r *Room) do(f func()) bool {
	select {
	case <-r.done:
		return false
	default:
	}
	select {
	case r.inbox <- f:
		return true
	case <-r.done:
		return false
	}
}

// call runs f on the room goroutine and waits for its error result.
func (r *Room) call(f func() error) error {
	res := make(chan error, 1)
	if !r.do(func() { res <- f() }) {
		return ErrRoomClosed
	}
	select {
	case err := <-res:
		return err
	case <-r.done:
		return ErrRoomClosed
	case <-time.After(5 * time.Second):
		return ErrTimeout
	}
}

// ---- Public API (each call is executed on the room goroutine) ----

func (r *Room) Join(id, name string, c Conn) error {
	return r.call(func() error { return r.join(id, name, c) })
}
func (r *Room) Leave(id string)              { r.do(func() { r.leave(id) }) }
func (r *Room) Disconnect(id string, c Conn) { r.do(func() { r.disconnect(id, c) }) }
func (r *Room) Start(by string) error        { return r.call(func() error { return r.start(by) }) }
func (r *Room) Ready(id string) error        { return r.call(func() error { return r.ready(id) }) }
func (r *Room) Next(by string) error         { return r.call(func() error { return r.skip(by) }) }
func (r *Room) PlayAgain(by string) error    { return r.call(func() error { return r.playAgain(by) }) }
func (r *Room) UpdateSettings(by string, s Settings) error {
	return r.call(func() error { return r.updateSettings(by, s) })
}
func (r *Room) Answer(id, qid string, choice, confidence int) error {
	return r.call(func() error { return r.answer(id, qid, choice, confidence) })
}

// Snapshot returns the current public state (used by tests and diagnostics).
func (r *Room) Snapshot() StateView {
	ch := make(chan StateView, 1)
	if !r.do(func() { ch <- r.state() }) {
		return StateView{Code: r.Code, Phase: PhaseClosed}
	}
	select {
	case s := <-ch:
		return s
	case <-time.After(5 * time.Second):
		return StateView{Code: r.Code}
	}
}

// Closed reports whether the room has shut down.
func (r *Room) Closed() bool {
	select {
	case <-r.done:
		return true
	default:
		return false
	}
}

// ---- Actor-side implementation ----

func (r *Room) now() time.Time { return time.Now() }

func (r *Room) join(id, name string, c Conn) error {
	p, exists := r.players[id]
	if !exists {
		if len(r.players) >= MaxPlayers {
			return ErrRoomFull
		}
		if r.phase != PhaseLobby && !r.settings.AllowLateJoin {
			return ErrGameInProgress
		}
		p = &player{ID: id, Name: name, JoinedAt: r.now(), Late: r.phase == PhaseQuestion || r.phase == PhaseReveal}
		r.players[id] = p
		r.order = append(r.order, id)
		if r.hostID == "" {
			r.hostID = id
		}
	} else if name != "" {
		p.Name = name
	}
	if p.conn != nil && p.conn != c {
		p.conn.Send(envelope("error", map[string]string{"code": "replaced", "message": "You joined this room from another tab."}))
	}
	p.conn, p.Connected, p.DisconnectedAt = c, true, time.Time{}
	p.lobbyGen++
	if id == r.hostID {
		r.hostGen++ // cancel any pending host migration
	}
	r.lastActive = r.now()
	r.broadcastState()
	r.catchUp(p)
	return nil
}

func (r *Room) catchUp(p *player) {
	switch r.phase {
	case PhaseQuestion:
		p.conn.Send(r.lastQuestion)
		if a := r.answers[p.ID]; a != nil {
			p.conn.Send(envelope("answer_ack", map[string]any{"questionId": r.questions[r.qIndex].ID, "choice": a.Display}))
		}
	case PhaseReveal:
		p.conn.Send(r.lastReveal)
	case PhaseResults:
		p.conn.Send(r.lastResults)
	}
}

func (r *Room) leave(id string) {
	if _, ok := r.players[id]; !ok {
		return
	}
	delete(r.players, id)
	delete(r.answers, id)
	delete(r.participants, id)
	r.order = slices.DeleteFunc(r.order, func(s string) bool { return s == id })
	if len(r.players) == 0 {
		r.close()
		return
	}
	if r.hostID == id {
		r.migrateHost()
	}
	r.lastActive = r.now()
	r.broadcastState()
	r.checkProgress()
}

func (r *Room) disconnect(id string, c Conn) {
	p := r.players[id]
	if p == nil || p.conn != c {
		return // stale connection (player already reconnected elsewhere)
	}
	p.conn, p.Connected, p.DisconnectedAt = nil, false, r.now()
	if id == r.hostID {
		r.hostGen++
		gen := r.hostGen
		time.AfterFunc(r.deps.Timing.HostGrace, func() {
			r.do(func() {
				if gen == r.hostGen {
					if hp := r.players[r.hostID]; hp != nil && !hp.Connected {
						r.migrateHost()
						r.broadcastState()
					}
				}
			})
		})
	}
	if r.phase == PhaseLobby {
		p.lobbyGen++
		gen := p.lobbyGen
		time.AfterFunc(r.deps.Timing.LobbyDrop, func() {
			r.do(func() {
				if q := r.players[id]; q != nil && !q.Connected && q.lobbyGen == gen && r.phase == PhaseLobby {
					r.leave(id)
				}
			})
		})
	}
	r.broadcastState()
	r.checkProgress()
}

func (r *Room) migrateHost() {
	for _, id := range r.order {
		if p := r.players[id]; p != nil && p.Connected && id != r.hostID {
			r.hostID = id
			return
		}
	}
	if _, ok := r.players[r.hostID]; !ok && len(r.order) > 0 {
		r.hostID = r.order[0]
	}
}

func (r *Room) updateSettings(by string, s Settings) error {
	if by != r.hostID {
		return ErrNotHost
	}
	if r.phase != PhaseLobby && r.phase != PhaseResults {
		return ErrBadPhase
	}
	r.settings = s.Normalize(r.deps.Lib.Taxonomy)
	r.broadcastState()
	return nil
}

func (r *Room) start(by string) error {
	if r.phase != PhaseLobby && r.phase != PhaseResults {
		return ErrBadPhase
	}
	if by != r.hostID {
		return ErrNotHost
	}
	var ids []string
	for _, id := range r.order {
		if r.players[id].Connected {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return ErrNoPlayers
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var profiles []*selection.Profile
	for _, id := range ids {
		p, err := store.LoadProfile(ctx, r.deps.Store, r.deps.Lib, id)
		if err != nil && r.deps.Log != nil {
			r.deps.Log.Warn("load profile", "player", id, "err", err)
		}
		profiles = append(profiles, p)
	}
	picks := r.deps.Selector.Select(selection.Merge(profiles), selection.Request{
		Count: r.settings.Questions, Categories: r.settings.Categories, AllowChains: true,
		Seed: rand.Int63(), Now: r.now(),
	})
	if len(picks) == 0 {
		return ErrNoQuestions
	}
	rng := rand.New(rand.NewSource(rand.Int63()))
	r.questions, r.orders = nil, nil
	for _, pk := range picks {
		r.questions = append(r.questions, pk.Question)
		r.orders = append(r.orders, game.ShuffleOrder(pk.Question, rng))
	}
	for _, p := range r.players {
		p.Score, p.Speed, p.Streak, p.Best, p.Ready, p.records, p.Late = 0, 0, 0, 0, false, nil, false
	}
	r.stats, r.gameID, r.startedAt, r.qIndex = nil, game.NewID(), r.now(), -1
	r.lastResults = nil
	r.nextQuestion()
	return nil
}

func (r *Room) nextQuestion() {
	r.timerGen++
	r.qIndex++
	if r.qIndex >= len(r.questions) {
		r.finish()
		return
	}
	now := r.now()
	r.phase = PhaseQuestion
	r.answers = map[string]*answer{}
	r.participants = map[string]bool{}
	for _, p := range r.players {
		p.Ready = false
		if p.Connected {
			r.participants[p.ID] = true
		}
	}
	dur := r.deps.Timing.Question(r.settings)
	r.qStarted, r.deadline = now, now.Add(dur)
	gen := r.timerGen
	time.AfterFunc(dur, func() {
		r.do(func() {
			if gen == r.timerGen && r.phase == PhaseQuestion {
				r.reveal()
			}
		})
	})
	q := r.questions[r.qIndex]
	view := game.BuildQuestionView(q, r.orders[r.qIndex], r.deps.Lib.Taxonomy, r.qIndex, len(r.questions))
	r.lastQuestion = envelope("question", map[string]any{
		"question": view, "deadline": r.deadline.UnixMilli(), "durationMs": dur.Milliseconds(),
		"serverTime": now.UnixMilli(), "confidence": r.settings.Confidence,
	})
	r.broadcastState()
	r.broadcast(r.lastQuestion)
}

func (r *Room) answer(id, qid string, choice, confidence int) error {
	if r.phase != PhaseQuestion {
		return ErrBadPhase
	}
	p := r.players[id]
	if p == nil {
		return ErrNotInRoom
	}
	q := r.questions[r.qIndex]
	if qid != q.ID {
		return ErrStaleQuestion
	}
	if r.answers[id] != nil {
		return ErrAlreadyAnswered
	}
	order := r.orders[r.qIndex]
	if choice < 0 || choice >= len(order) {
		return ErrInvalidChoice
	}
	now := r.now()
	if now.After(r.deadline.Add(300 * time.Millisecond)) {
		return ErrTooLate
	}
	if !r.settings.Confidence || confidence < 0 || confidence > 3 {
		confidence = 0
	}
	orig := order[choice]
	r.answers[id] = &answer{Display: choice, Original: orig, Correct: orig == q.CorrectIndex, Confidence: confidence,
		At: now, ResponseMS: min(now.Sub(r.qStarted), r.deadline.Sub(r.qStarted)).Milliseconds()}
	r.participants[id] = true
	r.lastActive = now
	if p.conn != nil {
		p.conn.Send(envelope("answer_ack", map[string]any{"questionId": qid, "choice": choice}))
	}
	r.broadcastState()
	r.checkProgress()
	return nil
}

// checkProgress advances early when every connected player has acted.
func (r *Room) checkProgress() {
	connected := 0
	for _, p := range r.players {
		if !p.Connected {
			continue
		}
		connected++
		if r.phase == PhaseQuestion && r.answers[p.ID] == nil {
			return
		}
		if r.phase == PhaseReveal && !p.Ready {
			return
		}
	}
	if connected == 0 {
		return // let timers carry the game; a reconnecting player resumes it
	}
	switch r.phase {
	case PhaseQuestion:
		r.reveal()
	case PhaseReveal:
		r.nextQuestion()
	}
}

type PlayerResult struct {
	PlayerID   string `json:"playerId"`
	Choice     int    `json:"choice"`
	Correct    bool   `json:"correct"`
	Points     int    `json:"points"`
	SpeedBonus int    `json:"speedBonus"`
	ResponseMS int64  `json:"responseMs"`
	Confidence int    `json:"confidence"`
}

func (r *Room) reveal() {
	r.timerGen++
	now := r.now()
	q := r.questions[r.qIndex]
	order := r.orders[r.qIndex]
	dur := r.deps.Timing.Question(r.settings)
	dist := make([]int, len(order))
	results := []PlayerResult{}
	stat := questionStat{QuestionID: q.ID}
	var recs []store.AnswerRecord
	for _, id := range r.order {
		p := r.players[id]
		a := r.answers[id]
		if a == nil && !r.participants[id] {
			continue // wasn't around for this question
		}
		res := PlayerResult{PlayerID: id, Choice: -1}
		rec := game.Record{QuestionID: q.ID, Category: q.Category, Difficulty: string(q.Difficulty), Choice: -1, Display: -1, At: now}
		if a != nil {
			dist[a.Display]++
			res.Choice, res.Correct, res.ResponseMS, res.Confidence = a.Display, a.Correct, a.ResponseMS, a.Confidence
			rec.Choice, rec.Display, rec.Correct, rec.Confidence, rec.ResponseMS = a.Original, a.Display, a.Correct, a.Confidence, a.ResponseMS
		}
		if res.Correct {
			p.Score++
			res.Points = 1
			p.Streak++
			p.Best = max(p.Best, p.Streak)
			stat.Correct++
			if r.settings.SpeedBonus {
				frac := 1 - float64(a.ResponseMS)/float64(dur.Milliseconds())
				res.SpeedBonus = int(math.Round(10 * math.Max(0, math.Min(1, frac))))
				p.Speed += res.SpeedBonus
			}
		} else {
			p.Streak = 0
		}
		stat.Participants++
		p.records = append(p.records, rec)
		results = append(results, res)
		recs = append(recs, store.AnswerRecord{PlayerID: id, QuestionID: q.ID, Category: q.Category, Difficulty: string(q.Difficulty),
			Context: "room", SessionID: r.gameID, Choice: rec.Choice, Correct: rec.Correct, Confidence: rec.Confidence,
			ResponseMS: rec.ResponseMS, AnsweredAt: now})
	}
	r.stats = append(r.stats, stat)
	r.persist(recs)

	r.phase = PhaseReveal
	for _, p := range r.players {
		p.Ready = false
	}
	rd := r.deps.Timing.Reveal(r.settings)
	r.revealEnds = now.Add(rd)
	gen := r.timerGen
	time.AfterFunc(rd, func() {
		r.do(func() {
			if gen == r.timerGen && r.phase == PhaseReveal {
				r.nextQuestion()
			}
		})
	})
	r.lastReveal = envelope("reveal", map[string]any{
		"index": r.qIndex, "total": len(r.questions), "reveal": game.BuildReveal(q, order),
		"results": results, "distribution": dist, "leaderboard": r.leaderboard(),
		"revealEndsAt": r.revealEnds.UnixMilli(), "serverTime": now.UnixMilli(),
		"isLast": r.qIndex == len(r.questions)-1,
	})
	r.broadcastState()
	r.broadcast(r.lastReveal)
}

func (r *Room) ready(id string) error {
	p := r.players[id]
	if p == nil {
		return ErrNotInRoom
	}
	if r.phase != PhaseReveal {
		return ErrBadPhase
	}
	p.Ready = true
	r.broadcastState()
	r.checkProgress()
	return nil
}

func (r *Room) skip(by string) error {
	if by != r.hostID {
		return ErrNotHost
	}
	if r.phase != PhaseReveal {
		return ErrBadPhase
	}
	r.nextQuestion()
	return nil
}

type Awards struct {
	Fastest         *Award `json:"fastest,omitempty"`
	BestStreak      *Award `json:"bestStreak,omitempty"`
	HardestQuestion *Award `json:"hardestQuestion,omitempty"`
}

type Award struct {
	PlayerID   string  `json:"playerId,omitempty"`
	QuestionID string  `json:"questionId,omitempty"`
	Value      float64 `json:"value"`
	Label      string  `json:"label,omitempty"`
}

func (r *Room) finish() {
	r.timerGen++
	r.phase = PhaseResults
	now := r.now()
	summaries := map[string]game.Summary{}
	aw := Awards{}
	for _, id := range r.order {
		p := r.players[id]
		s := game.Summarize(p.records, r.deps.Lib)
		summaries[id] = s
		if s.FastestCorrectMS > 0 && (aw.Fastest == nil || float64(s.FastestCorrectMS) < aw.Fastest.Value) {
			aw.Fastest = &Award{PlayerID: id, QuestionID: s.FastestQuestionID, Value: float64(s.FastestCorrectMS)}
		}
		if p.Best > 0 && (aw.BestStreak == nil || float64(p.Best) > aw.BestStreak.Value) {
			aw.BestStreak = &Award{PlayerID: id, Value: float64(p.Best)}
		}
	}
	for _, st := range r.stats {
		if st.Participants == 0 {
			continue
		}
		acc := float64(st.Correct) / float64(st.Participants)
		q := r.deps.Lib.Get(st.QuestionID)
		if aw.HardestQuestion == nil || acc < aw.HardestQuestion.Value ||
			(acc == aw.HardestQuestion.Value && q.Difficulty.Level() > r.deps.Lib.Get(aw.HardestQuestion.QuestionID).Difficulty.Level()) {
			aw.HardestQuestion = &Award{QuestionID: st.QuestionID, Value: acc, Label: q.Reveal}
		}
	}
	board := r.leaderboard()
	var winners []string
	for _, pv := range board {
		if pv.Score == board[0].Score && (!r.settings.SpeedBonus || pv.Speed == board[0].Speed) && board[0].Score > 0 {
			winners = append(winners, pv.ID)
		}
	}
	questions := []map[string]any{}
	for i, q := range r.questions {
		item := map[string]any{"id": q.ID, "emoji": q.Emoji, "question": q.Question, "reveal": q.Reveal, "category": q.Category}
		if i < len(r.stats) && r.stats[i].Participants > 0 {
			item["accuracy"] = float64(r.stats[i].Correct) / float64(r.stats[i].Participants)
		}
		questions = append(questions, item)
	}
	r.lastResults = envelope("results", map[string]any{
		"leaderboard": board, "summaries": summaries, "awards": aw, "winners": winners, "questions": questions,
	})
	r.broadcastState()
	r.broadcast(r.lastResults)

	// Persist one session record per player, off the room goroutine.
	if r.deps.Store != nil {
		var recs []store.SessionRecord
		for id, s := range summaries {
			recs = append(recs, store.SessionRecord{ID: r.gameID, PlayerID: id, Kind: "room", Score: s.Score, Total: s.Total,
				Summary: map[string]any{"accuracy": s.Accuracy, "bestStreak": s.BestStreak, "room": r.Code}, StartedAt: r.startedAt, FinishedAt: now})
		}
		st, log := r.deps.Store, r.deps.Log
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for _, rec := range recs {
				if err := st.SaveSession(ctx, rec); err != nil && log != nil {
					log.Warn("save room session", "err", err)
				}
			}
		}()
	}
}

func (r *Room) playAgain(by string) error {
	if by != r.hostID {
		return ErrNotHost
	}
	if r.phase != PhaseResults {
		return ErrBadPhase
	}
	r.phase = PhaseLobby
	for id, p := range r.players {
		p.Score, p.Speed, p.Streak, p.Best, p.Ready, p.records, p.Late = 0, 0, 0, 0, false, nil, false
		if !p.Connected {
			r.leave(id)
		}
	}
	if r.phase == PhaseClosed {
		return nil
	}
	r.broadcastState()
	return nil
}

func (r *Room) persist(recs []store.AnswerRecord) {
	if r.deps.Store == nil || len(recs) == 0 {
		return
	}
	st, log := r.deps.Store, r.deps.Log
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := st.RecordAnswers(ctx, recs); err != nil && log != nil {
			log.Warn("record room answers", "err", err)
		}
	}()
}

// idle is called periodically by the manager.
func (r *Room) idle(now time.Time, idleFor, finishedFor time.Duration) {
	connected := 0
	for _, p := range r.players {
		if p.Connected {
			connected++
		}
	}
	quiet := now.Sub(r.lastActive)
	if (connected == 0 && quiet > idleFor) || ((r.phase == PhaseResults || r.phase == PhaseLobby) && quiet > finishedFor) {
		r.close()
	}
}

func (r *Room) close() {
	r.timerGen++
	r.broadcast(envelope("room_closed", map[string]string{"code": r.Code}))
	r.phase = PhaseClosed
}

// ---- Views & broadcast ----

type PlayerView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Connected bool   `json:"connected"`
	IsHost    bool   `json:"isHost"`
	Score     int    `json:"score"`
	Speed     int    `json:"speed"`
	Streak    int    `json:"streak"`
	Answered  bool   `json:"answered"`
	Ready     bool   `json:"ready"`
	Late      bool   `json:"late"`
}

type StateView struct {
	Code          string       `json:"code"`
	Phase         Phase        `json:"phase"`
	HostID        string       `json:"hostId"`
	Settings      Settings     `json:"settings"`
	Players       []PlayerView `json:"players"`
	QuestionIndex int          `json:"questionIndex"`
	Total         int          `json:"total"`
	Deadline      int64        `json:"deadline,omitempty"`
	RevealEndsAt  int64        `json:"revealEndsAt,omitempty"`
	ServerTime    int64        `json:"serverTime"`
}

func (r *Room) playerViews() []PlayerView {
	out := make([]PlayerView, 0, len(r.order))
	for _, id := range r.order {
		p := r.players[id]
		out = append(out, PlayerView{ID: id, Name: p.Name, Connected: p.Connected, IsHost: id == r.hostID,
			Score: p.Score, Speed: p.Speed, Streak: p.Streak, Answered: r.phase == PhaseQuestion && r.answers[id] != nil,
			Ready: p.Ready, Late: p.Late})
	}
	return out
}

func (r *Room) leaderboard() []PlayerView {
	pv := r.playerViews()
	sort.SliceStable(pv, func(i, j int) bool {
		if pv[i].Score != pv[j].Score {
			return pv[i].Score > pv[j].Score
		}
		if r.settings.SpeedBonus && pv[i].Speed != pv[j].Speed {
			return pv[i].Speed > pv[j].Speed
		}
		return false
	})
	return pv
}

func (r *Room) state() StateView {
	s := StateView{Code: r.Code, Phase: r.phase, HostID: r.hostID, Settings: r.settings, Players: r.playerViews(),
		QuestionIndex: r.qIndex, Total: len(r.questions), ServerTime: r.now().UnixMilli()}
	if r.phase == PhaseQuestion {
		s.Deadline = r.deadline.UnixMilli()
	}
	if r.phase == PhaseReveal {
		s.RevealEndsAt = r.revealEnds.UnixMilli()
	}
	return s
}

func (r *Room) broadcastState() { r.broadcast(envelope("room_state", r.state())) }

func (r *Room) broadcast(msg []byte) {
	if msg == nil {
		return
	}
	for _, p := range r.players {
		if p.conn != nil {
			p.conn.Send(msg)
		}
	}
}

func envelope(typ string, data any) []byte {
	b, _ := json.Marshal(map[string]any{"type": typ, "data": data})
	return b
}

// Envelope is exported for the transport layer.
func Envelope(typ string, data any) []byte { return envelope(typ, data) }
