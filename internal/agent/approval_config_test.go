package agent_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"renstra/internal/agent"
	"renstra/internal/config"
	"renstra/internal/provider"
	"renstra/internal/tool"
)

func TestRunnerHonorsApprovalConfigAuto(t *testing.T) {
	st := newStore(t)
	reg := tool.NewRegistry(tool.NewTerminal(t.TempDir(), 0))

	fp := &fakeProvider{steps: [][]provider.Event{
		{{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "terminal", Arguments: `{"cmd":"echo lulus"}`}}}},
		{{Text: "selesai", Done: true}},
	}}

	// mode: "auto" -> tidak butuh Approve func sama sekali
	r := &agent.Runner{
		Provider: fp,
		Tools:    reg,
		Store:    st,
		Model:    "m",
		MaxIters: 3,
		Cwd:      t.TempDir(),
		Out:      io.Discard,
		ApprovalCfg: config.ApprovalConfig{
			Mode: "auto",
		},
	}

	ans, err := r.Run(context.Background(), 10, "jalankan echo")
	if err != nil {
		t.Fatal(err)
	}
	if ans != "selesai" {
		t.Fatalf("ans = %q", ans)
	}

	msgs, _ := st.Messages(context.Background(), 10)
	if !strings.Contains(msgs[2].Content, "lulus") {
		t.Fatalf("terminal tidak dieksekusi: %q", msgs[2].Content)
	}
}

func TestRunnerHonorsApprovalConfigDenyRule(t *testing.T) {
	st := newStore(t)
	reg := tool.NewRegistry(tool.NewTerminal(t.TempDir(), 0))

	fp := &fakeProvider{steps: [][]provider.Event{
		{{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "terminal", Arguments: `{"cmd":"rm -rf /penting"}`}}}},
		{{Text: "selesai", Done: true}},
	}}

	r := &agent.Runner{
		Provider: fp,
		Tools:    reg,
		Store:    st,
		Model:    "m",
		MaxIters: 3,
		Cwd:      t.TempDir(),
		Out:      io.Discard,
		ApprovalCfg: config.ApprovalConfig{
			Mode:         "prompt",
			AlwaysDenied: []string{"terminal:rm -rf"},
		},
	}

	_, err := r.Run(context.Background(), 11, "hapus")
	if err != nil {
		t.Fatal(err)
	}

	msgs, _ := st.Messages(context.Background(), 11)
	if !strings.Contains(msgs[2].Content, "aturan approval konfigurasi menolak") {
		t.Fatalf("seharusnya ditolak config: %q", msgs[2].Content)
	}
}
