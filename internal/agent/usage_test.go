package agent_test

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"renstra/internal/agent"
	"renstra/internal/provider"
	"renstra/internal/tool"
)

type usageProvider struct{}

func (u *usageProvider) Name() string { return "usage" }
func (u *usageProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	ch := make(chan provider.Event, 1)
	ch <- provider.Event{Text: "ok", Usage: &provider.Usage{InputTokens: 1000, OutputTokens: 500}, Done: true}
	close(ch)
	return ch, nil
}

func TestRunRecordsUsageAndCost(t *testing.T) {
	s := newStore(t)
	r := &agent.Runner{
		Provider: &usageProvider{}, Tools: tool.NewRegistry(), Store: s,
		Model: "m", MaxIters: 5, Cwd: t.TempDir(), Out: io.Discard,
		PriceIn: 1.0, PriceOut: 2.0, // per 1M tokens
	}
	if _, err := r.Run(context.Background(), 1, "x"); err != nil {
		t.Fatal(err)
	}
	u, err := s.UsageForDay(context.Background(), time.Now().Format("2006-01-02"))
	if err != nil {
		t.Fatal(err)
	}
	if u.TokensIn != 1000 || u.TokensOut != 500 {
		t.Fatalf("usage tidak dicatat: %+v", u)
	}
	want := (1000.0/1e6)*1.0 + (500.0/1e6)*2.0
	if u.CostUSD < want-1e-9 || u.CostUSD > want+1e-9 {
		t.Fatalf("cost = %v, want %v", u.CostUSD, want)
	}
}

func TestDelegateIsRegisteredAndRunsChild(t *testing.T) {
	var gotTask string
	reg := tool.NewRegistry()
	d := tool.NewDelegate(subagentFunc(func(ctx context.Context, task string) (string, error) {
		gotTask = task
		return "hasil anak", nil
	}), 0)
	reg.Add(d)

	fp := &fakeProvider{steps: [][]provider.Event{
		{{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "delegate", Arguments: `{"task":"cari bug"}`}}}},
		{{Text: "selesai", Done: true}},
	}}
	s := newStore(t)
	r := &agent.Runner{Provider: fp, Tools: reg, Store: s, Model: "m", MaxIters: 5, Cwd: t.TempDir(), Out: io.Discard}
	if _, err := r.Run(context.Background(), 1, "x"); err != nil {
		t.Fatal(err)
	}
	if gotTask != "cari bug" {
		t.Fatalf("task = %q", gotTask)
	}
	msgs, _ := s.Messages(context.Background(), 1)
	if !strings.Contains(msgs[2].Content, "hasil anak") {
		t.Fatalf("hasil = %q", msgs[2].Content)
	}
}

func TestDelegateDepthLimit(t *testing.T) {
	d := tool.NewDelegate(subagentFunc(func(context.Context, string) (string, error) { return "", nil }), 2)
	if _, err := d.Execute(context.Background(), json.RawMessage(`{"task":"x"}`)); err == nil {
		t.Fatal("want depth limit error")
	}
}

type subagentFunc func(ctx context.Context, task string) (string, error)

func (f subagentFunc) RunSubagent(ctx context.Context, task string) (string, error) {
	return f(ctx, task)
}
