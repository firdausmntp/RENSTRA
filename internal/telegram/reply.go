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

func (b *Bot) appendReplyContext(ctx, turnCtx context.Context, m message, text string) (string, bool) {
	var quoteText string
	if m.Quote != nil && strings.TrimSpace(m.Quote.Text) != "" {
		quoteText = truncate(strings.TrimSpace(m.Quote.Text), 1500)
	} else if m.ReplyToMessage != nil {
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

	var prefixParts []string
	if quoteText != "" {
		prefixParts = append(prefixParts, fmt.Sprintf("[Membalas pesan: %q]", quoteText))
	}

	if m.ReplyToMessage != nil && hasMsgAttachment(*m.ReplyToMessage) {
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
