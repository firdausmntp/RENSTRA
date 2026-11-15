package store_test

import (
	"context"
	"testing"
	"time"
)

func TestUpdateSeenLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	seen, err := s.IsUpdateSeen(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	if seen {
		t.Fatal("update 42 seharusnya belum pernah dilihat")
	}

	if err := s.MarkUpdateSeen(ctx, 42); err != nil {
		t.Fatal(err)
	}
	seen, err = s.IsUpdateSeen(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatal("update 42 seharusnya sudah dilihat")
	}

	ids, err := s.RecentUpdateIDs(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != 42 {
		t.Fatalf("got ids=%v", ids)
	}
}

func TestUsageDailyAccumulation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	today := time.Now().Format("2006-01-02")
	if err := s.RecordUsage(ctx, today, 100, 50, 0.005); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordUsage(ctx, today, 200, 100, 0.010); err != nil {
		t.Fatal(err)
	}

	u, err := s.UsageForDay(ctx, today)
	if err != nil {
		t.Fatal(err)
	}
	if u.TokensIn != 300 || u.TokensOut != 150 || u.CostUSD < 0.014 || u.CostUSD > 0.016 {
		t.Fatalf("got %+v", u)
	}
}

func TestJobsCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	id, err := s.AddJob(ctx, 1001, "@daily", "ringkas berita")
	if err != nil {
		t.Fatal(err)
	}

	jobs, err := s.ListJobs(ctx, 1001)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Prompt != "ringkas berita" {
		t.Fatalf("got %+v", jobs)
	}

	if err := s.RemoveJob(ctx, id); err != nil {
		t.Fatal(err)
	}
	jobs, err = s.ListJobs(ctx, 1001)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 0 {
		t.Fatalf("job belum terhapus: %+v", jobs)
	}
}
