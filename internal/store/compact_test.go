package store_test

import (
	"context"
	"testing"

	"renstra/internal/provider"
)

func TestCompactionPersistsAcrossCalls(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// 10 user messages
	for i := 0; i < 10; i++ {
		if _, err := s.AppendMessage(ctx, 1, provider.Message{Role: provider.RoleUser, Content: "pesan lama"}); err != nil {
			t.Fatal(err)
		}
	}

	msgs, err := s.Messages(ctx, 1)
	if err != nil || len(msgs) != 10 {
		t.Fatalf("sebelum kompaksi: %d pesan", len(msgs))
	}

	compacted, err := s.Compact(ctx, 1, 6, "Ringkasan: 4 pesan dibahas.")
	if err != nil {
		t.Fatal(err)
	}
	if len(compacted) != 7 { // 1 summary + 6 kept
		t.Fatalf("setelah kompaksi pertama: %d pesan, want 7", len(compacted))
	}

	// Turn berikutnya membaca dari disk: harus tetap 7 pesan!
	reloaded, err := s.Messages(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded) != 7 {
		t.Fatalf("reloaded messages dari disk: %d pesan, want 7", len(reloaded))
	}
	if reloaded[0].Role != provider.RoleSystem || reloaded[0].Content != "Ringkasan: 4 pesan dibahas." {
		t.Fatalf("ringkasan salah: %+v", reloaded[0])
	}

	// Setelah session break, summary lama harus hilang
	if err := s.SessionBreak(ctx, 1); err != nil {
		t.Fatal(err)
	}
	fresh, err := s.Messages(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh) != 0 {
		t.Fatalf("setelah session break: %d pesan, want 0", len(fresh))
	}
}
