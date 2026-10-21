// Package server exposes the HTTP API, the WebSocket endpoint and the
// static frontend.
package server

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"intuition/internal/content"
	"intuition/internal/game"
	"intuition/internal/room"
	"intuition/internal/selection"
	"intuition/internal/store"
)

type Config struct {
	Secret         string
	StaticDir      string
	AllowedOrigins []string
	DailyLocation  *time.Location
	DailySalt      string
	RoomTiming     *room.Timing // tests override
}

type Server struct {
	cfg      Config
	lib      *content.Library
	sel      *selection.Selector
	store    store.Store
	sessions *game.Manager
	rooms    *room.Manager
	log      *slog.Logger
	mux      *http.ServeMux
	dailyMu  sync.Mutex
	limiter  *ipLimiter
}

func New(cfg Config, lib *content.Library, sel *selection.Selector, st store.Store, log *slog.Logger) *Server {
	if cfg.DailyLocation == nil {
		cfg.DailyLocation = time.UTC
	}
	if cfg.DailySalt == "" {
		cfg.DailySalt = "intuition"
	}
	timing := room.DefaultTiming()
	if cfg.RoomTiming != nil {
		timing = *cfg.RoomTiming
	}
	s := &Server{
		cfg: cfg, lib: lib, sel: sel, store: st, log: log,
		sessions: game.NewManager(3 * time.Hour),
		rooms:    room.NewManager(room.Deps{Lib: lib, Selector: sel, Store: st, Timing: timing, Log: log}),
		mux:      http.NewServeMux(),
		limiter:  newIPLimiter(20, 60),
	}
	s.routes()
	go s.sweepLoop()
	return s
}

func (s *Server) routes() {
	m := s.mux
	m.HandleFunc("GET /api/health", s.handleHealth)
	m.HandleFunc("GET /api/categories", s.handleCategories)
	m.HandleFunc("POST /api/players", s.limit(s.handleCreatePlayer))
	m.HandleFunc("PATCH /api/players/me", s.auth(s.handleRenamePlayer))
	m.HandleFunc("GET /api/players/me/stats", s.auth(s.handleStats))
	m.HandleFunc("POST /api/sessions", s.limit(s.auth(s.handleStartSession)))
	m.HandleFunc("GET /api/sessions/{id}", s.auth(s.handleGetSession))
	m.HandleFunc("POST /api/sessions/{id}/answers", s.limit(s.auth(s.handleAnswer)))
	m.HandleFunc("GET /api/daily", s.auth(s.handleDaily))
	m.HandleFunc("POST /api/daily/start", s.limit(s.auth(s.handleDailyStart)))
	m.HandleFunc("GET /api/daily/leaderboard", s.handleDailyLeaderboard)
	m.HandleFunc("GET /ws", s.handleWS)
	m.HandleFunc("/", s.handleStatic)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if o := r.Header.Get("Origin"); o != "" && s.originAllowed(r) && strings.HasPrefix(r.URL.Path, "/api/") {
		w.Header().Set("Access-Control-Allow-Origin", o)
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, OPTIONS")
		w.Header().Set("Vary", "Origin")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	s.mux.ServeHTTP(w, r)
}

func (s *Server) Shutdown() { s.rooms.Shutdown() }

func (s *Server) sweepLoop() {
	for range time.Tick(5 * time.Minute) {
		s.sessions.Sweep(time.Now())
	}
}

// ---- Identity ----
// Players are anonymous but persistent: the server issues an id plus an
// HMAC-signed token the browser keeps in localStorage.

type ctxKey struct{}

func (s *Server) sign(id string) string {
	mac := hmac.New(sha256.New, []byte(s.cfg.Secret))
	mac.Write([]byte(id))
	return id + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))[:27]
}

func (s *Server) verify(token string) (string, bool) {
	id, _, ok := strings.Cut(token, ".")
	if !ok || id == "" {
		return "", false
	}
	return id, hmac.Equal([]byte(s.sign(id)), []byte(token))
}

func (s *Server) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		id, ok := s.verify(tok)
		if !ok {
			writeErr(w, http.StatusUnauthorized, "unauthorized", "Missing or invalid player token")
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, id)))
	}
}

func playerID(r *http.Request) string { id, _ := r.Context().Value(ctxKey{}).(string); return id }

func newPlayerID() string {
	b := make([]byte, 9)
	_, _ = rand.Read(b)
	return "p" + hex.EncodeToString(b)
}

// CleanName trims and limits display names to 1–24 printable characters.
func CleanName(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(name))
	name = strings.Join(strings.Fields(name), " ")
	if r := []rune(name); len(r) > 24 {
		name = string(r[:24])
	}
	return name
}

// ---- Helpers ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"error": code, "message": msg})
}

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 16<<10))
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func (s *Server) originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" || len(s.cfg.AllowedOrigins) == 0 {
		return true
	}
	for _, o := range s.cfg.AllowedOrigins {
		if o == "*" || strings.EqualFold(o, origin) {
			return true
		}
	}
	// Same-host requests are always fine.
	return strings.HasSuffix(origin, "://"+r.Host)
}

// ---- Rate limiting (per IP token bucket, in memory) ----

type bucket struct {
	tokens float64
	last   time.Time
}

type ipLimiter struct {
	mu      sync.Mutex
	rate    float64
	burst   float64
	buckets map[string]*bucket
}

func newIPLimiter(rate, burst float64) *ipLimiter {
	return &ipLimiter{rate: rate, burst: burst, buckets: map[string]*bucket{}}
}

func (l *ipLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	b := l.buckets[ip]
	if b == nil {
		if len(l.buckets) > 100000 {
			l.buckets = map[string]*bucket{}
		}
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[ip] = b
	}
	b.tokens = min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func clientIP(r *http.Request) string {
	if f := r.Header.Get("X-Forwarded-For"); f != "" {
		ip, _, _ := strings.Cut(f, ",")
		return strings.TrimSpace(ip)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) limit(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.limiter.allow(clientIP(r)) {
			writeErr(w, http.StatusTooManyRequests, "rate_limited", "Too many requests; slow down a little")
			return
		}
		h(w, r)
	}
}

// ---- Static frontend ----

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if s.cfg.StaticDir == "" {
		writeErr(w, http.StatusNotFound, "not_found", "No frontend build configured (set STATIC_DIR)")
		return
	}
	clean := filepath.Clean("/" + r.URL.Path)
	path := filepath.Join(s.cfg.StaticDir, clean)
	if st, err := os.Stat(path); err != nil || st.IsDir() {
		path = filepath.Join(s.cfg.StaticDir, "index.html") // SPA fallback
		w.Header().Set("Cache-Control", "no-cache")
	} else if strings.HasPrefix(clean, "/assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.ServeFile(w, r, path)
}
