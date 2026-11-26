package store

import (
	"context"
	"errors"
	"math"
	"sort"
	"strings"
	"time"
)

// MemoryFact represents a scoped long-term episodic fact stored in SQLite.
type MemoryFact struct {
	ID        int64  `json:"id"`
	Scope     string `json:"scope"`
	Tags      string `json:"tags"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func (s *Store) ensureMemoryFactsTable(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS memory_facts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		scope TEXT NOT NULL DEFAULT 'global',
		tags TEXT NOT NULL DEFAULT '',
		content TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`)
	return err
}

// SaveMemoryFact stores or updates a durable episodic fact under scope ("global" or "chat:<id>").
func (s *Store) SaveMemoryFact(ctx context.Context, scope, tags, content string) (int64, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("store tidak tersedia")
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return 0, errors.New("konten memori kosong")
	}
	scope = strings.TrimSpace(scope)
	if scope == "" {
		scope = "global"
	}
	tags = strings.TrimSpace(tags)
	if err := s.ensureMemoryFactsTable(ctx); err != nil {
		return 0, err
	}
	now := time.Now().Format(time.RFC3339)

	var existingID int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM memory_facts WHERE scope = ? AND lower(content) = lower(?) LIMIT 1`,
		scope, content).Scan(&existingID)
	if err == nil && existingID > 0 {
		_, err = s.db.ExecContext(ctx,
			`UPDATE memory_facts SET tags = CASE WHEN ? != '' THEN ? ELSE tags END, updated_at = ? WHERE id = ?`,
			tags, tags, now, existingID)
		return existingID, err
	}

	res, err := s.db.ExecContext(ctx,
		`INSERT INTO memory_facts(scope, tags, content, created_at, updated_at) VALUES(?, ?, ?, ?, ?)`,
		scope, tags, content, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// SearchMemoryFacts retrieves top-ranked facts across "global" and optional chat scope
// using BM25 + character trigram similarity.
func (s *Store) SearchMemoryFacts(ctx context.Context, scope, query string, limit int) ([]MemoryFact, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if err := s.ensureMemoryFactsTable(ctx); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 30 {
		limit = 5
	}
	q := strings.TrimSpace(query)
	if q == "" {
		return s.ListMemoryFacts(ctx, scope, limit)
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, scope, tags, content, created_at, updated_at
		   FROM memory_facts
		  WHERE scope = 'global' OR scope = ?
		  ORDER BY id DESC LIMIT 1000`, scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type factCand struct {
		fact  MemoryFact
		score float64
	}
	qLower := strings.ToLower(q)
	qTokens := tokenizeText(q)
	qGrams := charTrigrams(q)
	var hits []factCand
	for rows.Next() {
		var f MemoryFact
		if err := rows.Scan(&f.ID, &f.Scope, &f.Tags, &f.Content, &f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, err
		}
		hay := strings.TrimSpace(f.Tags + " " + f.Content)
		hayLower := strings.ToLower(hay)
		var score float64
		if strings.Contains(hayLower, qLower) {
			score += 3.5
		}
		dTokens := tokenizeText(hay)
		tf := make(map[string]int, len(dTokens))
		for _, dt := range dTokens {
			tf[dt]++
		}
		for _, qt := range qTokens {
			if tf[qt] > 0 {
				score += 1.2 + 0.3*float64(tf[qt])
			} else if len(qt) >= 4 {
				for dt := range tf {
					if len(dt) >= 4 && (strings.Contains(dt, qt) || strings.Contains(qt, dt)) {
						score += 0.45
						break
					}
				}
			}
		}
		if sim := trigramJaccard(qGrams, charTrigrams(hay)); sim >= 0.08 {
			score += sim * 2.5
		}
		if f.Scope == scope && scope != "global" && score > 0 {
			score += 0.35 // Prefer chat/topic-specific lessons slightly when matched.
		}
		if score > 0.4 {
			hits = append(hits, factCand{fact: f, score: score})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.Slice(hits, func(i, j int) bool {
		if math.Abs(hits[i].score-hits[j].score) > 1e-6 {
			return hits[i].score > hits[j].score
		}
		return hits[i].fact.ID > hits[j].fact.ID
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]MemoryFact, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.fact)
	}
	return out, nil
}

// ListMemoryFacts returns the newest stored facts for global + optional scope.
func (s *Store) ListMemoryFacts(ctx context.Context, scope string, limit int) ([]MemoryFact, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if err := s.ensureMemoryFactsTable(ctx); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, scope, tags, content, created_at, updated_at
		   FROM memory_facts
		  WHERE ? = '' OR scope = 'global' OR scope = ?
		  ORDER BY id DESC LIMIT ?`, scope, scope, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MemoryFact
	for rows.Next() {
		var f MemoryFact
		if err := rows.Scan(&f.ID, &f.Scope, &f.Tags, &f.Content, &f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// DeleteMemoryFact deletes a single episodic fact by ID.
func (s *Store) DeleteMemoryFact(ctx context.Context, id int64) error {
	if s == nil || s.db == nil || id <= 0 {
		return errors.New("id memori tidak valid")
	}
	if err := s.ensureMemoryFactsTable(ctx); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM memory_facts WHERE id = ?`, id)
	return err
}
