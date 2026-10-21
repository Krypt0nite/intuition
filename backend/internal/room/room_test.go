package room

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"intuition/internal/content"
	"intuition/internal/selection"
	"intuition/internal/store"
)

type fakeConn struct {
	mu   sync.Mutex
	msgs []map[string]json.RawMessage
}

func (f *fakeConn) Send(b []byte) bool {
	var m map[string]json.RawMessage
	_ = json.Unmarshal(b, &m)
	f.mu.Lock()
	f.msgs = append(f.msgs, m)
	f.mu.Unlock()
	return true
}

func (f *fakeConn) last(typ string) json.RawMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.msgs) - 1; i >= 0; i-- {
		var t string
		_ = json.Unmarshal(f.msgs[i]["type"], &t)
		if t == typ {
			return f.msgs[i]["data"]
		}
	}
	return nil
}

func (f *fakeConn) count(typ string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, m := range f.msgs {
		var t string
		_ = json.Unmarshal(m["type"], &t)
		if t == typ {
			n++
		}
	}
	return n
}

var lib *content.Library

func setup(t *testing.T, q, rev time.Duration) (*Manager, *content.Library) {
	if lib == nil {
		l, rep, err := content.LoadDir("../../../content")
		if err != nil || rep.HasErrors() {
			t.Fatal(err)
		}
		lib = l
	}
	m := NewManager(Deps{Lib: lib, Selector: selection.New(lib, selection.DefaultConfig()), Store: store.NewMemory(),
		Timing: Timing{Question: func(Settings) time.Duration { return q }, Reveal: func(Settings) time.Duration { return rev },
			HostGrace: 50 * time.Millisecond, LobbyDrop: time.Second}})
	t.Cleanup(m.Shutdown)
	return m, lib
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

type qmsg struct {
	Question struct {
		ID      string
		Index   int
		Options []struct {
			Index int
			Text  string
		}
	}
}

func current(t *testing.T, c *fakeConn) qmsg {
	var q qmsg
	_ = json.Unmarshal(c.last("question"), &q)
	return q
}

func correctChoice(l *content.Library, q qmsg) int {
	src := l.Get(q.Question.ID)
	for _, o := range q.Question.Options {
		if o.Text == src.Options[src.CorrectIndex] {
			return o.Index
		}
	}
	return -1
}

func TestFullGameAllAnswerEarlyAndNoLeaks(t *testing.T) {
	m, l := setup(t, 10*time.Second, 10*time.Second)
	r, _ := m.Create(five())
	a, b := &fakeConn{}, &fakeConn{}
	if err := r.Join("alice", "Alice", a); err != nil {
		t.Fatal(err)
	}
	if err := r.Join("bob", "Bob", b); err != nil {
		t.Fatal(err)
	}
	if err := r.Start("bob"); err != ErrNotHost {
		t.Fatalf("non-host start: %v", err)
	}
	if err := r.Start("alice"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		waitFor(t, "question", func() bool { return current(t, a).Question.Index == i && a.count("question") == i+1 })
		raw := string(a.last("question"))
		if strings.Contains(raw, "correctIndex") || strings.Contains(raw, "explanation") {
			t.Fatalf("question message leaks the answer: %s", raw)
		}
		q := current(t, a)
		if err := r.Answer("alice", q.Question.ID, correctChoice(l, q), 3); err != nil {
			t.Fatal(err)
		}
		if err := r.Answer("alice", q.Question.ID, 0, 3); err != ErrAlreadyAnswered {
			t.Fatalf("duplicate answer: %v", err)
		}
		if err := r.Answer("bob", q.Question.ID, (correctChoice(l, q)+1)%len(q.Question.Options), 0); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "reveal", func() bool { return a.count("reveal") == i+1 })
		_ = r.Ready("alice")
		_ = r.Ready("bob")
	}
	waitFor(t, "results", func() bool { return a.last("results") != nil })
	var res struct {
		Leaderboard []PlayerView
		Winners     []string
	}
	_ = json.Unmarshal(a.last("results"), &res)
	if res.Leaderboard[0].ID != "alice" || res.Leaderboard[0].Score != 5 || res.Leaderboard[1].Score != 0 || len(res.Winners) != 1 {
		t.Fatalf("bad results: %+v", res)
	}
}

func TestTimeoutAdvancesAndDisconnectedPlayerDoesNotBlock(t *testing.T) {
	m, l := setup(t, 150*time.Millisecond, 100*time.Millisecond)
	r, _ := m.Create(five())
	a, b := &fakeConn{}, &fakeConn{}
	_ = r.Join("alice", "Alice", a)
	_ = r.Join("bob", "Bob", b)
	_ = r.Start("alice")
	waitFor(t, "q1", func() bool { return a.count("question") == 1 })
	// Nobody answers: the timer must reveal.
	waitFor(t, "timeout reveal", func() bool { return a.count("reveal") == 1 })
	// Bob disconnects; Alice answering alone must reveal immediately.
	r.Disconnect("bob", b)
	waitFor(t, "q2", func() bool { return a.count("question") == 2 })
	q := current(t, a)
	start := time.Now()
	_ = r.Answer("alice", q.Question.ID, correctChoice(l, q), 0)
	waitFor(t, "early reveal", func() bool { return a.count("reveal") == 2 })
	if time.Since(start) > 120*time.Millisecond {
		t.Fatal("disconnected player blocked the reveal")
	}
}

func TestReconnectResumesAndHostMigrates(t *testing.T) {
	m, l := setup(t, 2*time.Second, 2*time.Second)
	r, _ := m.Create(five())
	a, b := &fakeConn{}, &fakeConn{}
	_ = r.Join("alice", "Alice", a)
	_ = r.Join("bob", "Bob", b)
	_ = r.Start("alice")
	waitFor(t, "q1", func() bool { return b.count("question") == 1 })
	q := current(t, b)
	_ = r.Answer("bob", q.Question.ID, correctChoice(l, q), 0)
	// Bob refreshes the page: new connection, same identity.
	b2 := &fakeConn{}
	r.Disconnect("bob", b)
	if err := r.Join("bob", "", b2); err != nil {
		t.Fatal(err)
	}
	if b2.last("question") == nil || b2.last("answer_ack") == nil {
		t.Fatal("reconnecting player did not receive the current question and their locked answer")
	}
	// Host disconnects; after the grace period Bob becomes host.
	r.Disconnect("alice", a)
	waitFor(t, "host migration", func() bool { return r.Snapshot().HostID == "bob" })
	// Host explicitly leaving also migrates immediately.
	c := &fakeConn{}
	_ = r.Join("carol", "Carol", c)
	r.Leave("bob")
	waitFor(t, "leave migration", func() bool { return r.Snapshot().HostID == "carol" })
}

func TestLateJoinAndConcurrentAnswers(t *testing.T) {
	m, l := setup(t, 2*time.Second, 2*time.Second)
	r, _ := m.Create(five())
	conns := map[string]*fakeConn{}
	for _, id := range []string{"p1", "p2", "p3", "p4", "p5", "p6"} {
		conns[id] = &fakeConn{}
		_ = r.Join(id, id, conns[id])
	}
	_ = r.Start("p1")
	waitFor(t, "q1", func() bool { return conns["p1"].count("question") == 1 })
	late := &fakeConn{}
	if err := r.Join("late", "Late", late); err != nil {
		t.Fatal(err)
	}
	if late.last("question") == nil {
		t.Fatal("late joiner should get the current question")
	}
	conns["late"] = late
	q := current(t, conns["p1"])
	var wg sync.WaitGroup
	for id := range conns {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_ = r.Answer(id, q.Question.ID, correctChoice(l, q), 0)
		}(id)
	}
	wg.Wait()
	waitFor(t, "reveal", func() bool { return conns["p1"].count("reveal") == 1 })
	for _, p := range r.Snapshot().Players {
		if p.Score != 1 {
			t.Fatalf("player %s score %d", p.ID, p.Score)
		}
	}
}

func TestRoomCodesAndLookup(t *testing.T) {
	m, _ := setup(t, time.Second, time.Second)
	r, _ := m.Create(DefaultSettings())
	if len(r.Code) != CodeLength || strings.ContainsAny(r.Code, "01OIL") {
		t.Fatalf("bad code %q", r.Code)
	}
	if m.Get(strings.ToLower(r.Code)+" ") != r {
		t.Fatal("lookup should be case/space insensitive")
	}
	_ = r.Join("solo", "Solo", &fakeConn{})
	r.Leave("solo")
	waitFor(t, "empty room closes", func() bool { return m.Get(r.Code) == nil })
}

func five() Settings { s := DefaultSettings(); s.Questions = 5; return s }

func TestLateJoinRefusedWhenDisabled(t *testing.T) {
	m, _ := setup(t, time.Second, time.Second)
	s := five()
	s.AllowLateJoin = false
	r, _ := m.Create(s)
	_ = r.Join("a", "A", &fakeConn{})
	_ = r.Start("a")
	if err := r.Join("b", "B", &fakeConn{}); err != ErrGameInProgress {
		t.Fatalf("expected game_in_progress, got %v", err)
	}
}
