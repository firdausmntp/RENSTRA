package telegram

import (
	"context"
	"errors"
	"strings"
)

// RichBlock represents a Telegram Bot API 10.1-10.5 InputRichBlock element
// (for example "paragraph", "section_heading", "divider", "expandable_blockquote", or "footer").
// In Bot API 10.5, InputRichBlockExpandableBlockQuotation replaced "text" with "blocks".
type RichBlock struct {
	Type   string      `json:"type"`
	Text   string      `json:"text,omitempty"`
	Blocks []RichBlock `json:"blocks,omitempty"`
}

// RichBlockDivider returns a native Telegram Bot API InputRichBlockDivider.
func RichBlockDivider() RichBlock {
	return RichBlock{Type: "divider"}
}

// RichBlockExpandableQuote returns a Telegram Bot API 10.5 expandable block quotation
// containing nested rich blocks.
func RichBlockExpandableQuote(blocks ...RichBlock) RichBlock {
	return RichBlock{
		Type:   "expandable_blockquote",
		Blocks: blocks,
	}
}

// SendRichBlocks sends a Bot API 10.1-10.5 rich_message.blocks payload (supporting
// native "divider" and 10.5 "expandable_blockquote" blocks), falling back to HTML SendMessage if rejected.
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
	renderBlocksFallback(&md, blocks, "")
	b.reply(ctx, chatID, strings.TrimSpace(md.String()))
	return nil
}

func renderBlocksFallback(md *strings.Builder, blocks []RichBlock, linePrefix string) {
	for _, blk := range blocks {
		if blk.Type == "divider" {
			md.WriteString("---\n\n")
			continue
		}
		if blk.Type == "expandable_blockquote" && len(blk.Blocks) > 0 {
			renderBlocksFallback(md, blk.Blocks, "> ")
			md.WriteString("\n")
			continue
		}
		if strings.TrimSpace(blk.Text) != "" {
			if blk.Type == "section_heading" {
				md.WriteString(linePrefix + "## " + blk.Text + "\n\n")
			} else {
				for _, line := range strings.Split(blk.Text, "\n") {
					md.WriteString(linePrefix + line + "\n")
				}
				md.WriteString("\n")
			}
		}
	}
}
