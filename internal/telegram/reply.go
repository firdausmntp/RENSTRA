package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type replyTargetKey struct{}

type replyTarget struct {
	replyToID int64
	threadID  int64
}

func withReplyTarget(ctx context.Context, replyToID, threadID int64) context.Context {
	if replyToID <= 0 && threadID <= 0 {
		return ctx
	}
	return context.WithValue(ctx, replyTargetKey{}, replyTarget{replyToID: replyToID, threadID: threadID})
}

func replyTargetFrom(ctx context.Context) replyTarget {
	if ctx == nil {
		return replyTarget{}
	}
	rt, _ := ctx.Value(replyTargetKey{}).(replyTarget)
	return rt
}

// TopicIDFrom returns the active forum topic message_thread_id from ctx (0 if none).
func TopicIDFrom(ctx context.Context) int64 {
	return replyTargetFrom(ctx).threadID
}

// WithTopic attaches a forum topic message_thread_id to ctx.
func WithTopic(ctx context.Context, threadID int64) context.Context {
	rt := replyTargetFrom(ctx)
	return withReplyTarget(ctx, rt.replyToID, threadID)
}

// SessionChatID returns an isolated session key per (chatID, threadID) when inside
// a forum topic so different topics in the same group keep separate histories.
func SessionChatID(ctx context.Context, chatID int64) int64 {
	return turnScopeKey(chatID, TopicIDFrom(ctx))
}

func applyThreadOnly(ctx context.Context, payload map[string]any) {
	rt := replyTargetFrom(ctx)
	if rt.threadID > 0 {
		payload["message_thread_id"] = rt.threadID
	}
}

func applyReplyTarget(ctx context.Context, payload map[string]any) {
	rt := replyTargetFrom(ctx)
	if rt.threadID > 0 {
		payload["message_thread_id"] = rt.threadID
	}
	if rt.replyToID > 0 {
		payload["reply_parameters"] = map[string]any{
			"message_id":                rt.replyToID,
			"allow_sending_without_reply": true,
		}
	}
}

func applyReplyFields(ctx context.Context, fields map[string]string) {
	rt := replyTargetFrom(ctx)
	if rt.threadID > 0 {
		fields["message_thread_id"] = fmt.Sprintf("%d", rt.threadID)
	}
	if rt.replyToID > 0 {
		rp, err := json.Marshal(map[string]any{
			"message_id":                rt.replyToID,
			"allow_sending_without_reply": true,
		})
		if err == nil {
			fields["reply_parameters"] = string(rp)
		}
	}
}

func (b *Bot) trackTopicName(m message) string {
	threadID := m.MessageThreadID
	var found string
	if m.ForumTopicCreated != nil && strings.TrimSpace(m.ForumTopicCreated.Name) != "" {
		found = strings.TrimSpace(m.ForumTopicCreated.Name)
	} else if m.ForumTopicEdited != nil && strings.TrimSpace(m.ForumTopicEdited.Name) != "" {
		found = strings.TrimSpace(m.ForumTopicEdited.Name)
	} else if m.ReplyToMessage != nil && m.ReplyToMessage.ForumTopicCreated != nil && strings.TrimSpace(m.ReplyToMessage.ForumTopicCreated.Name) != "" {
		found = strings.TrimSpace(m.ReplyToMessage.ForumTopicCreated.Name)
	}
	if threadID <= 0 {
		return found
	}
	key := turnScopeKey(m.Chat.ID, threadID)
	b.mu.Lock()
	if b.topicNames == nil {
		b.topicNames = map[int64]string{}
	}
	if found != "" {
		b.topicNames[key] = found
	} else {
		found = b.topicNames[key]
	}
	b.mu.Unlock()
	return found
}

func (b *Bot) appendReplyContext(ctx, turnCtx context.Context, m message, text string) (string, bool) {
	topicName := b.trackTopicName(m)

	var quoteText string
	isTopicRootReply := m.ReplyToMessage != nil &&
		m.ReplyToMessage.ForumTopicCreated != nil &&
		strings.TrimSpace(m.ReplyToMessage.Text) == "" &&
		strings.TrimSpace(m.ReplyToMessage.Caption) == ""

	if m.Quote != nil && strings.TrimSpace(m.Quote.Text) != "" {
		quoteText = truncate(strings.TrimSpace(m.Quote.Text), 1500)
	} else if m.ReplyToMessage != nil && !isTopicRootReply {
		qt := m.ReplyToMessage.Text
		if qt == "" {
			qt = m.ReplyToMessage.Caption
		}
		if strings.TrimSpace(qt) != "" {
			quoteText = truncate(strings.TrimSpace(qt), 1500)
		}
	}

	// Special UX: replying with `/remember` (no args) saves the quoted message directly.
	if strings.EqualFold(strings.TrimSpace(text), "/remember") && quoteText != "" {
		return "/remember " + quoteText, true
	}
	// Do not inject context prefix into other slash commands so `/model`, `/status`, etc. parse cleanly.
	if command(text) != "" {
		return text, true
	}

	var prefixParts []string
	var meta []string
	if m.Chat.Type == "group" || m.Chat.Type == "supergroup" {
		if strings.TrimSpace(m.Chat.Title) != "" {
			meta = append(meta, fmt.Sprintf("Grup: %q", strings.TrimSpace(m.Chat.Title)))
		} else {
			meta = append(meta, fmt.Sprintf("Grup ID: %d", m.Chat.ID))
		}
	}
	if topicName != "" {
		meta = append(meta, fmt.Sprintf("Topik: %q (#%d)", topicName, m.MessageThreadID))
	} else if m.IsTopicMessage && m.MessageThreadID > 0 {
		meta = append(meta, fmt.Sprintf("Topik #%d", m.MessageThreadID))
	}
	if (m.Chat.Type == "group" || m.Chat.Type == "supergroup") && m.From != nil {
		sender := strings.TrimSpace(m.From.FirstName)
		if m.From.Username != "" {
			if sender != "" {
				sender += " (@" + m.From.Username + ")"
			} else {
				sender = "@" + m.From.Username
			}
		}
		if sender != "" {
			meta = append(meta, "Pengirim: "+sender)
		}
	}
	if len(meta) > 0 {
		prefixParts = append(prefixParts, "["+strings.Join(meta, " | ")+"]")
	}

	if quoteText != "" {
		prefixParts = append(prefixParts, fmt.Sprintf("[Membalas pesan: %q]", quoteText))
	}

	if m.ReplyToMessage != nil && !isTopicRootReply && hasMsgAttachment(*m.ReplyToMessage) {
		attText, ok := b.prepareAttachmentText(ctx, turnCtx, *m.ReplyToMessage, "")
		if !ok {
			return "", false
		}
		if strings.TrimSpace(attText) != "" {
			prefixParts = append(prefixParts, attText)
		}
	}

	if len(prefixParts) == 0 {
		return text, true
	}
	prefix := strings.Join(prefixParts, "\n")
	if strings.TrimSpace(text) != "" {
		return prefix + "\n" + text, true
	}
	return prefix, true
}

func hasMsgAttachment(m message) bool {
	return m.Document != nil || len(m.Photo) > 0 || m.Voice != nil || m.Audio != nil
}
