package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// TelegramMessageSender delivers formatted Markdown messages to a Telegram chat
// or specific forum topic thread.
type TelegramMessageSender interface {
	SendToTopic(ctx context.Context, chatID, topicID int64, markdown string) error
}

// SendTelegram sends a formatted Markdown message to a Telegram chat or forum topic.
type SendTelegram struct {
	sender TelegramMessageSender
}

func NewSendTelegram(sender TelegramMessageSender) *SendTelegram {
	return &SendTelegram{sender: sender}
}

func (t *SendTelegram) Name() string { return "send_telegram" }

func (t *SendTelegram) Description() string {
	return "Mengirim pesan Markdown terformat (bold, code, blockquote >) ke chat atau topik forum Telegram tertentu (termasuk topik lain dalam grup yang sama)."
}

func (t *SendTelegram) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"chat_id":{"type":"integer","description":"ID chat/grup tujuan (opsional, default chat saat ini)"},"topic_id":{"type":"integer","description":"ID topik forum (message_thread_id) tujuan (0 atau kosong untuk chat utama)"},"text":{"type":"string","description":"isi pesan dalam format Markdown Telegram"}},"required":["text"]}`)
}

func (t *SendTelegram) Danger() bool { return false }

func (t *SendTelegram) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	if t.sender == nil {
		return "", errors.New("send_telegram: pengirim pesan Telegram tidak tersedia")
	}
	var in struct {
		ChatID  int64  `json:"chat_id"`
		TopicID int64  `json:"topic_id"`
		Text    string `json:"text"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", fmt.Errorf("argumen send_telegram tidak valid: %w", err)
	}
	if strings.TrimSpace(in.Text) == "" {
		return "", errors.New("text wajib diisi")
	}
	targetChat := in.ChatID
	if targetChat == 0 {
		targetChat = ChatIDFrom(ctx)
	}
	if targetChat == 0 {
		return "", errors.New("send_telegram hanya tersedia di sesi chat Telegram atau perlu chat_id")
	}
	var msgID int64
	if idSender, ok := t.sender.(interface {
		SendToTopicReturningID(ctx context.Context, chatID, topicID int64, markdown string) (int64, error)
	}); ok {
		id, err := idSender.SendToTopicReturningID(ctx, targetChat, in.TopicID, in.Text)
		if err != nil {
			return "", err
		}
		msgID = id
		MarkChatDelivered(ctx, targetChat)
	} else if err := t.sender.SendToTopic(ctx, targetChat, in.TopicID, in.Text); err != nil {
		return "", err
	} else {
		MarkChatDelivered(ctx, targetChat)
	}
	if in.TopicID > 0 {
		if msgID > 0 {
			return fmt.Sprintf("pesan terkirim ke chat %d topik %d (message_id=%d)", targetChat, in.TopicID, msgID), nil
		}
		return fmt.Sprintf("pesan terkirim ke chat %d topik %d", targetChat, in.TopicID), nil
	}
	if msgID > 0 {
		return fmt.Sprintf("pesan terkirim ke chat %d (message_id=%d)", targetChat, msgID), nil
	}
	return fmt.Sprintf("pesan terkirim ke chat %d", targetChat), nil
}
