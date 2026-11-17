package store_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"renstra/internal/provider"
	"renstra/internal/store"
)

func TestSearchMessagesFindsCompactedAndLive(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()

	// Seed multiple turns
	turns := []provider.Message{
		{Role: provider.RoleUser, Content: "port redis kita di 6389 ya bro"},
		{Role: provider.RoleAssistant, Content: "siap, kucatat redis di 6389"},
		{Role: provider.RoleUser, Content: "lalu postgres di 5433"},
		{Role: provider.RoleAssistant, Content: "oke postgres di 5433"},
		{Role: provider.RoleUser, Content: "lanjut ngobrol santai 1"},
		{Role: provider.RoleAssistant, Content: "oke santai 1"},
		{Role: provider.RoleUser, Content: "lanjut ngobrol santai 2"},
		{Role: provider.RoleAssistant, Content: "oke santai 2"},
	}
	for _, m := range turns {
		if _, err := st.AppendMessage(ctx, 42, m); err != nil {
			t.Fatal(err)
		}
	}

	// Compact history keeping only last 2 messages
	if _, err := st.Compact(ctx, 42, 2, "ringkasan santai"); err != nil {
		t.Fatal(err)
	}

	// Live messages should only have the summary + last turns
	live, _ := st.Messages(ctx, 42)
	for _, m := range live {
		if strings.Contains(m.Content, "6389") {
			t.Fatal("port redis seharusnya sudah terpotong dari live messages")
		}
	}

	// SearchMessages must still find the old compacted message!
	results, err := st.SearchMessages(ctx, 42, "redis", 5)
	if err != nil {
		t.Fatalf("SearchMessages: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 results for 'redis', got %d", len(results))
	}
	if !strings.Contains(results[0].Content, "6389") {
		t.Fatalf("hasil pertama = %q", results[0].Content)
	}

	// Wildcard literal test: '%' must not match all messages
	wildcardResults, err := st.SearchMessages(ctx, 42, "%", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(wildcardResults) != 0 {
		t.Fatalf("query %% tidak boleh mencocokkan jika tidak ada karakter %% literal, got %d", len(wildcardResults))
	}
}
