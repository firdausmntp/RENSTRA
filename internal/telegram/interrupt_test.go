package telegram

import (
	"context"
	"sync"
	"testing"
	"time"
)

type queueTestHandler struct {
	started  chan struct{}
	release  chan struct{}
	canceled chan struct{}
	mu       sync.Mutex
	calls    []string
}

func (s *queueTestHandler) Handle(ctx context.Context, chatID int64, text string) (string, error) {
	s.mu.Lock()
	s.calls = append(s.calls, text)
	s.mu.Unlock()
	if text == "slow" {
		close(s.started)
		select {
		case <-s.release:
			return "slow-done", nil
		case <-ctx.Done():
			close(s.canceled)
			return "", ctx.Err()
		}
	}
	return "ok:" + text, nil
}

func (s *queueTestHandler) Approve(ctx context.Context, chatID int64, tool, args string) (bool, error) {
	return false, nil
}

func TestNewMessageQueuesWhileTurnRunsAndCoalesces(t *testing.T) {
	_, b := newAPI(t)
	h := &queueTestHandler{
		started:  make(chan struct{}),
		release:  make(chan struct{}),
		canceled: make(chan struct{}),
	}

	doneFirst := make(chan struct{})
	go func() {
		b.handleUpdate(context.Background(), h, textUpdate(1, 42, "private", "slow"))
		close(doneFirst)
	}()

	select {
	case <-h.started:
	case <-time.After(time.Second):
		t.Fatal("turn pertama tidak kunjung mulai")
	}

	doneSecond := make(chan struct{})
	doneThird := make(chan struct{})
	go func() {
		b.handleUpdate(context.Background(), h, textUpdate(2, 42, "private", "fast-1"))
		close(doneSecond)
	}()
	time.Sleep(30 * time.Millisecond)
	go func() {
		b.handleUpdate(context.Background(), h, textUpdate(3, 42, "private", "fast-2"))
		close(doneThird)
	}()
	time.Sleep(50 * time.Millisecond)

	// Pesan kedua & ketiga tidak boleh membatalkan turn pertama.
	select {
	case <-h.canceled:
		t.Fatal("turn pertama tidak boleh dibatalkan saat ada pesan baru")
	default:
	}

	close(h.release)

	for _, ch := range []chan struct{}{doneFirst, doneSecond, doneThird} {
		select {
		case <-ch:
		case <-time.After(2 * time.Second):
			t.Fatal("turn tidak kunjung selesai")
		}
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.calls) != 2 {
		t.Fatalf("expected 2 coalesced calls (slow + fast-1\\nfast-2), got %d: %v", len(h.calls), h.calls)
	}
	if h.calls[1] != "fast-1\nfast-2" {
		t.Fatalf("expected coalesced queued text, got %q", h.calls[1])
	}
}
