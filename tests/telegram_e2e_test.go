package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"renstra/internal/agent"
	"renstra/internal/cli"
	"renstra/internal/config"
	"renstra/internal/provider"
	"renstra/internal/store"
	"renstra/internal/telegram"
	"renstra/internal/tool"
)

type e2eHandler struct {
	runner *agent.Runner
	bot    *telegram.Bot
}

func (h *e2eHandler) Handle(ctx context.Context, chatID int64, text string) (string, error) {
	sw := telegram.NewStreamWriterWithContext(ctx, h.bot, chatID)
	defer sw.Cancel()
	ctx = agent.WithStreamWriter(ctx, sw)
	reply, err := h.runner.Run(ctx, chatID, text)
	if err == nil && sw.Finalize(ctx, reply) {
		return "", nil
	}
	return reply, err
}

func (h *e2eHandler) Approve(ctx context.Context, chatID int64, toolName, args string) (bool, error) {
	return h.bot.AskApproval(ctx, chatID, toolName, args, 2*time.Second)
}

func TestIntegrationTelegramAndCLIHelp(t *testing.T) {
	var mu sync.Mutex
	var sentTexts []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var payload map[string]any
		_ = json.Unmarshal(raw, &payload)
		switch {
		case strings.HasSuffix(r.URL.Path, "/sendRichMessageDraft"):
			io.WriteString(w, `{"ok":true,"result":true}`)
		case strings.HasSuffix(r.URL.Path, "/sendRichMessage"):
			if rm, ok := payload["rich_message"].(map[string]any); ok {
				mu.Lock()
				sentTexts = append(sentTexts, rm["markdown"].(string))
				mu.Unlock()
			}
			io.WriteString(w, `{"ok":true,"result":{"message_id":10}}`)
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			mu.Lock()
			if txt, ok := payload["text"].(string); ok {
				sentTexts = append(sentTexts, txt)
			}
			mu.Unlock()
			io.WriteString(w, `{"ok":true,"result":{"message_id":11}}`)
		default:
			io.WriteString(w, `{"ok":true,"result":true}`)
		}
	}))
	defer srv.Close()

	home := t.TempDir()
	st, err := store.Open(filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	bot, err := telegram.New(telegram.Config{
		Token:        "test-token",
		AllowedUsers: []int64{42},
		APIBase:      srv.URL,
		WorkspaceDir: filepath.Join(home, "workspace"),
		OnSeen: func(id int64) {
			_ = st.MarkUpdateSeen(context.Background(), id)
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	runner := &agent.Runner{
		Provider:    &scriptedProvider{steps: [][]provider.Event{{{Text: "Halo dari integrasi!", Done: true}}}},
		Tools:       tool.NewRegistry(),
		Store:       st,
		Model:       "gpt-4o-mini",
		Cwd:         filepath.Join(home, "workspace"),
		ApprovalCfg: config.ApprovalConfig{Mode: "prompt"},
		Out:         io.Discard,
	}

	h := &e2eHandler{runner: runner, bot: bot}
	reply, err := h.Handle(context.Background(), 42, "tes integrasi")
	if err != nil {
		t.Fatalf("e2e Handle: %v", err)
	}
	if reply != "" {
		t.Fatalf("expected streamed delivery, got direct reply: %q", reply)
	}

	mu.Lock()
	joined := strings.Join(sentTexts, "\n")
	mu.Unlock()
	if !strings.Contains(joined, "Halo dari integrasi!") {
		t.Fatalf("telegram output missing response: %q", joined)
	}

	// Verify CLI PrintCLIUsage supports both languages cleanly
	var idBuf, enBuf bytes.Buffer
	cli.PrintCLIUsage(&idBuf, "id", "v1.2.5")
	cli.PrintCLIUsage(&enBuf, "en", "v1.2.5")
	if !strings.Contains(idBuf.String(), "jalankan gateway") || !strings.Contains(enBuf.String(), "start Telegram gateway") {
		t.Fatalf("PrintCLIUsage mismatch: id=%q en=%q", idBuf.String(), enBuf.String())
	}
}
