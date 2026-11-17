package google_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"renstra/internal/config"
	"renstra/internal/provider"
	"renstra/internal/provider/google"
)

func TestGoogleStreamTextAndToolCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-goog-api-key") != "gkey" {
			t.Fatalf("missing x-goog-api-key header")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"halo \"}]}}]}\n\n")
		io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"functionCall\":{\"name\":\"terminal\",\"args\":{\"cmd\":\"ls\"}}}]}}],\"usageMetadata\":{\"promptTokenCount\":12,\"candidatesTokenCount\":5}}\n\n")
	}))
	defer srv.Close()

	p, err := google.New(config.ProviderConfig{BaseURL: srv.URL}, "gkey")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "google" {
		t.Fatalf("name = %q", p.Name())
	}

	ch, err := p.Stream(context.Background(), provider.Request{
		Model:    "gemini-2.5-flash",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "cek file"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	var text strings.Builder
	var calls []provider.ToolCall
	var usage *provider.Usage
	for ev := range ch {
		if ev.Err != nil {
			t.Fatal(ev.Err)
		}
		text.WriteString(ev.Text)
		if len(ev.ToolCalls) > 0 {
			calls = ev.ToolCalls
		}
		if ev.Usage != nil {
			usage = ev.Usage
		}
	}
	if text.String() != "halo " {
		t.Fatalf("text = %q", text.String())
	}
	if len(calls) != 1 || calls[0].Name != "terminal" || !strings.Contains(calls[0].Arguments, "ls") {
		t.Fatalf("calls = %+v", calls)
	}
	if usage == nil || usage.InputTokens != 12 || usage.OutputTokens != 5 {
		t.Fatalf("usage = %+v", usage)
	}
}
