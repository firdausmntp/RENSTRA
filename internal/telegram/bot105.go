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
		if b.topicNames == nil {
			b.topicNames = map[int64]string{}
		}
		b.topicNames[turnScopeKey(chatID, res.MessageThreadID)] = trimmed
		b.mu.Unlock()
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
		if b.topicNames == nil {
			b.topicNames = map[int64]string{}
		}
		b.topicNames[turnScopeKey(chatID, threadID)] = trimmed
		b.mu.Unlock()
	}
	return err
}
