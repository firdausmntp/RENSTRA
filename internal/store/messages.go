package store

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"renstra/internal/provider"
)

// storedMessage keeps the row id so compaction can mark the exact cut point.
type storedMessage struct {
	ID int64
	provider.Message
}

func (s *Store) AppendMessage(ctx context.Context, chatID int64, m provider.Message) (int64, error) {
	calls := ""
	if len(m.ToolCalls) > 0 {
		raw, err := json.Marshal(m.ToolCalls)
		if err != nil {
			return 0, err
		}
		calls = string(raw)
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO messages(chat_id, role, content, tool_calls_json, tool_call_id) VALUES(?,?,?,?,?)`,
		chatID, string(m.Role), m.Content, calls, m.ToolCallID)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// liveMessages returns the session rows that are still verbatim: everything
// after the last session_break and not yet compacted.
func (s *Store) liveMessages(ctx context.Context, chatID int64) ([]storedMessage, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, role, content, tool_calls_json, tool_call_id
		   FROM messages
		  WHERE chat_id = ?
		    AND compacted_at IS NULL
		    AND id > COALESCE((SELECT MAX(id) FROM messages WHERE chat_id = ? AND role = 'session_break'), 0)
		  ORDER BY id`,
		chatID, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []storedMessage
	for rows.Next() {
		var m storedMessage
		var calls string
		if err := rows.Scan(&m.ID, &m.Role, &m.Content, &calls, &m.ToolCallID); err != nil {
			return nil, err
		}
		if calls != "" {
			if err := json.Unmarshal([]byte(calls), &m.ToolCalls); err != nil {
				return nil, err
			}
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Messages returns the live session: the persisted compaction summary (if any)
// followed by the rows that are still verbatim.
func (s *Store) Messages(ctx context.Context, chatID int64) ([]provider.Message, error) {
	live, err := s.liveMessages(ctx, chatID)
	if err != nil {
		return nil, err
	}
	out := make([]provider.Message, 0, len(live)+1)
	if summary := s.Summary(ctx, chatID); summary != "" {
		out = append(out, provider.Message{Role: provider.RoleSystem, Content: summary})
	}
	for _, m := range live {
		out = append(out, m.Message)
	}
	return out, nil
}

func (s *Store) summaryKey(chatID int64) string {
	return "summary:" + strconv.FormatInt(chatID, 10)
}

// Summary returns the compaction summary for a chat, or "" when none exists.
func (s *Store) Summary(ctx context.Context, chatID int64) string {
	var v string
	if err := s.db.QueryRowContext(ctx,
		`SELECT value FROM kv WHERE key = ?`, s.summaryKey(chatID)).Scan(&v); err != nil {
		return ""
	}
	return v
}

// Compact persists a compaction: rows before the cut are marked compacted_at
// (never deleted) and the summary is stored so the next turn reloads a bounded
// history instead of the whole transcript.
func (s *Store) Compact(ctx context.Context, chatID int64, keep int, summary string) ([]provider.Message, error) {
	live, err := s.liveMessages(ctx, chatID)
	if err != nil {
		return nil, err
	}
	if len(live) <= keep {
		return s.Messages(ctx, chatID)
	}

	cut := len(live) - keep
	// Never split an assistant(tool_calls) group from its tool results.
	for cut > 0 && live[cut].Role != provider.RoleUser {
		cut--
	}
	if cut <= 0 {
		return s.Messages(ctx, chatID)
	}

	if _, err := s.db.ExecContext(ctx,
		`UPDATE messages SET compacted_at = datetime('now')
		  WHERE chat_id = ? AND id < ? AND compacted_at IS NULL`,
		chatID, live[cut].ID); err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO kv(key, value) VALUES(?, ?)`,
		s.summaryKey(chatID), summary); err != nil {
		return nil, err
	}
	return s.Messages(ctx, chatID)
}

// SessionBreak starts a new session and drops the old compaction summary.
func (s *Store) SessionBreak(ctx context.Context, chatID int64) error {
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO messages(chat_id, role) VALUES(?, 'session_break')`, chatID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM kv WHERE key = ?`, s.summaryKey(chatID))
	return err
}

// SearchMessages finds past user/assistant messages in one chat by keyword.
// It searches the full transcript, including rows already compacted out of the
// live context, so the agent can recall details that compression dropped.
func (s *Store) SearchMessages(ctx context.Context, chatID int64, query string, limit int) ([]provider.Message, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, errors.New("query pencarian kosong")
	}
	if limit <= 0 || limit > 50 {
		limit = 10
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT role, content FROM messages
		  WHERE chat_id = ? AND role IN ('user','assistant')
		    AND content LIKE ? ESCAPE '\'
		  ORDER BY id DESC LIMIT ?`,
		chatID, "%"+escapeLike(q)+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []provider.Message
	for rows.Next() {
		var m provider.Message
		if err := rows.Scan(&m.Role, &m.Content); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Oldest first reads more naturally in the tool result.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// escapeLike neutralizes LIKE wildcards so a query containing % or _ matches
// literally instead of scanning everything.
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}
