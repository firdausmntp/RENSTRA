package cron

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"renstra/internal/store"
)

func TestNextAfterStep(t *testing.T) {
	loc := time.UTC
	from := time.Date(2026, 11, 15, 10, 2, 0, 0, loc)
	got, err := NextAfter("*/5 * * * *", from, loc)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 11, 15, 10, 5, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestNextAfterWeekdayRange(t *testing.T) {
	loc := time.UTC
	// 2026-11-13 adalah Jumat; jadwal 09:00 Senin-Jumat -> Senin 2026-11-16 09:00.
	from := time.Date(2026, 11, 13, 10, 0, 0, 0, loc)
	if from.Weekday() != time.Friday {
		t.Fatalf("asumsi hari salah: %s", from.Weekday())
	}
	got, err := NextAfter("0 9 * * 1-5", from, loc)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 11, 16, 9, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestNextAfterDailyShorthand(t *testing.T) {
	loc := time.UTC
	from := time.Date(2026, 11, 15, 10, 30, 0, 0, loc)
	got, err := NextAfter("@daily", from, loc)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 11, 16, 0, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestNextAfterListsAndRangeStep(t *testing.T) {
	loc := time.UTC
	from := time.Date(2026, 11, 15, 10, 0, 0, 0, loc)

	got, err := NextAfter("1,2,3 * * * *", from, loc)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 11, 15, 10, 1, 0, 0, loc); !got.Equal(want) {
		t.Fatalf("daftar: got %s want %s", got, want)
	}

	got, err = NextAfter("0-30/5 * * * *", from, loc)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 11, 15, 10, 5, 0, 0, loc); !got.Equal(want) {
		t.Fatalf("rentang+step: got %s want %s", got, want)
	}
}

func TestNextAfterRejectsBadSchedules(t *testing.T) {
	loc := time.UTC
	from := time.Date(2026, 11, 15, 10, 0, 0, 0, loc)
	for _, bad := range []string{"", "* * * *", "* * * * * *", "@yearly", "60 * * * *", "*/0 * * * *", "5-1 * * * *", "abc * * * *"} {
		if _, err := NextAfter(bad, from, loc); err == nil {
			t.Fatalf("want error untuk %q", bad)
		}
	}
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestRunFiresMissedJobOnce(t *testing.T) {
	st := openStore(t)
	loc := time.UTC
	past := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	if _, err := st.DB().Exec(`INSERT INTO jobs(chat_id, schedule, prompt, enabled, next_run_at) VALUES(7, '* * * * *', 'halo', 1, ?)`, past); err != nil {
		t.Fatal(err)
	}

	var fires atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		Run(ctx, st, loc, func(context.Context, Job) { fires.Add(1) }, 10*time.Millisecond)
	}()

	deadline := time.After(2 * time.Second)
	for fires.Load() == 0 {
		select {
		case <-deadline:
			cancel()
			<-done
			t.Fatal("job tidak pernah dipanggil")
		case <-time.After(5 * time.Millisecond):
		}
	}
	// Beri waktu untuk beberapa tick lagi: slot yang terlewat tidak boleh di-backfill.
	time.Sleep(80 * time.Millisecond)
	cancel()
	<-done

	if got := fires.Load(); got != 1 {
		t.Fatalf("fire = %d, want 1 (tanpa backfill)", got)
	}

	var lastRun, nextRun string
	if err := st.DB().QueryRow(`SELECT COALESCE(last_run_at,''), COALESCE(next_run_at,'') FROM jobs`).Scan(&lastRun, &nextRun); err != nil {
		t.Fatal(err)
	}
	if lastRun == "" {
		t.Fatal("last_run_at tidak diperbarui")
	}
	next, err := time.Parse(time.RFC3339, nextRun)
	if err != nil {
		t.Fatalf("next_run_at tidak valid: %q", nextRun)
	}
	if !next.After(time.Now().UTC().Add(-time.Minute)) {
		t.Fatalf("next_run_at tidak maju: %s", next)
	}
}

func TestRunSkipsDisabledJob(t *testing.T) {
	st := openStore(t)
	past := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	if _, err := st.DB().Exec(`INSERT INTO jobs(chat_id, schedule, prompt, enabled, next_run_at) VALUES(7, '* * * * *', 'halo', 0, ?)`, past); err != nil {
		t.Fatal(err)
	}

	var fires atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		Run(ctx, st, time.UTC, func(context.Context, Job) { fires.Add(1) }, 10*time.Millisecond)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done

	if got := fires.Load(); got != 0 {
		t.Fatalf("job nonaktif dipanggil %d kali", got)
	}
}
