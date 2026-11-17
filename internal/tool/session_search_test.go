package tool_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"renstra/internal/provider"
	"renstra/internal/store"
	"renstra/internal/tool"
)

func TestSessionSearchToolFindsPastTurns(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()

	if _, err := st.AppendMessage(ctx, 100, provider.Message{Role: provider.RoleUser, Content: "server kita pakai port 2222 untuk SSH"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendMessage(ctx, 100, provider.Message{Role: provider.RoleAssistant, Content: "siap, SSH port 2222"}); err != nil {
		t.Fatal(err)
	}

	ss := tool.NewSessionSearch(st)
	ctx = tool.WithChatID(ctx, 100)

	out, err := ss.Execute(ctx, json.RawMessage(`{"query":"SSH port"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "2222") {
		t.Fatalf("hasil pencarian tidak memuat port: %q", out)
	}

	// Empty result path
	out, err = ss.Execute(ctx, json.RawMessage(`{"query":"tidak-ada-kata-ini"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "tidak ditemukan") {
		t.Fatalf("hasil kosong harus melaporkan tidak ditemukan: %q", out)
	}
}
