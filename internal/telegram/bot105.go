package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// BotFullInfo represents the Telegram Bot API 10.5 BotFullInfo object returned
// by getMe (split out from User in Bot API 10.5 with active_usernames,
// inline_query_placeholder, needs_inline_query_location, and has_topic_sidebar).
type BotFullInfo struct {
	ID                      int64    `json:"id"`
	IsBot                   bool     `json:"is_bot"`
	FirstName               string   `json:"first_name"`
	Username                string   `json:"username"`
	ActiveUsernames         []string `json:"active_usernames,omitempty"`
	CanJoinGroups           bool     `json:"can_join_groups"`
	CanReadAllGroupMessages bool     `json:"can_read_all_group_messages"`
	SupportsInlineQueries   bool     `json:"supports_inline_queries"`
	InlineQueryPlaceholder  string   `json:"inline_query_placeholder,omitempty"`
	NeedsInlineQueryLoc     bool     `json:"needs_inline_query_location,omitempty"`
	HasTopicsEnabled        bool     `json:"has_topics_enabled,omitempty"`
	HasTopicSidebar         bool     `json:"has_topic_sidebar,omitempty"`
}

// BotSettings describes the Telegram Bot API 10.5 setMySettings parameters,
// allowing the bot to configure group joining, privacy mode, private chat forum
// topics, topic sidebar, and callback query delivery mode directly from code.
type BotSettings struct {
	CanJoinGroups           *bool  `json:"can_join_groups,omitempty"`
	CanReadAllGroupMessages *bool  `json:"can_read_all_group_messages,omitempty"`
	HasTopicsEnabled        *bool  `json:"has_topics_enabled,omitempty"`
	HasTopicSidebar         *bool  `json:"has_topic_sidebar,omitempty"`
	CallbackQueryMode       string `json:"callback_query_mode,omitempty"`
}

func (s BotSettings) isEmpty() bool {
	return s.CanJoinGroups == nil &&
		s.CanReadAllGroupMessages == nil &&
		s.HasTopicsEnabled == nil &&
		s.HasTopicSidebar == nil &&
		strings.TrimSpace(s.CallbackQueryMode) == ""
}

// GetMe fetches the bot's BotFullInfo (Bot API 10.5) and caches its username
// for group @mention routing.
func (b *Bot) GetMe(ctx context.Context) (*BotFullInfo, error) {
	raw, err := b.post(ctx, "getMe", map[string]any{})
	if err != nil {
		return nil, err
	}
	var info BotFullInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return nil, fmt.Errorf("telegram: getMe tidak valid: %w", err)
	}
	if info.Username != "" {
		b.SetBotUsername(info.Username)
	}
	return &info, nil
}

// SetMySettings calls the Telegram Bot API 10.5 setMySettings method to update
// the bot's operational configuration (groups, privacy, topics, sidebar, callbacks).
func (b *Bot) SetMySettings(ctx context.Context, settings BotSettings) error {
	if settings.isEmpty() {
		return nil
	}
	payload := map[string]any{}
	if settings.CanJoinGroups != nil {
		payload["can_join_groups"] = *settings.CanJoinGroups
	}
	if settings.CanReadAllGroupMessages != nil {
		payload["can_read_all_group_messages"] = *settings.CanReadAllGroupMessages
	}
	if settings.HasTopicsEnabled != nil {
		payload["has_topics_enabled"] = *settings.HasTopicsEnabled
	}
	if settings.HasTopicSidebar != nil {
		payload["has_topic_sidebar"] = *settings.HasTopicSidebar
	}
	if strings.TrimSpace(settings.CallbackQueryMode) != "" {
		payload["callback_query_mode"] = strings.TrimSpace(settings.CallbackQueryMode)
	}
	_, err := b.post(ctx, "setMySettings", payload)
	return err
}

// SetBotUsername sets the bot's @username (without leading @) for group mention matching.
func (b *Bot) SetBotUsername(username string) {
	u := strings.TrimPrefix(strings.TrimSpace(username), "@")
	b.mu.Lock()
	b.botUsername = u
	b.mu.Unlock()
}

// BotUsername returns the cached bot username (without leading @).
func (b *Bot) BotUsername() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.botUsername
}

// SetUpdateFooter sets an optional version update notice appended to /start and /help.
func (b *Bot) SetUpdateFooter(footer string) {
	b.mu.Lock()
	b.updateFooter = strings.TrimSpace(footer)
	b.mu.Unlock()
}

// UpdateFooter returns the configured version update notice if any.
func (b *Bot) UpdateFooter() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.updateFooter
}

// CreateForumTopic creates a new forum topic in a supergroup chat and returns its message_thread_id.
func (b *Bot) CreateForumTopic(ctx context.Context, chatID int64, name string) (int64, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return 0, fmt.Errorf("nama topik tidak boleh kosong")
	}
	raw, err := b.post(ctx, "createForumTopic", map[string]any{
		"chat_id": chatID,
		"name":    trimmed,
	})
	if err != nil {
		return 0, err
	}
	var res struct {
		MessageThreadID int64 `json:"message_thread_id"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return 0, err
	}
	if res.MessageThreadID > 0 {
		b.mu.Lock()
		changed := b.recordTopicLocked(chatID, res.MessageThreadID, trimmed)
		b.mu.Unlock()
		if changed {
			b.notifyTopicsChanged(chatID)
		}
	}
	return res.MessageThreadID, nil
}

// EditForumTopic renames an existing forum topic in a supergroup chat.
func (b *Bot) EditForumTopic(ctx context.Context, chatID, threadID int64, name string) error {
	trimmed := strings.TrimSpace(name)
	if threadID <= 0 || trimmed == "" {
		return fmt.Errorf("thread_id dan nama topik baru wajib diisi")
	}
	_, err := b.post(ctx, "editForumTopic", map[string]any{
		"chat_id":           chatID,
		"message_thread_id": threadID,
		"name":              trimmed,
	})
	if err == nil {
		b.mu.Lock()
		changed := b.recordTopicLocked(chatID, threadID, trimmed)
		b.mu.Unlock()
		if changed {
			b.notifyTopicsChanged(chatID)
		}
	}
	return err
}

// CloseForumTopic closes an open forum topic in a supergroup chat.
func (b *Bot) CloseForumTopic(ctx context.Context, chatID, threadID int64) error {
	if threadID <= 0 {
		return fmt.Errorf("topic_id wajib > 0")
	}
	_, err := b.post(ctx, "closeForumTopic", map[string]any{
		"chat_id":           chatID,
		"message_thread_id": threadID,
	})
	return err
}

// ReopenForumTopic reopens a closed forum topic in a supergroup chat.
func (b *Bot) ReopenForumTopic(ctx context.Context, chatID, threadID int64) error {
	if threadID <= 0 {
		return fmt.Errorf("topic_id wajib > 0")
	}
	_, err := b.post(ctx, "reopenForumTopic", map[string]any{
		"chat_id":           chatID,
		"message_thread_id": threadID,
	})
	return err
}

// DeleteForumTopic permanently deletes a forum topic and all its messages.
func (b *Bot) DeleteForumTopic(ctx context.Context, chatID, threadID int64) error {
	if threadID <= 0 {
		return fmt.Errorf("topic_id wajib > 0")
	}
	_, err := b.post(ctx, "deleteForumTopic", map[string]any{
		"chat_id":           chatID,
		"message_thread_id": threadID,
	})
	if err == nil {
		b.mu.Lock()
		delete(b.topicNames, turnScopeKey(chatID, threadID))
		if b.topicsByChat != nil && b.topicsByChat[chatID] != nil {
			delete(b.topicsByChat[chatID], threadID)
		}
		b.mu.Unlock()
		b.notifyTopicsChanged(chatID)
	}
	return err
}

// ListKnownTopics returns a copy of known topic_id -> name entries observed or created in chatID.
func (b *Bot) ListKnownTopics(chatID int64) map[int64]string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[int64]string)
	for tid, name := range b.topicsByChat[chatID] {
		out[tid] = name
	}
	return out
}

// DeleteMessage deletes a message in a Telegram chat.
func (b *Bot) DeleteMessage(ctx context.Context, chatID, messageID int64) error {
	if chatID == 0 || messageID <= 0 {
		return fmt.Errorf("chat_id dan message_id wajib > 0")
	}
	_, err := b.post(ctx, "deleteMessage", map[string]any{
		"chat_id":    chatID,
		"message_id": messageID,
	})
	return err
}

// EditFormattedMessage edits an existing bot message using Markdown -> Telegram HTML formatting.
func (b *Bot) EditFormattedMessage(ctx context.Context, chatID, messageID int64, markdown string) error {
	if chatID == 0 || messageID <= 0 {
		return fmt.Errorf("chat_id dan message_id wajib > 0")
	}
	htmlText := markdownToHTML(markdown)
	if strings.TrimSpace(htmlText) == "" {
		return fmt.Errorf("teks edit tidak boleh kosong")
	}
	_, err := b.post(ctx, "editMessageText", map[string]any{
		"chat_id":    chatID,
		"message_id": messageID,
		"text":       htmlText,
		"parse_mode": "HTML",
	})
	if err != nil {
		_, err = b.post(ctx, "editMessageText", map[string]any{
			"chat_id":    chatID,
			"message_id": messageID,
			"text":       markdown,
		})
	}
	return err
}

// PinMessage pins a message in a Telegram chat.
func (b *Bot) PinMessage(ctx context.Context, chatID, messageID int64, disableNotification bool) error {
	if chatID == 0 || messageID <= 0 {
		return fmt.Errorf("chat_id dan message_id wajib > 0")
	}
	_, err := b.post(ctx, "pinChatMessage", map[string]any{
		"chat_id":              chatID,
		"message_id":           messageID,
		"disable_notification": disableNotification,
	})
	return err
}

// UnpinMessage unpins a specific message (or the latest pinned message when messageID <= 0).
func (b *Bot) UnpinMessage(ctx context.Context, chatID, messageID int64) error {
	if chatID == 0 {
		return fmt.Errorf("chat_id wajib diisi")
	}
	payload := map[string]any{"chat_id": chatID}
	if messageID > 0 {
		payload["message_id"] = messageID
	}
	_, err := b.post(ctx, "unpinChatMessage", payload)
	return err
}

// SeedTopics preloads persisted forum topics from SQLite on startup.
func (b *Bot) SeedTopics(all map[int64]map[int64]string) {
	if len(all) == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for cid, topics := range all {
		for tid, name := range topics {
			b.recordTopicLocked(cid, tid, name)
		}
	}
}

func (b *Bot) notifyTopicsChanged(chatID int64) {
	if b.onTopicsChanged == nil || chatID == 0 {
		return
	}
	b.onTopicsChanged(chatID, b.ListKnownTopics(chatID))
}

func (b *Bot) recordTopicLocked(chatID, threadID int64, name string) bool {
	if threadID <= 0 {
		return false
	}
	if b.topicNames == nil {
		b.topicNames = map[int64]string{}
	}
	if b.topicsByChat == nil {
		b.topicsByChat = map[int64]map[int64]string{}
	}
	if b.topicsByChat[chatID] == nil {
		b.topicsByChat[chatID] = map[int64]string{}
	}
	if name != "" {
		prev, exists := b.topicsByChat[chatID][threadID]
		b.topicNames[turnScopeKey(chatID, threadID)] = name
		b.topicsByChat[chatID][threadID] = name
		return !exists || prev != name
	}
	if _, exists := b.topicsByChat[chatID][threadID]; !exists {
		b.topicsByChat[chatID][threadID] = b.topicNames[turnScopeKey(chatID, threadID)]
		return true
	}
	return false
}
