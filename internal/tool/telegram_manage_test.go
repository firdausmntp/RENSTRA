package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type fakeTGManager struct {
	topics map[int64]string
	calls  []string
}

func (f *fakeTGManager) CreateForumTopic(_ context.Context, _ int64, name string) (int64, error) {
	f.calls = append(f.calls, "create:"+name)
	if f.topics == nil {
		f.topics = map[int64]string{}
	}
	f.topics[42] = name
	return 42, nil
}
func (f *fakeTGManager) EditForumTopic(_ context.Context, _, tid int64, name string) error {
	f.calls = append(f.calls, "rename:"+name)
	f.topics[tid] = name
	return nil
}
func (f *fakeTGManager) CloseForumTopic(_ context.Context, _, _ int64) error {
	f.calls = append(f.calls, "close")
	return nil
}
func (f *fakeTGManager) ReopenForumTopic(_ context.Context, _, _ int64) error {
	f.calls = append(f.calls, "reopen")
	return nil
}
func (f *fakeTGManager) DeleteForumTopic(_ context.Context, _, tid int64) error {
	f.calls = append(f.calls, "delete_topic")
	delete(f.topics, tid)
	return nil
}
func (f *fakeTGManager) ListKnownTopics(_ int64) map[int64]string {
	out := map[int64]string{}
	for k, v := range f.topics {
		out[k] = v
	}
	return out
}
func (f *fakeTGManager) DeleteMessage(_ context.Context, _, _ int64) error {
	f.calls = append(f.calls, "delete_msg")
	return nil
}
func (f *fakeTGManager) EditFormattedMessage(_ context.Context, _, _ int64, text string) error {
	f.calls = append(f.calls, "edit_msg:"+text)
	return nil
}
func (f *fakeTGManager) PinMessage(_ context.Context, _, _ int64, _ bool) error {
	f.calls = append(f.calls, "pin")
	return nil
}
func (f *fakeTGManager) UnpinMessage(_ context.Context, _, _ int64) error {
	f.calls = append(f.calls, "unpin")
	return nil
}

func TestTelegramManageActions(t *testing.T) {
	mgr := &fakeTGManager{topics: map[int64]string{7: "General"}}
	tm := NewTelegramManage(mgr)
	ctx := WithChatID(context.Background(), -10012345)

	run := func(payload string) string {
		t.Helper()
		out, err := tm.Execute(ctx, json.RawMessage(payload))
		if err != nil {
			t.Fatalf("Execute(%s): %v", payload, err)
		}
		return out
	}

	if out := run(`{"action":"list_topics"}`); !strings.Contains(out, "topic_id=7: General") {
		t.Fatalf("unexpected list_topics: %s", out)
	}
	if out := run(`{"action":"create_topic","name":"DevOps"}`); !strings.Contains(out, "topic_id=42") {
		t.Fatalf("unexpected create_topic: %s", out)
	}
	run(`{"action":"rename_topic","topic_id":42,"name":"Infra"}`)
	run(`{"action":"close_topic","topic_id":42}`)
	run(`{"action":"reopen_topic","topic_id":42}`)
	run(`{"action":"delete_topic","topic_id":42}`)
	run(`{"action":"delete_message","message_id":99}`)
	run(`{"action":"edit_message","message_id":99,"text":"updated"}`)
	run(`{"action":"pin_message","message_id":99,"silent":true}`)
	run(`{"action":"unpin_message","message_id":99}`)

	if len(mgr.calls) != 9 {
		t.Fatalf("expected 9 calls, got %d: %v", len(mgr.calls), mgr.calls)
	}
}
