package store

import (
	"context"
	"time"
)

// DailyUsage holds token and cost metrics for one calendar day.
type DailyUsage struct {
	Day       string  `json:"day"`
	TokensIn  int64   `json:"tokens_in"`
	TokensOut int64   `json:"tokens_out"`
	CostUSD   float64 `json:"cost_usd"`
}

// TurnRecord describes a recorded turn execution from the turns table.
type TurnRecord struct {
	ID         int64  `json:"id"`
	ChatID     int64  `json:"chat_id"`
	Status     string `json:"status"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
	Error      string `json:"error"`
}

// ChatSummary describes one chat/topic session stored in SQLite.
type ChatSummary struct {
	ChatID       int64  `json:"chat_id"`
	Model        string `json:"model"`
	MessageCount int    `json:"message_count"`
	LastAt       string `json:"last_at"`
}

// UsageHistory returns daily usage records for the last N days ordered ascending.
func (s *Store) UsageHistory(ctx context.Context, days int) ([]DailyUsage, error) {
	if days <= 0 {
		days = 14
	}
	since := time.Now().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	rows, err := s.db.QueryContext(ctx,
		`SELECT day, tokens_in, tokens_out, cost_usd FROM usage_daily WHERE day >= ? ORDER BY day ASC`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DailyUsage
	for rows.Next() {
		var d DailyUsage
		if err := rows.Scan(&d.Day, &d.TokensIn, &d.TokensOut, &d.CostUSD); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// RecentTurns returns the most recent turns ordered newest first.
func (s *Store) RecentTurns(ctx context.Context, limit int) ([]TurnRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, chat_id, status, started_at, COALESCE(finished_at,''), COALESCE(error,'')
		   FROM turns ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TurnRecord
	for rows.Next() {
		var tr TurnRecord
		if err := rows.Scan(&tr.ID, &tr.ChatID, &tr.Status, &tr.StartedAt, &tr.FinishedAt, &tr.Error); err != nil {
			return nil, err
		}
		out = append(out, tr)
	}
	return out, rows.Err()
}

// AllJobs returns all cron jobs across every chat/topic.
func (s *Store) AllJobs(ctx context.Context) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, chat_id, schedule, prompt, enabled, COALESCE(next_run_at,''), COALESCE(last_run_at,'')
		   FROM jobs ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Job
	for rows.Next() {
		var j Job
		var enabled int
		if err := rows.Scan(&j.ID, &j.ChatID, &j.Schedule, &j.Prompt, &enabled, &j.NextRunAt, &j.LastRunAt); err != nil {
			return nil, err
		}
		j.Enabled = enabled == 1
		out = append(out, j)
	}
	return out, rows.Err()
}

// ListChatsSummary returns active sessions grouped by chat_id with message counts.
func (s *Store) ListChatsSummary(ctx context.Context, limit int) ([]ChatSummary, error) {
	if limit <= 0 {
		limit = 30
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT m.chat_id, COALESCE(c.model,''), COUNT(m.id), MAX(m.created_at)
		   FROM messages m
		   LEFT JOIN chats c ON c.id = m.chat_id
		  GROUP BY m.chat_id
		  ORDER BY MAX(m.id) DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ChatSummary
	for rows.Next() {
		var cs ChatSummary
		if err := rows.Scan(&cs.ChatID, &cs.Model, &cs.MessageCount, &cs.LastAt); err != nil {
			return nil, err
		}
		out = append(out, cs)
	}
	return out, rows.Err()
}

// UpdateJob updates the schedule and prompt of an existing cron job.
func (s *Store) UpdateJob(ctx context.Context, id int64, schedule, prompt string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE jobs SET schedule = ?, prompt = ? WHERE id = ?`, schedule, prompt, id)
	return err
}

// DeleteTurn removes one turn record from the turns table.
func (s *Store) DeleteTurn(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM turns WHERE id = ?`, id)
	return err
}

// SetGlobalModel updates every existing chat/topic session model to model.
func (s *Store) SetGlobalModel(ctx context.Context, model string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE chats SET model = ?`, model)
	return err
}
