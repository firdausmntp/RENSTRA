package telegram

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

// InlineButton represents a Telegram Bot API 9.4+ InlineKeyboardButton with
// optional visual style ("primary", "success", "danger").
type InlineButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
	Style        string `json:"style,omitempty"`
}

// SendMessageWithKeyboard sends a Markdown-formatted message with an inline keyboard.
func (b *Bot) SendMessageWithKeyboard(ctx context.Context, chatID int64, text string, rows [][]InlineButton) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("telegram: teks kosong")
	}
	kb := map[string]any{"inline_keyboard": rows}
	_, err := b.post(ctx, "sendMessage", map[string]any{
		"chat_id":      chatID,
		"text":         markdownToHTML(text),
		"parse_mode":   "HTML",
		"reply_markup": kb,
	})
	if isHTTP400(err) {
		_, err = b.post(ctx, "sendMessage", map[string]any{
			"chat_id":      chatID,
			"text":         text,
			"reply_markup": kb,
		})
	}
	return err
}

// EditMessageWithKeyboard edits an existing message and updates its inline keyboard.
func (b *Bot) EditMessageWithKeyboard(ctx context.Context, chatID, messageID int64, text string, rows [][]InlineButton) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("telegram: teks kosong")
	}
	kb := map[string]any{"inline_keyboard": rows}
	_, err := b.post(ctx, "editMessageText", map[string]any{
		"chat_id":      chatID,
		"message_id":   messageID,
		"text":         markdownToHTML(text),
		"parse_mode":   "HTML",
		"reply_markup": kb,
	})
	if err != nil {
		var ae *apiError
		if errors.As(err, &ae) {
			if strings.Contains(strings.ToLower(ae.description), "message is not modified") {
				return nil
			}
			if ae.status == http.StatusBadRequest {
				_, err = b.post(ctx, "editMessageText", map[string]any{
					"chat_id":      chatID,
					"message_id":   messageID,
					"text":         text,
					"reply_markup": kb,
				})
				if err != nil && errors.As(err, &ae) && strings.Contains(strings.ToLower(ae.description), "message is not modified") {
					return nil
				}
			}
		}
	}
	return err
}

func defaultQuickMenu() [][]InlineButton {
	return [][]InlineButton{
		{
			{Text: "🤖 Pilih Model", CallbackData: "cmd:model", Style: "primary"},
			{Text: "🔐 Mode Approval", CallbackData: "cmd:approval", Style: "primary"},
		},
		{
			{Text: "🛠 Skills", CallbackData: "cmd:skills", Style: "success"},
			{Text: "⏰ Cron", CallbackData: "cmd:cron", Style: "primary"},
			{Text: "⚙️ Config", CallbackData: "cmd:config", Style: "primary"},
			{Text: "📊 Status", CallbackData: "cmd:status", Style: "primary"},
		},
	}
}
