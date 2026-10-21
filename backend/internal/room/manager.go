package room

import (
	"crypto/rand"
	"errors"
	"math/big"
	"strings"
	"sync"
	"time"
)

// Unambiguous alphabet: no 0/O, 1/I/L.
const codeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

const CodeLength = 5

type Manager struct {
	mu    sync.Mutex
	rooms map[string]*Room
	deps  Deps
	stop  chan struct{}

	IdleTimeout     time.Duration // nobody connected
	FinishedTimeout time.Duration // lobby/results with no activity
}

func NewManager(deps Deps) *Manager {
	m := &Manager{rooms: map[string]*Room{}, deps: deps, stop: make(chan struct{}),
		IdleTimeout: 10 * time.Minute, FinishedTimeout: 60 * time.Minute}
	m.deps.onClose = m.remove
	go m.gcLoop()
	return m
}

func (m *Manager) Create(settings Settings) (*Room, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.rooms) > 5000 {
		return nil, errors.New("server_busy")
	}
	for attempt := 0; attempt < 20; attempt++ {
		code := randomCode()
		if _, taken := m.rooms[code]; !taken {
			r := newRoom(code, settings.Normalize(m.deps.Lib.Taxonomy), m.deps)
			m.rooms[code] = r
			return r, nil
		}
	}
	return nil, errors.New("no_code_available")
}

func NormalizeCode(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}

func (m *Manager) Get(code string) *Room {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.rooms[NormalizeCode(code)]
	if r != nil && r.Closed() {
		return nil
	}
	return r
}

func (m *Manager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.rooms)
}

func (m *Manager) remove(code string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.rooms, code)
}

func (m *Manager) gcLoop() {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-m.stop:
			return
		case now := <-t.C:
			m.Sweep(now)
		}
	}
}

// Sweep asks every room to close itself if idle.
func (m *Manager) Sweep(now time.Time) {
	m.mu.Lock()
	rooms := make([]*Room, 0, len(m.rooms))
	for _, r := range m.rooms {
		rooms = append(rooms, r)
	}
	m.mu.Unlock()
	for _, r := range rooms {
		r.do(func() { r.idle(now, m.IdleTimeout, m.FinishedTimeout) })
	}
}

func (m *Manager) Shutdown() {
	close(m.stop)
	m.mu.Lock()
	rooms := make([]*Room, 0, len(m.rooms))
	for _, r := range m.rooms {
		rooms = append(rooms, r)
	}
	m.mu.Unlock()
	for _, r := range rooms {
		r.do(r.close)
	}
}

func randomCode() string {
	var b strings.Builder
	for i := 0; i < CodeLength; i++ {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(codeAlphabet))))
		b.WriteByte(codeAlphabet[n.Int64()])
	}
	return b.String()
}
