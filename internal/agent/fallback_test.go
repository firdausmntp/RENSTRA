package agent_test

import (
	"context"
	"errors"
	"io"
	"testing"

	"renstra/internal/agent"
	"renstra/internal/provider"
	"renstra/internal/tool"
)

type failingProvider struct {
	name string
}

func (p *failingProvider) Name() string { return p.name }

func (p *failingProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
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
