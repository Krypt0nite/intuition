// Command server runs the INTUITION game server: HTTP API, WebSocket
// multiplayer and the built frontend, as a single binary.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // embed zone data so DAILY_TIMEZONE works in minimal containers

	"intuition/internal/content"
	"intuition/internal/selection"
	"intuition/internal/server"
	"intuition/internal/store"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	contentDir := env("CONTENT_DIR", firstDir("../content", "./content", "/app/content"))
	lib, rep, err := content.LoadDir(contentDir)
	if err != nil {
		log.Error("load content", "dir", contentDir, "err", err)
		os.Exit(1)
	}
	for _, w := range rep.Warnings() {
		log.Warn("content", "issue", w.String())
	}
	if rep.HasErrors() {
		for _, e := range rep.Errors() {
			log.Error("content", "issue", e.String())
		}
		log.Error("refusing to start: content has errors (run `go run ./cmd/content validate`)")
		os.Exit(1)
	}
	cfg := selection.DefaultConfig()
	cfg.IncludeDrafts = os.Getenv("INCLUDE_DRAFTS") == "true"
	sel := selection.New(lib, cfg)
	log.Info("content loaded", "questions", len(lib.Questions), "servable", sel.PoolSize(), "version", lib.Version)

	ctx := context.Background()
	var st store.Store
	if url := os.Getenv("DATABASE_URL"); url != "" {
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		pg, err := store.OpenPostgres(cctx, url)
		cancel()
		if err != nil {
			log.Error("postgres", "err", err)
			os.Exit(1)
		}
		st = pg
		log.Info("store: postgres")
	} else {
		st = store.NewMemory()
		log.Warn("store: in-memory (set DATABASE_URL for persistence)")
	}
	defer st.Close()

	secret := os.Getenv("SERVER_SECRET")
	if secret == "" {
		if os.Getenv("APP_ENV") == "production" {
			log.Error("SERVER_SECRET is required in production")
			os.Exit(1)
		}
		secret = "dev-insecure-secret-change-me"
		log.Warn("SERVER_SECRET not set; using an insecure development secret")
	}
	loc := time.UTC
	if tz := os.Getenv("DAILY_TIMEZONE"); tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l
		} else {
			log.Warn("bad DAILY_TIMEZONE, using UTC", "tz", tz)
		}
	}
	var origins []string
	if o := os.Getenv("ALLOWED_ORIGINS"); o != "" {
		origins = strings.Split(o, ",")
	}
	staticDir := env("STATIC_DIR", firstDir("../frontend/dist", "./web", "/app/web"))
	srv := server.New(server.Config{Secret: secret, StaticDir: staticDir, AllowedOrigins: origins, DailyLocation: loc,
		DailySalt: env("DAILY_SALT", "intuition")}, lib, sel, st, log)

	addr := ":" + env("PORT", "8080")
	hs := &http.Server{Addr: addr, Handler: srv, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Info("listening", "addr", addr, "static", staticDir)
		if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("listen", "err", err)
			os.Exit(1)
		}
	}()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Info("shutting down")
	srv.Shutdown()
	sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_ = hs.Shutdown(sctx)
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func firstDir(dirs ...string) string {
	for _, d := range dirs {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			return d
		}
	}
	return dirs[0]
}
