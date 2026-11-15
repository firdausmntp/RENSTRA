package telegram

import (
	"context"
	"testing"
	"time"
)

type slowHandler struct {
	started  chan struct{}
	canceled chan struct{}
}

func (s *slowHandler) Handle(ctx context.Context, chatID int64, text string) (string, error) {
	if text == "slow" {
		close(s.started)
		<-ctx.Done()
		close(s.canceled)
		return "", ctx.Err()
	}
	return "cepat", nil
}

func (s *slowHandler) Approve(ctx context.Context, chatID int64, tool, args string) (bool, error) {
	return false, nil
}

func TestNewMessageInterruptsRunningTurn(t *testing.T) {
	_, b := newAPI(t)
	h := &slowHandler{
		started:  make(chan struct{}),
		canceled: make(chan struct{}),
	}

	go b.handleUpdate(context.Background(), h, textUpdate(1, 42, "private", "slow"))

	select {
	case <-h.started:
	case <-time.After(time.Second):
		t.Fatal("turn pertama tidak kunjung mulai")
	}

	// Message kedua datang untuk chat yang sama
	doneSecond := make(chan struct{})
	go func() {
		b.handleUpdate(context.Background(), h, textUpdate(2, 42, "private", "fast"))
		close(doneSecond)
	}()

	select {
	case <-h.canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("turn pertama tidak dibatalkan oleh pesan kedua")
	}

	select {
	case <-doneSecond:
	case <-time.After(2 * time.Second):
		t.Fatal("turn kedua tidak kunjung selesai")
	}
}
