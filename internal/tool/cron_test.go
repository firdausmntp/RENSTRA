package tool_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"renstra/internal/store"
	"renstra/internal/tool"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestCronToolAddListRemove(t *testing.T) {
	st := newStore(t)
	c := tool.NewCron(st, 0)
	ctx := tool.WithChatID(context.Background(), 777)

	out, err := c.Execute(ctx, json.RawMessage(`{"action":"add","schedule":"@daily","prompt":"ringkas berita"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "ID") {
		t.Fatalf("got %q", out)
	}

	out, err = c.Execute(ctx, json.RawMessage(`{"action":"list"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "ringkas berita") {
		t.Fatalf("list = %q", out)
	}

	if _, err := c.Execute(ctx, json.RawMessage(`{"action":"remove","id":1}`)); err != nil {
		t.Fatal(err)
	}
	out, err = c.Execute(ctx, json.RawMessage(`{"action":"list"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "tidak ada") {
		t.Fatalf("list setelah remove = %q", out)
	}
}

func TestCronToolUsesContextChatID(t *testing.T) {
	st := newStore(t)
	c := tool.NewCron(st, 0)

	if _, err := c.Execute(tool.WithChatID(context.Background(), 42), json.RawMessage(`{"action":"add","schedule":"@hourly","prompt":"a"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Execute(tool.WithChatID(context.Background(), 43), json.RawMessage(`{"action":"add","schedule":"@hourly","prompt":"b"}`)); err != nil {
		t.Fatal(err)
	}

	out, err := c.Execute(tool.WithChatID(context.Background(), 42), json.RawMessage(`{"action":"list"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"a"`) || strings.Contains(out, `"b"`) {
		t.Fatalf("chat 42 melihat job chat lain: %q", out)
	}
}
