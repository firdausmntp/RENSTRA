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

func newClient(t *testing.T, url string) *openai.Client {
	t.Helper()
	c, err := openai.New(config.ProviderConfig{BaseURL: url, Path: "/chat/completions", Timeout: config.Duration(5 * time.Second)}, "k")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func collect(t *testing.T, ch <-chan provider.Event) (string, []provider.ToolCall, error) {
	t.Helper()
	var text strings.Builder
	var calls []provider.ToolCall
	var seen error
	for ev := range ch {
		text.WriteString(ev.Text)
		if len(ev.ToolCalls) > 0 {
			calls = ev.ToolCalls
		}
		if ev.Err != nil {
			seen = ev.Err
		}
	}
	return text.String(), calls, seen
}

func TestTruncatedStreamIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"separuh\"}}]}\n\n")
		// connection ends with no [DONE] and no finish_reason
	}))
	defer srv.Close()

	text, _, err := collect(t, mustStream(t, newClient(t, srv.URL)))
	if err == nil || !strings.Contains(err.Error(), "terputus") {
		t.Fatalf("got err=%v text=%q", err, text)
	}
	if text != "separuh" {
		t.Fatalf("teks parsial hilang: %q", text)
	}
}

func TestTruncatedToolCallIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c1\",\"function\":{\"name\":\"write_file\",\"arguments\":\"{\\\"path\\\":\\\"a.txt\\\"}\"}}]}}]}\n\n")
	}))
	defer srv.Close()

	_, calls, err := collect(t, mustStream(t, newClient(t, srv.URL)))
	if err == nil {
		t.Fatal("stream terputus diterima sebagai selesai")
	}
	if len(calls) > 0 {
		t.Fatalf("tool call separuh dieksekusi: %+v", calls)
	}
}

func TestInStreamErrorIsSurfaced(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"error\":{\"message\":\"model overloaded\",\"code\":503}}\n\n")
	}))
	defer srv.Close()

	_, _, err := collect(t, mustStream(t, newClient(t, srv.URL)))
	if err == nil || !strings.Contains(err.Error(), "model overloaded") {
		t.Fatalf("got %v", err)
	}
}

func TestMaxTokensIsSent(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)
	ch, err := c.Stream(context.Background(), provider.Request{Model: "m", MaxTokens: 512})
	if err != nil {
		t.Fatal(err)
	}
	collect(t, ch)
	if !strings.Contains(got, `"max_tokens":512`) {
		t.Fatalf("body tanpa max_tokens: %s", got)
	}
}

func TestInvalidProxyIsAnError(t *testing.T) {
	if _, err := openai.New(config.ProviderConfig{BaseURL: "http://127.0.0.1:1", Path: "/x", Proxy: "://bad"}, "k"); err == nil {
		t.Fatal("proxy tidak valid tidak dilaporkan")
	}
}

func mustStream(t *testing.T, c *openai.Client) <-chan provider.Event {
	t.Helper()
	ch, err := c.Stream(context.Background(), provider.Request{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	return ch
}
