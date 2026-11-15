package openai_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"renstra/internal/config"
	"renstra/internal/provider"
	"renstra/internal/provider/openai"
)

func TestStreamAssemblesToolCallFromChunks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c1\",\"function\":{\"name\":\"terminal\",\"arguments\":\"{\\\"cmd\\\":\"}}]}}]}\n\n")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"echo hi\\\"}\"}}]}}]}\n\n")
		io.WriteString(w, "data: {\"choices\":[{\"finish_reason\":\"tool_calls\"}]}\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	c, err := openai.New(config.ProviderConfig{BaseURL: srv.URL, Path: "/chat/completions", Timeout: config.Duration(5 * time.Second)}, "k")
	if err != nil {
		t.Fatal(err)
	}
	ch, err := c.Stream(context.Background(), provider.Request{Model: "m", Messages: []provider.Message{{Role: provider.RoleUser, Content: "x"}}})
	if err != nil {
		t.Fatal(err)
	}

	var calls []provider.ToolCall
	for ev := range ch {
		if ev.Err != nil {
			t.Fatal(ev.Err)
		}
		if len(ev.ToolCalls) > 0 {
			calls = ev.ToolCalls
		}
	}
	if len(calls) != 1 || calls[0].Name != "terminal" || calls[0].Arguments != `{"cmd":"echo hi"}` {
		t.Fatalf("got %+v", calls)
	}
}

func TestStreamSurfacesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"bad key"}}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	c, err := openai.New(config.ProviderConfig{BaseURL: srv.URL, Path: "/chat/completions"}, "k")
	if err != nil {
		t.Fatal(err)
	}
	ch, err := c.Stream(context.Background(), provider.Request{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	var seen error
	for ev := range ch {
		if ev.Err != nil {
			seen = ev.Err
		}
	}
	if seen == nil || !strings.Contains(seen.Error(), "401") {
		t.Fatalf("got %v", seen)
	}
}
