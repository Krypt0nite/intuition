package store

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/lib/pq"

	"intuition/internal/content"
	"intuition/internal/selection"
)

//go:embed migrations/*.sql
var migrations embed.FS

// PostgresStore implements Store on PostgreSQL.
type PostgresStore struct{ db *sql.DB }

// OpenPostgres connects and applies pending migrations.
func OpenPostgres(ctx context.Context, url string) (*PostgresStore, error) {
	db, err := sql.Open("postgres", url)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	s := &PostgresStore{db: db}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *PostgresStore) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (name TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		var exists bool
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE name=$1)`, name).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		body, _ := migrations.ReadFile("migrations/" + name)
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(name) VALUES ($1)`, name); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (s *PostgresStore) Kind() string { return "postgres" }
func (s *PostgresStore) Close() error { return s.db.Close() }

func (s *PostgresStore) UpsertPlayer(ctx context.Context, p Player) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO players(id,name) VALUES ($1,$2)
		ON CONFLICT (id) DO UPDATE SET name=EXCLUDED.name, last_seen_at=now()`, p.ID, p.Name)
	return err
}

func (s *PostgresStore) GetPlayer(ctx context.Context, id string) (*Player, error) {
	var p Player
	err := s.db.QueryRowContext(ctx, `SELECT id,name,created_at,last_seen_at FROM players WHERE id=$1`, id).
		Scan(&p.ID, &p.Name, &p.CreatedAt, &p.LastSeenAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &p, err
}

func (s *PostgresStore) RecordAnswers(ctx context.Context, recs []AnswerRecord) error {
	if len(recs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, r := range recs {
		if r.AnsweredAt.IsZero() {
			r.AnsweredAt = time.Now()
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO answers(player_id,question_id,category,difficulty,context,session_id,choice,correct,confidence,response_ms,answered_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
			r.PlayerID, r.QuestionID, r.Category, r.Difficulty, r.Context, r.SessionID, r.Choice, r.Correct, r.Confidence, r.ResponseMS, r.AnsweredAt); err != nil {
			return err
		}
		c, w := 0, 1
		if r.Correct {
			c, w = 1, 0
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO player_question_stats(player_id,question_id,times_seen,times_correct,times_wrong,last_seen_at,last_correct)
			VALUES ($1,$2,1,$3,$4,$5,$6)
			ON CONFLICT (player_id,question_id) DO UPDATE SET
			  times_seen=player_question_stats.times_seen+1,
			  times_correct=player_question_stats.times_correct+EXCLUDED.times_correct,
			  times_wrong=player_question_stats.times_wrong+EXCLUDED.times_wrong,
			  last_seen_at=GREATEST(player_question_stats.last_seen_at,EXCLUDED.last_seen_at),
			  last_correct=EXCLUDED.last_correct`, r.PlayerID, r.QuestionID, c, w, r.AnsweredAt, r.Correct); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *PostgresStore) PlayerHistory(ctx context.Context, playerID string) (map[string]selection.History, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT question_id,times_seen,times_correct,times_wrong,last_seen_at,last_correct
		FROM player_question_stats WHERE player_id=$1`, playerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]selection.History{}
	for rows.Next() {
		var id string
		var h selection.History
		if err := rows.Scan(&id, &h.TimesSeen, &h.TimesCorrect, &h.TimesWrong, &h.LastSeenAt, &h.LastCorrect); err != nil {
			return nil, err
		}
		out[id] = h
	}
	return out, rows.Err()
}

func (s *PostgresStore) PlayerAnswers(ctx context.Context, playerID string, limit int) ([]AnswerRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT player_id,question_id,category,difficulty,context,session_id,choice,correct,confidence,response_ms,answered_at
		FROM answers WHERE player_id=$1 ORDER BY answered_at DESC, id DESC LIMIT $2`, playerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AnswerRecord
	for rows.Next() {
		var r AnswerRecord
		if err := rows.Scan(&r.PlayerID, &r.QuestionID, &r.Category, &r.Difficulty, &r.Context, &r.SessionID, &r.Choice, &r.Correct, &r.Confidence, &r.ResponseMS, &r.AnsweredAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *PostgresStore) SaveSession(ctx context.Context, r SessionRecord) error {
	sum, _ := json.Marshal(r.Summary)
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions(id,player_id,kind,score,total,summary,started_at,finished_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (id,player_id) DO NOTHING`,
		r.ID, r.PlayerID, r.Kind, r.Score, r.Total, sum, r.StartedAt, r.FinishedAt)
	return err
}

func (s *PostgresStore) RecentSessions(ctx context.Context, playerID string, limit int) ([]SessionRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,player_id,kind,score,total,summary,started_at,finished_at
		FROM sessions WHERE player_id=$1 ORDER BY finished_at DESC LIMIT $2`, playerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionRecord
	for rows.Next() {
		var r SessionRecord
		var sum []byte
		if err := rows.Scan(&r.ID, &r.PlayerID, &r.Kind, &r.Score, &r.Total, &sum, &r.StartedAt, &r.FinishedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(sum, &r.Summary)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *PostgresStore) DailySet(ctx context.Context, day string) ([]string, bool, error) {
	var ids []string
	err := s.db.QueryRowContext(ctx, `SELECT question_ids FROM daily_sets WHERE day=$1`, day).Scan(pq.Array(&ids))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	return ids, err == nil, err
}

func (s *PostgresStore) SaveDailySet(ctx context.Context, day string, ids []string) ([]string, error) {
	if _, err := s.db.ExecContext(ctx, `INSERT INTO daily_sets(day,question_ids) VALUES ($1,$2) ON CONFLICT (day) DO NOTHING`, day, pq.Array(ids)); err != nil {
		return nil, err
	}
	stored, _, err := s.DailySet(ctx, day)
	return stored, err
}

func (s *PostgresStore) DailyQuestionIDsSince(ctx context.Context, day string) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT question_ids FROM daily_sets WHERE day >= $1`, day)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var ids []string
		if err := rows.Scan(pq.Array(&ids)); err != nil {
			return nil, err
		}
		for _, id := range ids {
			out[id] = true
		}
	}
	return out, rows.Err()
}

func (s *PostgresStore) StartDaily(ctx context.Context, r DailyResult) (bool, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO daily_results(day,player_id,score,total,total_ms,finished,started_at)
		VALUES ($1,$2,0,$3,0,false,$4) ON CONFLICT (day,player_id) DO NOTHING`, r.Day, r.PlayerID, r.Total, r.StartedAt)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func (s *PostgresStore) UpdateDaily(ctx context.Context, r DailyResult) error {
	var fin any
	if r.Finished {
		fin = r.FinishedAt
	}
	_, err := s.db.ExecContext(ctx, `UPDATE daily_results SET score=$3,total=$4,total_ms=$5,finished=$6,finished_at=$7 WHERE day=$1 AND player_id=$2`,
		r.Day, r.PlayerID, r.Score, r.Total, r.TotalMS, r.Finished, fin)
	return err
}

func (s *PostgresStore) GetDaily(ctx context.Context, day, playerID string) (*DailyResult, error) {
	var r DailyResult
	var fin sql.NullTime
	err := s.db.QueryRowContext(ctx, `SELECT to_char(day,'YYYY-MM-DD'),player_id,score,total,total_ms,finished,started_at,finished_at
		FROM daily_results WHERE day=$1 AND player_id=$2`, day, playerID).
		Scan(&r.Day, &r.PlayerID, &r.Score, &r.Total, &r.TotalMS, &r.Finished, &r.StartedAt, &fin)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	r.FinishedAt = fin.Time
	return &r, err
}

func (s *PostgresStore) DailyLeaderboard(ctx context.Context, day string, limit int) ([]DailyResult, int, error) {
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM daily_results WHERE day=$1 AND finished`, day).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT to_char(d.day,'YYYY-MM-DD'),d.player_id,p.name,d.score,d.total,d.total_ms,d.started_at,d.finished_at
		FROM daily_results d JOIN players p ON p.id=d.player_id
		WHERE d.day=$1 AND d.finished ORDER BY d.score DESC,d.total_ms ASC,d.finished_at ASC LIMIT $2`, day, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []DailyResult
	for rows.Next() {
		var r DailyResult
		var fin sql.NullTime
		if err := rows.Scan(&r.Day, &r.PlayerID, &r.PlayerName, &r.Score, &r.Total, &r.TotalMS, &r.StartedAt, &fin); err != nil {
			return nil, 0, err
		}
		r.Finished, r.FinishedAt = true, fin.Time
		out = append(out, r)
	}
	return out, total, rows.Err()
}

func (s *PostgresStore) DailyRank(ctx context.Context, day, playerID string) (int, error) {
	var rank int
	err := s.db.QueryRowContext(ctx, `SELECT r FROM (
		SELECT player_id, ROW_NUMBER() OVER (ORDER BY score DESC,total_ms ASC,finished_at ASC) AS r
		FROM daily_results WHERE day=$1 AND finished) t WHERE player_id=$2`, day, playerID).Scan(&rank)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return rank, err
}

// ImportQuestions upserts question metadata for analytics/admin tooling.
// JSON files remain the source of truth.
func (s *PostgresStore) ImportQuestions(ctx context.Context, qs []*content.Question) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	ids := make([]string, 0, len(qs))
	for _, q := range qs {
		data, _ := json.Marshal(q)
		if _, err := tx.ExecContext(ctx, `INSERT INTO content_questions(id,category,subcategory,difficulty,variant_group,concepts,review_status,content_hash,data,imported_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,now())
			ON CONFLICT (id) DO UPDATE SET category=EXCLUDED.category,subcategory=EXCLUDED.subcategory,difficulty=EXCLUDED.difficulty,
			  variant_group=EXCLUDED.variant_group,concepts=EXCLUDED.concepts,review_status=EXCLUDED.review_status,
			  content_hash=EXCLUDED.content_hash,data=EXCLUDED.data,imported_at=now()
			WHERE content_questions.content_hash <> EXCLUDED.content_hash`,
			q.ID, q.Category, q.Subcategory, string(q.Difficulty), q.VariantGroup, pq.Array(q.Concepts), q.ReviewStatus, q.Hash, data); err != nil {
			return 0, fmt.Errorf("import %s: %w", q.ID, err)
		}
		ids = append(ids, q.ID)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE content_questions SET review_status='retired' WHERE NOT (id = ANY($1))`, pq.Array(ids)); err != nil {
		return 0, err
	}
	return len(ids), tx.Commit()
}
