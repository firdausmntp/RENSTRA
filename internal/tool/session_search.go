package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"renstra/internal/provider"
	"renstra/internal/store"
)

// SessionSearch allows the agent to recall earlier conversation turns in the
// current chat, including turns already compacted out of the live context.
type SessionSearch struct {
	store *store.Store
}

func NewSessionSearch(s *store.Store) *SessionSearch {
	return &SessionSearch{store: s}
}

func (t *SessionSearch) Name() string { return "session_search" }

func (t *SessionSearch) Description() string {
	return "Mencari percakapan lama secara semantik/hybrid (BM25 + kemiripan kata) di chat ini atau seluruh sesi. Berguna untuk mengingat detail, konfigurasi, atau diskusi masa lalu yang sudah terpotong dari konteks aktif."
}

func (t *SessionSearch) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {
				"type": "string",
				"description": "Kata kunci, topik, atau deskripsi yang dicari"
			},
			"limit": {
				"type": "integer",
				"description": "Maksimal pesan yang dikembalikan (default 5, max 20)"
			},
			"all_chats": {
				"type": "boolean",
				"description": "Jika true, cari di seluruh sesi/topik dalam database, bukan hanya chat saat ini"
			}
		},
		"required": ["query"]
	}`)
}

func (t *SessionSearch) Danger() bool { return false }

func (t *SessionSearch) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	if t.store == nil {
		return "", fmt.Errorf("session_search: store tidak tersedia")
	}

	var in struct {
		Query    string `json:"query"`
		Limit    int    `json:"limit"`
		AllChats bool   `json:"all_chats"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", fmt.Errorf("argumen session_search tidak valid: %w", err)
	}

	chatID := ChatIDFrom(ctx)
	msgs, err := t.store.SearchMessagesRanked(ctx, chatID, in.Query, in.Limit, in.AllChats)
	if err != nil {
		return "", err
	}
	if len(msgs) == 0 {
		return fmt.Sprintf("tidak ditemukan percakapan dengan kata kunci %q", in.Query), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Ditemukan %d pesan terkait %q:\n\n", len(msgs), in.Query)
	for i, m := range msgs {
		role := "User"
		if m.Role == provider.RoleAssistant {
			role = "Assistant"
		}
		fmt.Fprintf(&b, "[%d] %s:\n%s\n\n", i+1, role, strings.TrimSpace(m.Content))
	}
	return strings.TrimRight(b.String(), "\n"), nil
}
