package main

import (
	"context"
	"strings"
	"testing"

	"renstra/internal/agent"
	"renstra/internal/telegram"
)

func TestSlashModelGlobalSwitchesAllSessions(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	h := &telegramHandler{store: st, runner: &agent.Runner{Store: st, Model: "old-model"}}

	ctxTopic1 := telegram.WithTopic(ctx, 101)
	ctxTopic2 := telegram.WithTopic(ctx, 202)
	_ = h.runnerForChat(ctxTopic1, -100111)
	_ = h.runnerForChat(ctxTopic2, -100111)
	_ = st.SetChatModel(ctx, telegram.SessionChatID(ctxTopic1, -100111), "old-model")
	_ = st.SetChatModel(ctx, telegram.SessionChatID(ctxTopic2, -100111), "old-model")

	reply, err := h.Handle(ctx, 1, "/model claude-3-7-sonnet --global")
	if err != nil {
		t.Fatalf("/model --global: %v", err)
	}
	if !strings.Contains(reply, "Model global diubah") || !strings.Contains(reply, "claude-3-7-sonnet") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if h.runner.GetModel() != "claude-3-7-sonnet" {
		t.Fatalf("base runner model = %q, want claude-3-7-sonnet", h.runner.GetModel())
	}
	if m1 := h.runnerForChat(ctxTopic1, -100111).GetModel(); m1 != "claude-3-7-sonnet" {
		t.Fatalf("topic 1 model = %q, want claude-3-7-sonnet", m1)
	}
	if m2 := h.runnerForChat(ctxTopic2, -100111).GetModel(); m2 != "claude-3-7-sonnet" {
		t.Fatalf("topic 2 model = %q, want claude-3-7-sonnet", m2)
	}
}
