package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"intuition/internal/content"
	"intuition/internal/room"
	"intuition/internal/selection"
	"intuition/internal/store"
)

func newTestServer(t *testing.T) (*httptest.Server, *content.Library) {
	lib, rep, err := content.LoadDir("../../../content")
	if err != nil || rep.HasErrors() {
		t.Fatal(err)
	}
	timing := room.Timing{Question: func(room.Settings) time.Duration { return 3 * time.Second },
		Reveal: func(room.Settings) time.Duration { return 3 * time.Second }, HostGrace: time.Second, LobbyDrop: time.Second}
	srv := New(Config{Secret: "test", RoomTiming: &timing}, lib, selection.New(lib, selection.DefaultConfig()), store.NewMemory(),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv)
	t.Cleanup(func() { srv.Shutdown(); ts.Close() })
	return ts, lib
}

func call(t *testing.T, method, url, token string, body any, out any) int {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil {
		_ = json.Unmarshal(raw, out)
	}
	return resp.StatusCode
}

func register(t *testing.T, base, name string) string {
	var r struct{ Token string }
	if code := call(t, "POST", base+"/api/players", "", map[string]string{"name": name}, &r); code != 201 {
		t.Fatalf("register: %d", code)
	}
	return r.Token
}

type qv struct {
	ID      string
	Options []struct {
		Index int
		Text  string
	}
}

func correctOf(lib *content.Library, q qv) int {
	src := lib.Get(q.ID)
	for _, o := range q.Options {
		if o.Text == src.Options[src.CorrectIndex] {
			return o.Index
		}
	}
	return -1
}

func TestSoloSessionFlow(t *testing.T) {
	ts, lib := newTestServer(t)
	tok := register(t, ts.URL, "Solo")
	if code := call(t, "POST", ts.URL+"/api/sessions", "", map[string]string{"mode": "quick"}, nil); code != 401 {
		t.Fatalf("unauthenticated start should be 401, got %d", code)
	}
	var start struct {
		SessionID string
		Question  *qv
	}
	raw := map[string]any{}
	call(t, "POST", ts.URL+"/api/sessions", tok, map[string]any{"mode": "quick", "confidence": true}, &raw)
	b, _ := json.Marshal(raw)
	if strings.Contains(string(b), "correctIndex") || strings.Contains(string(b), "explanation") {
		t.Fatalf("session start leaks the answer: %s", b)
	}
	_ = json.Unmarshal(b, &start)
	q := start.Question
	score := 0
	for i := 0; q != nil; i++ {
		var resp struct {
			Result struct {
				Correct bool
				Score   int
				Done    bool
			}
			Reveal struct {
				CorrectIndex int
				Explanation  string
			}
			Next    *qv
			Summary *struct{ Score, Total int }
		}
		choice := correctOf(lib, *q)
		if i%2 == 1 {
			choice = (choice + 1) % len(q.Options)
		}
		if code := call(t, "POST", ts.URL+"/api/sessions/"+start.SessionID+"/answers", tok,
			map[string]any{"questionId": q.ID, "choice": choice, "confidence": 3}, &resp); code != 200 {
			t.Fatalf("answer %d: status %d", i, code)
		}
		if resp.Reveal.Explanation == "" || resp.Result.Correct != (i%2 == 0) {
			t.Fatalf("answer %d: bad response %+v", i, resp)
		}
		if resp.Result.Correct {
			score++
		}
		q = resp.Next
		if q == nil && (resp.Summary == nil || resp.Summary.Score != score || resp.Summary.Total != 5) {
			t.Fatalf("missing/incorrect summary: %+v", resp.Summary)
		}
	}
	var stats struct{ Answered, Correct, Surprises int }
	call(t, "GET", ts.URL+"/api/players/me/stats", tok, nil, &stats)
	if stats.Answered != 5 || stats.Correct != score || stats.Surprises != 5-score {
		t.Fatalf("stats not tracked: %+v", stats)
	}
	// Another player cannot use this session.
	other := register(t, ts.URL, "Other")
	if code := call(t, "GET", ts.URL+"/api/sessions/"+start.SessionID, other, nil, nil); code != 404 {
		t.Fatalf("foreign session access should 404, got %d", code)
	}
}

func TestDailyFlowAndLeaderboard(t *testing.T) {
	ts, lib := newTestServer(t)
	var first []string
	for _, name := range []string{"Asha", "Ben"} {
		tok := register(t, ts.URL, name)
		var s struct {
			SessionID string
			Question  *qv
		}
		if code := call(t, "POST", ts.URL+"/api/daily/start", tok, nil, &s); code != 201 {
			t.Fatalf("daily start %d", code)
		}
		var ids []string
		for q := s.Question; q != nil; {
			ids = append(ids, q.ID)
			var resp struct{ Next *qv }
			choice := correctOf(lib, *q)
			if name == "Ben" {
				choice = (choice + 1) % len(q.Options)
			}
			call(t, "POST", ts.URL+"/api/sessions/"+s.SessionID+"/answers", tok, map[string]any{"questionId": q.ID, "choice": choice}, &resp)
			q = resp.Next
		}
		if first == nil {
			first = ids
		} else if strings.Join(first, ",") != strings.Join(ids, ",") {
			t.Fatal("players got different daily questions")
		}
		if code := call(t, "POST", ts.URL+"/api/daily/start", tok, nil, nil); code != 409 {
			t.Fatalf("second attempt should be 409, got %d", code)
		}
	}
	var board struct {
		Leaderboard []struct {
			PlayerName string
			Score      int
		}
		TotalPlayers int
	}
	call(t, "GET", ts.URL+"/api/daily/leaderboard", "", nil, &board)
	if board.TotalPlayers != 2 || board.Leaderboard[0].PlayerName != "Asha" || board.Leaderboard[0].Score != 5 {
		t.Fatalf("bad leaderboard %+v", board)
	}
}

type wsMsg struct {
	Type string
	Data json.RawMessage
}

func dial(t *testing.T, ts *httptest.Server, token, name string) *websocket.Conn {
	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	_ = c.WriteJSON(map[string]any{"type": "hello", "data": map[string]string{"token": token, "name": name}})
	expect(t, c, "welcome")
	return c
}

func expect(t *testing.T, c *websocket.Conn, typ string) json.RawMessage {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		var m wsMsg
		if err := c.ReadJSON(&m); err != nil {
			t.Fatalf("waiting for %s: %v", typ, err)
		}
		if m.Type == typ {
			return m.Data
		}
	}
}

func TestWebSocketMultiplayerGame(t *testing.T) {
	ts, lib := newTestServer(t)
	host := dial(t, ts, register(t, ts.URL, "Host"), "Host")
	guestTok := register(t, ts.URL, "Guest")
	guest := dial(t, ts, guestTok, "Guest")

	_ = host.WriteJSON(map[string]any{"type": "create_room", "data": map[string]any{"settings": map[string]any{"questions": 5, "allowLateJoin": true}}})
	var st struct{ Code string }
	_ = json.Unmarshal(expect(t, host, "room_state"), &st)
	_ = guest.WriteJSON(map[string]any{"type": "join_room", "data": map[string]string{"code": strings.ToLower(st.Code)}})
	expect(t, guest, "room_state")
	_ = guest.WriteJSON(map[string]any{"type": "start_game"})
	if e := expect(t, guest, "error"); !strings.Contains(string(e), "not_host") {
		t.Fatalf("guest start should fail: %s", e)
	}
	_ = host.WriteJSON(map[string]any{"type": "start_game"})
	for i := 0; i < 5; i++ {
		raw := expect(t, host, "question")
		expect(t, guest, "question")
		if strings.Contains(string(raw), "correctIndex") {
			t.Fatal("question leaks answer over the wire")
		}
		var q struct{ Question qv }
		_ = json.Unmarshal(raw, &q)
		_ = host.WriteJSON(map[string]any{"type": "answer", "data": map[string]any{"questionId": q.Question.ID, "choice": correctOf(lib, q.Question)}})
		_ = guest.WriteJSON(map[string]any{"type": "answer", "data": map[string]any{"questionId": q.Question.ID, "choice": correctOf(lib, q.Question)}})
		rev := expect(t, host, "reveal")
		if !strings.Contains(string(rev), "correctIndex") {
			t.Fatal("reveal missing answer")
		}
		if i == 2 {
			// Guest refreshes mid-game and rejoins with the same identity.
			guest.Close()
			guest = dial(t, ts, guestTok, "Guest")
			_ = guest.WriteJSON(map[string]any{"type": "join_room", "data": map[string]string{"code": st.Code}})
			expect(t, guest, "reveal") // catch-up
		}
		_ = host.WriteJSON(map[string]any{"type": "ready"})
		_ = guest.WriteJSON(map[string]any{"type": "ready"})
	}
	var res struct {
		Leaderboard []struct {
			Name  string
			Score int
		}
	}
	_ = json.Unmarshal(expect(t, host, "results"), &res)
	if len(res.Leaderboard) != 2 || res.Leaderboard[0].Score != 5 || res.Leaderboard[1].Score != 5 {
		t.Fatalf("bad results %+v", res)
	}
	_ = guest.WriteJSON(map[string]any{"type": "join_room", "data": map[string]string{"code": "ZZZZZ"}})
	if e := expect(t, guest, "error"); !strings.Contains(string(e), "room_not_found") {
		t.Fatalf("expected room_not_found: %s", e)
	}
}

func TestTokensAreUnforgeable(t *testing.T) {
	ts, _ := newTestServer(t)
	tok := register(t, ts.URL, "Real")
	id, _, _ := strings.Cut(tok, ".")
	for _, forged := range []string{id + ".AAAA", "pdeadbeef." + strings.SplitN(tok, ".", 2)[1], "garbage"} {
		if code := call(t, "GET", ts.URL+"/api/players/me/stats", forged, nil, nil); code != 401 {
			t.Fatalf("forged token %q accepted (%d)", forged, code)
		}
	}
}
