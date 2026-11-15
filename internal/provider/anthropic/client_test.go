package anthropic_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"renstra/internal/config"
	"renstra/internal/provider"
	"renstra/internal/provider/anthropic"
)

func newClient(t *testing.T, url string) *anthropic.Client {
	t.Helper()
	c, err := anthropic.New(config.ProviderConfig{BaseURL: url, Timeout: config.Duration(5 * time.Second)}, "k")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func collect(t *testing.T, ch <-chan provider.Event) (text string, calls []provider.ToolCall, usage *provider.Usage, seen error) {
	t.Helper()
	for ev := range ch {
		if ev.Err != nil {
			seen = ev.Err
			continue
		}
		text += ev.Text
		if len(ev.ToolCalls) > 0 {
			calls = ev.ToolCalls
		}
		if ev.Usage != nil {
			usage = ev.Usage
		}
	}
	return
}

func TestName(t *testing.T) {
	if got := newClient(t, "http://example.invalid").Name(); got != "anthropic" {
		t.Fatalf("got %q", got)
	}
}

func TestInvalidProxy(t *testing.T) {
	if _, err := anthropic.New(config.ProviderConfig{Proxy: "://bad"}, "k"); err == nil {
		t.Fatal("expected error")
	}
}

func TestStreamAssemblesToolUseFromDeltas(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "k" || r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Errorf("headers: %v", r.Header)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":11,\"output_tokens\":0}}}\n\n")
		io.WriteString(w, "data: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"tu1\",\"name\":\"terminal\"}}\n\n")
		io.WriteString(w, "data: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"cmd\\\":\"}}\n\n")
		io.WriteString(w, "data: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"\\\"echo hi\\\"}\"}}\n\n")
		io.WriteString(w, "data: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":7}}\n\n")
		io.WriteString(w, "data: {\"type\":\"message_stop\"}\n\n")
	}))
	defer srv.Close()

	ch, err := newClient(t, srv.URL).Stream(context.Background(), provider.Request{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	_, calls, usage, seen := collect(t, ch)
	if seen != nil {
		t.Fatal(seen)
	}
	if len(calls) != 1 || calls[0].ID != "tu1" || calls[0].Name != "terminal" || calls[0].Arguments != `{"cmd":"echo hi"}` {
		t.Fatalf("got %+v", calls)
	}
	if usage == nil || usage.InputTokens != 11 || usage.OutputTokens != 7 {
		t.Fatalf("usage %+v", usage)
	}
}

func TestStreamTextDeltas(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"halo \"}}\n\n")
		io.WriteString(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"dunia\"}}\n\n")
		io.WriteString(w, "data: {\"type\":\"message_stop\"}\n\n")
	}))
	defer srv.Close()

	ch, err := newClient(t, srv.URL).Stream(context.Background(), provider.Request{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	text, _, _, seen := collect(t, ch)
	if seen != nil {
		t.Fatal(seen)
	}
	if text != "halo dunia" {
		t.Fatalf("got %q", text)
	}
}

func TestStreamTruncatedIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"potong\"}}\n\n")
	}))
	defer srv.Close()

	ch, err := newClient(t, srv.URL).Stream(context.Background(), provider.Request{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, seen := collect(t, ch)
	if seen == nil || !strings.Contains(seen.Error(), "message_stop") {
		t.Fatalf("got %v", seen)
	}
}

func TestStreamSurfacesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"bad key"}}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	ch, err := newClient(t, srv.URL).Stream(context.Background(), provider.Request{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, seen := collect(t, ch)
	if seen == nil || !strings.Contains(seen.Error(), "401") || !strings.Contains(seen.Error(), "bad key") {
		t.Fatalf("got %v", seen)
	}
}

func TestStreamSurfacesStreamErrorEvent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "data: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"overloaded\"}}\n\n")
	}))
	defer srv.Close()

	ch, err := newClient(t, srv.URL).Stream(context.Background(), provider.Request{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, seen := collect(t, ch)
	if seen == nil || !strings.Contains(seen.Error(), "overloaded") {
		t.Fatalf("got %v", seen)
	}
}

// capture runs one request and returns the decoded request body.
func capture(t *testing.T, req provider.Request) map[string]any {
	t.Helper()
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("body: %v", err)
		}
		io.WriteString(w, "data: {\"type\":\"message_stop\"}\n\n")
	}))
	defer srv.Close()

	ch, err := newClient(t, srv.URL).Stream(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
	return body
}

func TestRequestToolResultConversion(t *testing.T) {
	body := capture(t, provider.Request{
		Model: "m",
		Messages: []provider.Message{
			{Role: provider.RoleUser, Content: "jalankan"},
			{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "tu1", Name: "terminal", Arguments: `{"cmd":"ls"}`}}},
			{Role: provider.RoleTool, ToolCallID: "tu1", Content: "file.txt"},
		},
	})

	msgs, _ := body["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("messages %+v", msgs)
	}
	assistant, _ := msgs[1].(map[string]any)
	blocks, _ := assistant["content"].([]any)
	use, _ := blocks[0].(map[string]any)
	if assistant["role"] != "assistant" || use["type"] != "tool_use" || use["id"] != "tu1" || use["name"] != "terminal" {
		t.Fatalf("assistant %+v", assistant)
	}
	if input, _ := use["input"].(map[string]any); input["cmd"] != "ls" {
		t.Fatalf("input %+v", use["input"])
	}

	toolMsg, _ := msgs[2].(map[string]any)
	toolBlocks, _ := toolMsg["content"].([]any)
	result, _ := toolBlocks[0].(map[string]any)
	if toolMsg["role"] != "user" || result["type"] != "tool_result" || result["tool_use_id"] != "tu1" || result["content"] != "file.txt" {
		t.Fatalf("tool message %+v", toolMsg)
	}
}

func TestRequestMergesConsecutiveUserMessages(t *testing.T) {
	body := capture(t, provider.Request{
		Model: "m",
		Messages: []provider.Message{
			{Role: provider.RoleUser, Content: "satu"},
			{Role: provider.RoleUser, Content: "dua"},
		},
	})
	msgs, _ := body["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages %+v", msgs)
	}
	m, _ := msgs[0].(map[string]any)
	blocks, _ := m["content"].([]any)
	if len(blocks) != 2 {
		t.Fatalf("blocks %+v", blocks)
	}
}

func TestRequestHoistsSystemAndDefaultsMaxTokens(t *testing.T) {
	body := capture(t, provider.Request{
		Model: "m",
		Messages: []provider.Message{
			{Role: provider.RoleSystem, Content: "a"},
			{Role: provider.RoleSystem, Content: "b"},
			{Role: provider.RoleUser, Content: "x"},
		},
	})
	if body["system"] != "a\n\nb" {
		t.Fatalf("system %v", body["system"])
	}
	if body["max_tokens"] != float64(4096) {
		t.Fatalf("max_tokens %v", body["max_tokens"])
	}
	msgs, _ := body["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages %+v", msgs)
	}
}

func TestRequestToolsAndMaxTokensOverride(t *testing.T) {
	body := capture(t, provider.Request{
		Model:     "m",
		MaxTokens: 100,
		Tools:     []provider.ToolDef{{Name: "terminal", Description: "run", Schema: json.RawMessage(`{"type":"object"}`)}},
		Messages:  []provider.Message{{Role: provider.RoleUser, Content: "x"}},
	})
	if body["max_tokens"] != float64(100) {
		t.Fatalf("max_tokens %v", body["max_tokens"])
	}
	tools, _ := body["tools"].([]any)
	tool, _ := tools[0].(map[string]any)
	if tool["name"] != "terminal" || tool["description"] != "run" {
		t.Fatalf("tools %+v", tools)
	}
	if schema, _ := tool["input_schema"].(map[string]any); schema["type"] != "object" {
		t.Fatalf("input_schema %+v", tool["input_schema"])
	}
}
