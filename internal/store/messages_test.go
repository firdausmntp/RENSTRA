package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"renstra/internal/provider"
	"renstra/internal/store"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestAppendAndReadMessagesInOrder(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for _, c := range []string{"halo", "dunia"} {
		if _, err := s.AppendMessage(ctx, 1, provider.Message{Role: provider.RoleUser, Content: c}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Messages(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Content != "halo" || got[1].Content != "dunia" {
		t.Fatalf("got %+v", got)
	}
}

func TestSessionBreakHidesOlderMessages(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.AppendMessage(ctx, 1, provider.Message{Role: provider.RoleUser, Content: "lama"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SessionBreak(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendMessage(ctx, 1, provider.Message{Role: provider.RoleUser, Content: "baru"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Messages(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Content != "baru" {
		t.Fatalf("got %+v", got)
	}
}
