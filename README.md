# 🔬 INTUITION — What happens next?

A game about discovering that your intuition is sometimes wrong. Each round gives you a short situation, you predict what happens, and the reveal shows what actually happens, why, and which instinct misled you.

Solo (Quick 5 / Standard 10 / Marathon 25), a daily five-question challenge with a global leaderboard, and real-time multiplayer rooms with join codes. Optional confidence mode tracks your **surprise rate**: how often you were sure and wrong.

## What's in the box

| Area | Status |
| --- | --- |
| Question bank | 149 hand-written questions across 9 domains (physics, chemistry, biology, space, earth, everyday, technology, maths, perception), 176 distinct concepts, 1 three-step chain round. 0 validator errors/warnings. |
| Content pipeline | JSON packs + taxonomy, validator CLI (schema, taxonomy, answers, explanation length, placeholders, item-writing heuristics, near-duplicate detection), Postgres import, demo-pack export |
| Selection engine | Anti-repetition (72 h hard cooldown, 30-day soft cooldown), category rotation, variant-group exclusion, concept spacing, difficulty arc by experience tier, weak-concept reinforcement, exploration slots, chain rounds, deterministic daily sets |
| Solo + daily | Server-authoritative; answers never reach the browser before the reveal; one daily attempt per player; same daily answer order for everyone |
| Multiplayer | Go WebSocket server, one actor goroutine per room, timers, early reveal when all answered, ready-up, host migration, reconnect/refresh resume, late join, slow-client protection |
| Persistence | PostgreSQL (players, every answer, per-question history, sessions, daily sets/results) or in-memory for dev |
| Frontend | React + TypeScript (Vite), mobile-first, keyboard play (1–4 / A–D, Enter), screen-reader labels, reduced motion, synthesized sounds with mute, shareable results |
| Demo mode | Full offline solo + daily play with a TypeScript port of the selector; also builds to a single HTML file |
| Tests | Go (content, selection, game, room, HTTP + WebSocket integration, store contract against real Postgres) with `-race`; Vitest for the demo engine; `tests/e2e.sh` runs the real server against four bots |

## Architecture

```
               ┌──────────────────────── single Go binary ─────────────────────────┐
 Browser ──────│ HTTP API ── game.Session (solo/daily)          content.Library     │
  React/TS     │                    │                           (JSON packs,       │
  ─ REST ─────▶│                    ├── selection.Selector ◀────  validated at boot)│
  ─ WebSocket ▶│ /ws ── room.Manager ─ room.Room (actor/goroutine per room)         │
               │                    │                                               │
               │              store.Store ── Postgres  (or MemoryStore in dev)      │
               │ static files (frontend/dist)                                       │
               └────────────────────────────────────────────────────────────────────┘
```

Why Vite rather than Next.js: the game is a client-rendered SPA behind a Go server, so SSR adds nothing, and serving the built files from the Go binary means one process to deploy. Details, the selection algorithm and the WebSocket protocol are in [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md).

```
intuition/
├── backend/          Go module "intuition"
│   ├── cmd/server    the game server
│   ├── cmd/content   validate | stats | export-demo | import
│   ├── cmd/bots      multiplayer simulator / load + e2e driver
│   └── internal/     content · selection · game · room · store · server
├── content/          taxonomy.json, <category>/*.json packs, daily/schedule.json, schema/
├── frontend/         React + TS (Vite); src/demo has the offline engine + generated pack
├── docs/             ARCHITECTURE.md, SCIENCE_NOTES.md
├── tests/e2e.sh
├── CONTENT_GUIDE.md
└── Dockerfile, docker-compose.yml, Makefile
```

## Quick start

Requirements: Go 1.22+, Node 20+. Postgres is optional.

```bash
# 1. Just play (no Go needed): offline demo mode with the bundled pack
cd frontend && npm install && npm run dev        # http://localhost:5173

# 2. Full stack with in-memory storage
make dev                                         # Go on :8080, Vite on :5173 (proxied)

# 3. Full stack with Postgres
docker compose up -d db
export DATABASE_URL=postgres://intuition:intuition@localhost:5432/intuition?sslmode=disable
make dev
```

The frontend detects the server automatically; if `/api/health` doesn't answer it switches to demo mode (shown as a badge in the top bar). Multiplayer needs the server.

To try multiplayer alone, create a room in the browser, then add bots:

```bash
cd backend && go run ./cmd/bots -players 3 -room ABCDE -accuracy 0.6
# or let bots host and play a whole game:
go run ./cmd/bots -players 4 -create -start -questions 5
```

## Environment variables

| Variable | Default | Purpose |
| --- | --- | --- |
| `PORT` | `8080` | HTTP port |
| `DATABASE_URL` | empty | Postgres URL. Empty = in-memory store (data lost on restart). Migrations run automatically. |
| `SERVER_SECRET` | dev value | HMAC key for player tokens. **Required** when `APP_ENV=production`. |
| `APP_ENV` | `development` | `production` enforces a real secret |
| `CONTENT_DIR` | `../content` | Question bank location |
| `STATIC_DIR` | `../frontend/dist` | Built frontend to serve |
| `DAILY_TIMEZONE` | `UTC` | When the daily rolls over, e.g. `Asia/Kolkata` |
| `DAILY_SALT` | `intuition` | Seeds generated daily sets |
| `ALLOWED_ORIGINS` | empty (all) | CORS/WebSocket origin allow-list when the frontend is hosted elsewhere |
| `INCLUDE_DRAFTS` | `false` | Serve `draft` questions (local authoring) |
| `VITE_DEMO_FALLBACK` | `false` in prod builds | Ship the offline pack in production. Off by default because the pack contains answers. |

## Content

```bash
make validate-content     # errors fail CI and the server refuses to boot on them
make content-stats        # counts by category / difficulty, concepts, variant groups
make export-demo          # regenerate frontend/src/demo/pack.json after edits
cd backend && go run ./cmd/content import -database-url "$DATABASE_URL"   # mirror into Postgres
```

Adding questions never touches game code: drop a pack into `content/<category>/`, run the validator, done. See [`CONTENT_GUIDE.md`](CONTENT_GUIDE.md) for the schema and writing standards.

## Tests

```bash
make test        # validator + go test -race ./... + typecheck + vitest
make e2e         # real server + four WebSocket bots play a full game
TEST_DATABASE_URL=postgres://... go test ./internal/store/   # store contract against Postgres
```

Highlights: a 30-day simulation (3 games a day) proves nothing repeats inside the 72-hour cooldown; questions sent over HTTP and WebSocket are checked for leaked answers; reconnect, host migration, late join, timeouts and 7 concurrent answerers are exercised under the race detector.

## Deployment

One container plus Postgres:

```bash
docker build -t intuition .
docker run -p 8080:8080 -e DATABASE_URL=... -e SERVER_SECRET=... -e DAILY_TIMEZONE=Asia/Kolkata intuition
```

Cheap options: Fly.io / Render / Railway for the container, Neon or Supabase free tier for Postgres. Rooms live in process memory, so run **one instance** (see scaling notes in the architecture doc). The Docker build runs the content validator, so bad content can't ship. The Dockerfile has not been run in this environment; it uses standard images and the same commands verified here.

## Honest limits of this version

- **Content volume is the bottleneck, not the engine.** 149 vetted questions cover about two weeks of daily play for a heavy player. In the 30-day simulation, a player doing 3 games a day sees repeats (correctly spaced past the cooldown) from about day 5. The schema, validator and selector are built for 2,000–10,000+; reaching that is a writing-and-review effort. `CONTENT_GUIDE.md` describes a draft → reviewed → verified pipeline for it.
- All questions are marked `reviewed` with a note that they're AI-authored and self-checked. They should get an independent expert pass before being marked `verified`. Several of the example situations in the original brief needed correcting; see [`docs/SCIENCE_NOTES.md`](docs/SCIENCE_NOTES.md).
- Players are anonymous (signed device token, no accounts), so one person could play the daily from several browsers.
- Multiplayer is single-instance; horizontal scaling needs sticky routing by room code or a shared room store.
