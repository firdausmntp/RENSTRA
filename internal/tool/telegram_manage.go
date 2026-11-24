package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// TelegramManager exposes Telegram Bot API group/topic/message management actions.
type TelegramManager interface {
	CreateForumTopic(ctx context.Context, chatID int64, name string) (int64, error)
	EditForumTopic(ctx context.Context, chatID, threadID int64, name string) error
	CloseForumTopic(ctx context.Context, chatID, threadID int64) error
	ReopenForumTopic(ctx context.Context, chatID, threadID int64) error
	DeleteForumTopic(ctx context.Context, chatID, threadID int64) error
	ListKnownTopics(chatID int64) map[int64]string
	DeleteMessage(ctx context.Context, chatID, messageID int64) error
	EditFormattedMessage(ctx context.Context, chatID, messageID int64, markdown string) error
	PinMessage(ctx context.Context, chatID, messageID int64, disableNotification bool) error
	UnpinMessage(ctx context.Context, chatID, messageID int64) error
	ReactMessage(ctx context.Context, chatID, messageID int64, emoji string) error
}

// TelegramManage lets the agent create, rename, close, reopen, delete, and list
// forum topics, as well as edit, delete, pin, and unpin messages in Telegram.
type TelegramManage struct {
	mgr TelegramManager
}

func NewTelegramManage(mgr TelegramManager) *TelegramManage {
	return &TelegramManage{mgr: mgr}
}

func (t *TelegramManage) Name() string { return "telegram_manage" }

func (t *TelegramManage) Description() string {
	return "Kelola topik forum dan pesan Telegram: list_topics, create_topic, rename_topic, close_topic, reopen_topic, delete_topic, delete_message, edit_message, pin_message, unpin_message, react_message."
}

func (t *TelegramManage) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["list_topics","create_topic","rename_topic","close_topic","reopen_topic","delete_topic","delete_message","edit_message","pin_message","unpin_message","react_message"]},"chat_id":{"type":"integer","description":"ID chat/grup tujuan (opsional, default chat saat ini)"},"topic_id":{"type":"integer","description":"ID topik forum (message_thread_id)"},"message_id":{"type":"integer","description":"ID pesan target"},"name":{"type":"string","description":"nama topik baru"},"text":{"type":"string","description":"teks Markdown baru untuk edit_message"},"emoji":{"type":"string","description":"emoji reaksi untuk react_message (mis. 👍, 🔥, ❤️, 👀)"},"silent":{"type":"boolean","description":"pin tanpa notifikasi"}},"required":["action"]}`)
}

func (t *TelegramManage) Danger() bool { return false }

func (t *TelegramManage) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	if t.mgr == nil {
		return "", errors.New("telegram_manage: client Telegram tidak tersedia")
	}
	var in struct {
		Action    string `json:"action"`
		ChatID    int64  `json:"chat_id"`
		TopicID   int64  `json:"topic_id"`
		MessageID int64  `json:"message_id"`
		Name      string `json:"name"`
		Text      string `json:"text"`
		Emoji     string `json:"emoji"`
		Silent    bool   `json:"silent"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", fmt.Errorf("argumen telegram_manage tidak valid: %w", err)
	}
	targetChat := in.ChatID
	if targetChat == 0 {
		targetChat = ChatIDFrom(ctx)
	}
	if targetChat == 0 {
		return "", errors.New("telegram_manage hanya tersedia di sesi Telegram atau perlu chat_id")
	}

	switch strings.TrimSpace(in.Action) {
	case "list_topics":
		topics := t.mgr.ListKnownTopics(targetChat)
		if len(topics) == 0 {
			return fmt.Sprintf("belum ada topik tercatat di chat %d (kirim/buat pesan di topik dulu agar ID tercatat; Bot API tidak menyediakan daftar topik historis sebelum bot melihatnya)", targetChat), nil
		}
		ids := make([]int64, 0, len(topics))
		for id := range topics {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		var lines []string
		for _, id := range ids {
			name := topics[id]
			if name == "" {
				name = "(tanpa nama tercatat)"
			}
			lines = append(lines, fmt.Sprintf("- topic_id=%d: %s", id, name))
		}
		return strings.Join(lines, "\n"), nil
	case "create_topic":
		tid, err := t.mgr.CreateForumTopic(ctx, targetChat, in.Name)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("topik dibuat: topic_id=%d name=%q", tid, strings.TrimSpace(in.Name)), nil
	case "rename_topic":
		if err := t.mgr.EditForumTopic(ctx, targetChat, in.TopicID, in.Name); err != nil {
			return "", err
		}
		return fmt.Sprintf("topik %d diubah menjadi %q", in.TopicID, strings.TrimSpace(in.Name)), nil
	case "close_topic":
		if err := t.mgr.CloseForumTopic(ctx, targetChat, in.TopicID); err != nil {
			return "", err
		}
		return fmt.Sprintf("topik %d ditutup", in.TopicID), nil
	case "reopen_topic":
		if err := t.mgr.ReopenForumTopic(ctx, targetChat, in.TopicID); err != nil {
			return "", err
		}
		return fmt.Sprintf("topik %d dibuka kembali", in.TopicID), nil
	case "delete_topic":
		if err := t.mgr.DeleteForumTopic(ctx, targetChat, in.TopicID); err != nil {
			return "", err
		}
		return fmt.Sprintf("topik %d dihapus", in.TopicID), nil
	case "delete_message":
		if err := t.mgr.DeleteMessage(ctx, targetChat, in.MessageID); err != nil {
			return "", err
		}
		return fmt.Sprintf("pesan %d dihapus", in.MessageID), nil
	case "edit_message":
		if err := t.mgr.EditFormattedMessage(ctx, targetChat, in.MessageID, in.Text); err != nil {
			return "", err
		}
		return fmt.Sprintf("pesan %d diperbarui", in.MessageID), nil
	case "pin_message":
		if err := t.mgr.PinMessage(ctx, targetChat, in.MessageID, in.Silent); err != nil {
			return "", err
		}
		return fmt.Sprintf("pesan %d di-pin", in.MessageID), nil
	case "unpin_message":
		if err := t.mgr.UnpinMessage(ctx, targetChat, in.MessageID); err != nil {
			return "", err
		}
		return fmt.Sprintf("pesan %d di-unpin", in.MessageID), nil
	case "react_message":
		if err := t.mgr.ReactMessage(ctx, targetChat, in.MessageID, in.Emoji); err != nil {
			return "", err
		}
		return fmt.Sprintf("reaksi %q dikirim ke pesan %d", strings.TrimSpace(in.Emoji), in.MessageID), nil
	default:
		return "", fmt.Errorf("action tidak dikenal: %q", in.Action)
	}
}
