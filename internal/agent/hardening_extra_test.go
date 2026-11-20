package agent_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"renstra/internal/agent"
	"renstra/internal/provider"
	"renstra/internal/tool"
)

func TestUnattendedContextDeniesDangerousTool(t *testing.T) {
	s := newStore(t)
	approverCalled := false
	fp := &fakeProvider{steps: [][]provider.Event{
		{{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "danger", Arguments: `{}`}}}},
		{{Text: "selesai", Done: true}},
	}}
	r := &agent.Runner{
		Provider: fp,
		Tools:    tool.NewRegistry(&dangerTool{}),
		Store:    s,
		Model:    "m",
		MaxIters: 4,
		Cwd:      t.TempDir(),
		Out:      nil,
		Approve: func(context.Context, string, json.RawMessage) (bool, error) {
			approverCalled = true
			return true, nil
		},
	}
	ctx := agent.WithUnattended(context.Background())
	if _, err := r.Run(ctx, 1, "cron"); err != nil {
		t.Fatal(err)
	}
	if approverCalled {
		t.Fatal("approver interaktif tidak boleh dipanggil pada konteks unattended")
	}
	msgs, _ := s.Messages(context.Background(), 1)
	if !strings.Contains(msgs[2].Content, "cron") {
		t.Fatalf("pesan penolakan harus menyebut cron/unattended, dapat %q", msgs[2].Content)
	}
}

type fakeMCPTool struct{}

func (m *fakeMCPTool) Name() string            { return "mcp__demo__ping" }
func (m *fakeMCPTool) Description() string     { return "mcp ping" }
func (m *fakeMCPTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (m *fakeMCPTool) Danger() bool            { return false }
func (m *fakeMCPTool) Execute(context.Context, json.RawMessage) (string, error) {
	return "pong", nil
}

func TestUnattendedContextDeniesMCPTool(t *testing.T) {
	s := newStore(t)
	fp := &fakeProvider{steps: [][]provider.Event{
		{{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "mcp__demo__ping", Arguments: `{}`}}}},
		{{Text: "ok", Done: true}},
	}}
	r := &agent.Runner{
		Provider: fp,
		Tools:    tool.NewRegistry(&fakeMCPTool{}),
		Store:    s,
		Model:    "m",
		MaxIters: 3,
		Cwd:      t.TempDir(),
	}
	if _, err := r.Run(agent.WithUnattended(context.Background()), 1, "cron mcp"); err != nil {
		t.Fatal(err)
	}
	msgs, _ := s.Messages(context.Background(), 1)
	if !strings.Contains(msgs[2].Content, "MCP") || strings.Contains(msgs[2].Content, "pong") {
		t.Fatalf("tool MCP harus ditolak di cron unattended, dapat %q", msgs[2].Content)
	}
}

func TestRunSubagentPropagatesDelegateDepthLimit(t *testing.T) {
	s := newStore(t)
	fp := &fakeProvider{steps: [][]provider.Event{
		{{ToolCalls: []provider.ToolCall{{ID: "d1", Name: "delegate", Arguments: `{"task":"sub1"}`}}}},
		{{ToolCalls: []provider.ToolCall{{ID: "d2", Name: "delegate", Arguments: `{"task":"sub2"}`}}}},
		{{ToolCalls: []provider.ToolCall{{ID: "d3", Name: "delegate", Arguments: `{"task":"sub3"}`}}}},
		{{Text: "selesai depth2", Done: true}},
		{{Text: "selesai depth1", Done: true}},
		{{Text: "selesai root", Done: true}},
	}}
	reg := tool.NewRegistry()
	r := &agent.Runner{Provider: fp, Tools: reg, Store: s, Model: "m", MaxIters: 5, Cwd: t.TempDir()}
	reg.Add(tool.NewDelegate(r, 0))

	got, err := r.Run(context.Background(), 1, "mulai")
	if err != nil {
		t.Fatal(err)
	}
	if got != "selesai root" {
		t.Fatalf("got %q", got)
	}
}

func TestPromptedToolsAndVisionAttachment(t *testing.T) {
	s := newStore(t)
	imgPath := filepath.Join(t.TempDir(), "foto.png")
	if err := os.WriteFile(imgPath, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}

	var sawAttachment bool
	var sawNoNativeTools bool
	step := 0
	prov := &inspectProvider{fn: func(req provider.Request) []provider.Event {
		step++
		if step == 1 {
			sawNoNativeTools = len(req.Tools) == 0
			for _, m := range req.Messages {
				if len(m.Attachments) > 0 && m.Attachments[0].Path == imgPath {
					sawAttachment = true
				}
			}
			return []provider.Event{{Text: "```json\n{\"tool_call\":{\"name\":\"counter\",\"arguments\":{}}}\n```", Done: true}}
		}
		return []provider.Event{{Text: "sudah dijalankan", Done: true}}
	}}

	ct := &countTool{}
	r := &agent.Runner{
		Provider:      prov,
		Tools:         tool.NewRegistry(ct),
		Store:         s,
		Model:         "m",
		MaxIters:      4,
		Cwd:           t.TempDir(),
		Vision:        true,
		ToolsDisabled: true,
	}
	got, err := r.Run(context.Background(), 1, "[Foto diterima: "+imgPath+"]\nlihat ini")
	if err != nil {
		t.Fatal(err)
	}
	if got != "sudah dijalankan" || !sawAttachment || !sawNoNativeTools || ct.count != 1 {
		t.Fatalf("got=%q att=%v noTools=%v count=%d", got, sawAttachment, sawNoNativeTools, ct.count)
	}
}

type inspectProvider struct {
	fn func(provider.Request) []provider.Event
}

func (i *inspectProvider) Name() string { return "inspect" }
func (i *inspectProvider) Stream(_ context.Context, req provider.Request) (<-chan provider.Event, error) {
	ch := make(chan provider.Event, 2)
	for _, ev := range i.fn(req) {
		ch <- ev
	}
	close(ch)
	return ch, nil
}

func TestRunnerCloneIsolatesModel(t *testing.T) {
	s := newStore(t)
	var mu sync.Mutex
	var seen []string
	prov := &inspectProvider{fn: func(req provider.Request) []provider.Event {
		mu.Lock()
		seen = append(seen, req.Model)
		mu.Unlock()
		return []provider.Event{{Text: "ok:" + req.Model, Done: true}}
	}}
	parent := &agent.Runner{
		Provider: prov,
		Tools:    tool.NewRegistry(),
		Store:    s,
		Model:    "model-a",
		Cwd:      t.TempDir(),
	}
	clone := parent.Clone()
	clone.SetModel(prov, "model-b", 1024, 32000, 0, 0)

	if _, err := parent.Run(context.Background(), 1, "satu"); err != nil {
		t.Fatal(err)
	}
	if _, err := clone.Run(context.Background(), 2, "dua"); err != nil {
		t.Fatal(err)
	}
	if parent.GetModel() != "model-a" || clone.GetModel() != "model-b" {
		t.Fatalf("model tidak terisolasi: parent=%s clone=%s", parent.GetModel(), clone.GetModel())
	}
	if len(seen) != 2 || seen[0] != "model-a" || seen[1] != "model-b" {
		t.Fatalf("model request salah: %v", seen)
	}
}

func TestSummarizeUsesAuxProviderAndRecordsUsage(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	for i := 0; i < 8; i++ {
		if _, err := s.AppendMessage(ctx, 1, provider.Message{Role: provider.RoleUser, Content: strings.Repeat("panjang ", 50)}); err != nil {
			t.Fatal(err)
		}
	}

	var auxCalled bool
	auxProv := &inspectProvider{fn: func(req provider.Request) []provider.Event {
		auxCalled = true
		if req.Model != "aux-cheap" {
			t.Errorf("aux model = %q, ingin aux-cheap", req.Model)
		}
		return []provider.Event{
			{Text: "ringkas aux", Usage: &provider.Usage{InputTokens: 1000, OutputTokens: 500}, Done: true},
		}
	}}
	mainProv := &inspectProvider{fn: func(req provider.Request) []provider.Event {
		return []provider.Event{{Text: "jawaban akhir", Done: true}}
	}}

	r := &agent.Runner{
		Provider:      mainProv,
		AuxProvider:   auxProv,
		AuxModel:      "aux-cheap",
		AuxPriceIn:    2.0,
		AuxPriceOut:   4.0,
		Tools:         tool.NewRegistry(),
		Store:         s,
		Model:         "main-model",
		ContextWindow: 200,
		Threshold:     0.5,
		Cwd:           t.TempDir(),
	}
	if _, err := r.Run(ctx, 1, "lanjut"); err != nil {
		t.Fatal(err)
	}
	if !auxCalled {
		t.Fatal("AuxProvider tidak dipanggil saat kompaksi")
	}
	u, err := s.UsageForDay(ctx, time.Now().Format("2006-01-02"))
	if err != nil {
		t.Fatal(err)
	}
	if u.TokensIn != 1000 || u.TokensOut != 500 {
		t.Fatalf("usage aux tidak tercatat: %+v", u)
	}
}
