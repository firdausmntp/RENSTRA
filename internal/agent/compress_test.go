package agent_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"renstra/internal/agent"
	"renstra/internal/provider"
	"renstra/internal/store"
)

func TestCompactPreservesRecentMessages(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	// 10 user messages
	for i := 0; i < 10; i++ {
		if _, err := s.AppendMessage(ctx, 1, provider.Message{Role: provider.RoleUser, Content: "pesan lama"}); err != nil {
			t.Fatal(err)
		}
	}

	msgs, _ := s.Messages(ctx, 1)
	if len(msgs) != 10 {
		t.Fatalf("sebelum kompaksi: %d pesan", len(msgs))
	}

	compacted := agent.CompactHistory(msgs, 6, "ringkasan: 4 pesan pertama dibahas")
	if len(compacted) != 7 { // 1 ringkasan + 6 pesan terbaru
		t.Fatalf("setelah kompaksi: %d pesan, want 7", len(compacted))
	}
	if compacted[0].Role != provider.RoleSystem || compacted[0].Content != "ringkasan: 4 pesan pertama dibahas" {
		t.Fatalf("ringkasan salah: %+v", compacted[0])
	}
}

func TestShouldCompactRequestIncludesSystemAndTools(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleUser, Content: "halo"}}
	if agent.ShouldCompact(msgs, 100, 0.7) {
		t.Fatal("history kecil tidak seharusnya memicu ShouldCompact")
	}
	bigSys := strings.Repeat("a", 400)
	defs := []provider.ToolDef{{
		Name:        "big_tool",
		Description: strings.Repeat("b", 200),
		Schema:      json.RawMessage(`{"type":"object"}`),
	}}
	if !agent.ShouldCompactRequest(bigSys, defs, msgs, 100, 0.7) {
		t.Fatal("ShouldCompactRequest seharusnya memperhitungkan system prompt dan definisi tool")
	}
}
