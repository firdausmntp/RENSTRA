package store

import (
	"context"
	"time"
)

// ---- Telegram update dedup ----

func (s *Store) IsUpdateSeen(ctx context.Context, updateID int64) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM updates_seen WHERE update_id = ?`, updateID).Scan(&n)
	return n > 0, err
}

func (s *Store) MarkUpdateSeen(ctx context.Context, updateID int64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO updates_seen(update_id) VALUES(?)`, updateID)
	return err
}

// RecentUpdateIDs feeds Bot.MarkSeen after a restart, so updates already
// answered before the restart are not answered twice.
func (s *Store) RecentUpdateIDs(ctx context.Context, limit int) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT update_id FROM updates_seen ORDER BY update_id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// PruneUpdatesSeen drops rows older than 7 days.
func (s *Store) PruneUpdatesSeen(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM updates_seen WHERE seen_at < datetime('now', '-7 days')`)
	return err
}

// ---- Usage / cost ----

type Usage struct {
	TokensIn  int64
	TokensOut int64
	CostUSD   float64
}

func (s *Store) RecordUsage(ctx context.Context, day string, in, out int64, cost float64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO usage_daily(day, tokens_in, tokens_out, cost_usd) VALUES(?,?,?,?)
		 ON CONFLICT(day) DO UPDATE SET
		   tokens_in = tokens_in + excluded.tokens_in,
		   tokens_out = tokens_out + excluded.tokens_out,
		   cost_usd = cost_usd + excluded.cost_usd`,
		day, in, out, cost)
	return err
}

func (s *Store) UsageForDay(ctx context.Context, day string) (Usage, error) {
	var u Usage
	err := s.db.QueryRowContext(ctx,
		`SELECT tokens_in, tokens_out, cost_usd FROM usage_daily WHERE day = ?`, day).
		Scan(&u.TokensIn, &u.TokensOut, &u.CostUSD)
	if err != nil {
		return Usage{}, err
	}
	return u, nil
}

func (s *Store) UsageSince(ctx context.Context, from time.Time) (Usage, error) {
	var u Usage
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(tokens_in),0), COALESCE(SUM(tokens_out),0), COALESCE(SUM(cost_usd),0)
		   FROM usage_daily WHERE day >= ?`, from.Format("2006-01-02")).
		Scan(&u.TokensIn, &u.TokensOut, &u.CostUSD)
	return u, err
}

// ---- Cron jobs ----

type Job struct {
	ID        int64
	ChatID    int64
	Schedule  string
	Prompt    string
	Enabled   bool
	NextRunAt string
	LastRunAt string
}

func (s *Store) AddJob(ctx context.Context, chatID int64, schedule, prompt string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO jobs(chat_id, schedule, prompt, enabled) VALUES(?,?,?,1)`,
		chatID, schedule, prompt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) ListJobs(ctx context.Context, chatID int64) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, chat_id, schedule, prompt, enabled, COALESCE(next_run_at,''), COALESCE(last_run_at,'')
		   FROM jobs WHERE chat_id = ? ORDER BY id`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []Job
	for rows.Next() {
		var j Job
		var enabled int
		if err := rows.Scan(&j.ID, &j.ChatID, &j.Schedule, &j.Prompt, &enabled, &j.NextRunAt, &j.LastRunAt); err != nil {
			return nil, err
		}
		j.Enabled = enabled == 1
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

func (s *Store) RemoveJob(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM jobs WHERE id = ?`, id)
	return err
}

func (s *Store) SetJobEnabled(ctx context.Context, id int64, enabled bool) error {
	v := 0
	if enabled {
		v = 1
	}
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET enabled = ? WHERE id = ?`, v, id)
	return err
}

func (s *Store) UpdateJobRun(ctx context.Context, id int64, nextRun, lastRun string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE jobs SET next_run_at = ?, last_run_at = ? WHERE id = ?`, nextRun, lastRun, id)
	return err
}
