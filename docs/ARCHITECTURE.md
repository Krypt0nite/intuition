# Architecture

## Backend packages (`backend/internal`)

| Package | Responsibility |
| --- | --- |
| `content` | Schema types, taxonomy, pack loading with defaults, validation, duplicate detection. Loaded once at boot; the server refuses to start on content errors. |
| `selection` | Chooses questions per player or room; deterministic daily sets. Pure logic, no I/O. |
| `game` | Option shuffling, public views (no answers), solo/daily sessions, scoring, summaries. |
| `room` | Multiplayer rooms as actors, room manager, codes, idle cleanup. |
| `store` | `Store` interface with `MemoryStore` and `PostgresStore` (embedded migrations). |
| `server` | HTTP API, WebSocket transport, identity tokens, rate limiting, static files. |

## Identity

No accounts. `POST /api/players` creates an id and returns `id.HMAC(secret, id)` as a token, which the browser keeps in localStorage and sends as `Authorization: Bearer …` (HTTP) or in `hello` (WebSocket). Tokens can't be forged without `SERVER_SECRET`. If the server rejects a token, the client silently registers again.

## Cheating resistance

- The correct answer never leaves the server before the reveal. `QuestionView` has no answer fields; tests marshal every question and assert nothing leaks over HTTP or WebSocket.
- Options are shuffled per session (per room for multiplayer; per *day* for the daily so everyone sees the same order). The client sends a display index; the server maps it back.
- All scoring and timing is server-side. Late multiplayer answers (300 ms grace past the deadline) are rejected; duplicates are rejected (rooms) or idempotent (solo retries).
- Production builds exclude the offline demo pack, which contains answers.

## Selection algorithm

`selection.Select(profile, request)` builds a session slot by slot rather than sampling the bank.

1. **Eligible pool**: servable questions in the requested categories. Chain steps are only reachable through their first step.
2. **Chain round** (sessions of 8+, 35% chance): a chain whose steps are all outside the hard cooldown is placed as a consecutive block, never first.
3. **Difficulty arc**: the player's experience tier (answers so far, nudged up or down by accuracy) picks a mix, for example novice 45/40/13/2% easy/medium/hard/expert and veteran 10/30/35/25%. The session starts gentle, ends with its hardest question, and shuffles the middle.
4. **Category rotation**: deficit scheduling across available categories with heavy penalties for back-to-back repeats, giving sequences like physics, biology, everyday, space, chemistry…
5. **Reinforce and explore slots**: about 20% of slots favour concepts the player keeps missing (only when there's evidence: 2+ attempts under 50%), and one slot in longer sessions favours an unseen subcategory. Never the first slot, never the whole session.
6. **Fill each slot** by scoring every candidate:
   - unseen +3; seen: penalty decaying over the 30-day soft cooldown, −0.3 per previous view, a small "redemption" bonus for old misses
   - −0.8 per level away from the slot's target difficulty
   - −1.2 per concept already in this session, −0.4 per concept in the last 25 answers
   - −0.6 for a repeated subcategory
   - random jitter, then a weighted pick among the top three

   Hard constraints, relaxed in order only if nothing qualifies: right category → any other category than the previous slot → any category; never inside the 72-hour hard cooldown; never a variant group already in the session (chains reserve theirs up front). The last fallbacks guarantee a full session even for someone who has seen everything.

**Rooms** merge players' profiles: a question counts as "recently seen" if *anyone* saw it, and difficulty targets the median player.

**Daily**: seeded by `salt:date`. Five questions from five different categories, difficulty 1-2-2-3-4, no shared concepts, excluding the last 60 days of daily questions. The first request each day generates and stores the set (first writer wins), so it's stable for everyone. Dates in `content/daily/schedule.json` override generation with curated sets.

The TypeScript demo engine (`frontend/src/demo/engine.ts`) implements the same rules for offline play.

## Multiplayer rooms

Each room is one goroutine reading closures from an inbox. All state is owned by that goroutine, so there are no locks around game state; exported methods post work and wait for the result. Timers use `time.AfterFunc` and a generation counter so stale timers are ignored.

```
lobby ──start──▶ question ──all connected answered | deadline──▶ reveal ──all connected ready | reveal timer | host skip──▶ question …
  ▲                                                                                    └── after last question ──▶ results
  └──────────────────────────────── play_again (host) ◀───────────────────────────────────────────────────────────────┘
```

- **Disconnects** never block progress: "everyone answered" counts only connected players. A disconnected player keeps their score.
- **Reconnect / refresh**: rejoining with the same identity reattaches; the server replays the current question (and the player's locked answer), reveal or results.
- **Host leaving**: an explicit leave migrates host immediately; a disconnect migrates after a 20-second grace period.
- **Late join** (setting, default on): joins at 0 points; accuracy counts only questions they were present for.
- **Lobby cleanup**: players disconnected 90 s in the lobby are removed. Empty rooms close; idle rooms close after 10 minutes with nobody connected or 60 minutes inactive in lobby/results.
- **Slow clients**: each connection has a 64-message buffer; a client that can't keep up is disconnected instead of stalling the room. Messages are capped at 8 KB and 30 per second.
- **Scoring**: +1 per correct answer decides the ranking. With speed bonus on, a correct answer also earns 0–10 speed points used only as a tie-breaker, so intuition matters more than reflexes.

### WebSocket protocol (`/ws`)

Every message is `{"type": "...", "data": {...}}`.

Client → server: `hello {token, name}` (must be first) · `create_room {settings?}` · `join_room {code}` · `leave_room` · `update_settings {settings}` (host) · `start_game` (host) · `answer {questionId, choice, confidence}` · `ready` · `next` (host, skip reveal wait) · `play_again` (host) · `ping`.

Server → client: `welcome` · `room_state {code, phase, hostId, settings, players[], questionIndex, total, deadline?, revealEndsAt?, serverTime}` (sent on every change) · `question {question, deadline, durationMs, serverTime, confidence}` · `answer_ack` · `reveal {reveal, results[], distribution[], leaderboard[], revealEndsAt, isLast}` · `results {leaderboard, summaries, awards, winners, questions}` · `room_closed` · `error {code, message}` · `pong`.

Clients estimate clock offset from `serverTime` so countdowns match the server.

## HTTP API

| Method & path | Auth | Purpose |
| --- | --- | --- |
| `GET /api/health` | | status, question count, content version, store |
| `GET /api/categories` | | taxonomy with question counts |
| `POST /api/players` | | create anonymous player → `{playerId, token, name}` |
| `PATCH /api/players/me` | ✓ | rename |
| `GET /api/players/me/stats` | ✓ | lifetime accuracy, streaks, categories, calibration, concepts mastered, surprise rate |
| `POST /api/sessions` | ✓ | `{mode, categories, confidence}` → first question |
| `GET /api/sessions/{id}` | ✓ | resume after refresh |
| `POST /api/sessions/{id}/answers` | ✓ | `{questionId, choice, confidence}` → result, reveal, next question, summary |
| `GET /api/daily` | ✓ | today's status, your result and rank, top 20 |
| `POST /api/daily/start` | ✓ | start (or resume) today's set; 409 if already played |
| `GET /api/daily/leaderboard?date=` | | top 50 |

## Persistence

`players`, `answers` (every answer with context, confidence and response time), `player_question_stats` (the per-question rollup the selector reads), `sessions`, `daily_sets`, `daily_results`, and `content_questions` (optional mirror of the JSON bank for analytics). Migrations are embedded and applied at startup. Solo sessions and rooms are in memory; finished results are persisted.

## Performance notes

The browser never downloads the question bank in production; it receives one question at a time, and each answer response includes the next question so transitions need no extra round trip. The production JS bundle is about 59 KB gzipped. Selection over ~150 questions takes well under a millisecond; it's linear in pool size and comfortably fast at 10,000.

## Scaling beyond one instance

Rooms live in process memory, so run a single instance (fine for thousands of concurrent players). To scale out: route `/ws` by room code (sticky hashing at the load balancer) or move room state to a shared store, and move solo sessions into Postgres or Redis.
