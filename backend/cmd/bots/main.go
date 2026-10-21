// Command bots simulates multiplayer players for development and end-to-end
// testing. Bots register over HTTP, connect over WebSocket and answer with a
// configurable accuracy (they peek at the local content files, so this is a
// dev tool only).
//
//	go run ./cmd/bots -players 3 -create -start -questions 5   # bots host a room
//	go run ./cmd/bots -players 2 -room X7K9P                   # bots join your room
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"intuition/internal/content"
)

type envelope struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

func main() {
	server := flag.String("server", "http://localhost:8080", "server base URL")
	n := flag.Int("players", 3, "number of bots")
	code := flag.String("room", "", "room code to join")
	create := flag.Bool("create", false, "first bot creates a room")
	start := flag.Bool("start", false, "creator starts the game once everyone has joined")
	questions := flag.Int("questions", 5, "questions per game when creating (5, 10, 25)")
	timer := flag.Int("timer", 15, "seconds per question when creating")
	accuracy := flag.Float64("accuracy", 0.6, "probability a bot picks the correct answer")
	contentDir := flag.String("content", "../content", "content dir (used to look up answers)")
	timeout := flag.Duration("timeout", 5*time.Minute, "give up after this long")
	flag.Parse()

	lib, rep, err := content.LoadDir(*contentDir)
	if err != nil || rep.HasErrors() {
		fmt.Fprintln(os.Stderr, "content:", err)
		os.Exit(1)
	}
	if !*create && *code == "" {
		fmt.Fprintln(os.Stderr, "use -create or -room CODE")
		os.Exit(2)
	}
	roomCode := make(chan string, 1)
	if *code != "" {
		roomCode <- strings.ToUpper(*code)
	}
	var wg sync.WaitGroup
	results := make(chan string, *n)
	var codeOnce sync.Once
	var shared string
	for i := 0; i < *n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			b := &bot{name: fmt.Sprintf("Bot %d", i+1), server: *server, lib: lib, accuracy: *accuracy, rng: rand.New(rand.NewSource(time.Now().UnixNano() + int64(i)))}
			isCreator := *create && i == 0
			var c string
			if !isCreator {
				c = <-roomCode
				roomCode <- c
			}
			res, err := b.play(isCreator, c, *questions, *timer, *n, *start, func(rc string) {
				codeOnce.Do(func() { shared = rc; fmt.Println("room code:", rc); roomCode <- rc })
			})
			if err != nil {
				results <- fmt.Sprintf("%s: error: %v", b.name, err)
				return
			}
			results <- res
		}(i)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(*timeout):
		fmt.Fprintln(os.Stderr, "timed out")
		os.Exit(1)
	}
	close(results)
	failed := false
	for r := range results {
		fmt.Println(r)
		failed = failed || strings.Contains(r, "error")
	}
	_ = shared
	if failed {
		os.Exit(1)
	}
}

type bot struct {
	name, server string
	lib          *content.Library
	accuracy     float64
	rng          *rand.Rand
	conn         *websocket.Conn
	mu           sync.Mutex
}

func (b *bot) send(typ string, data any) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.conn.WriteJSON(map[string]any{"type": typ, "data": data})
}

func (b *bot) play(creator bool, code string, questions, timer, expected int, autoStart bool, announce func(string)) (string, error) {
	body, _ := json.Marshal(map[string]string{"name": b.name})
	resp, err := http.Post(b.server+"/api/players", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	var reg struct{ Token string }
	_ = json.NewDecoder(resp.Body).Decode(&reg)
	resp.Body.Close()
	u, _ := url.Parse(b.server)
	u.Scheme = map[string]string{"http": "ws", "https": "wss"}[u.Scheme]
	u.Path = "/ws"
	b.conn, _, err = websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		return "", err
	}
	defer b.conn.Close()
	if err := b.send("hello", map[string]string{"token": reg.Token, "name": b.name}); err != nil {
		return "", err
	}
	if creator {
		_ = b.send("create_room", map[string]any{"settings": map[string]any{"questions": questions, "timerSeconds": timer, "revealSeconds": 8, "speedBonus": true, "allowLateJoin": true}})
	} else {
		_ = b.send("join_room", map[string]string{"code": code})
	}
	started := false
	for {
		var msg envelope
		if err := b.conn.ReadJSON(&msg); err != nil {
			return "", err
		}
		switch msg.Type {
		case "room_state":
			var st struct {
				Code    string
				Phase   string
				Players []struct{ Connected bool }
			}
			_ = json.Unmarshal(msg.Data, &st)
			if creator && st.Phase == "lobby" {
				announce(st.Code)
				connected := 0
				for _, p := range st.Players {
					if p.Connected {
						connected++
					}
				}
				if autoStart && !started && connected >= expected {
					started = true
					_ = b.send("start_game", nil)
				}
			}
		case "question":
			var q struct {
				Question struct {
					ID      string
					Options []struct {
						Index int
						Text  string
					}
				}
			}
			_ = json.Unmarshal(msg.Data, &q)
			choice := b.rng.Intn(len(q.Question.Options))
			if src := b.lib.Get(q.Question.ID); src != nil && b.rng.Float64() < b.accuracy {
				for _, o := range q.Question.Options {
					if o.Text == src.Options[src.CorrectIndex] {
						choice = o.Index
					}
				}
			}
			delay := time.Duration(300+b.rng.Intn(1500)) * time.Millisecond
			qid := q.Question.ID
			go func() {
				time.Sleep(delay)
				_ = b.send("answer", map[string]any{"questionId": qid, "choice": choice, "confidence": 1 + b.rng.Intn(3)})
			}()
		case "reveal":
			go func() {
				time.Sleep(time.Duration(200+b.rng.Intn(800)) * time.Millisecond)
				_ = b.send("ready", nil)
			}()
		case "results":
			var r struct {
				Leaderboard []struct {
					Name  string
					Score int
					Speed int
				}
				Winners []string
			}
			_ = json.Unmarshal(msg.Data, &r)
			var parts []string
			for _, p := range r.Leaderboard {
				parts = append(parts, fmt.Sprintf("%s %d (+%d)", p.Name, p.Score, p.Speed))
			}
			return fmt.Sprintf("%s finished. Leaderboard: %s", b.name, strings.Join(parts, ", ")), nil
		case "error":
			var e struct{ Code, Message string }
			_ = json.Unmarshal(msg.Data, &e)
			if e.Code == "room_not_found" || e.Code == "unauthorized" || e.Code == "room_full" {
				return "", fmt.Errorf("%s: %s", e.Code, e.Message)
			}
		case "room_closed":
			return "", fmt.Errorf("room closed")
		}
	}
}
