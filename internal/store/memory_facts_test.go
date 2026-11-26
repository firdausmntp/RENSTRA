package store_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"renstra/internal/store"
)

func TestSmartMemoryFactsAndModelUsage(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()

	id1, err := st.SaveMemoryFact(ctx, "global", "threads,api", "Jangan pakai REST /api/v1/media/<id>/delete/ untuk hapus komentar")
	if err != nil || id1 <= 0 {
		t.Fatalf("SaveMemoryFact 1: id=%d err=%v", id1, err)
	}
	idDup, err := st.SaveMemoryFact(ctx, "global", "threads,delete", "jangan pakai rest /api/v1/media/<id>/delete/ untuk hapus komentar")
	if err != nil || idDup != id1 {
		t.Fatalf("expected dedup id=%d, got id=%d err=%v", id1, idDup, err)
	}
	if _, err := st.SaveMemoryFact(ctx, "chat:77", "docker", "Port nginx container 8443"); err != nil {
		t.Fatal(err)
	}

	hits, err := st.SearchMemoryFacts(ctx, "chat:77", "hapus komentar threads", 3)
	if err != nil || len(hits) == 0 || !strings.Contains(hits[0].Content, "media") {
		t.Fatalf("expected relevant fact hit, got %+v err=%v", hits, err)
	}
	if err := st.DeleteMemoryFact(ctx, id1); err != nil {
		t.Fatalf("DeleteMemoryFact: %v", err)
	}

	today := time.Now().Format("2006-01-02")
	_ = st.RecordModelUsage(ctx, today, "v4", 1200, 300, 0.01)
	_ = st.RecordModelUsage(ctx, today, "v4", 800, 200, 0.005)
	_ = st.RecordModelUsage(ctx, today, "gpt-4o-mini", 400, 100, 0.001)

	models, err := st.ModelUsageSince(ctx, time.Now().AddDate(0, 0, -7))
	if err != nil || len(models) != 2 {
		t.Fatalf("ModelUsageSince: %+v err=%v", models, err)
	}
	if models[0].Model != "v4" || models[0].Requests != 2 || models[0].TokensIn != 2000 {
		t.Fatalf("unexpected top model usage: %+v", models[0])
	}
}
