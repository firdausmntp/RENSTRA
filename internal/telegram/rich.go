package telegram

import (
	"context"
	"errors"
	"strings"
)

// RichBlock represents a Telegram Bot API 10.1+ InputRichBlock element
// (for example "paragraph", "section_heading", "divider", or "footer").
type RichBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// RichBlockDivider returns a native Telegram Bot API InputRichBlockDivider.
func RichBlockDivider() RichBlock {
	return RichBlock{Type: "divider"}
}

// SendRichBlocks sends a Bot API 10.1+ rich_message.blocks payload (supporting
// native "divider" blocks), falling back to HTML SendMessage if rejected.
func (b *Bot) SendRichBlocks(ctx context.Context, chatID int64, blocks []RichBlock) error {
	if len(blocks) == 0 {
		return errors.New("telegram: rich blocks kosong")
	}
	payload := map[string]any{
		"chat_id": chatID,
		"rich_message": map[string]any{
			"blocks": blocks,
		},
	}
	applyReplyTarget(ctx, payload)
	if _, err := b.post(ctx, "sendRichMessage", payload); err == nil {
		return nil
	}
	var md strings.Builder
	for _, blk := range blocks {
		if blk.Type == "divider" {
			md.WriteString("---\n\n")
			continue
		}
		if strings.TrimSpace(blk.Text) != "" {
			if blk.Type == "section_heading" {
				md.WriteString("## " + blk.Text + "\n\n")
			} else {
				md.WriteString(blk.Text + "\n\n")
			}
		}
	}
	b.reply(ctx, chatID, strings.TrimSpace(md.String()))
	return nil
}
