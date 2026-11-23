package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"strings"
	"time"
)

func (b *Bot) handleCallback(ctx context.Context, cq callbackQuery) {
	b.answerCallback(ctx, cq.ID)

	if cq.Message == nil {
		return
	}
	chatID := cq.Message.Chat.ID
	if len(b.allowed) > 0 && cq.From.ID != 0 && !b.isChatOrUserAllowed(chatID, &cq.From) {
		return
	}
	ctx = withReplyTarget(ctx, cq.Message.MessageID, cq.Message.MessageThreadID)

	action, id, ok := parseCallback(cq.Data)
	if !ok || (action != "approve" && action != "deny" && action != "auto") {
		if b.onCallback != nil {
			b.onCallback(ctx, chatID, cq.Message.MessageID, cq.Data)
		}
		return
	}

	b.mu.Lock()
	p := b.pending[id]
	var msgID int64
	var toolName string
	if p != nil && p.chatID == chatID {
		msgID = p.msgID
		toolName = p.tool
	}
	b.mu.Unlock()
	if p == nil || p.chatID != chatID {
		return
	}
	approved := action == "approve" || action == "auto"
	if action == "auto" && b.onAutoApprove != nil {
		b.onAutoApprove(chatID)
	}
	select {
	case p.ch <- approved:
	default:
	}
	if msgID > 0 {
		var status string
		switch action {
		case "auto":
			status = fmt.Sprintf("⚡ **Disetujui & Auto-Approve aktif** (`%s`)\n_Gunakan /approval prompt untuk kembali meminta konfirmasi._", toolName)
		case "approve":
			status = fmt.Sprintf("✅ **Disetujui:** `%s`", toolName)
		default:
			status = fmt.Sprintf("❌ **Ditolak:** `%s`", toolName)
		}
		_ = b.EditMessage(ctx, chatID, msgID, status)
	}
}

func (b *Bot) answerCallback(ctx context.Context, id string) {
	_, _ = b.post(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": id})
}

// AskApproval sends an inline keyboard and waits for the matching callback.
// A timeout is a deny, not an error.
func (b *Bot) AskApproval(ctx context.Context, chatID int64, tool, args string, timeout time.Duration) (bool, error) {
	if timeout <= 0 {
		timeout = approvalTimeout
	}

	b.mu.Lock()
	b.approvalSeq++
	id := b.approvalSeq
	p := &pendingApproval{id: id, chatID: chatID, tool: tool, ch: make(chan bool, 1)}
	b.pending[id] = p
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		if b.pending[id] == p {
			delete(b.pending, id)
		}
		b.mu.Unlock()
	}()

	htmlText, plainText := formatApprovalMessage(tool, args, timeout)
	kb := map[string]any{
		"inline_keyboard": [][]InlineButton{
			{
				{Text: "✅ Setuju", CallbackData: fmt.Sprintf("approve:%d", id), Style: "success"},
				{Text: "❌ Tolak", CallbackData: fmt.Sprintf("deny:%d", id), Style: "danger"},
			},
			{
				{Text: "⚡ Auto-Approve Chat Ini", CallbackData: fmt.Sprintf("auto:%d", id), Style: "primary"},
			},
		},
	}
	payload := map[string]any{
		"chat_id":      chatID,
		"text":         htmlText,
		"parse_mode":   "HTML",
		"reply_markup": kb,
	}
	applyReplyTarget(ctx, payload)
	raw, err := b.post(ctx, "sendMessage", payload)
	if isHTTP400(err) {
		plain := map[string]any{
			"chat_id":      chatID,
			"text":         plainText,
			"reply_markup": kb,
		}
		applyReplyTarget(ctx, plain)
		raw, err = b.post(ctx, "sendMessage", plain)
	}
	if err != nil {
		return false, err
	}
	var sent struct {
		MessageID int64 `json:"message_id"`
	}
	if len(raw) > 0 && json.Unmarshal(raw, &sent) == nil && sent.MessageID > 0 {
		b.mu.Lock()
		p.msgID = sent.MessageID
		b.mu.Unlock()
	}

	editOutcome := func(status string) {
		if sent.MessageID <= 0 {
			return
		}
		saveCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = b.EditMessage(saveCtx, chatID, sent.MessageID, status)
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case v := <-p.ch:
		return v, nil
	case <-timer.C:
		editOutcome(fmt.Sprintf("⌛ **Approval kedaluwarsa:** `%s`", tool))
		return false, nil
	case <-ctx.Done():
		editOutcome(fmt.Sprintf("🛑 **Approval dibatalkan:** `%s`", tool))
		return false, ctx.Err()
	}
}

func formatApprovalMessage(tool, args string, timeout time.Duration) (htmlText, plainText string) {
	secs := int(timeout.Seconds())
	if secs <= 0 {
		secs = int(approvalTimeout.Seconds())
	}

	var cmdBody string
	var lang string
	trimmed := strings.TrimSpace(args)
	if trimmed != "" {
		if tool == "terminal" {
			var payload struct {
				Cmd string `json:"cmd"`
			}
			if json.Unmarshal([]byte(trimmed), &payload) == nil && strings.TrimSpace(payload.Cmd) != "" {
				cmdBody = truncate(strings.TrimSpace(payload.Cmd), 1500)
				lang = "bash"
			}
		}
		if cmdBody == "" {
			var buf bytes.Buffer
			if json.Indent(&buf, []byte(trimmed), "", "  ") == nil {
				cmdBody = truncate(buf.String(), 1500)
				lang = "json"
			} else {
				cmdBody = truncate(trimmed, 1500)
			}
		}
	}

	var hb strings.Builder
	fmt.Fprintf(&hb, "🔐 <b>Konfirmasi Eksekusi</b>\n<b>Tool:</b> <code>%s</code>", html.EscapeString(tool))
	if cmdBody != "" {
		if lang != "" {
			fmt.Fprintf(&hb, "\n\n<pre><code class=\"language-%s\">%s</code></pre>", lang, html.EscapeString(cmdBody))
		} else {
			fmt.Fprintf(&hb, "\n\n<pre><code>%s</code></pre>", html.EscapeString(cmdBody))
		}
	}
	fmt.Fprintf(&hb, "\n<i>⏱ Menunggu persetujuan (%ds)...</i>", secs)

	var pb strings.Builder
	fmt.Fprintf(&pb, "🔐 Konfirmasi Eksekusi (%s)", tool)
	if cmdBody != "" {
		fmt.Fprintf(&pb, "\n\n%s", cmdBody)
	}
	fmt.Fprintf(&pb, "\n\n⏱ Menunggu persetujuan (%ds)...", secs)

	return hb.String(), pb.String()
}
