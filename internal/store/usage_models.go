package store

import (
	"context"
	"strings"
	"time"
)

// ModelUsageSummary holds aggregated request, token, and cost stats per model.
type ModelUsageSummary struct {
	Model     string  `json:"model"`
	Requests  int64   `json:"requests"`
	TokensIn  int64   `json:"tokens_in"`
	TokensOut int64   `json:"tokens_out"`
	CostUSD   float64 `json:"cost_usd"`
}

func (s *Store) ensureModelUsageTable(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS usage_by_model (
		day TEXT NOT NULL,
		model TEXT NOT NULL,
		requests INTEGER NOT NULL DEFAULT 0,
		tokens_in INTEGER NOT NULL DEFAULT 0,
		tokens_out INTEGER NOT NULL DEFAULT 0,
		cost_usd REAL NOT NULL DEFAULT 0,
		PRIMARY KEY(day, model)
	)`)
	return err
}

// RecordModelUsage increments daily request, token, and cost counters for model.
func (s *Store) RecordModelUsage(ctx context.Context, day, model string, tokensIn, tokensOut int64, costUSD float64) error {
	if s == nil || s.db == nil {
		return nil
	}
	model = strings.TrimSpace(model)
	if model == "" {
		model = "unknown"
	}
	if err := s.ensureModelUsageTable(ctx); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO usage_by_model(day, model, requests, tokens_in, tokens_out, cost_usd)
		VALUES(?, ?, 1, ?, ?, ?)
		ON CONFLICT(day, model) DO UPDATE SET
			requests   = requests + 1,
			tokens_in  = tokens_in + excluded.tokens_in,
			tokens_out = tokens_out + excluded.tokens_out,
			cost_usd   = cost_usd + excluded.cost_usd`,
		day, model, tokensIn, tokensOut, costUSD)
	return err
}

// ModelUsageSince returns aggregated usage per model since the given date,
// ordered by total tokens descending.
func (s *Store) ModelUsageSince(ctx context.Context, since time.Time) ([]ModelUsageSummary, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if err := s.ensureModelUsageTable(ctx); err != nil {
		return nil, err
	}
	day := since.Format("2006-01-02")
	rows, err := s.db.QueryContext(ctx, `
		SELECT model, SUM(requests), SUM(tokens_in), SUM(tokens_out), SUM(cost_usd)
		  FROM usage_by_model
		 WHERE day >= ?
		 GROUP BY model
		 ORDER BY (SUM(tokens_in) + SUM(tokens_out)) DESC`, day)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ModelUsageSummary
	for rows.Next() {
		var m ModelUsageSummary
		if err := rows.Scan(&m.Model, &m.Requests, &m.TokensIn, &m.TokensOut, &m.CostUSD); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
