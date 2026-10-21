package store

import (
	"context"
	"os"
	"testing"
	"time"
)

// Runs against the memory store always, and against Postgres when
// TEST_DATABASE_URL is set.
func stores(t *testing.T) map[string]Store {
	out := map[string]Store{"memory": NewMemory()}
	if url := os.Getenv("TEST_DATABASE_URL"); url != "" {
		pg, err := OpenPostgres(context.Background(), url)
		if err != nil {
			t.Fatal(err)
		}
		for _, tbl := range []string{"answers", "player_question_stats", "sessions", "daily_results", "daily_sets", "players"} {
			if _, err := pg.db.Exec("DELETE FROM " + tbl); err != nil {
				t.Fatal(err)
			}
		}
		t.Cleanup(func() { pg.Close() })
		out["postgres"] = pg
	} else {
		t.Log("TEST_DATABASE_URL not set; skipping Postgres")
	}
	return out
}

func TestStoreContract(t *testing.T) {
	for name, st := range stores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			must(st.UpsertPlayer(ctx, Player{ID: "p1", Name: "Asha"}))
			must(st.UpsertPlayer(ctx, Player{ID: "p2", Name: "Ben"}))
			must(st.UpsertPlayer(ctx, Player{ID: "p1", Name: "Asha K"}))
			p, err := st.GetPlayer(ctx, "p1")
			must(err)
			if p.Name != "Asha K" {
				t.Fatalf("rename failed: %+v", p)
			}
			if _, err := st.GetPlayer(ctx, "nobody"); err != ErrNotFound {
				t.Fatalf("want ErrNotFound, got %v", err)
			}
			t0 := time.Now().Add(-time.Hour).UTC().Truncate(time.Millisecond)
			must(st.RecordAnswers(ctx, []AnswerRecord{
				{PlayerID: "p1", QuestionID: "q1", Category: "physics", Difficulty: "easy", Context: "solo", SessionID: "s1", Choice: 1, Correct: true, Confidence: 3, ResponseMS: 1200, AnsweredAt: t0},
				{PlayerID: "p1", QuestionID: "q1", Category: "physics", Difficulty: "easy", Context: "solo", SessionID: "s2", Choice: 0, Correct: false, AnsweredAt: t0.Add(time.Minute)},
				{PlayerID: "p1", QuestionID: "q2", Category: "biology", Difficulty: "hard", Context: "room", SessionID: "s2", Choice: -1, Correct: false, AnsweredAt: t0.Add(2 * time.Minute)},
			}))
			h, err := st.PlayerHistory(ctx, "p1")
			must(err)
			if q1 := h["q1"]; q1.TimesSeen != 2 || q1.TimesCorrect != 1 || q1.TimesWrong != 1 || q1.LastCorrect || !q1.LastSeenAt.Equal(t0.Add(time.Minute)) {
				t.Fatalf("bad history %+v", h["q1"])
			}
			ans, err := st.PlayerAnswers(ctx, "p1", 2)
			must(err)
			if len(ans) != 2 || ans[0].QuestionID != "q2" {
				t.Fatalf("answers should be newest first: %+v", ans)
			}
			must(st.SaveSession(ctx, SessionRecord{ID: "s1", PlayerID: "p1", Kind: "quick", Score: 3, Total: 5, Summary: map[string]any{"accuracy": 0.6}, StartedAt: t0, FinishedAt: t0}))
			ss, err := st.RecentSessions(ctx, "p1", 5)
			must(err)
			if len(ss) != 1 || ss[0].Score != 3 {
				t.Fatalf("sessions %+v", ss)
			}

			day := "2026-10-06"
			got, err := st.SaveDailySet(ctx, day, []string{"a", "b", "c"})
			must(err)
			again, err := st.SaveDailySet(ctx, day, []string{"x", "y", "z"})
			must(err)
			if got[0] != "a" || again[0] != "a" {
				t.Fatalf("first daily set must win: %v %v", got, again)
			}
			recent, err := st.DailyQuestionIDsSince(ctx, "2026-09-01")
			must(err)
			if !recent["b"] {
				t.Fatal("recent daily ids missing")
			}
			for _, pid := range []string{"p1", "p2"} {
				ok, err := st.StartDaily(ctx, DailyResult{Day: day, PlayerID: pid, Total: 5, StartedAt: t0})
				must(err)
				if !ok {
					t.Fatal("first start should insert")
				}
			}
			if ok, _ := st.StartDaily(ctx, DailyResult{Day: day, PlayerID: "p1", Total: 5, StartedAt: t0}); ok {
				t.Fatal("second start must not insert")
			}
			must(st.UpdateDaily(ctx, DailyResult{Day: day, PlayerID: "p1", Score: 4, Total: 5, TotalMS: 9000, Finished: true, FinishedAt: t0.Add(time.Minute)}))
			must(st.UpdateDaily(ctx, DailyResult{Day: day, PlayerID: "p2", Score: 4, Total: 5, TotalMS: 7000, Finished: true, FinishedAt: t0.Add(2 * time.Minute)}))
			board, total, err := st.DailyLeaderboard(ctx, day, 10)
			must(err)
			if total != 2 || board[0].PlayerID != "p2" || board[0].PlayerName != "Ben" {
				t.Fatalf("ties should break on time: %+v", board)
			}
			if r, _ := st.DailyRank(ctx, day, "p1"); r != 2 {
				t.Fatalf("rank %d", r)
			}
			res, err := st.GetDaily(ctx, day, "p1")
			must(err)
			if !res.Finished || res.Score != 4 || res.Day != day {
				t.Fatalf("daily result %+v", res)
			}
		})
	}
}
