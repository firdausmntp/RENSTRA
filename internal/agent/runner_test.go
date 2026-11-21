package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"renstra/internal/agent"
	"renstra/internal/provider"
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

type fakeProvider struct{ steps [][]provider.Event }

func (f *fakeProvider) Name() string { return "fake" }
func (f *fakeProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	ch := make(chan provider.Event)
	go func() {
		defer close(ch)
		if len(f.steps) == 0 {
			ch <- provider.Event{Text: "selesai", Done: true}
			return
		}
		for _, ev := range f.steps[0] {
			ch <- ev
		}
		f.steps = f.steps[1:]
	}()
	return ch, nil
}

type alwaysToolProvider struct{}

func (p *alwaysToolProvider) Name() string { return "always" }
func (p *alwaysToolProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	ch := make(chan provider.Event, 1)
	ch <- provider.Event{ToolCalls: []provider.ToolCall{{ID: "c", Name: "noop", Arguments: `{}`}}}
	close(ch)
	return ch, nil
}

type cancelAfterTextProvider struct {
	cancel context.CancelFunc
	text   string
}

func (p *cancelAfterTextProvider) Name() string { return "cancel" }
func (p *cancelAfterTextProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	ch := make(chan provider.Event, 1)
	ch <- provider.Event{Text: p.text}
	close(ch)
	p.cancel()
	return ch, nil
}

func TestRunExecutesToolThenAnswers(t *testing.T) {
	s := newStore(t)
	fp := &fakeProvider{steps: [][]provider.Event{
		{{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "terminal", Arguments: `{"cmd":"echo halo"}`}}}},
		{{Text: "sudah dijalankan", Done: true}},
	}}
	r := &agent.Runner{Provider: fp, Tools: tool.NewRegistry(tool.NewTerminal(t.TempDir(), 5*time.Second)), Store: s, Model: "m", MaxIters: 5, Cwd: t.TempDir(), Out: io.Discard,
		Approve: func(context.Context, string, json.RawMessage) (bool, error) { return true, nil }}
	got, err := r.Run(context.Background(), 1, "jalankan echo")
	if err != nil {
		t.Fatal(err)
	}
	if got != "sudah dijalankan" {
		t.Fatalf("got %q", got)
	}
	msgs, err := s.Messages(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 4 {
		t.Fatalf("pesan tersimpan = %d, want 4", len(msgs))
	}
	if msgs[2].Role != provider.RoleTool || !strings.Contains(msgs[2].Content, "halo") {
		t.Fatalf("hasil tool salah: %+v", msgs[2])
	}
}

func TestRunStopsAtMaxIters(t *testing.T) {
	r := &agent.Runner{Provider: &alwaysToolProvider{}, Tools: tool.NewRegistry(), Store: newStore(t), Model: "m", MaxIters: 1, Cwd: t.TempDir(), Out: io.Discard}
	_, err := r.Run(context.Background(), 1, "x")
	if err == nil || !strings.Contains(err.Error(), "iterasi") {
		t.Fatalf("got %v", err)
	}
}

func TestUnknownToolBecomesErrorResult(t *testing.T) {
	s := newStore(t)
	fp := &fakeProvider{steps: [][]provider.Event{
		{{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "ghost", Arguments: `{}`}}}},
		{{Text: "ok", Done: true}},
	}}
	r := &agent.Runner{Provider: fp, Tools: tool.NewRegistry(), Store: s, Model: "m", MaxIters: 5, Cwd: t.TempDir(), Out: io.Discard}
	if _, err := r.Run(context.Background(), 1, "x"); err != nil {
		t.Fatal(err)
	}
	msgs, _ := s.Messages(context.Background(), 1)
	if len(msgs) != 4 || !strings.Contains(msgs[2].Content, "tidak dikenal") {
		t.Fatalf("got %+v", msgs)
	}
}

func TestInvalidToolArgsBecomesErrorResult(t *testing.T) {
	s := newStore(t)
	fp := &fakeProvider{steps: [][]provider.Event{
		{{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "terminal", Arguments: `{bukan json`}}}},
		{{Text: "ok", Done: true}},
	}}
	r := &agent.Runner{Provider: fp, Tools: tool.NewRegistry(tool.NewTerminal(t.TempDir(), time.Second)), Store: s, Model: "m", MaxIters: 5, Cwd: t.TempDir(), Out: io.Discard}
	if _, err := r.Run(context.Background(), 1, "x"); err != nil {
		t.Fatal(err)
	}
	msgs, _ := s.Messages(context.Background(), 1)
	if len(msgs) != 4 {
		t.Fatalf("pesan = %d, want 4", len(msgs))
	}
}

func TestCancelSavesPartialAssistantText(t *testing.T) {
	s := newStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	fp := &cancelAfterTextProvider{cancel: cancel, text: "separuh jawaban"}
	r := &agent.Runner{Provider: fp, Tools: tool.NewRegistry(), Store: s, Model: "m", MaxIters: 5, Cwd: t.TempDir(), Out: io.Discard}
	_, err := r.Run(ctx, 1, "x")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	msgs, _ := s.Messages(context.Background(), 1)
	last := msgs[len(msgs)-1]
	if last.Role != provider.RoleAssistant || last.Content != "separuh jawaban" {
		t.Fatalf("got %+v", last)
	}
}

func TestTurnStatsFilled(t *testing.T) {
	s := newStore(t)
	fp := &fakeProvider{steps: [][]provider.Event{
		{{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "terminal", Arguments: `{"cmd":"echo halo"}`}}, Usage: &provider.Usage{InputTokens: 100, OutputTokens: 20}}},
		{{Text: "selesai", Usage: &provider.Usage{InputTokens: 200, OutputTokens: 50}, Done: true}},
	}}
	r := &agent.Runner{
		Provider: fp, Tools: tool.NewRegistry(tool.NewTerminal(t.TempDir(), 5*time.Second)), Store: s,
		Model: "m", MaxIters: 5, Cwd: t.TempDir(), Out: io.Discard,
		ContextWindow: 16000, PriceIn: 3.0, PriceOut: 6.0,
		Approve: func(context.Context, string, json.RawMessage) (bool, error) { return true, nil },
	}
	var stats agent.TurnStats
	ctx := agent.WithTurnStats(context.Background(), &stats)
	if _, err := r.Run(ctx, 1, "jalankan echo"); err != nil {
		t.Fatal(err)
	}
	if stats.TokensIn != 300 || stats.TokensOut != 70 {
		t.Fatalf("tokens = %d/%d, want 300/70", stats.TokensIn, stats.TokensOut)
	}
	if stats.ToolCalls != 1 || stats.Iterations != 2 {
		t.Fatalf("tools/iters = %d/%d, want 1/2", stats.ToolCalls, stats.Iterations)
	}
	if stats.ContextUsed != 200 || stats.ContextWindow != 16000 {
		t.Fatalf("context = %d/%d, want 200/16000 (usage provider menang)", stats.ContextUsed, stats.ContextWindow)
	}
	wantCost := (300.0/1e6)*3.0 + (70.0/1e6)*6.0
	if stats.CostUSD < wantCost*0.99 || stats.CostUSD > wantCost*1.01 {
		t.Fatalf("cost = %f, want ~%f", stats.CostUSD, wantCost)
	}
	if stats.Elapsed <= 0 {
		t.Fatal("elapsed harus terisi")
	}
}

func TestTurnStatsFallbackEstimateWithoutUsage(t *testing.T) {
	s := newStore(t)
	fp := &fakeProvider{steps: [][]provider.Event{{{Text: "tanpa usage", Done: true}}}}
	r := &agent.Runner{Provider: fp, Tools: tool.NewRegistry(), Store: s, Model: "m", MaxIters: 5, Cwd: t.TempDir(), Out: io.Discard}
	var stats agent.TurnStats
	ctx := agent.WithTurnStats(context.Background(), &stats)
	if _, err := r.Run(ctx, 1, "halo dunia"); err != nil {
		t.Fatal(err)
	}
	if stats.TokensIn != 0 || stats.TokensOut != 0 {
		t.Fatalf("tanpa usage provider tokens harus 0, got %d/%d", stats.TokensIn, stats.TokensOut)
	}
	if stats.ContextUsed <= 0 {
		t.Fatal("estimasi context harus terisi walau tanpa usage provider")
	}
	if stats.ContextWindow != 32000 {
		t.Fatalf("window default = %d, want 32000", stats.ContextWindow)
	}
}

func TestEmptyModelResponseReturnsErrorAndNotSaved(t *testing.T) {
	s := newStore(t)
	fp := &fakeProvider{steps: [][]provider.Event{{{Done: true}}}}
	r := &agent.Runner{Provider: fp, Tools: tool.NewRegistry(), Store: s, Model: "m", MaxIters: 5, Cwd: t.TempDir(), Out: io.Discard}
	if _, err := r.Run(context.Background(), 1, "halo"); err == nil {
		t.Fatal("diharapkan error ketika model memberi respons kosong")
	}
	msgs, err := s.Messages(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		if m.Role == provider.RoleAssistant {
			t.Fatalf("pesan assistant kosong tidak boleh tersimpan: %+v", m)
		}
	}
}
