package openai_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"renstra/internal/config"
	"renstra/internal/provider"
	"renstra/internal/provider/openai"
)

func TestProviderRetries500ThenSucceeds(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&attempts, 1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			io.WriteString(w, "server busy")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"halo\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()

	c, err := openai.New(config.ProviderConfig{BaseURL: srv.URL, Timeout: config.Duration(5 * time.Second)}, "key")
	if err != nil {
		t.Fatal(err)
	}

	ch, err := c.Stream(context.Background(), provider.Request{Model: "gpt-4o", Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}

	var text string
	for ev := range ch {
		if ev.Err != nil {
			t.Fatal(ev.Err)
		}
		text += ev.Text
	}
	if text != "halo" {
		t.Fatalf("got %q, want 'halo'", text)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}
