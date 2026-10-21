CREATE TABLE IF NOT EXISTS players (
  id           TEXT PRIMARY KEY,
  name         TEXT NOT NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Every answer ever given (solo, daily and multiplayer).
CREATE TABLE IF NOT EXISTS answers (
  id          BIGSERIAL PRIMARY KEY,
  player_id   TEXT NOT NULL REFERENCES players(id) ON DELETE CASCADE,
  question_id TEXT NOT NULL,
  category    TEXT NOT NULL,
  difficulty  TEXT NOT NULL,
  context     TEXT NOT NULL,
  session_id  TEXT NOT NULL,
  choice      SMALLINT NOT NULL,
  correct     BOOLEAN NOT NULL,
  confidence  SMALLINT NOT NULL DEFAULT 0,
  response_ms INTEGER NOT NULL DEFAULT 0,
  answered_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS answers_player_time ON answers (player_id, answered_at DESC);
CREATE INDEX IF NOT EXISTS answers_question ON answers (question_id);

-- Per player × question rollup used by the anti-repetition selector.
CREATE TABLE IF NOT EXISTS player_question_stats (
  player_id     TEXT NOT NULL REFERENCES players(id) ON DELETE CASCADE,
  question_id   TEXT NOT NULL,
  times_seen    INTEGER NOT NULL DEFAULT 0,
  times_correct INTEGER NOT NULL DEFAULT 0,
  times_wrong   INTEGER NOT NULL DEFAULT 0,
  last_seen_at  TIMESTAMPTZ NOT NULL,
  last_correct  BOOLEAN NOT NULL DEFAULT false,
  PRIMARY KEY (player_id, question_id)
);

CREATE TABLE IF NOT EXISTS sessions (
  id          TEXT NOT NULL,
  player_id   TEXT NOT NULL REFERENCES players(id) ON DELETE CASCADE,
  kind        TEXT NOT NULL,
  score       INTEGER NOT NULL,
  total       INTEGER NOT NULL,
  summary     JSONB NOT NULL DEFAULT '{}'::jsonb,
  started_at  TIMESTAMPTZ NOT NULL,
  finished_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (id, player_id)
);
CREATE INDEX IF NOT EXISTS sessions_player ON sessions (player_id, finished_at DESC);

CREATE TABLE IF NOT EXISTS daily_sets (
  day          DATE PRIMARY KEY,
  question_ids TEXT[] NOT NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS daily_results (
  day         DATE NOT NULL,
  player_id   TEXT NOT NULL REFERENCES players(id) ON DELETE CASCADE,
  score       INTEGER NOT NULL DEFAULT 0,
  total       INTEGER NOT NULL,
  total_ms    BIGINT NOT NULL DEFAULT 0,
  finished    BOOLEAN NOT NULL DEFAULT false,
  started_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  finished_at TIMESTAMPTZ,
  PRIMARY KEY (day, player_id)
);
CREATE INDEX IF NOT EXISTS daily_results_rank ON daily_results (day, score DESC, total_ms ASC);

-- Optional mirror of the JSON content for analytics/admin tooling.
CREATE TABLE IF NOT EXISTS content_questions (
  id            TEXT PRIMARY KEY,
  category      TEXT NOT NULL,
  subcategory   TEXT NOT NULL,
  difficulty    TEXT NOT NULL,
  variant_group TEXT NOT NULL,
  concepts      TEXT[] NOT NULL,
  review_status TEXT NOT NULL,
  content_hash  TEXT NOT NULL,
  data          JSONB NOT NULL,
  imported_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
