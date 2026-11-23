package agent_test

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"

	"renstra/internal/agent"
	"renstra/internal/provider"
	"renstra/internal/tool"
)

type failingProvider struct {
	name  string
	err   error
	calls atomic.Int32
}

func (p *failingProvider) Name() string { return p.name }

func (p *failingProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	p.calls.Add(1)
	if p.err != nil {
		return nil, p.err
	}
	return nil, errors.New("503 service unavailable / rate limit")
}

func TestRunnerFailsOverToFallbackProvider(t *testing.T) {
	st := newStore(t)
	reg := tool.NewRegistry()

	primary := &failingProvider{name: "primary-down"}

	fallback := &fakeProvider{steps: [][]provider.Event{
		{{Text: "jawaban dari fallback", Done: true}},
	}}

	r := &agent.Runner{
		Provider: primary,
		Tools:    reg,
		Store:    st,
		Model:    "primary:model",
		MaxIters: 3,
		Cwd:      t.TempDir(),
		Out:      io.Discard,
		Fallbacks: []agent.FallbackTarget{
			{
				Provider:  fallback,
				Model:     "fallback:model",
				MaxTokens: 2048,
				PriceIn:   0.1,
				PriceOut:  0.2,
			},
		},
	}

	reply, err := r.Run(context.Background(), 99, "halo")
	if err != nil {
		t.Fatalf("Run seharusnya sukses dengan fallback, dapat error: %v", err)
	}
	if reply != "jawaban dari fallback" {
		t.Fatalf("reply = %q, want 'jawaban dari fallback'", reply)
	}
}

func TestSmartFallbackCircuitBreakerAndReset(t *testing.T) {
	st := newStore(t)
	primary := &failingProvider{name: "primary-down"}
	fallback := &fakeProvider{steps: [][]provider.Event{
		{{Text: "turn1", Done: true}},
		{{Text: "turn2", Done: true}},
		{{Text: "turn3", Done: true}},
	}}
	r := &agent.Runner{
		Provider: primary,
		Tools:    tool.NewRegistry(),
		Store:    st,
		Model:    "m1",
		MaxIters: 3,
		Cwd:      t.TempDir(),
		Out:      io.Discard,
		Fallbacks: []agent.FallbackTarget{
			{Provider: fallback, Model: "m2"},
		},
	}

	if _, err := r.Run(context.Background(), 1, "satu"); err != nil {
		t.Fatal(err)
	}
	if got := primary.calls.Load(); got != 1 {
		t.Fatalf("turn 1 primary calls = %d, want 1", got)
	}

	// Turn 2 must skip cooling-down primary immediately
	if _, err := r.Run(context.Background(), 1, "dua"); err != nil {
		t.Fatal(err)
	}
	if got := primary.calls.Load(); got != 1 {
		t.Fatalf("turn 2 primary should stay in cooldown (calls=%d, want 1)", got)
	}

	statuses := r.FallbackStatus()
	if len(statuses) != 2 || statuses[0].CooldownLeft <= 0 || statuses[1].CooldownLeft != 0 {
		t.Fatalf("unexpected statuses: %+v", statuses)
	}

	// Reset clears cooldown so primary is retried on turn 3
	r.ResetFallbacks()
	if _, err := r.Run(context.Background(), 1, "tiga"); err != nil {
		t.Fatal(err)
	}
	if got := primary.calls.Load(); got != 2 {
		t.Fatalf("turn 3 after reset primary calls = %d, want 2", got)
	}
}

func TestSmartFallbackContextOverflowSkipsSmallWindow(t *testing.T) {
	st := newStore(t)
	primary := &failingProvider{name: "p32k", err: errors.New("maximum context length exceeded")}
	smallFB := &failingProvider{name: "p16k", err: errors.New("should not be called")}
	largeFB := &fakeProvider{steps: [][]provider.Event{{{Text: "muat di 128k", Done: true}}}}

	r := &agent.Runner{
		Provider:      primary,
		Tools:         tool.NewRegistry(),
		Store:         st,
		Model:         "m32k",
		ContextWindow: 32000,
		MaxIters:      3,
		Cwd:           t.TempDir(),
		Out:           io.Discard,
		Fallbacks: []agent.FallbackTarget{
			{Provider: smallFB, Model: "m16k", ContextWindow: 16000},
			{Provider: largeFB, Model: "m128k", ContextWindow: 128000},
		},
	}

	reply, err := r.Run(context.Background(), 2, "panjang")
	if err != nil {
		t.Fatal(err)
	}
	if reply != "muat di 128k" {
		t.Fatalf("reply = %q", reply)
	}
	if smallFB.calls.Load() != 0 {
		t.Fatalf("small fallback (16k) should be skipped after 32k context overflow, calls=%d", smallFB.calls.Load())
	}
}

func TestSmartFallbackEmptyResponseFallsBack(t *testing.T) {
	st := newStore(t)
	empty := []provider.Event{{Done: true}}
	primary := &fakeProvider{steps: [][]provider.Event{empty, empty, empty}}
	fallback := &fakeProvider{steps: [][]provider.Event{{{Text: "ok cadangan", Done: true}}}}
	r := &agent.Runner{
		Provider:  primary,
		Tools:     tool.NewRegistry(),
		Store:     st,
		Model:     "m1",
		MaxIters:  3,
		Cwd:       t.TempDir(),
		Out:       io.Discard,
		Fallbacks: []agent.FallbackTarget{{Provider: fallback, Model: "m2"}},
	}
	reply, err := r.Run(context.Background(), 3, "halo")
	if err != nil || reply != "ok cadangan" {
		t.Fatalf("got reply=%q err=%v", reply, err)
	}
}
