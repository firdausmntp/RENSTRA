package store

import (
	"context"
	"encoding/json"

	"renstra/internal/provider"
)

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

// Messages returns the live session: everything after the last session_break.
func (s *Store) Messages(ctx context.Context, chatID int64) ([]provider.Message, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT role, content, tool_calls_json, tool_call_id
		   FROM messages
		  WHERE chat_id = ?
		    AND id > COALESCE((SELECT MAX(id) FROM messages WHERE chat_id = ? AND role = 'session_break'), 0)
		  ORDER BY id`,
		chatID, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []provider.Message
	for rows.Next() {
		var m provider.Message
		var calls string
		if err := rows.Scan(&m.Role, &m.Content, &calls, &m.ToolCallID); err != nil {
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

func (s *Store) SessionBreak(ctx context.Context, chatID int64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO messages(chat_id, role) VALUES(?, 'session_break')`, chatID)
	return err
}
