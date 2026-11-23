package tool

import (
	"context"
	"encoding/json"
	"testing"
)

type fakeTelegramSender struct {
	chatID   int64
	topicID  int64
	markdown string
}

func (f *fakeTelegramSender) SendToTopic(ctx context.Context, chatID, topicID int64, markdown string) error {
	f.chatID = chatID
	f.topicID = topicID
	f.markdown = markdown
	return nil
}

func TestSendTelegramTool(t *testing.T) {
	sender := &fakeTelegramSender{}
	tl := NewSendTelegram(sender)
	ctx := WithChatID(context.Background(), -100123456)
	args, _ := json.Marshal(map[string]any{
		"topic_id": 2041,
		"text":     "> RENSTRA online — topik 2041\nStatus: OK",
	})
	out, err := tl.Execute(ctx, args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if sender.chatID != -100123456 || sender.topicID != 2041 {
		t.Fatalf("unexpected target: chat=%d topic=%d", sender.chatID, sender.topicID)
	}
	if out == "" {
		t.Fatal("expected non-empty output")
	}
}
