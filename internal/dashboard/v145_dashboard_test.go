package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"renstra/internal/config"
)

func TestAccountsIncludesAliasesInDetailsAndChatSwitchesModel(t *testing.T) {
	cfg := &config.Config{
		DefaultModel: "custom:v4",
		Providers: map[string]config.ProviderConfig{
			"custom": {
				Kind:         "openai",
				DefaultModel: "v4",
				BaseURL:      "https://example.invalid/v1",
			},
		},
		Aliases: map[string]string{
			"deepseek": "custom:onihub/deepseek-v4.1-flash",
			"opus":     "custom:wildan/claude-opus-4-6-thinking",
			"sonnet":   "custom:wildan/claude-sonnet-4-6",
		},
	}

	var switchedChat int64
	var switchedModel string
	srv, err := New(Options{
		Config: cfg,
		SwitchChatModel: func(_ context.Context, chatID int64, ref string) error {
			switchedChat = chatID
			switchedModel = ref
			return nil
		},
		RunChat: func(_ context.Context, _ int64, prompt string) (string, error) {
			return "ok:" + prompt, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	rec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/accounts status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		ModelOptions []struct {
			Ref   string `json:"ref"`
			Label string `json:"label"`
		} `json:"model_options"`
		ModelDetails []struct {
			Ref      string `json:"ref"`
			Provider string `json:"provider"`
			Model    string `json:"model"`
			Alias    string `json:"alias"`
		} `json:"model_details"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.ModelDetails) != 4 {
		t.Fatalf("expected 4 model_details (3 aliases + 1 default), got %d: %+v", len(resp.ModelDetails), resp.ModelDetails)
	}
	foundOpus := false
	for _, d := range resp.ModelDetails {
		if d.Alias == "opus" && d.Model == "wildan/claude-opus-4-6-thinking" && d.Provider == "custom" {
			foundOpus = true
		}
	}
	if !foundOpus {
		t.Fatalf("expected opus alias in model_details, got: %+v", resp.ModelDetails)
	}
	if len(resp.ModelOptions) < 4 {
		t.Fatalf("expected >=4 model_options, got %+v", resp.ModelOptions)
	}

	chatBody := bytes.NewBufferString(`{"chat_id":42,"prompt":"halo","model":"opus"}`)
	chatReq := httptest.NewRequest(http.MethodPost, "/api/chat", chatBody)
	chatRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(chatRec, chatReq)
	if chatRec.Code != http.StatusOK {
		t.Fatalf("POST /api/chat status=%d body=%s", chatRec.Code, chatRec.Body.String())
	}
	if switchedChat != 42 || switchedModel != "opus" {
		t.Fatalf("expected SwitchChatModel(42, opus), got (%d, %q)", switchedChat, switchedModel)
	}
}
