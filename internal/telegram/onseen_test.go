package telegram

import (
	"context"
	"sync/atomic"
	"testing"
)

func TestOnSeenFiresOncePerUpdate(t *testing.T) {
	_, b := newAPI(t)
	var seen atomic.Int32
	b.onSeen = func(int64) { seen.Add(1) }

	h := &fakeHandler{reply: "ok"}
	u := textUpdate(500, 42, "private", "halo")
	b.handleUpdate(context.Background(), h, u)
	b.handleUpdate(context.Background(), h, u) // duplikat

	if got := seen.Load(); got != 1 {
		t.Fatalf("OnSeen dipanggil %d kali, mau 1", got)
	}
}
