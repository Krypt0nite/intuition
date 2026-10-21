package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"intuition/internal/room"
	"intuition/internal/store"
)

const (
	wsSendBuffer   = 64
	wsReadLimit    = 8 << 10
	wsPongWait     = 60 * time.Second
	wsPingInterval = 25 * time.Second
	wsMaxPerSecond = 30
)

type wsClient struct {
	srv       *Server
	conn      *websocket.Conn
	send      chan []byte
	closed    chan struct{}
	closeOnce sync.Once

	playerID string
	name     string
	room     *room.Room // only touched by the read loop goroutine
}

// Send implements room.Conn. It never blocks: a client too slow to drain its
// buffer is disconnected rather than stalling the room.
func (c *wsClient) Send(b []byte) bool {
	select {
	case <-c.closed:
		return false
	default:
	}
	select {
	case c.send <- b:
		return true
	default:
		go c.close()
		return false
	}
}

func (c *wsClient) close() {
	c.closeOnce.Do(func() {
		close(c.closed)
		_ = c.conn.Close()
	})
}

type inbound struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	up := websocket.Upgrader{ReadBufferSize: 4096, WriteBufferSize: 4096, CheckOrigin: s.originAllowed}
	conn, err := up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	c := &wsClient{srv: s, conn: conn, send: make(chan []byte, wsSendBuffer), closed: make(chan struct{})}
	go c.writeLoop()
	c.readLoop()
}

func (c *wsClient) writeLoop() {
	ping := time.NewTicker(wsPingInterval)
	defer ping.Stop()
	for {
		select {
		case b := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.TextMessage, b); err != nil {
				c.close()
				return
			}
		case <-ping.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				c.close()
				return
			}
		case <-c.closed:
			return
		}
	}
}

func (c *wsClient) readLoop() {
	defer func() {
		if c.room != nil {
			c.room.Disconnect(c.playerID, c)
		}
		c.close()
	}()
	c.conn.SetReadLimit(wsReadLimit)
	_ = c.conn.SetReadDeadline(time.Now().Add(wsPongWait))
	c.conn.SetPongHandler(func(string) error { return c.conn.SetReadDeadline(time.Now().Add(wsPongWait)) })
	windowStart, count := time.Now(), 0
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		_ = c.conn.SetReadDeadline(time.Now().Add(wsPongWait))
		if time.Since(windowStart) > time.Second {
			windowStart, count = time.Now(), 0
		}
		if count++; count > wsMaxPerSecond {
			c.Send(room.Envelope("error", errBody("rate_limited", "Too many messages")))
			return
		}
		var msg inbound
		if err := json.Unmarshal(data, &msg); err != nil {
			c.Send(room.Envelope("error", errBody("bad_message", "Messages must be JSON {type, data}")))
			continue
		}
		c.handle(msg)
	}
}

func errBody(code, message string) map[string]string {
	return map[string]string{"code": code, "message": message}
}

var errMessages = map[error]string{
	room.ErrRoomClosed:      "That room has closed.",
	room.ErrRoomFull:        "That room is full.",
	room.ErrGameInProgress:  "That game has already started.",
	room.ErrNotHost:         "Only the host can do that.",
	room.ErrBadPhase:        "That isn't possible right now.",
	room.ErrNotInRoom:       "You're not in that room.",
	room.ErrStaleQuestion:   "That question has already moved on.",
	room.ErrAlreadyAnswered: "Your answer is already locked in.",
	room.ErrInvalidChoice:   "That isn't one of the options.",
	room.ErrTooLate:         "Time ran out before your answer arrived.",
	room.ErrNoQuestions:     "No questions match those settings.",
	room.ErrNoPlayers:       "Nobody is connected.",
	room.ErrTimeout:         "The room didn't respond in time.",
}

func (c *wsClient) fail(err error, ctx string) {
	msg, ok := errMessages[err]
	if !ok {
		msg = "Something went wrong."
	}
	code := err.Error()
	c.Send(room.Envelope("error", map[string]string{"code": code, "message": msg, "context": ctx}))
}

func (c *wsClient) handle(msg inbound) {
	s := c.srv
	if msg.Type == "ping" {
		c.Send(room.Envelope("pong", map[string]any{"serverTime": time.Now().UnixMilli(), "echo": msg.Data}))
		return
	}
	if msg.Type == "hello" {
		var d struct {
			Token string `json:"token"`
			Name  string `json:"name"`
		}
		_ = json.Unmarshal(msg.Data, &d)
		id, ok := s.verify(d.Token)
		if !ok {
			c.Send(room.Envelope("error", errBody("unauthorized", "Invalid player token")))
			return
		}
		c.playerID, c.name = id, CleanName(d.Name)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if c.name != "" {
			_ = s.store.UpsertPlayer(ctx, store.Player{ID: id, Name: c.name})
		} else if p, err := s.store.GetPlayer(ctx, id); err == nil {
			c.name = p.Name
		} else {
			c.name = "Curious player"
			_ = s.store.UpsertPlayer(ctx, store.Player{ID: id, Name: c.name})
		}
		c.Send(room.Envelope("welcome", map[string]any{"playerId": id, "name": c.name, "serverTime": time.Now().UnixMilli()}))
		return
	}
	if c.playerID == "" {
		c.Send(room.Envelope("error", errBody("hello_required", "Send hello first")))
		return
	}
	switch msg.Type {
	case "create_room":
		var d struct {
			Settings *room.Settings `json:"settings"`
		}
		_ = json.Unmarshal(msg.Data, &d)
		settings := room.DefaultSettings()
		if d.Settings != nil {
			settings = *d.Settings
		}
		c.leaveRoom()
		r, err := s.rooms.Create(settings)
		if err != nil {
			c.Send(room.Envelope("error", errBody(err.Error(), "Could not create a room")))
			return
		}
		if err := r.Join(c.playerID, c.name, c); err != nil {
			c.fail(err, msg.Type)
			return
		}
		c.room = r
	case "join_room":
		var d struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(msg.Data, &d)
		r := s.rooms.Get(d.Code)
		if r == nil {
			c.Send(room.Envelope("error", map[string]string{"code": "room_not_found", "message": "No room with that code. Check the code and try again.", "context": msg.Type}))
			return
		}
		if c.room != nil && c.room != r {
			c.leaveRoom()
		}
		if err := r.Join(c.playerID, c.name, c); err != nil {
			c.fail(err, msg.Type)
			return
		}
		c.room = r
	case "leave_room":
		c.leaveRoom()
		c.Send(room.Envelope("left", map[string]string{}))
	default:
		if c.room == nil {
			c.Send(room.Envelope("error", errBody("not_in_room", "Join a room first")))
			return
		}
		var err error
		switch msg.Type {
		case "update_settings":
			var st room.Settings
			_ = json.Unmarshal(msg.Data, &st)
			err = c.room.UpdateSettings(c.playerID, st)
		case "start_game":
			err = c.room.Start(c.playerID)
		case "answer":
			var d struct {
				QuestionID string `json:"questionId"`
				Choice     int    `json:"choice"`
				Confidence int    `json:"confidence"`
			}
			if jerr := json.Unmarshal(msg.Data, &d); jerr != nil {
				err = room.ErrInvalidChoice
				break
			}
			err = c.room.Answer(c.playerID, d.QuestionID, d.Choice, d.Confidence)
		case "ready":
			err = c.room.Ready(c.playerID)
		case "next":
			err = c.room.Next(c.playerID)
		case "play_again":
			err = c.room.PlayAgain(c.playerID)
		default:
			err = errors.New("unknown_message")
		}
		if err != nil {
			c.fail(err, msg.Type)
		}
	}
}

func (c *wsClient) leaveRoom() {
	if c.room != nil {
		c.room.Leave(c.playerID)
		c.room = nil
	}
}
